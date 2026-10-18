package harness

import (
	"fmt"
	"strconv"
	"strings"
)

// Helpers for Advisor scenarios. They only edit files on the fake machine.

// SetMemoryPSI writes /proc/pressure/memory with the given avg10 percentages.
func (m *Machine) SetMemoryPSI(some, full float64) {
	m.t.Helper()
	m.WriteFile("/proc/pressure/memory", fmt.Sprintf(
		"some avg10=%.2f avg60=%.2f avg300=%.2f total=1000\nfull avg10=%.2f avg60=%.2f avg300=%.2f total=500\n",
		some, some, some, full, full, full))
}

// AddSwapActivity adds to the system-wide pswpin/pswpout counters in /proc/vmstat.
func (m *Machine) AddSwapActivity(pagesIn, pagesOut uint64) {
	m.t.Helper()
	m.bumpVmstat(map[string]uint64{"pswpin": pagesIn, "pswpout": pagesOut})
}

// AddZramActivity adds swap-in/out pages to /sys/block/<dev>/stat (read and
// write sectors). The system-wide counters are NOT changed; use AddSwapActivity too.
func (m *Machine) AddZramActivity(dev string, pagesIn, pagesOut uint64) {
	m.t.Helper()
	path := "/sys/block/" + dev + "/stat"
	var f []string
	if b, err := readOr(m, path); err == nil {
		f = strings.Fields(b)
	}
	for len(f) < 11 {
		f = append(f, "0")
	}
	add := func(i int, pages uint64) {
		n, _ := strconv.ParseUint(f[i], 10, 64)
		f[i] = strconv.FormatUint(n+pages*8, 10) // 8 sectors of 512 bytes per 4 KiB page
	}
	add(2, pagesIn)
	add(6, pagesOut)
	m.WriteFile(path, strings.Join(f, " ")+"\n")
}

// SetProcessMemory rewrites /proc/<pid>/smaps_rollup with the given PSS and swap in KiB.
func (m *Machine) SetProcessMemory(pid int, pssKiB, swapKiB uint64) {
	m.t.Helper()
	m.WriteFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid), fmt.Sprintf(
		"560ce6258000-7ffc18f63000 ---p 00000000 00:00 0                          [rollup]\nRss:  %d kB\nPss:  %d kB\nSwap:  %d kB\nSwapPss:  %d kB\n",
		pssKiB+swapKiB/2, pssKiB, swapKiB, swapKiB))
}

func readOr(m *Machine, path string) (string, error) { return m.tryRead(path) }

func (m *Machine) bumpVmstat(add map[string]uint64) {
	lines := strings.Split(m.ReadFile("/proc/vmstat"), "\n")
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) == 2 {
			if a, ok := add[f[0]]; ok {
				n, _ := strconv.ParseUint(f[1], 10, 64)
				lines[i] = fmt.Sprintf("%s %d", f[0], n+a)
			}
		}
	}
	m.WriteFile("/proc/vmstat", strings.Join(lines, "\n"))
}
