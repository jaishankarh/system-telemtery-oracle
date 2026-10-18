package collector

import (
	"github.com/jaishankarh/system-telemtery-oracle/internal/availability"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/gpu"
)

func init() {
	// NVIDIA hardware that this binary cannot read is listed as unavailable
	// with the reader's reason (ADR 0006), instead of silently missing.
	availability.RegisterProbe("nvidia_gpu", func(m machine.Machine, env availability.Environment) *availability.Item {
		ex, ok := m.GPU.(gpu.Explainer)
		if !ok || !nvidiaPresent(m) {
			return nil
		}
		reason := ex.Unavailable()
		if reason == "" {
			return nil
		}
		return &availability.Item{ID: "nvidia_gpu", Feature: "NVIDIA GPU", Reason: reason, Affects: []string{"GPU section", "GPU graph"}}
	})
}

// nvidiaPresent reports NVIDIA hardware or driver, judged from sysfs and /proc.
func nvidiaPresent(m machine.Machine) bool {
	if len(m.Glob("/proc/driver/nvidia/version")) > 0 {
		return true
	}
	for _, v := range m.Glob("/sys/class/drm/card*/device/vendor") {
		if b, err := m.ReadFile(v); err == nil && strings.TrimSpace(string(b)) == "0x10de" {
			return true
		}
	}
	return false
}

var cardName = regexp.MustCompile(`^card[0-9]+$`)

// readGPUs returns NVIDIA GPUs from the Machine's NVML reader, then AMD and
// Intel GPUs from sysfs. No GPU, or any failure, gives an empty result: the
// GPU section is then simply absent (ADR 0006).
func (l *Linux) readGPUs() []gpu.Device {
	var out []gpu.Device
	if l.m.GPU != nil {
		out = append(out, l.m.GPU.Read()...)
	}
	return append(out, l.sysfsGPUs()...)
}

// sysfsGPUs reads /sys/class/drm/card<N>/device. NVIDIA cards are skipped
// (their data comes from NVML); connectors such as card0-DP-1 are not cards.
func (l *Linux) sysfsGPUs() []gpu.Device {
	names, err := l.m.ReadDir("/sys/class/drm")
	if err != nil {
		return nil
	}
	sort.Strings(names)
	var out []gpu.Device
	for _, n := range names {
		if !cardName.MatchString(n) {
			continue
		}
		dev := "/sys/class/drm/" + n + "/device/"
		var d gpu.Device
		switch strings.TrimSpace(l.readText(dev + "vendor")) {
		case "0x1002":
			d.Vendor = "amd"
		case "0x8086":
			d.Vendor = "intel"
		default:
			continue
		}
		d.Name = strings.TrimSpace(l.readText(dev + "product_name"))
		if d.Name == "" {
			d.Name = strings.ToUpper(d.Vendor[:1]) + d.Vendor[1:] + " GPU (" + n + ")"
		}
		if v, ok := l.readUint(dev + "gpu_busy_percent"); ok {
			f := float64(v)
			d.UtilPercent = &f
		}
		if v, ok := l.readUint(dev + "mem_info_vram_used"); ok {
			d.VRAMUsedBytes = &v
		}
		if v, ok := l.readUint(dev + "mem_info_vram_total"); ok {
			d.VRAMTotalBytes = &v
		}
		if hw := l.m.Glob(dev + "hwmon/hwmon*/temp1_input"); len(hw) > 0 {
			sort.Strings(hw)
			if v, ok := l.readUint(hw[0]); ok {
				f := float64(v) / 1000
				d.TempC = &f
			}
		}
		out = append(out, d)
	}
	return out
}

func (l *Linux) readText(p string) string {
	b, err := l.m.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (l *Linux) readUint(p string) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(l.readText(p)), 10, 64)
	return v, err == nil
}
