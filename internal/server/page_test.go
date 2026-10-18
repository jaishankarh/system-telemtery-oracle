package server_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// The dashboard has no browser test. Its logic lives in small plain functions
// (replay.js) and the page scripts are run with node against a tiny DOM stand-in
// and the real program's API. Both tests skip when node is not installed.

var pageScripts = []string{"index.html", "replay.js", "app.js", "timeslider.js", "graphs.js", "style.css"}

func nodeOrSkip(t *testing.T) string {
	t.Helper()
	n, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed: dashboard script checks skipped")
	}
	return n
}

// serveAssets copies the assets the program serves into a temp dir.
func serveAssets(t *testing.T, p *harness.Program) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range pageScripts {
		code, b := p.Get("/" + f)
		if code != 200 {
			t.Fatalf("GET /%s = %d", f, code)
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	return dir
}

func runNode(t *testing.T, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(nodeOrSkip(t), args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReplayLogicFunctions(t *testing.T) {
	nodeOrSkip(t)
	m := harness.New(t, "dev-box-applications")
	p := m.Start()
	p.Tick()
	dir := serveAssets(t, p)
	if out := runNode(t, []string{"TZ=UTC"}, "testdata/replay_logic.js", dir); out != "ok" {
		t.Fatalf("got %q", out)
	}
	for _, f := range []string{"app.js", "graphs.js", "timeslider.js"} { // graphs.js needs uPlot and a canvas, so it is only syntax-checked
		runNode(t, nil, "--check", filepath.Join(dir, f))
	}
}

func TestPageLoadsRendersApplicationRowsAndReplaysAnEarlierTime(t *testing.T) {
	nodeOrSkip(t)
	m := harness.New(t, "dev-box-applications")
	p := m.Start()
	for i := 0; i < 5; i++ {
		p.Tick()
	}
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	p.AppendEvent(history.Event{Time: start.Add(4 * time.Second), Kind: history.AdviceStarted, AdviceID: "thrash", Title: "Thrashing", Category: "hardware_limit", Severity: "warning"})
	for i := 0; i < 25; i++ {
		p.Tick() // 60 s of History; slider 0..1000 maps to 12:00:00..12:01:00
	}
	if code, b := p.Get("/"); code != 200 || !strings.Contains(string(b), `id="time-slider"`) || !strings.Contains(string(b), `src="replay.js"`) {
		t.Fatalf("page does not carry the slider: %d", code)
	}
	dir := serveAssets(t, p)
	out := runNode(t, []string{"SLIDER=250", "TZ=UTC"}, "testdata/dom_smoke.js", dir, "http://"+p.Addr)
	var r struct {
		LiveRows, ReplayRows, BackToLiveRows [][]string
		LiveAdviceControls                   int
		SliderEnabled                        bool
		ReplayLabel, Banner, AppNote         string
		ProcNote                             string
		ProcRows                             int
		AdviceTitles                         []string
		ReplayAdviceControls                 int
		ReplayGraphPoints, ReplayGraphEvents int
		LiveButtonHidden                     bool
		BackToLiveLabel                      string
		BackToLiveBannerHidden               bool
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(r.LiveRows) == 0 {
		t.Fatalf("the page rendered no Application rows:\n%s", out)
	}
	if !r.SliderEnabled || len(r.ReplayRows) == 0 {
		t.Fatalf("Replay rendered no Application rows:\n%s", out)
	}
	if !strings.Contains(r.Banner, "Replay of") || !strings.Contains(r.Banner, "12:00:") || r.ReplayLabel == "Live" {
		t.Fatalf("Replay does not say which time it shows: %q / %q", r.Banner, r.ReplayLabel)
	}
	if !strings.Contains(r.AppNote, "Applications") || !strings.Contains(r.ProcNote, "Processes") {
		t.Fatalf("truncation not stated: %q / %q", r.AppNote, r.ProcNote)
	}
	if r.ProcRows == 0 || r.ReplayGraphPoints == 0 || r.ReplayGraphEvents != 1 {
		t.Fatalf("Processes %d, graph points %d, graph events %d", r.ProcRows, r.ReplayGraphPoints, r.ReplayGraphEvents)
	}
	if len(r.AdviceTitles) != 1 || !strings.Contains(r.AdviceTitles[0], "Thrashing") {
		t.Fatalf("Advice at that time: %v", r.AdviceTitles)
	}
	if r.ReplayAdviceControls != 0 {
		t.Fatalf("Hide/Snooze controls offered while viewing the past: %d", r.ReplayAdviceControls)
	}
	if r.BackToLiveLabel != "Live" || !r.BackToLiveBannerHidden || len(r.BackToLiveRows) == 0 {
		t.Fatalf("Back to Live: %+v", r)
	}
}
