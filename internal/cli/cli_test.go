package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type sys struct {
	Time time.Time `json:"time"`
	CPU  struct {
		TotalPercent *float64 `json:"total_percent"`
		Load1        *float64 `json:"load1"`
		Cores        int      `json:"cores"`
	} `json:"cpu"`
	Memory *struct {
		TotalBytes     uint64 `json:"total_bytes"`
		AvailableBytes uint64 `json:"available_bytes"`
		UsedBytes      uint64 `json:"used_bytes"`
		CachedBytes    uint64 `json:"cached_bytes"`
		BuffersBytes   uint64 `json:"buffers_bytes"`
		DirtyBytes     uint64 `json:"dirty_bytes"`
	} `json:"memory"`
	Swap struct {
		Disk *struct {
			TotalBytes uint64 `json:"total_bytes"`
			UsedBytes  uint64 `json:"used_bytes"`
		} `json:"disk"`
		Compressed *struct {
			TotalBytes   uint64 `json:"total_bytes"`
			UsedBytes    uint64 `json:"used_bytes"`
			RAMUsedBytes uint64 `json:"ram_used_bytes"`
		} `json:"compressed"`
		InPagesPerSec  *float64 `json:"in_pages_per_sec"`
		OutPagesPerSec *float64 `json:"out_pages_per_sec"`
	} `json:"swap"`
	Missing []string `json:"missing"`
}

const fx = "laptop-disk-and-zram"

func TestAPIShowsMemoryAndSplitSwap(t *testing.T) {
	m := harness.New(t, fx)
	p := m.Start()
	var s sys
	p.GetJSON("/api/system", &s)
	if s.Memory == nil || s.Memory.TotalBytes != 32768000*1024 || s.Memory.AvailableBytes != 9000000*1024 ||
		s.Memory.UsedBytes != (32768000-9000000)*1024 || s.Memory.CachedBytes != 7000000*1024 ||
		s.Memory.BuffersBytes != 400000*1024 || s.Memory.DirtyBytes != 2048*1024 {
		t.Fatalf("memory = %+v", s.Memory)
	}
	if s.Swap.Disk == nil || s.Swap.Disk.TotalBytes != 8388608*1024 || s.Swap.Disk.UsedBytes != 3000000*1024 {
		t.Fatalf("disk swap = %+v", s.Swap.Disk)
	}
	c := s.Swap.Compressed
	if c == nil || c.TotalBytes != 4194304*1024 || c.UsedBytes != 582912*1024 || c.RAMUsedBytes != 160000000 {
		t.Fatalf("compressed swap = %+v", c)
	}
	if s.CPU.Load1 == nil || *s.CPU.Load1 != 1.5 || s.CPU.Cores != 2 {
		t.Fatalf("cpu = %+v", s.CPU)
	}
	if s.CPU.TotalPercent != nil || s.Swap.InPagesPerSec != nil {
		t.Fatal("rates must be absent on the first sample")
	}
}

func TestRatesAppearAfterSecondSample(t *testing.T) {
	m := harness.New(t, fx)
	p := m.Start()
	// 2s later: 100 more pages in, 40 more out; CPU: 100 busy of 200 total jiffies
	m.WriteFile("/proc/vmstat", "pswpin 1200\npswpout 2080\n")
	m.WriteFile("/proc/stat", "cpu  1240 0 335 29168 99 0 64 55 0 0\ncpu0 1 0 0 1 0 0 0 0 0 0\ncpu1 1 0 0 1 0 0 0 0 0 0\n")
	p.Tick()
	var s sys
	p.GetJSON("/api/system", &s)
	if s.Swap.InPagesPerSec == nil || *s.Swap.InPagesPerSec != 100 || *s.Swap.OutPagesPerSec != 40 {
		t.Fatalf("swap rates = %v %v", s.Swap.InPagesPerSec, s.Swap.OutPagesPerSec)
	}
	if s.CPU.TotalPercent == nil || *s.CPU.TotalPercent != 50 {
		t.Fatalf("cpu = %v", s.CPU.TotalPercent)
	}
}

func TestMissingSourcesAreReportedNotFatal(t *testing.T) {
	m := harness.New(t, fx)
	m.Remove("/proc/swaps")
	m.Remove("/proc/loadavg")
	p := m.Start()
	var s sys
	p.GetJSON("/api/system", &s)
	if s.Swap.Disk != nil || s.Swap.Compressed != nil || s.CPU.Load1 != nil || s.Memory == nil {
		t.Fatalf("unexpected %+v", s)
	}
	// this fixture has no PSI or diskstats, so those are reported too
	if strings.Join(s.Missing, ",") != "/proc/diskstats,/proc/loadavg,/proc/pressure,/proc/swaps" {
		t.Fatalf("missing = %v", s.Missing)
	}
}

func TestReportPrintsMemorySwapAndCPU(t *testing.T) {
	m := harness.New(t, fx)
	m.Clock.OnSleep = func() { // report samples twice, 1s apart
		m.WriteFile("/proc/vmstat", "pswpin 1100\npswpout 2040\n")
	}
	out := m.Report()
	for _, want := range []string{
		"RAM  total 31.2 GiB  available 8.6 GiB  used 22.7 GiB  cached 6.7 GiB  buffers 0.4 GiB  dirty 2.0 MiB",
		"Disk Swap        used 2.9 GiB of 8.0 GiB",
		"Compressed Swap  used 0.6 GiB of 4.0 GiB  (RAM cost 0.1 GiB)",
		"Swap-in 100 pages/s  Swap-out 40 pages/s",
		"CPU  load 1.50 1.20 0.90  cores 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}
}

func TestDashboardEmbeddedAndConfigAndFlags(t *testing.T) {
	m := harness.New(t, fx)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", "# comment\nport = 0\ninterval = \"3s\"\n")
	p := m.Start()
	if !strings.HasPrefix(p.Addr, "127.0.0.1:") {
		t.Fatalf("must bind loopback only, got %s", p.Addr)
	}
	code, body := p.Get("/")
	if code != 200 || !strings.Contains(string(body), "sys-telemetry-app") {
		t.Fatalf("dashboard: %d %s", code, body)
	}
	if strings.Contains(string(body), "http://") || strings.Contains(string(body), "https://") {
		t.Fatal("dashboard must not reference external URLs")
	}
	if code, _ := p.Get("/app.js"); code != 200 {
		t.Fatal("app.js not served")
	}
	var info struct {
		IntervalSeconds float64 `json:"interval_seconds"`
	}
	p.GetJSON("/api/info", &info)
	if info.IntervalSeconds != 3 {
		t.Fatalf("config interval not applied: %v", info.IntervalSeconds)
	}
	p.Stop()
	p2 := m.Start("--interval", "5s") // flag beats config
	p2.GetJSON("/api/info", &info)
	if info.IntervalSeconds != 5 {
		t.Fatalf("flag interval not applied: %v", info.IntervalSeconds)
	}
}

func TestInvalidConfigAndIntervalRejected(t *testing.T) {
	m := harness.New(t, fx)
	if code, _, _ := m.Run("run", "--interval", "500ms"); code == 0 {
		t.Fatal("interval below 1s must be rejected")
	}
	if code, _, _ := m.Run("report", "--config", "/nope.toml"); code == 0 {
		t.Fatal("explicit missing config must fail")
	}
	m.WriteFile("/bad.toml", "port = banana\n")
	if code, _, _ := m.Run("report", "--config", "/bad.toml"); code == 0 {
		t.Fatal("bad config must fail")
	}
}

func TestEveryRouteRefusesANonLoopbackHost(t *testing.T) {
	m := harness.New(t, "dev-box-applications")
	p := m.Start()
	p.Tick()
	paths := []string{"/", "/api/system", "/api/processes", "/api/applications", "/api/history?minutes=1",
		"/api/history/span", "/api/history/series", "/api/advice", "/api/self"}
	for _, path := range paths {
		if code, _ := p.Get(path); code == 403 {
			t.Fatalf("GET %s on a loopback Host was refused", path)
		}
		for _, host := range []string{"evil.example.com", "evil.example.com:8080", "192.168.1.5:9000"} {
			if code, body := p.GetAsHost(path, host); code != 403 {
				t.Fatalf("GET %s with Host %s = %d, want 403 (DNS rebinding); body %.80s", path, host, code, body)
			}
		}
		for _, host := range []string{"localhost:8080", "127.0.0.1", "[::1]:80"} {
			if code, _ := p.GetAsHost(path, host); code == 403 {
				t.Fatalf("GET %s with loopback Host %s was refused", path, host)
			}
		}
	}
}
