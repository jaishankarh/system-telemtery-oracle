package cli_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

const apps = "dev-box-applications"

type appProc struct {
	PID     int     `json:"pid"`
	PPID    int     `json:"ppid"`
	Command string  `json:"command"`
	Cwd     string  `json:"cwd"`
	Cgroup  string  `json:"cgroup"`
	PSS     *uint64 `json:"pss_bytes"`
	RSS     *uint64 `json:"rss_bytes"`
	Swap    *uint64 `json:"swap_bytes"`
}

type application struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	PIDs         []int     `json:"pids"`
	PrimaryPIDs  []int     `json:"primary_pids"`
	ProcessCount int       `json:"process_count"`
	RSS          *uint64   `json:"rss_bytes"`
	PSS          *uint64   `json:"pss_bytes"`
	Swap         *uint64   `json:"swap_bytes"`
	Processes    []appProc `json:"processes"`
}

type appList struct {
	Applications []application `json:"applications"`
}

func (l appList) find(t *testing.T, name string) application {
	t.Helper()
	for _, a := range l.Applications {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no Application %q in %v", name, l.names())
	return application{}
}

func (l appList) names() []string {
	var n []string
	for _, a := range l.Applications {
		n = append(n, a.Name)
	}
	return n
}

func sortedPIDs(a application) []int {
	p := append([]int(nil), a.PIDs...)
	sort.Ints(p)
	return p
}

func TestEveryProcessBelongsToExactlyOneNamedApplication(t *testing.T) {
	m := harness.New(t, apps)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)

	want := []struct {
		name, kind string
		pids       []int
	}{
		{"code", "systemd", []int{100, 101}},                        // desktop launch scope, no list needed
		{"org.mozilla.firefox", "flatpak", []int{200, 201}},         // flatpak from the cgroup path
		{"spotify", "snap", []int{500, 501}},                        // snap from the cgroup path
		{"org.gnome.Terminal", "systemd", []int{410}},               //
		{"ssh", "systemd", []int{600}},                              // service unit
		{"gnome-terminal-server", "ancestor", []int{300, 301, 400}}, // shell and dev server in a terminal scope: top ancestor
		{"mystery-tool", "ancestor", []int{700, 701}},               // session scope names nothing: top ancestor
		{"Kernel threads", "kernel", []int{2, 3}},
		{"Other", "other", []int{800}}, // no executable name anywhere
	}
	for _, w := range want {
		a := l.find(t, w.name)
		if a.Kind != w.kind || !equalInts(sortedPIDs(a), w.pids) || a.ProcessCount != len(w.pids) {
			t.Errorf("%s = kind %s pids %v count %d; want kind %s pids %v", w.name, a.Kind, a.PIDs, a.ProcessCount, w.kind, w.pids)
		}
	}

	var procs procList
	p.GetJSON("/api/processes", &procs)
	seen := map[int]int{}
	for _, a := range l.Applications {
		for _, pr := range a.Processes {
			seen[pr.PID]++
		}
	}
	for _, pr := range procs.Processes {
		if seen[pr.PID] != 1 {
			t.Errorf("pid %d belongs to %d Applications", pr.PID, seen[pr.PID])
		}
	}
	if len(seen) != len(procs.Processes) {
		t.Errorf("Applications hold %d processes, system has %d", len(seen), len(procs.Processes))
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestApplicationTotalsAddUp(t *testing.T) {
	m := harness.New(t, apps)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)
	code := l.find(t, "code")
	// pids 100 and 101: PSS 500000+200000 kB, RSS 800000+300000 kB, swap 120000 kB.
	if code.PSS == nil || *code.PSS != 700000*1024 || code.RSS == nil || *code.RSS != 1100000*1024 || code.Swap == nil || *code.Swap != 120000*1024 {
		t.Fatalf("code totals = %+v", code)
	}
	var pss, rss uint64
	for _, a := range l.Applications {
		if a.PSS != nil {
			pss += *a.PSS
		}
		if a.RSS != nil {
			rss += *a.RSS
		}
	}
	// independent sum of the fixture's per-process kB values
	wantPSS := uint64(9000+6000+500000+200000+300000+350000+30000+3000+150000+30000+180000+60000+5000+50000+10000+4000) * 1024
	if pss != wantPSS {
		t.Fatalf("sum of Application PSS = %d, want %d", pss, wantPSS)
	}
	if k := l.find(t, "Kernel threads"); k.PSS != nil || k.RSS != nil {
		t.Fatalf("kernel threads have no memory figures: %+v", k)
	}
	if got := code.PrimaryPIDs; len(got) != 1 || got[0] != 100 {
		t.Fatalf("primary PIDs = %v (the root of the Application)", got)
	}
}

func TestDrilldownShowsFullCommandLinesCwdAndCgroup(t *testing.T) {
	m := harness.New(t, apps)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)
	var tsserver appProc
	for _, pr := range l.find(t, "code").Processes {
		if pr.PID == 101 {
			tsserver = pr
		}
	}
	if tsserver.Command != "node /opt/acme-ide/server.js" || tsserver.Cwd != "/home/dev/my proj" ||
		tsserver.Cgroup != "/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome-code-4242.scope" || tsserver.PPID != 100 {
		t.Fatalf("pid 101 = %+v", tsserver)
	}
}

func TestApplicationsSortableAndDefaultByPSSPlusSwap(t *testing.T) {
	m := harness.New(t, apps)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)
	// PSS + swap: firefox 650000+1000000 > code 700000+120000 > spotify ...
	if got := strings.Join(l.names()[:3], ","); got != "org.mozilla.firefox,code,spotify" {
		t.Fatalf("default order = %s", got)
	}
	p.GetJSON("/api/applications?sort=name&order=asc", &l)
	if l.names()[0] != "code" || l.names()[1] != "gnome-terminal-server" {
		t.Fatalf("name order = %v", l.names())
	}
	p.GetJSON("/api/applications?sort=process_count&order=desc", &l)
	if l.Applications[0].ProcessCount != 3 {
		t.Fatalf("process_count desc = %v", l.names())
	}
	p.GetJSON("/api/applications?sort=pss_bytes&order=asc", &l)
	// smallest PSS first (Other, 4000 kB); the Application without memory figures stays last
	if n := l.names(); n[0] != "Other" || n[len(n)-1] != "Kernel threads" {
		t.Fatalf("pss asc last = %v", l.names())
	}
	if code, _ := p.Get("/api/applications?sort=bogus"); code != 400 {
		t.Fatalf("bad sort status %d", code)
	}
	p.GetJSON("/api/applications?filter=^spot", &l)
	if len(l.Applications) != 1 || l.Applications[0].Name != "spotify" {
		t.Fatalf("filter = %v", l.names())
	}
}

func TestReportListsApplications(t *testing.T) {
	m := harness.New(t, apps)
	out := m.Report()
	i := strings.Index(out, "Applications")
	if i < 0 {
		t.Fatalf("report has no Applications section:\n%s", out)
	}
	sec := out[i:]
	for _, w := range []string{"org.mozilla.firefox", "code", "spotify", "Kernel threads", "Other"} {
		if !strings.Contains(sec, w) {
			t.Errorf("report Applications section lacks %q:\n%s", w, sec)
		}
	}
	if strings.Index(sec, "org.mozilla.firefox") > strings.Index(sec, "spotify") {
		t.Errorf("report not ordered by PSS + swap:\n%s", sec)
	}
}

func TestDashboardDefaultsToApplicationsView(t *testing.T) {
	m := harness.New(t, apps)
	p := m.Start()
	_, html := p.Get("/")
	s := string(html)
	if strings.Index(s, `id="applications"`) < 0 || strings.Index(s, `id="applications"`) > strings.Index(s, `id="processes"`) {
		t.Fatal("Applications view must come before the Processes view")
	}
}

func TestGroupingDegradesWithoutCgroupV2(t *testing.T) {
	m := harness.New(t, apps)
	// pid 500 on cgroup v1 (systemd hierarchy among others), pid 100 without any cgroup file.
	m.WriteFile("/proc/500/cgroup", "12:memory:/user.slice\n1:name=systemd:/user.slice/user-1000.slice/user@1000.service/app.slice/snap.spotify.spotify-5f2a.scope\n")
	m.Remove("/proc/100/cgroup")
	m.Remove("/proc/101/cgroup")
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)
	if a := l.find(t, "spotify"); a.Kind != "snap" || len(a.PIDs) != 2 {
		t.Fatalf("cgroup v1 snap = %+v", a)
	}
	if a := l.find(t, "acme-ide"); a.Kind != "ancestor" || !equalInts(sortedPIDs(a), []int{100, 101}) {
		t.Fatalf("without cgroup, acme-ide falls back to its top ancestor: %+v", a)
	}
}
