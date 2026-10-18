package advisor

import (
	"fmt"
	"math"
)

// StandardRAMGB are the RAM sizes suggested by a Hardware Limit verdict.
var StandardRAMGB = []float64{8, 16, 24, 32, 48, 64, 96, 128}

// HardwareLimitRule fires when the Working Set does not fit even with sensible
// settings: memory PSI above threshold, Working Set above the configured share
// of RAM (90%) and Thrashing on Disk Swap, all Sustained. Compressed Swap
// Thrashing alone never counts (that is a Settings Fix, #14).
type HardwareLimitRule struct{}

func (HardwareLimitRule) ID() string    { return "hardware-limit" }
func (HardwareLimitRule) Title() string { return "Hardware Limit (more physical RAM needed)" }

func (HardwareLimitRule) Needs(in *Input) string {
	switch {
	case in.Sample.Memory == nil:
		return "physical RAM size is unknown: /proc/meminfo is missing"
	case in.PSI == nil:
		return in.PSIReason
	case in.Swap.VmstatGone:
		return "swap-in/out rates are unavailable: /proc/vmstat is missing or lacks pswpin/pswpout"
	case in.Swap.DiskUnknown != "":
		return in.Swap.DiskUnknown
	}
	if _, ok := in.WorkingSet(); !ok && len(in.Sample.Processes) > 0 {
		return "per-process PSS and swap are unavailable, so the Working Set cannot be computed"
	}
	if len(in.Sample.Processes) == 0 {
		return "no per-process data: /proc/<pid> could not be read, so the Working Set cannot be computed"
	}
	return ""
}

func (HardwareLimitRule) Check(in *Input) []Finding {
	ws, ok := in.WorkingSet()
	ram := in.Sample.Memory.TotalBytes
	if !ok || ram == 0 || !in.Swap.HasDisk {
		return nil
	}
	cfg := in.Cfg
	if in.PSI.Max() <= cfg.PSIPercent {
		return nil
	}
	if float64(ws) <= float64(ram)*cfg.WorkingSetPercent/100 {
		return nil
	}
	if !in.Swap.Disk.Thrashing(cfg.ThrashPagesPerSec) {
		return nil
	}
	sev := Warning
	deficit := int64(ws) - int64(ram)
	if deficit > 0 {
		sev = Critical
	}
	const gib = 1 << 30
	facts := []Fact{
		fnum("working_set_bytes", float64(ws), "bytes"),
		fnum("ram_bytes", float64(ram), "bytes"),
		fnum("memory_deficit_bytes", math.Max(0, float64(deficit)), "bytes"),
		fnum("working_set_percent_of_ram", 100*float64(ws)/float64(ram), "percent"),
		fnum("memory_pressure_some_avg10", in.PSI.SomeAvg10, "percent"),
		fnum("disk_swap_in", *in.Swap.Disk.In, "pages/s"),
		fnum("disk_swap_out", *in.Swap.Disk.Out, "pages/s"),
		{Name: "swap_attribution", Text: in.Swap.Attribution},
	}
	if in.PSI.HasFull {
		facts = append(facts, fnum("memory_pressure_full_avg10", in.PSI.FullAvg10, "percent"))
	}
	ramLabel := installedGB(ram)
	wsGB := float64(ws) / gib
	sizes := coveringSizes(wsGB, ramLabel)
	return []Finding{{
		Category:     HardwareLimit,
		Severity:     sev,
		Title:        "More physical RAM is needed",
		Summary:      verdict(wsGB, ramLabel, sizes),
		Applications: in.TopApps(5),
		Facts:        facts,
	}}
}

// installedGB names the installed RAM the way it is sold: the nearest standard
// size when MemTotal is within 10% below it (the kernel and firmware reserve
// some), otherwise the exact figure.
func installedGB(ramBytes uint64) float64 {
	gb := float64(ramBytes) / (1 << 30)
	for _, s := range StandardRAMGB {
		if gb <= s && gb >= s*0.9 {
			return s
		}
	}
	return math.Round(gb*10) / 10
}

// coveringSizes returns up to two standard sizes larger than the installed
// RAM that hold the Working Set.
func coveringSizes(wsGB, ramGB float64) []float64 {
	var out []float64
	for _, s := range StandardRAMGB {
		if s > ramGB && s >= wsGB && len(out) < 2 {
			out = append(out, s)
		}
	}
	return out
}

func gbText(v float64) string {
	if v >= 10 || v == math.Trunc(v) {
		return fmt.Sprintf("%.0f GB", v)
	}
	return fmt.Sprintf("%.1f GB", v)
}

func verdict(wsGB, ramGB float64, sizes []float64) string {
	s := fmt.Sprintf("Your applications need ~%s at once; you have %s and are thrashing to disk. ", gbText(wsGB), gbText(ramGB))
	if d := wsGB - ramGB; d > 0.5 {
		s += fmt.Sprintf("Memory Deficit: ~%s. ", gbText(d))
	} else {
		s += "Memory Deficit: none yet, but the Working Set leaves almost no headroom. "
	}
	s += "Settings alone won't fix this without stopping services. "
	switch len(sizes) {
	case 0:
		s += "No standard size up to 128 GB covers it; spread the workload over more machines."
	case 1:
		s += gbText(sizes[0]) + " would cover it."
	default:
		s += gbText(sizes[0]) + " or " + gbText(sizes[1]) + " would cover it."
	}
	return s
}
