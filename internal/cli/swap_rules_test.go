package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

func swapBox(t *testing.T, cachedKiB string, swappiness string) (*harness.Machine, *harnessProgram) {
	m := harness.New(t, tree)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", "advisor_window = \"30s\"\n")
	m.WriteFile("/proc/meminfo", strings.Replace(m.ReadFile("/proc/meminfo"), "Cached:      7000000 kB", "Cached:      "+cachedKiB+" kB", 1))
	m.WriteFile("/proc/sys/vm/swappiness", swappiness+"\n")
	m.SetMemoryPSI(0, 0)
	m.AddZramActivity("zram0", 0, 0)
	return m, m.Start("--config", "/etc/sys-telemetry-app/config.toml")
}

type harnessProgram = harness.Program

func findAdvice(d adviceDoc, rule string) *struct {
	ID, Category, Severity, Title, Summary, Kind, Target, Content string
	Facts                                                         map[string]float64
} {
	for _, a := range d.Advice {
		if a.Rule != rule {
			continue
		}
		r := &struct {
			ID, Category, Severity, Title, Summary, Kind, Target, Content string
			Facts                                                         map[string]float64
		}{ID: a.ID, Category: a.Category, Severity: a.Severity, Title: a.Title, Summary: a.Summary, Facts: map[string]float64{}}
		if a.Change != nil {
			r.Kind, r.Target, r.Content = a.Change.Kind, a.Change.Target, a.Change.Content
		}
		for _, f := range a.Evidence.Facts {
			if f.Value != nil {
				r.Facts[f.Name] = *f.Value
			}
		}
		return r
	}
	return nil
}

func swapTicks(m *harness.Machine, p *harnessProgram, n int, diskIn, diskOut, zramIn, zramOut uint64) {
	for i := 0; i < n; i++ {
		if zramIn+zramOut > 0 {
			m.AddZramActivity("zram0", zramIn, zramOut)
		}
		m.AddSwapActivity(diskIn+zramIn, diskOut+zramOut)
		p.Tick()
	}
}

func TestSwappinessRuleFiresWhenDiskSwapIsWrittenWithLargePageCache(t *testing.T) {
	m, p := swapBox(t, "12000000", "60") // 11.4 GiB of 31.25 GiB is page cache
	swapTicks(m, p, 10, 0, 100, 0, 0)    // 50 pages/s out of Disk Swap
	if findAdvice(getAdvice(p), "swappiness-too-aggressive") != nil {
		t.Fatal("fired before the Sustained window")
	}
	swapTicks(m, p, 20, 0, 100, 0, 0)
	a := findAdvice(getAdvice(p), "swappiness-too-aggressive")
	if a == nil {
		t.Fatalf("no advice: %+v", getAdvice(p))
	}
	if a.Category != "settings_fix" || a.Kind != "sysctl.d" || a.Target != "/etc/sysctl.d/99-swappiness.conf" {
		t.Fatalf("advice = %+v", a)
	}
	for _, want := range []string{"vm.swappiness = 10", "# Reason:"} {
		if !strings.Contains(a.Content, want) {
			t.Errorf("sysctl text lacks %q:\n%s", want, a.Content)
		}
	}
	if a.Facts["swappiness"] != 60 {
		t.Errorf("facts = %v", a.Facts)
	}
	if got := m.ReadFile("/proc/sys/vm/swappiness"); strings.TrimSpace(got) != "60" {
		t.Errorf("swappiness was changed to %q", got)
	}
}

func TestSwappinessRuleStaysQuietOtherwise(t *testing.T) {
	cases := map[string]struct {
		cached, swappiness string
		out                uint64
	}{
		"small page cache":    {"2000000", "60", 100},
		"already low":         {"12000000", "10", 100},
		"no Disk Swap writes": {"12000000", "60", 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, p := swapBox(t, c.cached, c.swappiness)
			swapTicks(m, p, 30, 0, c.out, 0, 0)
			if d := getAdvice(p); findAdvice(d, "swappiness-too-aggressive") != nil || len(d.Pending) != 0 {
				t.Fatalf("advice = %+v pending %v", d.Advice, d.Pending)
			}
		})
	}
}

func TestSwappinessRuleIsDisabledWithoutSwappinessFile(t *testing.T) {
	m, p := swapBox(t, "12000000", "60")
	m.Remove("/proc/sys/vm/swappiness")
	p.Tick()
	found := false
	for _, x := range getAdvice(p).Disabled {
		if x.Rule == "swappiness-too-aggressive" && strings.Contains(x.Reason, "/proc/sys/vm/swappiness") {
			found = true
		}
	}
	if !found {
		t.Fatalf("disabled = %+v", getAdvice(p).Disabled)
	}
}

func TestCompressedSwapThrashingIsExplainedAsCPUCost(t *testing.T) {
	m, p := swapBox(t, "2000000", "60")
	swapTicks(m, p, 30, 0, 0, 400, 400) // 200 pages/s each way, all on zram
	d := getAdvice(p)
	a := findAdvice(d, "compressed-swap-thrashing")
	if a == nil {
		t.Fatalf("no advice: %+v", d)
	}
	if a.Category != "settings_fix" || a.Kind != "sysctl.d" || !strings.Contains(a.Content, "vm.page-cluster = 0") ||
		!strings.Contains(a.Summary, "CPU") || !strings.Contains(a.Summary, "zram") {
		t.Fatalf("advice = %+v", a)
	}
	if findAdvice(d, "hardware-limit") != nil {
		t.Fatal("Compressed Swap Thrashing must not be a Hardware Limit")
	}
}

func TestCompressedSwapThrashingStaysQuietWhenDiskSwapThrashesToo(t *testing.T) {
	m, p := swapBox(t, "2000000", "60")
	swapTicks(m, p, 30, 400, 400, 400, 400)
	if findAdvice(getAdvice(p), "compressed-swap-thrashing") != nil {
		t.Fatal("Disk Swap is Thrashing too: that is not a CPU-only cost")
	}
	m2, p2 := swapBox(t, "2000000", "60")
	swapTicks(m2, p2, 30, 0, 0, 20, 20) // 10 pages/s: not Thrashing
	if findAdvice(getAdvice(p2), "compressed-swap-thrashing") != nil {
		t.Fatal("below the Thrashing threshold")
	}
}

func TestSwapWearShowsDailyWriteEstimateInGBPerDay(t *testing.T) {
	m, p := swapBox(t, "2000000", "60")
	swapTicks(m, p, 1000, 0, 400, 0, 0) // 200 pages/s of 4 KiB = ~70.8 GB/day for 33 minutes
	d := getAdvice(p)
	a := findAdvice(d, "swap-wear")
	if a == nil {
		t.Fatalf("no advice: %+v", d)
	}
	gb := a.Facts["disk_swap_write_gb_per_day"]
	if gb < 60 || gb > 80 || !strings.Contains(a.Title, "GB/day") || a.Severity != "warning" {
		t.Fatalf("estimate %v advice %+v", gb, a)
	}
	if a.Kind != "zram-generator" || !strings.Contains(a.Content, "swap-priority = 100") || !strings.Contains(a.Summary, "Compressed Swap") {
		t.Fatalf("advice = %+v", a)
	}
}

func TestSwapWearStaysQuietForBurstsAndLowRates(t *testing.T) {
	m, p := swapBox(t, "2000000", "60")
	swapTicks(m, p, 60, 0, 400, 0, 0) // a two minute burst
	swapTicks(m, p, 600, 0, 0, 0, 0)
	if d := getAdvice(p); findAdvice(d, "swap-wear") != nil || len(d.Pending) != 0 {
		t.Fatalf("a burst is not swap wear: %+v pending %v", d.Advice, d.Pending)
	}
	m2, p2 := swapBox(t, "2000000", "60")
	swapTicks(m2, p2, 1000, 0, 20, 0, 0) // 5 pages/s is ~1.8 GB/day
	if d := getAdvice(p2); findAdvice(d, "swap-wear") != nil {
		t.Fatalf("low rate: %+v", d.Advice)
	}
}

func TestSwapRulesNeverChangeTheSystem(t *testing.T) {
	m, p := swapBox(t, "12000000", "60")
	swapTicks(m, p, 40, 0, 400, 0, 0)
	for _, f := range []string{"/etc/sysctl.d/99-swappiness.conf", "/etc/sysctl.d/99-zram-swap.conf", "/etc/systemd/zram-generator.conf"} {
		if _, err := os.Stat(filepath.Join(m.Root, f)); err == nil {
			t.Errorf("%s was written", f)
		}
	}
}
