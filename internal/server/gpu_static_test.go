//go:build !nvml

package server_test

import (
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type unavailableDoc struct {
	Availability struct {
		Unavailable []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"unavailable"`
	} `json:"availability"`
	GPUs []any `json:"gpus"`
}

func nvidiaReason(d unavailableDoc) string {
	for _, u := range d.Availability.Unavailable {
		if u.ID == "nvidia_gpu" {
			return u.Reason
		}
	}
	return ""
}

// The default (static) build cannot read NVIDIA GPUs: with NVIDIA hardware
// present it says so, naming the nvml build, and shows no NVIDIA data.
func TestStaticBuildSaysNVIDIANeedsNvmlBuild(t *testing.T) {
	m := harness.New(t, "laptop-live")
	m.UseBuildGPUReader()
	m.WriteFile("/proc/driver/nvidia/version", "NVRM version: NVIDIA UNIX x86_64 Kernel Module  550.54.14\n")
	p := m.Start()
	var d unavailableDoc
	p.GetJSON("/api/system", &d)
	if r := nvidiaReason(d); !strings.Contains(r, "nvml build") {
		t.Fatalf("want the nvml-build reason, got %q in %+v", r, d.Availability.Unavailable)
	}
	if len(d.GPUs) != 0 {
		t.Fatalf("no NVIDIA data expected: %+v", d.GPUs)
	}
	if out := m.Report(); !strings.Contains(out, "nvml build") {
		t.Fatalf("report must list the reason:\n%s", out)
	}
}

// With no NVIDIA hardware, the static build says nothing about NVIDIA.
func TestStaticBuildSilentWithoutNVIDIAHardware(t *testing.T) {
	m := harness.New(t, "laptop-live")
	m.UseBuildGPUReader()
	p := m.Start()
	var d unavailableDoc
	p.GetJSON("/api/system", &d)
	if r := nvidiaReason(d); r != "" {
		t.Fatalf("unexpected NVIDIA item: %q", r)
	}
}

// AMD and Intel still work in the static build.
func TestStaticBuildStillReadsAMD(t *testing.T) {
	m := harness.New(t, "laptop-live")
	m.UseBuildGPUReader()
	m.WriteFile("/sys/class/drm/card0/device/vendor", "0x1002\n")
	m.WriteFile("/sys/class/drm/card0/device/gpu_busy_percent", "10\n")
	p := m.Start()
	var d unavailableDoc
	p.GetJSON("/api/system", &d)
	if len(d.GPUs) != 1 {
		t.Fatalf("%+v", d.GPUs)
	}
}
