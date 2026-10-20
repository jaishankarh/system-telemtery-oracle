package harness

import (
	"fmt"
	"io"
	"net/http"
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

// AddOOMKill bumps the oom_kill counter in /proc/vmstat by n, as the kernel
// does each time its OOM killer fires.
func (m *Machine) AddOOMKill(n uint64) {
	m.t.Helper()
	m.bumpVmstat(map[string]uint64{"oom_kill": n})
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

// AddProcessCPU adds CPU time to a recorded process: ticks of 1/100 s added to
// the utime field of /proc/<pid>/stat. 200 ticks per 2 s interval is one full core.
func (m *Machine) AddProcessCPU(pid int, ticks uint64) {
	m.t.Helper()
	path := fmt.Sprintf("/proc/%d/stat", pid)
	b := m.ReadFile(path)
	s := string(b)
	c := strings.LastIndexByte(s, ')')
	f := strings.Fields(s[c+1:]) // f[0] is field 3; utime is field 14
	n, _ := strconv.ParseUint(f[11], 10, 64)
	f[11] = strconv.FormatUint(n+ticks, 10)
	m.WriteFile(path, s[:c+1]+" "+strings.Join(f, " ")+"\n")
}

// ReadFileOrEmpty returns a file's content, or "" when it does not exist, so a
// test can show that the program wrote nothing.
func (m *Machine) ReadFileOrEmpty(path string) string {
	s, err := m.tryRead(path)
	if err != nil {
		return ""
	}
	return s
}

// Post sends a POST with a JSON body to the program, like the dashboard does,
// and returns status and body. Headers (for example "Origin") are added on top.
func (p *Program) Post(path, body string, headers ...string) (int, []byte) {
	p.t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+p.Addr+path, strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// PostHost is Post with a different Host header, as a DNS-rebinding page would send.
func (p *Program) PostHost(path, body, host string) int {
	p.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+p.Addr+path, strings.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatalf("POST %s: %v", path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
