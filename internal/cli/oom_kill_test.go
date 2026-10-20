package cli_test

import (
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

// TestOOMKillIsReportedWithTheLikelyVictim: pid 300 ("node (dev)", 150000 kB
// PSS) vanishes from /proc in the same sample the kernel's oom_kill counter
// rises, as it would after the kernel killed it to free memory.
func TestOOMKillIsReportedWithTheLikelyVictim(t *testing.T) {
	m := harness.New(t, tree)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", "advisor_window = \"4s\"\n")
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	p.Tick() // a sample that has seen pid 300 alive, so its exit is a diff, not "never seen"

	m.Remove("/proc/300")
	m.AddOOMKill(1)
	p.Tick()
	p.Tick() // >= the 4s Sustained window at the default 2s interval
	p.Tick()

	d := getAdvice(p)
	if got := findAll(d, "oom-kill"); len(got) != 1 {
		t.Fatalf("advice ids = %+v", got)
	}
	var found bool
	for _, a := range d.Advice {
		if a.Rule != "oom-kill" {
			continue
		}
		found = true
		if a.Category != "hardware_limit" || a.Severity != "critical" || a.ID == "" {
			t.Fatalf("advice = %+v", a)
		}
		if !strings.Contains(a.Summary, "node (dev)") {
			t.Fatalf("summary does not name the victim: %s", a.Summary)
		}
		var sawPID, sawMiB bool
		for _, f := range a.Evidence.Facts {
			if f.Name == "oom_killed_process" && strings.Contains(f.Text, "pid 300") {
				sawPID = true
				sawMiB = strings.Contains(f.Text, "146 MiB")
			}
		}
		if !sawPID || !sawMiB {
			t.Fatalf("facts lack the victim's pid and memory: %+v", a.Evidence.Facts)
		}
	}
	if !found {
		t.Fatalf("no oom-kill advice: %+v", d.Advice)
	}
}

// TestOOMKillExpiresAfterItsMemoryWindow: a one-shot kernel event, unlike
// every other rule's condition, so it must stop being reported once its
// memory window (the Sustained window plus a buffer, see oom_kill.go) has
// passed, instead of staying stuck as Advice forever.
func TestOOMKillExpiresAfterItsMemoryWindow(t *testing.T) {
	m := harness.New(t, tree)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", "advisor_window = \"2s\"\n")
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	p.Tick()

	m.Remove("/proc/300")
	m.AddOOMKill(1)
	p.Tick()
	p.Tick()
	if got := findAll(getAdvice(p), "oom-kill"); len(got) != 1 {
		t.Fatalf("expected oom-kill Advice once Sustained, got %+v", got)
	}

	for i := 0; i < 20; i++ { // well past window(2s) + buffer(30s) at a 2s interval
		p.Tick()
	}
	if got := findAll(getAdvice(p), "oom-kill"); len(got) != 0 {
		t.Fatalf("oom-kill Advice should have expired: %+v", got)
	}
}

// TestNoOOMKillMeansNoAdvice: the common case, nothing fires.
func TestNoOOMKillMeansNoAdvice(t *testing.T) {
	m := harness.New(t, tree)
	p := m.Start()
	for i := 0; i < 5; i++ {
		p.Tick()
	}
	if got := findAll(getAdvice(p), "oom-kill"); len(got) != 0 {
		t.Fatalf("advice = %+v", got)
	}
}
