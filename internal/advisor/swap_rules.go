package advisor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Thresholds of the swap Settings Fixes.
const (
	// A page cache of at least this share of physical RAM is large.
	largePageCachePercent = 25
	// Disk Swap out-rate (pages/s) above which Disk Swap is being written to.
	diskSwapActivePagesPerSec = 10
	// Daily Disk Swap write volume (GB) at which swap wear is worth mentioning, and warning level.
	swapWearInfoGBPerDay    = 10
	swapWearWarningGBPerDay = 50
	// How much recent Disk Swap write history the daily estimate is built from.
	wearHorizon = time.Hour
	gb          = 1e9
)

func readSwappiness(m machine.Machine) *int {
	b, err := m.ReadFile("/proc/sys/vm/swappiness")
	if err != nil {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return nil
	}
	return &n
}

// diskWriteHistory keeps (time, bytes written to Disk Swap) per sample for the
// trailing wearHorizon. This is what stands in for History (#11) today: it is
// lost on restart and covers at most an hour.
type diskWriteHistory struct {
	at    []time.Time
	bytes []float64
	last  time.Time
}

func (h *diskWriteHistory) add(now time.Time, outPagesPerSec *float64) {
	if !h.last.IsZero() && outPagesPerSec != nil {
		dt := now.Sub(h.last).Seconds()
		h.at = append(h.at, now)
		h.bytes = append(h.bytes, *outPagesPerSec*dt*float64(os.Getpagesize()))
	}
	h.last = now
	for len(h.at) > 0 && now.Sub(h.at[0]) > wearHorizon {
		h.at, h.bytes = h.at[1:], h.bytes[1:]
	}
}

// minEstimateBasis is the shortest span a daily estimate is divided over, so a
// short burst (a build) is not projected to a whole day.
const minEstimateBasis = 30 * time.Minute

// estimate extrapolates the trailing writes to a day, dividing by the observed
// span but never by less than minEstimateBasis. It returns the basis used.
func (h *diskWriteHistory) estimate() (*float64, time.Duration) {
	if len(h.at) < 2 {
		return nil, 0
	}
	basis := h.at[len(h.at)-1].Sub(h.at[0])
	if basis < minEstimateBasis {
		basis = minEstimateBasis
	}
	var sum float64
	for _, b := range h.bytes {
		sum += b
	}
	v := sum / basis.Seconds() * 86400
	return &v, basis
}

func needSwapRates(in *Input) string {
	switch {
	case in.Sample.Memory == nil:
		return "physical RAM size is unknown: /proc/meminfo is missing"
	case in.Swap.VmstatGone:
		return "swap-in/out rates are unavailable: /proc/vmstat is missing or lacks pswpin/pswpout"
	}
	return ""
}

// SwappinessRule: pages are being written to Disk Swap while the page cache is
// large, so the kernel swaps out Application memory instead of dropping cache.
type SwappinessRule struct{}

func (SwappinessRule) ID() string    { return "swappiness-too-aggressive" }
func (SwappinessRule) Title() string { return "Swappiness too aggressive" }

func (SwappinessRule) Needs(in *Input) string {
	if r := needSwapRates(in); r != "" {
		return r
	}
	if in.Swap.DiskUnknown != "" {
		return in.Swap.DiskUnknown
	}
	if in.Swappiness == nil {
		return "vm.swappiness is unknown: /proc/sys/vm/swappiness is unreadable"
	}
	return ""
}

func (SwappinessRule) Check(in *Input) []Finding {
	if !in.Swap.HasDisk || in.Swap.Disk.Out == nil || *in.Swap.Disk.Out <= diskSwapActivePagesPerSec {
		return nil
	}
	mem := in.Sample.Memory
	if mem.TotalBytes == 0 || *in.Swappiness <= 10 {
		return nil
	}
	cachePct := 100 * float64(mem.CachedBytes) / float64(mem.TotalBytes)
	if cachePct < largePageCachePercent {
		return nil
	}
	reason := fmt.Sprintf("Swappiness is %d, so the kernel writes Application memory to Disk Swap although %s of RAM (%.0f%%) is page cache that could be dropped instead.",
		*in.Swappiness, gbText(float64(mem.CachedBytes)/(1<<30)), cachePct)
	content := "# Written by hand from sys-telemetry-app advice; nothing was applied for you.\n" +
		"# Reason: Disk Swap was written to for a Sustained period while " + fmt.Sprintf("%.0f%%", cachePct) + " of RAM is page cache.\n" +
		"# A lower vm.swappiness makes the kernel drop page cache before it swaps out Application memory.\n" +
		"vm.swappiness = 10\n" +
		"# Keeping directory and inode caches a little longer (default 100) makes file access cheaper once Application memory stays in RAM.\n" +
		"vm.vfs_cache_pressure = 50\n"
	return []Finding{{
		Category: SettingsFix,
		Severity: Warning,
		Title:    "Swappiness is too aggressive: Application memory goes to Disk Swap while page cache is large",
		Summary:  reason + " Lowering it to 10 (and vm.vfs_cache_pressure to 50) keeps Application memory in RAM.",
		Facts: []Fact{
			fnum("swappiness", float64(*in.Swappiness), ""),
			fnum("page_cache_bytes", float64(mem.CachedBytes), "bytes"),
			fnum("page_cache_percent_of_ram", cachePct, "percent"),
			fnum("disk_swap_out", *in.Swap.Disk.Out, "pages/s"),
		},
		Applications: in.TopApps(5),
		Change: &Change{Kind: "sysctl.d", Target: "/etc/sysctl.d/99-swappiness.conf", Content: content,
			Note: "Copy this into the file, then run `sudo sysctl --system` to apply it. Nothing has been changed."},
	}}
}

// CompressedSwapThrashingRule: Thrashing that is only on Compressed Swap is a
// CPU cost, not disk wear and not a Hardware Limit.
type CompressedSwapThrashingRule struct{}

func (CompressedSwapThrashingRule) ID() string    { return "compressed-swap-thrashing" }
func (CompressedSwapThrashingRule) Title() string { return "Compressed Swap Thrashing" }

func (CompressedSwapThrashingRule) Needs(in *Input) string {
	if r := needSwapRates(in); r != "" {
		return r
	}
	return in.Swap.DiskUnknown
}

func (CompressedSwapThrashingRule) Check(in *Input) []Finding {
	thr := in.Cfg.ThrashPagesPerSec
	if !in.Swap.HasZram && in.Sample.Swap.Compressed == nil {
		return nil
	}
	if !in.Swap.Compressed.Thrashing(thr) || in.Swap.Disk.Thrashing(thr) {
		return nil
	}
	facts := []Fact{
		fnum("compressed_swap_in", *in.Swap.Compressed.In, "pages/s"),
		fnum("compressed_swap_out", *in.Swap.Compressed.Out, "pages/s"),
	}
	if in.Sample.CPU.TotalPercent != nil {
		facts = append(facts, fnum("cpu_total_percent", *in.Sample.CPU.TotalPercent, "percent"))
	}
	content := "# Written by hand from sys-telemetry-app advice; nothing was applied for you.\n" +
		"# Reason: pages are moving in and out of Compressed Swap at the same time. That costs CPU\n" +
		"# (compression), not disk wear. page-cluster 0 stops the kernel reading extra pages around\n" +
		"# each swap-in, which only helps on disks and wastes work on Compressed Swap.\n" +
		"vm.page-cluster = 0\n"
	return []Finding{{
		Category: SettingsFix,
		Severity: Info,
		Title:    "Compressed Swap is Thrashing: the cost is CPU, not disk",
		Summary: "Pages are moving in and out of Compressed Swap at the same time. No disk is involved, so this is a CPU cost (compression and decompression), not wear and not a Hardware Limit. " +
			"Set vm.page-cluster to 0 and check that zram uses a fast algorithm (lz4 or zstd) and is not larger than half of RAM; if the Working Set stays this large, stop the biggest Applications.",
		Facts:        facts,
		Applications: in.TopApps(5),
		Change: &Change{Kind: "sysctl.d", Target: "/etc/sysctl.d/99-zram-swap.conf", Content: content,
			Note: "Copy this into the file, then run `sudo sysctl --system` to apply it. For the zram device itself, see zram-generator (zram-size = ram / 2, compression-algorithm = zstd). Nothing has been changed."},
	}}
}

// SwapWearRule: Sustained Disk Swap writes at a rate that adds up to a
// meaningful daily SSD write volume.
type SwapWearRule struct{}

func (SwapWearRule) ID() string    { return "swap-wear" }
func (SwapWearRule) Title() string { return "Swap wear on SSD" }

func (SwapWearRule) Needs(in *Input) string {
	if r := needSwapRates(in); r != "" {
		return r
	}
	return in.Swap.DiskUnknown
}

func (SwapWearRule) Check(in *Input) []Finding {
	if !in.Swap.HasDisk || in.Swap.DiskWriteBytesPerDay == nil || in.Swap.Disk.Out == nil {
		return nil
	}
	gbDay := *in.Swap.DiskWriteBytesPerDay / gb
	if gbDay < swapWearInfoGBPerDay {
		return nil
	}
	sev := Info
	if gbDay >= swapWearWarningGBPerDay {
		sev = Warning
	}
	content := "# Written by hand from sys-telemetry-app advice; nothing was applied for you.\n" +
		"# Reason: Disk Swap is written at about " + fmt.Sprintf("%.0f GB/day", gbDay) + ", which wears an SSD.\n" +
		"# Compressed Swap in RAM has no disk wear. Put it first (highest priority) and size it to half of RAM.\n" +
		"[zram0]\nzram-size = ram / 2\ncompression-algorithm = zstd\nswap-priority = 100\n"
	return []Finding{{
		Category: SettingsFix,
		Severity: sev,
		Title:    fmt.Sprintf("Disk Swap writes about %.0f GB/day to the SSD", gbDay),
		Summary: fmt.Sprintf("At the current rate (averaged over the last %.0f minutes) Disk Swap writes about %.0f GB/day to the SSD. "+
			"Prefer Compressed Swap: it lives in RAM and does not wear storage.", in.Swap.DiskWriteObserved.Minutes(), gbDay),
		Facts: []Fact{
			fnum("disk_swap_write_gb_per_day", gbDay, "GB/day"),
			fnum("estimate_basis_minutes", in.Swap.DiskWriteObserved.Minutes(), "minutes"),
			fnum("disk_swap_out", *in.Swap.Disk.Out, "pages/s"),
		},
		Applications: in.TopApps(5),
		Change: &Change{Kind: "zram-generator", Target: "/etc/systemd/zram-generator.conf", Content: content,
			Note: "Needs the zram-generator package. Copy this into the file and reboot (or `sudo systemctl restart systemd-zram-setup@zram0`). Nothing has been changed."},
	}}
}
