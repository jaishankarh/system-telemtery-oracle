package cli_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type adviceDoc struct {
	WindowSeconds float64 `json:"window_seconds"`
	Advice        []struct {
		ID           string `json:"id"`
		Rule         string `json:"rule"`
		Category     string `json:"category"`
		Severity     string `json:"severity"`
		Title        string `json:"title"`
		Summary      string `json:"summary"`
		Applications []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"applications"`
		Evidence struct {
			WindowSeconds float64   `json:"window_seconds"`
			Start         time.Time `json:"start"`
			End           time.Time `json:"end"`
			Facts         []struct {
				Name  string   `json:"name"`
				Value *float64 `json:"value"`
				Avg   *float64 `json:"avg"`
				Unit  string   `json:"unit"`
				Text  string   `json:"text"`
			} `json:"facts"`
		} `json:"evidence"`
		Change *struct {
			Kind    string `json:"kind"`
			Target  string `json:"target"`
			Content string `json:"content"`
		} `json:"change"`
	} `json:"advice"`
	Pending []struct {
		ID string `json:"id"`
	} `json:"pending"`
	Disabled []struct {
		Rule   string `json:"rule"`
		Reason string `json:"reason"`
	} `json:"disabled"`
}

type adviceEvents struct {
	Events []struct {
		Type             string    `json:"type"`
		Time             time.Time `json:"time"`
		AdviceID         string    `json:"advice_id"`
		Rule             string    `json:"rule"`
		Category         string    `json:"category"`
		Severity         string    `json:"severity"`
		PreviousSeverity string    `json:"previous_severity"`
	} `json:"events"`
}

// thrashingBox: 32 GB laptop whose Applications need ~46 GiB, with memory
// pressure and Disk Swap in/out of 200 pages/s each.
func thrashingBox(t *testing.T, config string) (*harness.Machine, *harness.Program) {
	m := thrashingMachine(t, config)
	return m, m.Start("--config", "/etc/sys-telemetry-app/config.toml")
}

func thrashingMachine(t *testing.T, config string) *harness.Machine {
	m := harness.New(t, tree)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", config)
	m.SetProcessMemory(200, 45*1024*1024, 0)
	m.AddZramActivity("zram0", 0, 0)
	m.SetMemoryPSI(30, 12)
	return m
}

func thrashTick(m *harness.Machine, p *harness.Program) {
	m.AddSwapActivity(400, 400) // 200 pages/s over a 2s interval
	p.Tick()
}

func getAdvice(p *harness.Program) adviceDoc {
	var d adviceDoc
	p.GetJSON("/api/advice", &d)
	return d
}

func TestHardwareLimitFiresOnlyAfterSustainedWindow(t *testing.T) {
	m, p := thrashingBox(t, "")
	for i := 0; i < 140; i++ { // 280s < 5 min
		thrashTick(m, p)
	}
	if d := getAdvice(p); len(d.Advice) != 0 || len(d.Pending) != 1 {
		t.Fatalf("before the window: advice=%d pending=%d", len(d.Advice), len(d.Pending))
	}
	for i := 0; i < 20; i++ {
		thrashTick(m, p)
	}
	d := getAdvice(p)
	if len(d.Advice) != 1 || d.WindowSeconds != 300 {
		t.Fatalf("advice = %+v window %v", d.Advice, d.WindowSeconds)
	}
	a := d.Advice[0]
	if a.Category != "hardware_limit" || a.Severity != "critical" || a.Rule != "hardware-limit" || a.ID == "" || a.Change != nil {
		t.Fatalf("advice = %+v", a)
	}
	for _, want := range []string{"~46 GB", "32 GB", "Memory Deficit", "14 GB", "48 GB or 64 GB would cover it"} {
		if !strings.Contains(a.Summary, want) {
			t.Errorf("summary lacks %q: %s", want, a.Summary)
		}
	}
	if len(a.Applications) == 0 || a.Evidence.WindowSeconds < 300 || a.Evidence.End.Sub(a.Evidence.Start) < 300*time.Second || len(a.Evidence.Facts) < 5 {
		t.Fatalf("evidence = %+v apps %v", a.Evidence, a.Applications)
	}
}

func TestShortBuildSpikeDoesNotFireHardwareLimit(t *testing.T) {
	m, p := thrashingBox(t, "")
	for i := 0; i < 60; i++ { // two minutes of spike
		thrashTick(m, p)
	}
	m.SetMemoryPSI(0, 0)
	m.SetProcessMemory(200, 500*1024, 0)
	for i := 0; i < 200; i++ {
		p.Tick()
	}
	if d := getAdvice(p); len(d.Advice) != 0 || len(d.Pending) != 0 {
		t.Fatalf("advice after spike: %+v pending %v", d.Advice, d.Pending)
	}
	var ev adviceEvents
	p.GetJSON("/api/advice/events", &ev)
	if len(ev.Events) != 0 {
		t.Fatalf("events = %+v", ev.Events)
	}
}

func TestCompressedSwapThrashingIsNotAHardwareLimit(t *testing.T) {
	m, p := thrashingBox(t, `advisor_window = "30s"`)
	for i := 0; i < 30; i++ {
		m.AddZramActivity("zram0", 400, 400) // all swap traffic is on zram
		thrashTick(m, p)
	}
	if d := getAdvice(p); len(d.Advice) != 0 {
		t.Fatalf("advice = %+v", d.Advice)
	}
}

func TestWindowAndThresholdsComeFromConfig(t *testing.T) {
	m, p := thrashingBox(t, "advisor_window = \"30s\"\n")
	for i := 0; i < 17; i++ {
		thrashTick(m, p)
	}
	if d := getAdvice(p); len(d.Advice) != 1 || d.WindowSeconds != 30 {
		t.Fatalf("30s window: %+v", d)
	}
	m2, p2 := thrashingBox(t, "advisor_window = \"30s\"\nadvisor_psi_percent = 50\n")
	for i := 0; i < 30; i++ {
		thrashTick(m2, p2)
	}
	if d := getAdvice(p2); len(d.Advice) != 0 {
		t.Fatalf("PSI 30 is below a 50 threshold: %+v", d.Advice)
	}
	m3, p3 := thrashingBox(t, "advisor_window = \"30s\"\nadvisor_thrash_pages_per_sec = 500\n")
	for i := 0; i < 30; i++ {
		thrashTick(m3, p3)
	}
	if d := getAdvice(p3); len(d.Advice) != 0 {
		t.Fatalf("200 pages/s is below a 500 threshold: %+v", d.Advice)
	}
	m4, p4 := thrashingBox(t, "advisor_window = \"30s\"\nadvisor_working_set_percent = 99\n")
	m4.SetProcessMemory(200, 28*1024*1024, 0) // ~94% of RAM: over the default 90%, under 99%
	for i := 0; i < 30; i++ {
		thrashTick(m4, p4)
	}
	if d := getAdvice(p4); len(d.Advice) != 0 {
		t.Fatalf("Working Set under 99%%: %+v", d.Advice)
	}
}

func TestBadAdvisorConfigIsRejected(t *testing.T) {
	m := harness.New(t, tree)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", "advisor_window = \"soon\"\n")
	code, _, errs := m.Run("report", "--config", "/etc/sys-telemetry-app/config.toml")
	if code == 0 || !strings.Contains(errs, "advisor_window") {
		t.Fatalf("code %d stderr %q", code, errs)
	}
}

func TestDisabledAdviceIsListedWithReason(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	d := getAdvice(p)
	if len(d.Disabled) != 1 || d.Disabled[0].Rule != "hardware-limit" ||
		!strings.Contains(d.Disabled[0].Reason, "/proc/pressure/memory") {
		t.Fatalf("disabled = %+v", d.Disabled)
	}
	m.SetMemoryPSI(0, 0)
	p.Tick()
	d = getAdvice(p)
	if len(d.Disabled) != 1 || !strings.Contains(d.Disabled[0].Reason, "Disk Swap") ||
		!strings.Contains(d.Disabled[0].Reason, "/sys/block/zram0/stat") {
		t.Fatalf("both swap kinds without zram counters: %+v", d.Disabled)
	}
	m.AddZramActivity("zram0", 0, 0)
	p.Tick()
	if d = getAdvice(p); len(d.Disabled) != 0 {
		t.Fatalf("disabled = %+v", d.Disabled)
	}
	m.Remove("/proc/vmstat")
	p.Tick()
	if d = getAdvice(p); len(d.Disabled) != 1 || !strings.Contains(d.Disabled[0].Reason, "/proc/vmstat") {
		t.Fatalf("disabled = %+v", d.Disabled)
	}
}

func TestAdviceEventsStartSeverityChangeAndEnd(t *testing.T) {
	m, p := thrashingBox(t, "advisor_window = \"30s\"")
	for i := 0; i < 17; i++ {
		thrashTick(m, p)
	}
	m.SetProcessMemory(200, 28*1024*1024, 0) // Working Set ~30 GiB: over 90% of RAM but no Memory Deficit, so warning
	for i := 0; i < 6; i++ {                 // per-process memory refreshes every 10s
		thrashTick(m, p)
	}
	m.SetMemoryPSI(0, 0)
	p.Tick()
	var ev adviceEvents
	p.GetJSON("/api/advice/events", &ev)
	var types []string
	for _, e := range ev.Events {
		types = append(types, e.Type+":"+e.Severity)
		if e.AdviceID == "" || e.Rule != "hardware-limit" || e.Category != "hardware_limit" || e.Time.IsZero() {
			t.Errorf("event = %+v", e)
		}
	}
	got := strings.Join(types, ",")
	if got != "advice_started:critical,advice_severity_changed:warning,advice_ended:warning" {
		t.Fatalf("events = %s", got)
	}
	if ev.Events[1].PreviousSeverity != "critical" {
		t.Fatalf("event = %+v", ev.Events[1])
	}
	if d := getAdvice(p); len(d.Advice) != 0 {
		t.Fatalf("advice should have ended: %+v", d.Advice)
	}
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()[strings.LastIndex(l.Addr().String(), ":")+1:]
}

func TestReportShowsAdviceFromRunningInstanceAndWhenAlone(t *testing.T) {
	m := thrashingMachine(t, "advisor_window = \"30s\"")
	port := freePort(t)
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml", "--port", port)
	for i := 0; i < 17; i++ {
		thrashTick(m, p)
	}
	m.AddSwapActivity(200, 200) // report advances the shared fake clock 1s, which makes the program sample once more
	out := m.Report("--port", port, "--config", "/etc/sys-telemetry-app/config.toml")
	for _, want := range []string{"Advice", "Hardware Limit", "critical", "48 GB or 64 GB would cover it"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	// With nothing listening, report evaluates once and says the window cannot be verified.
	alone := harness.New(t, tree)
	out = alone.Report("--port", freePort(t))
	if !strings.Contains(out, "Advice") || !strings.Contains(out, "disabled") || !strings.Contains(out, "/proc/pressure/memory") {
		t.Errorf("report alone:\n%s", out)
	}
}

func TestDashboardHasAdvicePanelAtTop(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	_, html := p.Get("/")
	s := string(html)
	i, j := strings.Index(s, `id="advice"`), strings.Index(s, `id="applications"`)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("advice panel must come before the Applications view: %d %d", i, j)
	}
	_, js := p.Get("/app.js")
	if !strings.Contains(string(js), "/api/advice") {
		t.Fatal("dashboard script does not load /api/advice")
	}
}
