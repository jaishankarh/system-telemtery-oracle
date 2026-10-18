package collector

import (
	"bufio"
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Linux reads /proc and /sys through a Machine.
type Linux struct {
	m    machine.Machine
	prev *prevReading
}

type prevReading struct {
	at                int64
	cpuTotal, cpuIdle uint64
	haveCPU           bool
	pswpin, pswpout   uint64
	haveVmstat        bool
}

// NewLinux returns the Linux Collector.
func NewLinux(m machine.Machine) *Linux { return &Linux{m: m} }

// Collect implements Collector.
func (l *Linux) Collect() (Sample, error) {
	now := l.m.Clock.Now()
	s := Sample{Time: now, Missing: []string{}}
	missing := func(p string) { s.Missing = append(s.Missing, p) }
	cur := &prevReading{at: now.UnixNano()}
	var dt float64
	if l.prev != nil {
		dt = float64(cur.at-l.prev.at) / 1e9
	}

	if b, err := l.m.ReadFile("/proc/meminfo"); err != nil {
		missing("/proc/meminfo")
	} else {
		s.Memory = parseMeminfo(b)
	}

	if b, err := l.m.ReadFile("/proc/loadavg"); err != nil {
		missing("/proc/loadavg")
	} else if f := strings.Fields(string(b)); len(f) >= 3 {
		s.CPU.Load1, s.CPU.Load5, s.CPU.Load15 = pf(f[0]), pf(f[1]), pf(f[2])
	}

	if b, err := l.m.ReadFile("/proc/stat"); err != nil {
		missing("/proc/stat")
	} else {
		cores, total, idle, ok := parseStat(b)
		s.CPU.Cores = cores
		if ok {
			cur.cpuTotal, cur.cpuIdle, cur.haveCPU = total, idle, true
			if l.prev != nil && l.prev.haveCPU && total > l.prev.cpuTotal {
				v := 100 * (1 - float64(idle-l.prev.cpuIdle)/float64(total-l.prev.cpuTotal))
				s.CPU.TotalPercent = &v
			}
		}
	}

	if b, err := l.m.ReadFile("/proc/vmstat"); err != nil {
		missing("/proc/vmstat")
	} else {
		in, out, ok := parseVmstat(b)
		if ok {
			cur.pswpin, cur.pswpout, cur.haveVmstat = in, out, true
			if l.prev != nil && l.prev.haveVmstat && dt > 0 {
				a, c := float64(in-l.prev.pswpin)/dt, float64(out-l.prev.pswpout)/dt
				s.Swap.InPagesPerSec, s.Swap.OutPagesPerSec = &a, &c
			}
		}
	}

	if b, err := l.m.ReadFile("/proc/swaps"); err != nil {
		missing("/proc/swaps")
	} else {
		l.parseSwaps(b, &s.Swap)
	}
	if b, err := l.m.ReadFile("/sys/module/zswap/parameters/enabled"); err == nil {
		v := strings.TrimSpace(string(b)) == "Y"
		s.Swap.ZswapEnabled = &v
	}

	sort.Strings(s.Missing)
	l.prev = cur
	return s, nil
}

func pf(v string) *float64 {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &f
}

func parseMeminfo(b []byte) *Memory {
	kb := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err == nil {
			kb[k] = n * 1024 // meminfo is in KiB
		}
	}
	m := &Memory{TotalBytes: kb["MemTotal"], AvailableBytes: kb["MemAvailable"],
		CachedBytes: kb["Cached"], BuffersBytes: kb["Buffers"], DirtyBytes: kb["Dirty"]}
	if _, ok := kb["MemAvailable"]; !ok { // kernels before 3.14
		m.AvailableBytes = kb["MemFree"] + kb["Buffers"] + kb["Cached"]
	}
	if m.TotalBytes >= m.AvailableBytes {
		m.UsedBytes = m.TotalBytes - m.AvailableBytes
	}
	return m
}

func parseStat(b []byte) (cores int, total, idle uint64, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "cpu" && len(f) >= 5:
			for i, v := range f[1:] {
				n, err := strconv.ParseUint(v, 10, 64)
				if err != nil || i >= 8 { // guest columns are already inside user/nice
					continue
				}
				total += n
				if i == 3 || i == 4 { // idle, iowait
					idle += n
				}
			}
			ok = true
		case strings.HasPrefix(f[0], "cpu"):
			cores++
		}
	}
	return
}

func parseVmstat(b []byte) (in, out uint64, ok bool) {
	var gi, go_ bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "pswpin":
			in, _ = strconv.ParseUint(f[1], 10, 64)
			gi = true
		case "pswpout":
			out, _ = strconv.ParseUint(f[1], 10, 64)
			go_ = true
		}
	}
	return in, out, gi && go_
}

// parseSwaps splits /proc/swaps into Disk Swap and Compressed Swap (zram).
func (l *Linux) parseSwaps(b []byte, sw *Swap) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		size, e1 := strconv.ParseUint(f[2], 10, 64)
		used, e2 := strconv.ParseUint(f[3], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		space := SwapSpace{TotalBytes: size * 1024, UsedBytes: used * 1024}
		if strings.HasPrefix(f[0], "/dev/zram") {
			if sw.Compressed == nil {
				sw.Compressed = &SwapSpace{}
			}
			sw.Compressed.TotalBytes += space.TotalBytes
			sw.Compressed.UsedBytes += space.UsedBytes
			if ram, ok := l.zramRAM(strings.TrimPrefix(f[0], "/dev/")); ok {
				v := uint64(0)
				if sw.Compressed.RAMUsedBytes != nil {
					v = *sw.Compressed.RAMUsedBytes
				}
				v += ram
				sw.Compressed.RAMUsedBytes = &v
			}
			continue
		}
		if sw.Disk == nil {
			sw.Disk = &SwapSpace{}
		}
		sw.Disk.TotalBytes += space.TotalBytes
		sw.Disk.UsedBytes += space.UsedBytes
	}
}

// zramRAM returns mem_used_total (field 3 of mm_stat): RAM the device occupies.
func (l *Linux) zramRAM(dev string) (uint64, bool) {
	b, err := l.m.ReadFile("/sys/block/" + dev + "/mm_stat")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, false
	}
	n, err := strconv.ParseUint(f[2], 10, 64)
	return n, err == nil
}
