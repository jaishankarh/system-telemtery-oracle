package collector

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"time"
)

// Stall is the share of the last interval during which some (or all) tasks
// were stalled on a resource, from pressure stall information (PSI). Nil when
// unknown (first sample, or no "full" line as for CPU).
type Stall struct {
	Some *float64 `json:"some_percent"`
	Full *float64 `json:"full_percent"`
}

// Pressure is the stall percentages for CPU, memory and I/O. The whole object
// is nil on kernels without /proc/pressure (ADR 0006).
type Pressure struct {
	CPU    Stall `json:"cpu"`
	Memory Stall `json:"memory"`
	IO     Stall `json:"io"`
}

// Disk is the throughput of one whole block device (partitions, loop and RAM
// devices and zram are left out; zram is Compressed Swap, not a disk).
type Disk struct {
	Device     string   `json:"device"`
	Read       *float64 `json:"read_bytes_per_sec"`
	Write      *float64 `json:"write_bytes_per_sec"`
	QueueDepth uint64   `json:"queue_depth"`
}

type diskPrev struct{ readSectors, writeSectors uint64 }

// pressureState holds the previous counters needed for rates.
type pressureState struct {
	at     time.Time
	stall  map[string][2]uint64 // resource -> some,full total microseconds
	disks  map[string]diskPrev
	loaded bool
}

// collectPressureAndDisks fills s.Pressure and s.Disks, listing absent sources in s.Missing.
func (l *Linux) collectPressureAndDisks(now time.Time, s *Sample) {
	prev := l.ps
	dt := now.Sub(prev.at).Seconds()
	haveRates := prev.loaded && dt > 0
	next := pressureState{at: now, stall: map[string][2]uint64{}, disks: map[string]diskPrev{}, loaded: true}

	var pr Pressure
	found := false
	for name, dst := range map[string]*Stall{"cpu": &pr.CPU, "memory": &pr.Memory, "io": &pr.IO} {
		b, err := l.m.ReadFile("/proc/pressure/" + name)
		if err != nil {
			continue
		}
		some, full, hasFull, ok := parsePSI(b)
		if !ok {
			continue
		}
		found = true
		next.stall[name] = [2]uint64{some, full}
		if p, had := prev.stall[name]; haveRates && had {
			rate := func(cur, old uint64) *float64 {
				if cur < old {
					return nil
				}
				v := float64(cur-old) / 1e6 / dt * 100 // total is microseconds
				if v > 100 {
					v = 100
				}
				return &v
			}
			dst.Some = rate(some, p[0])
			if hasFull {
				dst.Full = rate(full, p[1])
			}
		}
	}
	if found {
		s.Pressure = &pr
	} else {
		s.Missing = append(s.Missing, "/proc/pressure")
	}

	if b, err := l.m.ReadFile("/proc/diskstats"); err != nil {
		s.Missing = append(s.Missing, "/proc/diskstats")
	} else {
		for _, d := range l.parseDiskstats(b) {
			next.disks[d.Device] = diskPrev{d.rs, d.ws}
			disk := Disk{Device: d.Device, QueueDepth: d.inflight}
			if p, had := prev.disks[d.Device]; haveRates && had && d.rs >= p.readSectors && d.ws >= p.writeSectors {
				r, w := float64(d.rs-p.readSectors)*512/dt, float64(d.ws-p.writeSectors)*512/dt
				disk.Read, disk.Write = &r, &w
			}
			s.Disks = append(s.Disks, disk)
		}
	}
	l.ps = next
}

// parsePSI reads "some ... total=N" and "full ... total=N".
func parsePSI(b []byte) (some, full uint64, hasFull, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		for _, kv := range f[1:] {
			if v, found := strings.CutPrefix(kv, "total="); found {
				n, err := strconv.ParseUint(v, 10, 64)
				if err != nil {
					continue
				}
				switch f[0] {
				case "some":
					some, ok = n, true
				case "full":
					full, hasFull = n, true
				}
			}
		}
	}
	return
}

type diskRow struct {
	Device   string
	rs, ws   uint64
	inflight uint64
}

// parseDiskstats keeps whole block devices only: those with a /sys/block entry
// when /sys/block exists, otherwise every non-partition-looking name.
func (l *Linux) parseDiskstats(b []byte) []diskRow {
	whole := map[string]bool{}
	haveSys := false
	if names, err := l.m.ReadDir("/sys/block"); err == nil {
		haveSys = true
		for _, n := range names {
			whole[n] = true
		}
	}
	var out []diskRow
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 12 {
			continue
		}
		name := f[2]
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		if haveSys && !whole[name] {
			continue
		}
		rs, e1 := strconv.ParseUint(f[5], 10, 64)
		ws, e2 := strconv.ParseUint(f[9], 10, 64)
		in, _ := strconv.ParseUint(f[11], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		out = append(out, diskRow{name, rs, ws, in})
	}
	return out
}
