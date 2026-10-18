package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/gpu"
	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type gpuSystem struct {
	GPUs []struct {
		Vendor string   `json:"vendor"`
		Name   string   `json:"name"`
		Util   *float64 `json:"util_percent"`
		Temp   *float64 `json:"temperature_celsius"`
		Used   *uint64  `json:"vram_used_bytes"`
		Total  *uint64  `json:"vram_total_bytes"`
	} `json:"gpus"`
}

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

// No GPU: the section is absent from the API and the report, with no errors.
func TestNoGPUMeansNoGPUSection(t *testing.T) {
	m := harness.New(t, "laptop-live")
	p := m.Start()
	var top map[string]json.RawMessage
	p.GetJSON("/api/system", &top)
	if _, ok := top["gpus"]; ok {
		t.Fatalf("want no gpus key, got %s", top["gpus"])
	}
	for _, line := range strings.Split(m.Report(), "\n") {
		if strings.HasPrefix(line, "GPU  ") { // the data line; the unavailable list says "  GPU: ..."
			t.Fatalf("report must have no GPU line: %q", line)
		}
	}
	_, live := p.Get("/api/live")
	if strings.Contains(string(live), `"gpu_util_percent":0`) {
		t.Fatalf("no GPU must not chart zeros: %s", live)
	}
}

// AMD and Intel GPUs are read from sysfs.
func TestAMDAndIntelFromSysfs(t *testing.T) {
	m := harness.New(t, "laptop-live")
	m.WriteFile("/sys/class/drm/card0/device/vendor", "0x1002\n")
	m.WriteFile("/sys/class/drm/card0/device/gpu_busy_percent", "37\n")
	m.WriteFile("/sys/class/drm/card0/device/mem_info_vram_used", "2147483648\n")
	m.WriteFile("/sys/class/drm/card0/device/mem_info_vram_total", "8589934592\n")
	m.WriteFile("/sys/class/drm/card0/device/product_name", "Radeon RX 6600\n")
	m.WriteFile("/sys/class/drm/card0/device/hwmon/hwmon3/temp1_input", "61000\n")
	m.WriteFile("/sys/class/drm/card1/device/vendor", "0x8086\n")
	m.WriteFile("/sys/class/drm/card1/device/hwmon/hwmon5/temp1_input", "48500\n")
	m.WriteFile("/sys/class/drm/card0-DP-1/status", "connected\n") // a connector, not a GPU
	m.WriteFile("/sys/class/drm/card2/device/vendor", "0x10de\n")  // NVIDIA comes from NVML only
	p := m.Start()
	var s gpuSystem
	p.GetJSON("/api/system", &s)
	if len(s.GPUs) != 2 {
		t.Fatalf("want amd and intel, got %+v", s.GPUs)
	}
	a, i := s.GPUs[0], s.GPUs[1]
	if a.Vendor != "amd" || a.Name != "Radeon RX 6600" || !near(a.Util, 37) || !near(a.Temp, 61) ||
		a.Used == nil || *a.Used != 2<<30 || a.Total == nil || *a.Total != 8<<30 {
		t.Fatalf("amd %+v", a)
	}
	if i.Vendor != "intel" || !near(i.Temp, 48.5) || i.Util != nil || i.Total != nil {
		t.Fatalf("intel %+v", i)
	}
	out := m.Report()
	if !strings.Contains(out, "GPU") || !strings.Contains(out, "Radeon RX 6600") || !strings.Contains(out, "37%") {
		t.Fatalf("report lacks GPU:\n%s", out)
	}
}

// NVIDIA comes from the (fake) NVML reader and feeds the live graphs.
func TestNVIDIAFromNVMLFeedsLiveGraphs(t *testing.T) {
	m := harness.New(t, "laptop-live")
	m.GPU.Set(gpu.Device{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 3060", UtilPercent: f64(55), TempC: f64(70),
		VRAMUsedBytes: u64(3 << 30), VRAMTotalBytes: u64(12 << 30)})
	p := m.Start()
	var s gpuSystem
	p.GetJSON("/api/system", &s)
	if len(s.GPUs) != 1 || s.GPUs[0].Vendor != "nvidia" || !near(s.GPUs[0].Util, 55) {
		t.Fatalf("nvidia %+v", s.GPUs)
	}
	p.Tick()
	var live struct {
		Points []struct {
			Util  *float64 `json:"gpu_util_percent"`
			Temp  *float64 `json:"gpu_temperature_celsius"`
			Used  *uint64  `json:"gpu_vram_used_bytes"`
			Total *uint64  `json:"gpu_vram_total_bytes"`
		} `json:"points"`
	}
	p.GetJSON("/api/live", &live)
	last := live.Points[len(live.Points)-1]
	if !near(last.Util, 55) || !near(last.Temp, 70) || last.Used == nil || *last.Used != 3<<30 || *last.Total != 12<<30 {
		t.Fatalf("live point %+v", last)
	}
	if out := m.Report(); !strings.Contains(out, "RTX 3060") || !strings.Contains(out, "3.0 GiB of 12.0 GiB") {
		t.Fatalf("report:\n%s", out)
	}
}

// The graphs page declares the GPU chart.
func TestDashboardHasGPUChart(t *testing.T) {
	m := harness.New(t, "laptop-live")
	p := m.Start()
	_, js := p.Get("/graphs.js")
	if !strings.Contains(string(js), "gpu_util_percent") {
		t.Fatal("graphs.js lacks the GPU chart")
	}
}
