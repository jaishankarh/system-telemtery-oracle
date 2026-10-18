package cli_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

const histFx = "dev-box-applications"

type histResult struct {
	Live    bool `json:"live"`
	Unclean bool `json:"unclean_previous_shutdown"`
	Samples []struct {
		Time time.Time `json:"time"`
	} `json:"samples"`
}

func ticks(p *harness.Program, n int) {
	for i := 0; i < n; i++ {
		p.Tick()
	}
}

func replayOut(t *testing.T, m *harness.Machine, args ...string) string {
	t.Helper()
	code, out, errs := m.Run(append([]string{"replay"}, args...)...)
	if code != 0 {
		t.Fatalf("replay %v exited %d: %s", args, code, errs)
	}
	return out
}

func TestHistoryFileIsFixedSizeAndRootOnly(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	p.Tick()
	st, err := os.Stat(m.HistoryPath())
	if err != nil {
		t.Fatal(err)
	}
	size := st.Size()
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	ticks(p, 300) // 10 minutes of samples
	p.Stop()
	st, _ = os.Stat(m.HistoryPath())
	if st.Size() != size {
		t.Fatalf("file grew from %d to %d", size, st.Size())
	}
	if size != history.FileSize || size > 16<<20 {
		t.Fatalf("size %d", size)
	}
}

func TestKilledMidRunLosesAtMostThirtySeconds(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 22) // 44 s; the last flush was at 30 s
	p.Kill()
	out := replayOut(t, m, "--last-boot", "--minutes", "5")
	if !strings.Contains(out, "12:00:30") || strings.Contains(out, "12:00:40") {
		t.Fatalf("expected samples up to 12:00:30 only:\n%s", out)
	}
	if !strings.Contains(out, "12:00:00") {
		t.Fatalf("lead-up missing:\n%s", out)
	}
}

func TestCrashMidFlushKeepsEarlierSamples(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 16) // flush at 30 s wrote samples 0,10,20,30
	p.Kill()
	m.TearNewestHistorySlot()
	out := replayOut(t, m, "--last-boot")
	if !strings.Contains(out, "12:00:20") || strings.Contains(out, "12:00:30") {
		t.Fatalf("torn slot should be skipped:\n%s", out)
	}
}

func TestPressureSpikeFlushesImmediately(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 2)
	m.WriteFile("/proc/pressure/memory", "some avg10=42.00 avg60=10.00 avg300=1.00 total=1\nfull avg10=20.00 avg60=1.00 avg300=0.00 total=1\n")
	p.Tick() // 6 s: no periodic flush due
	p.Kill()
	out := replayOut(t, m, "--last-boot")
	if !strings.Contains(out, "12:00:06") || !strings.Contains(out, "mem-psi 42.0%") {
		t.Fatalf("spike sample not on disk:\n%s", out)
	}
}

func TestThrashingFlushesImmediately(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 2)
	m.WriteFile("/proc/vmstat", "pswpin 100000\npswpout 100000\n")
	p.Tick()
	p.Kill()
	if out := replayOut(t, m, "--last-boot"); !strings.Contains(out, "THRASHING") {
		t.Fatalf("thrashing sample not on disk:\n%s", out)
	}
}

func TestUncleanShutdownIsSurfacedOnNextStart(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 16)
	p.Kill()
	var h histResult
	p2 := m.Start()
	p2.GetJSON("/api/history", &h)
	if !h.Unclean {
		t.Fatal("unclean shutdown not reported")
	}
	if out := replayOut(t, m); !strings.Contains(out, "ended without clean shutdown at 2026-10-19T12:00:30Z") {
		t.Fatalf("notice missing:\n%s", out)
	}
	p2.Stop()
	p3 := m.Start()
	p3.GetJSON("/api/history", &h)
	if h.Unclean {
		t.Fatal("clean stop reported as unclean")
	}
}

func TestReplayLastBootShowsCommandLinesAndAdviceEvents(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 10)
	p.AppendEvent(history.Event{Time: m.Clock.Now(), Kind: history.AdviceStarted, AdviceID: "uncapped-container", Title: "Uncapped container", Category: "settings_fix", Severity: "critical", Applications: []string{"code"}})
	p.Kill()
	out := replayOut(t, m, "--last-boot", "--minutes", "2")
	for _, want := range []string{"/usr/share/code/code --type=extensionHost", "node /usr/share/code/tsserver.js", "ADVICE advice_start", "Uncapped container"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestReplayReadsRunningInstanceIncludingUnflushedSamples(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 5) // 10 s sample is not flushed yet
	out := replayOut(t, m)
	if !strings.Contains(out, "12:00:10") {
		t.Fatalf("running instance not consulted:\n%s", out)
	}
	var h histResult
	p.GetJSON("/api/history?minutes=1", &h)
	if !h.Live || len(h.Samples) != 2 {
		t.Fatalf("api = %+v", h)
	}
}

func TestReplayAroundTimeWindow(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	ticks(p, 60) // 2 minutes
	p.Stop()
	out := replayOut(t, m, "--around", "12:01:00", "--minutes", "1")
	if !strings.Contains(out, "12:00:30") || !strings.Contains(out, "12:01:30") || strings.Contains(out, "12:00:20") || strings.Contains(out, "12:01:40") {
		t.Fatalf("window wrong:\n%s", out)
	}
}

func TestReplayWithoutHistoryAndWithUnwritableStateDir(t *testing.T) {
	m := harness.New(t, histFx)
	if code, _, errs := m.Run("replay"); code != 1 || !strings.Contains(errs, "no History") {
		t.Fatalf("code %d %q", code, errs)
	}
	m.Remove("/state")
	m.WriteFile("/state", "not a directory") // History cannot be created
	p := m.Start()
	p.Tick() // still runs
}
