package cli_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

// Graceful degradation (ADR 0006): each variant is the same modern machine
// with one source taken away. Assertions go through the JSON API and `report`.

type availabilityItem struct {
	ID      string   `json:"id"`
	Feature string   `json:"feature"`
	Reason  string   `json:"reason"`
	Affects []string `json:"affects"`
}

type availabilityDoc struct {
	Environment struct {
		Cgroup            string   `json:"cgroup"`
		Systemd           bool     `json:"systemd"`
		ContainerRuntimes []string `json:"container_runtimes"`
		GPUs              []string `json:"gpus"`
	} `json:"environment"`
	Unavailable   []availabilityItem `json:"unavailable"`
	DisabledRules []struct {
		Rule   string `json:"rule"`
		Reason string `json:"reason"`
	} `json:"disabled_rules"`
}

func (a availabilityDoc) ids() string {
	var ids []string
	for _, u := range a.Unavailable {
		ids = append(ids, u.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func (a availabilityDoc) disabled(rule string) string {
	for _, r := range a.DisabledRules {
		if r.Rule == rule {
			return r.Reason
		}
	}
	return ""
}

type livePoint struct {
	MemoryPressureSome *float64 `json:"memory_pressure_some_percent"`
	TopApps            []struct {
		Name string `json:"name"`
	} `json:"top_apps"`
}

type degradedView struct {
	System struct {
		Pressure *struct{} `json:"pressure"`
		Swap     struct {
			Disk       *struct{} `json:"disk"`
			Compressed *struct{} `json:"compressed"`
		} `json:"swap"`
		Availability availabilityDoc `json:"availability"`
	}
	Apps      []application
	Processes []appProc
	Live      []livePoint
	Report    string
}

// observe runs the program on m for three samples, reads every API a view
// uses, and runs `report`. Nothing may crash or answer an error status.
func observe(t *testing.T, m *harness.Machine) degradedView {
	t.Helper()
	var v degradedView
	p := m.Start()
	p.Tick()
	p.Tick()
	p.GetJSON("/api/system", &v.System)
	var al appList
	p.GetJSON("/api/applications", &al)
	v.Apps = al.Applications
	var pl struct {
		Processes []appProc `json:"processes"`
	}
	p.GetJSON("/api/processes", &pl)
	v.Processes = pl.Processes
	var lv struct {
		Points []livePoint `json:"points"`
	}
	p.GetJSON("/api/live", &lv)
	v.Live = lv.Points
	var doc availabilityDoc
	p.GetJSON("/api/availability", &doc)
	if doc.ids() != v.System.Availability.ids() {
		t.Fatalf("/api/availability %q differs from /api/system %q", doc.ids(), v.System.Availability.ids())
	}
	if code, _ := p.Get("/"); code != 200 {
		t.Fatalf("dashboard status %d", code)
	}
	v.Report = m.Report()
	if len(v.Apps) == 0 || len(v.Processes) == 0 {
		t.Fatalf("no Applications (%d) or Processes (%d)", len(v.Apps), len(v.Processes))
	}
	return v
}

// wantUnavailable asserts the exact set of unavailable sources, that each has
// a reason and appears with it in `report`.
func (v degradedView) wantUnavailable(t *testing.T, ids string) {
	t.Helper()
	a := v.System.Availability
	if a.ids() != ids {
		t.Fatalf("unavailable = %q, want %q", a.ids(), ids)
	}
	for _, u := range a.Unavailable {
		if u.Feature == "" || u.Reason == "" || strings.Contains(u.Reason, "\n") {
			t.Fatalf("%s needs a feature and a one-line reason: %+v", u.ID, u)
		}
		if !strings.Contains(v.Report, u.Feature+": "+u.Reason) {
			t.Fatalf("report does not list %q with its reason:\n%s", u.ID, v.Report)
		}
	}
	if ids == "" && strings.Contains(v.Report, "Unavailable on this machine") {
		t.Fatalf("report lists unavailable data on a complete machine:\n%s", v.Report)
	}
}

func appNames(v degradedView) string {
	var n []string
	for _, a := range v.Apps {
		n = append(n, a.Name)
	}
	sort.Strings(n)
	return strings.Join(n, "|")
}

func TestModernMachineHasNothingUnavailable(t *testing.T) {
	v := observe(t, harness.NewModern(t))
	v.wantUnavailable(t, "")
	a := v.System.Availability
	if a.Environment.Cgroup != "v2" || !a.Environment.Systemd || len(a.Environment.ContainerRuntimes) == 0 || len(a.Environment.GPUs) == 0 {
		t.Fatalf("environment = %+v", a.Environment)
	}
	if len(a.DisabledRules) != 0 || v.System.Pressure == nil {
		t.Fatalf("disabled rules %v, pressure %v", a.DisabledRules, v.System.Pressure)
	}
}

func TestCgroupV1IsSupportedNotMissing(t *testing.T) {
	modern := observe(t, harness.NewModern(t))
	m := harness.NewModern(t)
	m.AsCgroupV1()
	v := observe(t, m)
	v.wantUnavailable(t, "")
	if v.System.Availability.Environment.Cgroup != "v1" {
		t.Fatalf("environment = %+v", v.System.Availability.Environment)
	}
	if appNames(v) != appNames(modern) {
		t.Fatalf("cgroup v1 groups differently:\n v1:     %s\n modern: %s", appNames(v), appNames(modern))
	}
}

func TestKernelWithoutPSIHidesPressureAndDisablesHardwareLimit(t *testing.T) {
	m := harness.NewModern(t)
	m.WithoutPSI()
	v := observe(t, m)
	v.wantUnavailable(t, "pressure")
	if v.System.Pressure != nil {
		t.Fatalf("pressure should be hidden (null), got %v", v.System.Pressure)
	}
	for _, p := range v.Live {
		if p.MemoryPressureSome != nil {
			t.Fatal("live points must not carry a pressure value (not zero) without PSI")
		}
	}
	why := v.System.Availability.disabled("Hardware Limit")
	if !strings.Contains(why, "Pressure stall information (PSI)") || strings.Contains(why, "\n") {
		t.Fatalf("Hardware Limit reason = %q", why)
	}
	if v.System.Availability.disabled("Swap wear") != "" {
		t.Fatal("Swap wear does not need PSI")
	}
	if !strings.Contains(v.Report, "Disabled: needs Pressure stall information (PSI)") {
		t.Fatalf("report does not list the disabled rule:\n%s", v.Report)
	}
}

func TestNoGPUHidesGPU(t *testing.T) {
	m := harness.NewModern(t)
	m.WithoutGPU()
	v := observe(t, m)
	v.wantUnavailable(t, "gpu")
	if len(v.System.Availability.Environment.GPUs) != 0 {
		t.Fatalf("gpus = %v", v.System.Availability.Environment.GPUs)
	}
}

func TestNoContainerRuntimeLeavesNoContainerRows(t *testing.T) {
	m := harness.NewModern(t)
	m.WithoutContainerRuntime()
	v := observe(t, m)
	v.wantUnavailable(t, "container_runtime")
	for _, a := range v.Apps {
		if a.Kind == "container" || a.Kind == "pod" {
			t.Fatalf("Application %q is a %s without a runtime", a.Name, a.Kind)
		}
	}
	if v.System.Availability.disabled("Uncapped container") == "" {
		t.Fatal("Uncapped container must be disabled without a runtime")
	}
}

func TestNoSystemdFallsBackToTheProcessTree(t *testing.T) {
	m := harness.NewModern(t)
	m.WithoutSystemd()
	v := observe(t, m)
	v.wantUnavailable(t, "systemd")
	if v.System.Availability.Environment.Systemd {
		t.Fatal("environment says systemd is present")
	}
	count := 0
	for _, a := range v.Apps {
		switch a.Kind {
		case "systemd", "snap", "flatpak":
			t.Fatalf("Application %q has unit kind %s without systemd", a.Name, a.Kind)
		}
		count += a.ProcessCount
	}
	if count != len(v.Processes) {
		t.Fatalf("every Process belongs to exactly one Application: %d in Applications, %d listed", count, len(v.Processes))
	}
}

func TestCompressedSwapOnlyHidesDiskSwap(t *testing.T) {
	m := harness.NewModern(t)
	m.CompressedSwapOnly()
	v := observe(t, m)
	v.wantUnavailable(t, "disk_swap")
	if v.System.Swap.Disk != nil || v.System.Swap.Compressed == nil {
		t.Fatalf("swap = %+v", v.System.Swap)
	}
	for _, rule := range []string{"Hardware Limit", "Swap wear", "Swappiness too aggressive"} {
		if !strings.Contains(v.System.Availability.disabled(rule), "Disk Swap") {
			t.Fatalf("%s should be disabled for lack of Disk Swap: %q", rule, v.System.Availability.disabled(rule))
		}
	}
	if v.System.Availability.disabled("Compressed Swap thrashing") != "" {
		t.Fatal("Compressed Swap thrashing works with Compressed Swap only")
	}
	if !strings.Contains(v.Report, "Disk Swap        none") {
		t.Fatalf("report:\n%s", v.Report)
	}
}

func TestOldKernelWithoutSmapsRollupHidesPSSButKeepsSwap(t *testing.T) {
	m := harness.NewModern(t)
	m.OldKernelWithoutSmapsRollup()
	v := observe(t, m)
	v.wantUnavailable(t, "smaps_rollup")
	swapSeen := false
	for _, p := range v.Processes {
		if p.PSS != nil {
			t.Fatalf("PID %d has PSS without smaps_rollup", p.PID)
		}
		swapSeen = swapSeen || (p.Swap != nil && *p.Swap > 0)
	}
	for _, a := range v.Apps {
		if a.PSS != nil {
			t.Fatalf("Application %q has PSS %d without smaps_rollup", a.Name, *a.PSS)
		}
	}
	if !swapSeen {
		t.Fatal("per-process swap should fall back to VmSwap")
	}
	for _, p := range v.Live {
		if len(p.TopApps) != 0 {
			t.Fatalf("RAM/swap split needs PSS; it must be hidden, got %v", p.TopApps)
		}
	}
	for _, rule := range []string{"Hardware Limit", "Browser process sprawl"} {
		if !strings.Contains(v.System.Availability.disabled(rule), "Per-process PSS") {
			t.Fatalf("%s reason = %q", rule, v.System.Availability.disabled(rule))
		}
	}
}

func TestOldMinimalMachineCombinesEveryGap(t *testing.T) {
	m := harness.NewModern(t)
	m.AsCgroupV1()
	m.WithoutPSI()
	m.WithoutGPU()
	m.WithoutContainerRuntime()
	m.WithoutSystemd()
	m.CompressedSwapOnly()
	m.OldKernelWithoutSmapsRollup()
	m.Remove("/proc/diskstats")
	m.Remove("/proc/loadavg")
	v := observe(t, m)
	v.wantUnavailable(t, "container_runtime,disk_swap,diskstats,gpu,loadavg,pressure,smaps_rollup,systemd")
}
