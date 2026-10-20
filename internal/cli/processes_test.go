package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

const tree = "dev-box-process-tree"

type proc struct {
	PID       int      `json:"pid"`
	User      string   `json:"user"`
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	RSS       *uint64  `json:"rss_bytes"`
	PSS       *uint64  `json:"pss_bytes"`
	Swap      *uint64  `json:"swap_bytes"`
	CPU       *float64 `json:"cpu_percent"`
	DiskRead  *float64 `json:"disk_read_bytes_per_sec"`
	DiskWrite *float64 `json:"disk_write_bytes_per_sec"`
	Threads   int      `json:"threads"`
	OpenFDs   *int     `json:"open_fds"`
}

type procList struct {
	Processes []proc   `json:"processes"`
	Missing   []string `json:"missing"`
}

func pids(l procList) string {
	var s []string
	for _, p := range l.Processes {
		s = append(s, fmt.Sprint(p.PID))
	}
	return strings.Join(s, ",")
}

func byPID(l procList, pid int) proc {
	for _, p := range l.Processes {
		if p.PID == pid {
			return p
		}
	}
	return proc{PID: -1}
}

func TestProcessesListShowsEveryColumn(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	var l procList
	p.GetJSON("/api/processes", &l)
	if len(l.Processes) != 5 {
		t.Fatalf("want 5 processes, got %s", pids(l))
	}
	c := byPID(l, 100)
	if c.User != "dev" || c.Name != "code" ||
		c.Command != "/usr/share/code/code --type=extensionHost --folder=/home/dev/my proj" ||
		c.RSS == nil || *c.RSS != 800000*1024 || c.PSS == nil || *c.PSS != 500000*1024 ||
		c.Swap == nil || *c.Swap != 120000*1024 || c.Threads != 40 || c.OpenFDs == nil || *c.OpenFDs != 150 {
		t.Fatalf("pid 100 = %+v", c)
	}
	if c.CPU != nil || c.DiskRead != nil || c.DiskWrite != nil {
		t.Fatal("rates must be absent on the first sample")
	}
	if w := byPID(l, 200); w.Name != "Web Content" {
		t.Fatalf("comm with a space: %+v", w)
	}
	if n := byPID(l, 300); n.Name != "node (dev)" {
		t.Fatalf("comm with parentheses: %+v", n)
	}
	if r := byPID(l, 1); r.User != "root" {
		t.Fatalf("user = %q", r.User)
	}
	k := byPID(l, 2)
	if k.Command != "[kthreadd]" || k.PSS != nil || k.Swap != nil {
		t.Fatalf("kernel thread = %+v", k)
	}
}

func TestProcessCPUAndDiskRatesFollowInterval(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	// 2s later pid 100 used 200 more ticks (100 ticks/s => one full core), wrote 4000 more bytes, read 2000.
	m.WriteFile("/proc/100/stat", strings.Replace(m.ReadFile("/proc/100/stat"), " 4000 1000 ", " 4200 1000 ", 1))
	m.WriteFile("/proc/100/io", "read_bytes: 10002000\nwrite_bytes: 4004000\n")
	p.Tick()
	var l procList
	p.GetJSON("/api/processes", &l)
	c := byPID(l, 100)
	if c.CPU == nil || *c.CPU != 100 || c.DiskRead == nil || *c.DiskRead != 1000 || c.DiskWrite == nil || *c.DiskWrite != 2000 {
		t.Fatalf("rates = %+v", c)
	}
	if i := byPID(l, 1); i.CPU == nil || *i.CPU != 0 {
		t.Fatalf("idle process cpu = %v", i.CPU)
	}
}

func TestPSSAndSwapRefreshEveryTenSeconds(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	m.WriteFile("/proc/300/smaps_rollup", "Rss: 1 kB\nPss: 999 kB\nSwap: 77 kB\n")
	m.WriteFile("/proc/300/status", "Name:\tnode\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t 200001 kB\nThreads:\t8\n")
	for i := 0; i < 4; i++ { // 8s
		p.Tick()
	}
	var l procList
	p.GetJSON("/api/processes", &l)
	n := byPID(l, 300)
	if *n.PSS != 150000*1024 || *n.Swap != 0 {
		t.Fatalf("PSS refreshed too early: %+v", n)
	}
	if *n.RSS != 200001*1024 {
		t.Fatalf("RSS follows the sampling interval: %d", *n.RSS)
	}
	p.Tick() // 10s
	p.GetJSON("/api/processes", &l)
	n = byPID(l, 300)
	if *n.PSS != 999*1024 || *n.Swap != 77*1024 {
		t.Fatalf("PSS not refreshed at 10s: %+v", n)
	}
}

func TestProcessesSortAndFilter(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	get := func(q string) procList {
		var l procList
		p.GetJSON("/api/processes?"+q, &l)
		return l
	}
	for q, want := range map[string]string{
		"sort=pid":                         "1,2,100,200,300",
		"sort=pid&order=desc":              "300,200,100,2,1",
		"":                                 "200,100,300,1,2", // default: PSS + swap, largest first
		"sort=pss_bytes&order=desc":        "100,200,300,1,2",
		"sort=swap_bytes&order=desc":       "200,100,1,300,2",
		"sort=rss_bytes&order=desc":        "100,200,300,1,2",
		"sort=threads&order=desc":          "100,200,300,1,2",
		"sort=open_fds&order=desc":         "100,200,300,1,2",
		"sort=user&order=desc&filter=.":    "1,2,100,200,300",
		"sort=name":                        "100,2,300,1,200",
		"sort=command":                     "1,200,100,2,300",
		"filter=100":                       "100",
		"filter=^node":                     "300",
		"filter=firefox":                   "200", // matches the command line
		"filter=^(code|systemd)$&sort=pid": "1,100",
		"filter=Web+Content":               "200",
	} {
		if got := pids(get(q)); got != want {
			t.Errorf("?%s = %s, want %s", q, got, want)
		}
	}
	if code, _ := p.Get("/api/processes?filter=(unclosed"); code != 400 {
		t.Fatalf("bad regex status = %d", code)
	}
	if code, _ := p.Get("/api/processes?sort=bogus"); code != 400 {
		t.Fatalf("bad sort status = %d", code)
	}
}

func TestMissingPerProcessSourcesDegrade(t *testing.T) {
	m := harness.New(t, tree)
	m.Remove("/proc/100/smaps_rollup")
	m.Remove("/proc/100/io")
	m.Remove("/proc/200") // process that vanished
	p := m.Start()
	var l procList
	p.GetJSON("/api/processes", &l)
	c := byPID(l, 100)
	if c.PSS != nil || c.Swap == nil || *c.Swap != 120000*1024 || c.DiskRead != nil {
		t.Fatalf("old-kernel degrade: %+v", c)
	}
	if len(l.Processes) != 4 {
		t.Fatalf("got %s", pids(l))
	}
	if got := strings.Join(l.Missing, ","); got != "/proc/PID/io,/proc/PID/smaps_rollup" {
		t.Fatalf("missing = %q", got)
	}
}

func TestReportListsTopProcessesByPSSPlusSwap(t *testing.T) {
	m := harness.New(t, tree)
	out := m.Report()
	i := strings.Index(out, "Top processes by PSS + swap")
	if i < 0 {
		t.Fatalf("no process section:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(out[i:]), "\n")
	rows := lines[2:] // title and column header first
	if len(rows) < 4 {
		t.Fatalf("rows = %v", rows)
	}
	for k, want := range []struct{ pid, user string }{{"200", "dev"}, {"100", "dev"}, {"300", "dev"}, {"1", "root"}} {
		f := strings.Fields(rows[k])
		if f[0] != want.pid || f[1] != want.user {
			t.Errorf("row %d = %q, want pid %s user %s", k, rows[k], want.pid, want.user)
		}
	}
	for _, want := range []string{"293.0 MiB", "878.9 MiB", "/usr/lib/firefox/firefox -contentproc -childID 1"} {
		if !strings.Contains(rows[0], want) {
			t.Errorf("first row lacks %q: %s", want, rows[0])
		}
	}
}
