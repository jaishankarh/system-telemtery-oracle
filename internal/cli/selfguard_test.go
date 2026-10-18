package cli_test

import (
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type self struct {
	OOMScoreAdj    *int   `json:"oom_score_adj"`
	OOMProtected   bool   `json:"oom_protected"`
	MemoryCapBytes uint64 `json:"memory_cap_bytes"`
	CapSource      string `json:"memory_cap_source"`
	ShedCount      int    `json:"shed_count"`
}

func TestOwnProcessIsExemptFromOOMKillerOnStart(t *testing.T) {
	m := harness.New(t, fx)
	p := m.Start()
	var s self
	p.GetJSON("/api/self", &s)
	if v, ok := m.Host.OOMScoreAdj(); !ok || v != -1000 {
		t.Fatalf("host oom score = %v %v", v, ok)
	}
	if !s.OOMProtected || s.OOMScoreAdj == nil || *s.OOMScoreAdj != -1000 {
		t.Fatalf("self = %+v", s)
	}
}

func TestStillRunsWhenOOMScoreCannotBeSet(t *testing.T) {
	m := harness.New(t, fx)
	m.Host.OOMErr = errPerm
	p := m.Start()
	var s self
	p.GetJSON("/api/self", &s)
	if s.OOMProtected || s.OOMScoreAdj != nil {
		t.Fatalf("self = %+v", s)
	}
}

func TestMemoryCapDefaultsInternallyAndFollowsSystemdMemoryMax(t *testing.T) {
	m := harness.New(t, fx)
	p := m.Start()
	var s self
	p.GetJSON("/api/self", &s)
	if s.MemoryCapBytes != 64<<20 || s.CapSource != "internal" {
		t.Fatalf("default cap = %+v", s)
	}
	p.Stop()

	m2 := harness.New(t, fx)
	m2.WriteFile("/proc/self/cgroup", "0::/system.slice/sys-telemetry-app.service\n")
	m2.WriteFile("/sys/fs/cgroup/system.slice/sys-telemetry-app.service/memory.max", "20971520\n")
	p2 := m2.Start()
	p2.GetJSON("/api/self", &s)
	if s.MemoryCapBytes != 20<<20 || s.CapSource != "systemd" {
		t.Fatalf("systemd cap = %+v", s)
	}
}

func TestShedsCachesNearTheCapOnly(t *testing.T) {
	m := harness.New(t, fx)
	p := m.Start()
	m.Host.SetRSS(40 << 20) // 62% of 64 MiB
	p.Tick()
	var s self
	p.GetJSON("/api/self", &s)
	if s.ShedCount != 0 {
		t.Fatalf("shed below threshold: %+v", s)
	}
	m.Host.SetRSS(56 << 20) // 87%
	p.Tick()
	p.GetJSON("/api/self", &s)
	if s.ShedCount != 1 {
		t.Fatalf("expected one shed near cap: %+v", s)
	}
}

func TestInstallWritesAndEnablesUnitThenUninstallLeavesNothing(t *testing.T) {
	m := harness.New(t, fx)
	code, _, errs := m.Run("install")
	if code != 0 {
		t.Fatalf("install exited %d: %s", code, errs)
	}
	const path = "/etc/systemd/system/sys-telemetry-app.service"
	units := m.Host.Units()
	u, ok := units[path]
	if len(units) != 1 || !ok {
		t.Fatalf("units = %v", units)
	}
	for _, want := range []string{"ExecStart=/usr/local/bin/sys-telemetry-app run", "MemoryMax=64M", "OOMScoreAdjust=-1000", "WantedBy=multi-user.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
	wantCalls := "daemon-reload;enable --now sys-telemetry-app.service"
	if got := joinCalls(m.Host.Systemctls()); got != wantCalls {
		t.Fatalf("systemctl calls = %q", got)
	}

	code, _, errs = m.Run("uninstall")
	if code != 0 {
		t.Fatalf("uninstall exited %d: %s", code, errs)
	}
	if len(m.Host.Units()) != 0 {
		t.Fatalf("units left behind: %v", m.Host.Units())
	}
	wantCalls += ";disable --now sys-telemetry-app.service;daemon-reload"
	if got := joinCalls(m.Host.Systemctls()); got != wantCalls {
		t.Fatalf("systemctl calls = %q", got)
	}
}

func TestUninstallWhenNotInstalledIsHarmless(t *testing.T) {
	m := harness.New(t, fx)
	if code, _, errs := m.Run("uninstall"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if len(m.Host.Units()) != 0 {
		t.Fatal("unit appeared")
	}
}

func TestInstallFailureIsReported(t *testing.T) {
	m := harness.New(t, fx)
	m.Host.SystemctlEr = errPerm
	if code, _, errs := m.Run("install"); code == 0 || errs == "" {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
}

func TestReportAndOtherCommandsTouchNothingOnTheHost(t *testing.T) {
	m := harness.New(t, fx)
	m.Report()
	if _, ok := m.Host.OOMScoreAdj(); ok || m.Host.OOMWrites() != 0 || len(m.Host.Units()) != 0 || len(m.Host.Systemctls()) != 0 {
		t.Fatal("report changed the host")
	}
	p := m.Start()
	p.Tick()
	if len(m.Host.Units()) != 0 || len(m.Host.Systemctls()) != 0 || m.Host.OOMWrites() != 1 {
		t.Fatalf("run changed more than its own OOM score: writes=%d units=%v calls=%v", m.Host.OOMWrites(), m.Host.Units(), m.Host.Systemctls())
	}
}

func TestNearTheCapTheLiveBufferIsShedAndSaidSo(t *testing.T) {
	m := harness.New(t, fx)
	p := m.Start()
	for i := 0; i < 100; i++ { // 200 s of the 5-minute Live buffer
		p.Tick()
	}
	count := func() int {
		var l struct {
			Points []struct{} `json:"points"`
		}
		p.GetJSON("/api/live", &l)
		return len(l.Points)
	}
	before := count()
	m.Host.SetRSS(56 << 20)
	p.Tick()
	if after := count(); after >= before/2 {
		t.Fatalf("Live buffer held %d points, still %d after shedding near the cap", before, after)
	}
	if !strings.Contains(p.Stderr(), "shed") {
		t.Fatalf("shedding should be logged to stderr:\n%s", p.Stderr())
	}
}

func TestOwnMemoryLimitIsSetBelowTheCap(t *testing.T) {
	m := harness.New(t, fx)
	m.Start()
	got := m.Host.MemoryLimit()
	if got <= 0 || got >= 64<<20 {
		t.Fatalf("Go memory limit = %d, want a soft limit just under the 64 MiB cap", got)
	}
}
