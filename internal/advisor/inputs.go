package advisor

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// PSI is memory pressure stall information, avg10 percentages.
type PSI struct {
	SomeAvg10 float64
	FullAvg10 float64 // 0 when the kernel has no "full" line
	HasFull   bool
}

// Max is the higher of some and full.
func (p PSI) Max() float64 {
	if p.FullAvg10 > p.SomeAvg10 {
		return p.FullAvg10
	}
	return p.SomeAvg10
}

func readPSI(m machine.Machine) (*PSI, string) {
	b, err := m.ReadFile("/proc/pressure/memory")
	if err != nil {
		return nil, "memory pressure (PSI) is not available: /proc/pressure/memory is missing (kernel older than 4.20 or PSI disabled)"
	}
	var p PSI
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, kv := range f[1:] {
			k, v, _ := strings.Cut(kv, "=")
			if k != "avg10" {
				continue
			}
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			switch f[0] {
			case "some":
				p.SomeAvg10, found = n, true
			case "full":
				p.FullAvg10, p.HasFull = n, true
			}
		}
	}
	if !found {
		return nil, "memory pressure (PSI) could not be read from /proc/pressure/memory"
	}
	return &p, ""
}

// Rate is a swap rate in pages per second; nil when unknown.
type Rate struct{ In, Out *float64 }

// Thrashing reports in and out both above threshold.
func (r Rate) Thrashing(threshold float64) bool {
	return r.In != nil && r.Out != nil && *r.In > threshold && *r.Out > threshold
}

// SwapActivity is the swap-in/out rate attributed to Disk Swap and to
// Compressed Swap.
//
// ATTRIBUTION RULE. The kernel only counts swap-in/out system-wide
// (pswpin/pswpout in /proc/vmstat). Compressed Swap traffic is measured
// directly where the kernel exposes it, and Disk Swap is the remainder:
//
//	Compressed = zram: read/write sectors of every /sys/block/zramN/stat that
//	             is a swap device in /proc/swaps (sector = 512 B), plus zswap:
//	             zswpin/zswpout in /proc/vmstat;
//	Disk       = max(0, system-wide - Compressed), only when a Disk Swap exists.
//
// When Compressed Swap exists but its counters cannot be read and a Disk Swap
// exists too, the split is unknowable: Disk stays nil and DiskUnknown carries
// the reason, so rules that need Disk Swap Thrashing are disabled rather than
// guessing. With only one kind of swap, all traffic belongs to it.
type SwapActivity struct {
	System      Rate
	Disk        Rate
	Compressed  Rate
	HasDisk     bool
	HasZram     bool
	DiskUnknown string // non-empty: Disk Swap activity cannot be told from Compressed Swap
	VmstatGone  bool
	Attribution string // human text for evidence
	// DiskWriteBytesPerDay estimates the daily Disk Swap write volume from the
	// trailing DiskWriteObserved of samples; nil until a full Sustained window was observed.
	DiskWriteBytesPerDay *float64
	DiskWriteObserved    time.Duration
}

type swapCounters struct {
	at                       time.Time
	pswpin, pswpout          uint64
	zswpin, zswpout          uint64
	zramIn, zramOut          uint64 // pages
	haveVmstat, haveZswpStat bool
	haveZram                 bool
}

type swapTracker struct{ prev *swapCounters }

func (t *swapTracker) observe(m machine.Machine, now time.Time, hasDisk, hasZram, zswapOn bool) SwapActivity {
	cur := &swapCounters{at: now}
	if b, err := m.ReadFile("/proc/vmstat"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) != 2 {
				continue
			}
			n, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				continue
			}
			switch f[0] {
			case "pswpin":
				cur.pswpin = n
				cur.haveVmstat = true
			case "pswpout":
				cur.pswpout = n
			case "zswpin":
				cur.zswpin, cur.haveZswpStat = n, zswapOn // zswap counters exist even when zswap is off
			case "zswpout":
				cur.zswpout = n
			}
		}
	}
	zramOK := hasZram
	badDev := "zram0"
	if hasZram {
		swaps, _ := m.ReadFile("/proc/swaps")
		for _, line := range strings.Split(string(swaps), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || !strings.HasPrefix(f[0], "/dev/zram") {
				continue
			}
			b, err := m.ReadFile("/sys/block/" + strings.TrimPrefix(f[0], "/dev/") + "/stat")
			st := strings.Fields(string(b))
			if err != nil || len(st) < 7 {
				zramOK = false
				badDev = strings.TrimPrefix(f[0], "/dev/")
				continue
			}
			rs, _ := strconv.ParseUint(st[2], 10, 64)
			ws, _ := strconv.ParseUint(st[6], 10, 64)
			per := uint64(os.Getpagesize() / 512)
			cur.zramIn += rs / per
			cur.zramOut += ws / per
		}
		cur.haveZram = zramOK
	}
	act := SwapActivity{HasDisk: hasDisk, HasZram: hasZram, VmstatGone: !cur.haveVmstat}
	if hasZram && hasDisk && !cur.haveZram && !cur.haveZswpStat {
		act.DiskUnknown = "Disk Swap and Compressed Swap are both in use but their swap traffic cannot be told apart (/sys/block/" + badDev + "/stat is unreadable)"
	}
	prev := t.prev
	t.prev = cur
	if prev == nil || !cur.haveVmstat || !prev.haveVmstat {
		return act
	}
	dt := now.Sub(prev.at).Seconds()
	if dt <= 0 {
		return act
	}
	rate := func(a, b uint64) *float64 {
		if a < b {
			return nil // counter reset
		}
		v := float64(a-b) / dt
		return &v
	}
	sysIn, sysOut := rate(cur.pswpin, prev.pswpin), rate(cur.pswpout, prev.pswpout)
	act.System = Rate{sysIn, sysOut}
	if sysIn == nil || sysOut == nil {
		return act
	}
	// Compressed Swap rate.
	var cIn, cOut float64
	known := false
	if cur.haveZram && prev.haveZram {
		if a, b := rate(cur.zramIn, prev.zramIn), rate(cur.zramOut, prev.zramOut); a != nil && b != nil {
			cIn, cOut, known = *a, *b, true
		}
	}
	if cur.haveZswpStat && prev.haveZswpStat {
		if a, b := rate(cur.zswpin, prev.zswpin), rate(cur.zswpout, prev.zswpout); a != nil && b != nil {
			cIn += *a
			cOut += *b
			known = true
		}
	}
	switch {
	case hasZram && !hasDisk:
		act.Compressed = act.System
		act.Attribution = "only Compressed Swap exists: all swap traffic is Compressed Swap"
	case !hasZram && !hasDisk:
		act.Attribution = "no swap"
	case !hasZram && known && hasDisk: // zswap in front of Disk Swap
		act.Compressed = Rate{&cIn, &cOut}
		act.Disk = Rate{sub0(*sysIn, cIn), sub0(*sysOut, cOut)}
		act.Attribution = "zswap counters subtracted from system-wide swap traffic"
	case !hasZram && hasDisk:
		act.Disk = act.System
		act.Attribution = "only Disk Swap exists: all swap traffic is Disk Swap"
	case known:
		act.Compressed = Rate{&cIn, &cOut}
		if hasDisk {
			act.Disk = Rate{sub0(*sysIn, cIn), sub0(*sysOut, cOut)}
		}
		act.Attribution = "Compressed Swap measured from zram/zswap counters; Disk Swap is system-wide swap traffic minus that"
	default: // zram plus Disk Swap and no counters
		act.Attribution = act.DiskUnknown
	}
	return act
}

func sub0(a, b float64) *float64 {
	v := a - b
	if v < 0 {
		v = 0
	}
	return &v
}
