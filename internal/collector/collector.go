// Package collector reads system metrics through a Machine and returns a
// Sample. Collector is the platform interface (ADR 0001): Linux today, a macOS
// implementation can be added in phase 2 without touching callers.
// Every data source is optional (ADR 0006): a missing file leaves its part of
// the Sample nil and is listed in Sample.Missing.
package collector

import (
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/gpu"
	"github.com/jaishankarh/system-telemtery-oracle/internal/availability"
)

// Collector produces one Sample per call. Rates are derived from the previous
// call, so they are nil on the first sample.
type Collector interface {
	Collect() (Sample, error)
}

// Sample is one reading of the whole system. JSON field names are the API.
type Sample struct {
	Time    time.Time `json:"time"`
	CPU     CPU       `json:"cpu"`
	Memory  *Memory   `json:"memory"`
	Swap    Swap      `json:"swap"`
	Missing []string  `json:"missing"`
	// Availability says which data is unavailable on this machine and why (ADR 0006).
	Availability availability.Report `json:"availability"`
	// Pressure (PSI) and Disks: see pressure.go. Pressure is nil without /proc/pressure.
	Pressure *Pressure `json:"pressure"`
	Disks    []Disk    `json:"disks"`
	// GPUs is absent (omitted) on a machine without a GPU: see gpu.go.
	GPUs []gpu.Device `json:"gpus,omitempty"`

	// Processes is served by /api/processes, not inside /api/system.
	Processes []Process `json:"-"`
	// ProcessMissing lists per-process sources the kernel lacks (ADR 0006),
	// with the PID replaced by the literal "PID".
	ProcessMissing []string `json:"-"`
}

// CPU is total utilisation and load.
type CPU struct {
	TotalPercent *float64 `json:"total_percent"`
	Load1        *float64 `json:"load1"`
	Load5        *float64 `json:"load5"`
	Load15       *float64 `json:"load15"`
	Cores        int      `json:"cores"`
}

// Memory is RAM in bytes. Used = total - available.
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	CachedBytes    uint64 `json:"cached_bytes"`
	BuffersBytes   uint64 `json:"buffers_bytes"`
	DirtyBytes     uint64 `json:"dirty_bytes"`
}

// Swap splits Disk Swap from Compressed Swap. The in/out rates are
// system-wide (the kernel does not split them by swap device).
type Swap struct {
	Disk           *SwapSpace `json:"disk"`
	Compressed     *SwapSpace `json:"compressed"`
	ZswapEnabled   *bool      `json:"zswap_enabled"`
	InPagesPerSec  *float64   `json:"in_pages_per_sec"`
	OutPagesPerSec *float64   `json:"out_pages_per_sec"`
}

// SwapSpace is the size and use of one kind of swap. RAMUsedBytes is the RAM a
// Compressed Swap itself occupies (nil for Disk Swap).
type SwapSpace struct {
	TotalBytes   uint64  `json:"total_bytes"`
	UsedBytes    uint64  `json:"used_bytes"`
	RAMUsedBytes *uint64 `json:"ram_used_bytes,omitempty"`
}
