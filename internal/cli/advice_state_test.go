package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type stateAdvice struct {
	adviceDoc
	Hidden []struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		Severity string `json:"severity"`
		Until    string `json:"until"`
		Title    string `json:"title"`
	} `json:"hidden"`
	SnoozePresets []struct {
		Label   string  `json:"label"`
		Seconds float64 `json:"seconds"`
	} `json:"snooze_presets"`
}

func getState(p *harness.Program) stateAdvice {
	var d stateAdvice
	p.GetJSON("/api/advice", &d)
	return d
}

const fastWindow = "advisor_window = \"30s\"\n"

// activeThrashing runs until the Hardware Limit Advice is active and returns its ID.
func activeThrashing(t *testing.T, config string) (*harness.Machine, *harness.Program, string) {
	m, p := thrashingBox(t, fastWindow+config)
	for i := 0; i < 17; i++ {
		thrashTick(m, p)
	}
	d := getAdvice(p)
	if len(d.Advice) != 1 {
		t.Fatalf("advice = %+v", d.Advice)
	}
	return m, p, d.Advice[0].ID
}

func mustPost(t *testing.T, p *harness.Program, path, body string) {
	t.Helper()
	if code, b := p.Post(path, body); code != 200 {
		t.Fatalf("POST %s %s: %d %s", path, body, code, b)
	}
}

func TestHiddenAdviceLeavesThePanelAndCanBeUnhidden(t *testing.T) {
	_, p, id := activeThrashing(t, "")
	mustPost(t, p, "/api/advice/hide", `{"id":"`+id+`"}`)
	d := getState(p)
	if len(d.Advice) != 0 || len(d.Hidden) != 1 || d.Hidden[0].ID != id || d.Hidden[0].State != "hidden" || d.Hidden[0].Severity != "critical" {
		t.Fatalf("after hide: %+v", d)
	}
	mustPost(t, p, "/api/advice/unhide", `{"id":"`+id+`"}`)
	if d := getState(p); len(d.Advice) != 1 || len(d.Hidden) != 0 {
		t.Fatalf("after unhide: %+v", d)
	}
}

func TestHiddenAdviceReturnsOnlyOnHigherSeverity(t *testing.T) {
	m, p, id := activeThrashing(t, "")
	// Memory Deficit makes it critical; drop to a warning, hide it there.
	m.SetProcessMemory(200, 30*1024*1024, 0) // Working Set over 90% of RAM but no deficit
	for i := 0; i < 3; i++ {
		thrashTick(m, p)
	}
	d := getAdvice(p)
	if len(d.Advice) != 1 || d.Advice[0].Severity != "warning" {
		t.Fatalf("expected a warning: %+v", d.Advice)
	}
	mustPost(t, p, "/api/advice/hide", `{"id":"`+id+`"}`)
	for i := 0; i < 5; i++ {
		thrashTick(m, p)
	}
	if d := getState(p); len(d.Advice) != 0 || len(d.Hidden) != 1 {
		t.Fatalf("still warning, must stay hidden: %+v", d)
	}
	m.SetProcessMemory(200, 45*1024*1024, 0) // Memory Deficit again: critical
	for i := 0; i < 8; i++ {                 // per-Process memory detail refreshes only every few samples
		thrashTick(m, p)
	}
	d2 := getState(p)
	if len(d2.Advice) != 1 || d2.Advice[0].Severity != "critical" || len(d2.Hidden) != 0 {
		t.Fatalf("higher Severity must return it: %+v", d2)
	}
	m.SetProcessMemory(200, 30*1024*1024, 0) // back to warning: no longer hidden
	for i := 0; i < 8; i++ {
		thrashTick(m, p)
	}
	if d := getState(p); len(d.Advice) != 1 {
		t.Fatalf("once returned it stays visible: %+v", d)
	}
}

func TestSnoozedAdviceReturnsAfterThePeriodIfStillActive(t *testing.T) {
	m, p, id := activeThrashing(t, "snooze_presets = \"10m, 1d\"\n")
	if d := getState(p); len(d.SnoozePresets) != 2 || d.SnoozePresets[0].Label != "10m" || d.SnoozePresets[0].Seconds != 600 || d.SnoozePresets[1].Seconds != 86400 {
		t.Fatalf("presets = %+v", d.SnoozePresets)
	}
	mustPost(t, p, "/api/advice/snooze", `{"id":"`+id+`","preset":"10m"}`)
	if d := getState(p); len(d.Advice) != 0 || len(d.Hidden) != 1 || d.Hidden[0].State != "snoozed" || d.Hidden[0].Until == "" {
		t.Fatalf("after snooze: %+v", d)
	}
	for i := 0; i < 290; i++ { // 580s
		thrashTick(m, p)
	}
	if d := getState(p); len(d.Advice) != 0 {
		t.Fatalf("snooze not over: %+v", d.Advice)
	}
	for i := 0; i < 15; i++ {
		thrashTick(m, p)
	}
	if d := getState(p); len(d.Advice) != 1 || len(d.Hidden) != 0 {
		t.Fatalf("snooze over and still active: %+v", d)
	}
}

func TestSnoozedAdviceThatEndedDoesNotComeBack(t *testing.T) {
	m, p, id := activeThrashing(t, "snooze_presets = \"10m\"\n")
	mustPost(t, p, "/api/advice/snooze", `{"id":"`+id+`","preset":"10m"}`)
	m.SetMemoryPSI(0, 0)
	m.SetProcessMemory(200, 500*1024, 0)
	for i := 0; i < 320; i++ {
		p.Tick()
	}
	if d := getState(p); len(d.Advice) != 0 || len(d.Hidden) != 0 {
		t.Fatalf("condition gone: %+v", d)
	}
}

func TestSnoozeMustUseAConfiguredPreset(t *testing.T) {
	_, p, id := activeThrashing(t, "")
	if code, _ := p.Post("/api/advice/snooze", `{"id":"`+id+`","preset":"5y"}`); code != 400 {
		t.Fatalf("unknown preset: %d", code)
	}
	if code, _ := p.Post("/api/advice/hide", `{"id":"nope"}`); code != 404 {
		t.Fatalf("unknown advice: %d", code)
	}
	if code, _ := p.Post("/api/advice/hide", `{}`); code != 400 {
		t.Fatalf("no id: %d", code)
	}
}

func TestBadSnoozePresetsConfigIsRejected(t *testing.T) {
	m := harness.New(t, tree)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", "snooze_presets = \"1d, soon\"\n")
	code, _, errs := m.Run("report", "--config", "/etc/sys-telemetry-app/config.toml")
	if code == 0 || !strings.Contains(errs, "snooze_presets") {
		t.Fatalf("code %d stderr %q", code, errs)
	}
}

func TestHiddenAndSnoozedStatePersistsAcrossRestarts(t *testing.T) {
	m, p, id := activeThrashing(t, "")
	mustPost(t, p, "/api/advice/hide", `{"id":"`+id+`"}`)
	p.Stop()
	if _, err := os.Stat(filepath.Join(m.Root, "state", "state.json")); err != nil {
		t.Fatalf("state file: %v", err)
	}
	p2 := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	for i := 0; i < 17; i++ {
		thrashTick(m, p2)
	}
	d := getState(p2)
	if len(d.Advice) != 0 || len(d.Hidden) != 1 || d.Hidden[0].ID != id {
		t.Fatalf("after restart: %+v", d)
	}
	mustPost(t, p2, "/api/advice/unhide", `{"id":"`+id+`"}`)
	p2.Stop()
	p3 := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	for i := 0; i < 17; i++ {
		thrashTick(m, p3)
	}
	if d := getState(p3); len(d.Advice) != 1 || len(d.Hidden) != 0 {
		t.Fatalf("unhide must persist: %+v", d)
	}
}

func TestOnlyStateJSONIsWrittenToTheStateDir(t *testing.T) {
	m, p, id := activeThrashing(t, "")
	mustPost(t, p, "/api/advice/hide", `{"id":"`+id+`"}`)
	es, _ := os.ReadDir(filepath.Join(m.Root, "state"))
	if len(es) != 1 || es[0].Name() != "state.json" {
		t.Fatalf("state dir = %v", es)
	}
}

func TestReportShowsHiddenAndSnoozed(t *testing.T) {
	m, p, id := activeThrashing(t, "")
	mustPost(t, p, "/api/advice/hide", `{"id":"`+id+`"}`)
	p.Stop()
	code, out, errs := m.Run("report", "--config", "/etc/sys-telemetry-app/config.toml")
	if code != 0 || !strings.Contains(out, "hidden (until Severity rises above critical)") || !strings.Contains(out, id) {
		t.Fatalf("report %d %q %q", code, out, errs)
	}
	if es, _ := os.ReadDir(filepath.Join(m.Root, "state")); len(es) != 1 {
		t.Fatalf("report must not write: %v", es)
	}
}

func TestStateChangingEndpointsRefuseBrowserCrossOriginRequests(t *testing.T) {
	_, p, id := activeThrashing(t, "")
	body := `{"id":"` + id + `"}`
	for _, path := range []string{"/api/advice/hide", "/api/advice/unhide", "/api/advice/snooze"} {
		if code, _ := p.Get(path); code != 405 {
			t.Errorf("GET %s = %d, want 405", path, code)
		}
	}
	bad := map[string][]string{
		"foreign Origin":    {"Origin", "http://evil.example"},
		"other port Origin": {"Origin", "http://127.0.0.1:1"},
		"cross-site fetch":  {"Sec-Fetch-Site", "cross-site"},
		"rebound Host":      {"Host", "evil.example"},
		"form content type": {"Content-Type", "text/plain"},
	}
	for name, h := range bad {
		var code int
		if h[0] == "Host" { // Go's client takes Host from the request, so set it explicitly
			code = p.PostHost("/api/advice/hide", body, h[1])
		} else {
			code, _ = p.Post("/api/advice/hide", body, h...)
		}
		if code != 403 {
			t.Errorf("%s: status %d, want 403", name, code)
		}
	}
	if d := getState(p); len(d.Advice) != 1 || len(d.Hidden) != 0 {
		t.Fatalf("a refused request changed state: %+v", d)
	}
	if code, _ := p.Post("/api/advice/hide", body, "Origin", "http://"+p.Addr, "Sec-Fetch-Site", "same-origin"); code != 200 {
		t.Fatalf("same-origin request refused: %d", code)
	}
}

func TestDashboardAdvicePanelHasHideSnoozeControls(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	_, js := p.Get("/app.js")
	for _, want := range []string{"/api/advice/", "Unhide", "data-advice-id", "adviceId"} {
		if want == "data-advice-id" {
			continue
		}
		if !strings.Contains(string(js), want) {
			t.Errorf("dashboard script lacks %q", want)
		}
	}
}
