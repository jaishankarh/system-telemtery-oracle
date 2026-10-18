// Package live is the Live buffer: the last 5 minutes of samples at full
// sampling resolution, held in RAM for the Graphs view (SPEC 4.4).
//
// A Point is one Sample reduced to what the charts need. Adding a chart for a
// later metric (GPU, ticket 9) means: add a field to Point, fill it in
// NewPoint, and add one entry to CHARTS in server/assets/graphs.js. The buffer,
// /api/live and the stream do not change.
package live

import (
	"sync"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
)

// Window is how much history the Live buffer keeps.
const Window = 5 * time.Minute

// TopApplications is how many Applications are kept per Point for the RAM/swap split.
const TopApplications = 10

// AppSplit is one Application's RAM (PSS) and swapped-out memory.
type AppSplit struct {
	Name string `json:"name"`
	PSS  uint64 `json:"pss_bytes"`
	Swap uint64 `json:"swap_bytes"`
}

// Point is one sample for the graphs. JSON field names are the API; nil means
// the value was unavailable (first sample for rates, data source missing).
type Point struct {
	Time time.Time `json:"time"`

	CPUPercent *float64 `json:"cpu_percent"`
	Load1      *float64 `json:"load1"`

	RAMTotalBytes     *uint64 `json:"ram_total_bytes"`
	RAMUsedBytes      *uint64 `json:"ram_used_bytes"`
	DiskSwapUsed      *uint64 `json:"disk_swap_used_bytes"`
	CompressedSwapUse *uint64 `json:"compressed_swap_used_bytes"`

	MemoryPressureSome *float64 `json:"memory_pressure_some_percent"`
	MemoryPressureFull *float64 `json:"memory_pressure_full_percent"`
	IOPressureSome     *float64 `json:"io_pressure_some_percent"`
	IOPressureFull     *float64 `json:"io_pressure_full_percent"`
	CPUPressureSome    *float64 `json:"cpu_pressure_some_percent"`

	// Disk throughput summed over all whole devices.
	DiskRead  *float64 `json:"disk_read_bytes_per_sec"`
	DiskWrite *float64 `json:"disk_write_bytes_per_sec"`

	SwapInPages  *float64 `json:"swap_in_pages_per_sec"`
	SwapOutPages *float64 `json:"swap_out_pages_per_sec"`

	// GPU: the busiest and hottest GPU, VRAM summed over all GPUs; nil without a GPU.
	GPUUtil      *float64 `json:"gpu_util_percent"`
	GPUTemp      *float64 `json:"gpu_temperature_celsius"`
	GPUVRAMUsed  *uint64  `json:"gpu_vram_used_bytes"`
	GPUVRAMTotal *uint64  `json:"gpu_vram_total_bytes"`

	TopApps []AppSplit `json:"top_apps"`
}

// NewPoint reduces a Sample and its Applications (already ordered by PSS + swap) to a Point.
func NewPoint(s collector.Sample, apps []grouping.Application) Point {
	p := Point{Time: s.Time, CPUPercent: s.CPU.TotalPercent, Load1: s.CPU.Load1,
		SwapInPages: s.Swap.InPagesPerSec, SwapOutPages: s.Swap.OutPagesPerSec, TopApps: []AppSplit{}}
	if m := s.Memory; m != nil {
		t, u := m.TotalBytes, m.UsedBytes
		p.RAMTotalBytes, p.RAMUsedBytes = &t, &u
	}
	if d := s.Swap.Disk; d != nil {
		u := d.UsedBytes
		p.DiskSwapUsed = &u
	}
	if c := s.Swap.Compressed; c != nil {
		u := c.UsedBytes
		p.CompressedSwapUse = &u
	}
	if pr := s.Pressure; pr != nil {
		p.MemoryPressureSome, p.MemoryPressureFull = pr.Memory.Some, pr.Memory.Full
		p.IOPressureSome, p.IOPressureFull = pr.IO.Some, pr.IO.Full
		p.CPUPressureSome = pr.CPU.Some
	}
	for _, d := range s.Disks {
		if d.Read != nil {
			p.DiskRead = add(p.DiskRead, *d.Read)
		}
		if d.Write != nil {
			p.DiskWrite = add(p.DiskWrite, *d.Write)
		}
	}
	for _, g := range s.GPUs {
		if g.UtilPercent != nil && (p.GPUUtil == nil || *g.UtilPercent > *p.GPUUtil) {
			v := *g.UtilPercent
			p.GPUUtil = &v
		}
		if g.TempC != nil && (p.GPUTemp == nil || *g.TempC > *p.GPUTemp) {
			v := *g.TempC
			p.GPUTemp = &v
		}
		if g.VRAMUsedBytes != nil && g.VRAMTotalBytes != nil {
			u, t := *g.VRAMUsedBytes, *g.VRAMTotalBytes
			if p.GPUVRAMUsed != nil {
				u, t = u+*p.GPUVRAMUsed, t+*p.GPUVRAMTotal
			}
			p.GPUVRAMUsed, p.GPUVRAMTotal = &u, &t
		}
	}
	for _, a := range apps {
		if len(p.TopApps) == TopApplications {
			break
		}
		if a.PSS == nil { // the RAM part of the split is PSS; without it the Application is hidden, not shown as 0
			continue
		}
		sp := AppSplit{Name: a.Name}
		if a.PSS != nil {
			sp.PSS = *a.PSS
		}
		if a.Swap != nil {
			sp.Swap = *a.Swap
		}
		p.TopApps = append(p.TopApps, sp)
	}
	return p
}

func add(acc *float64, v float64) *float64 {
	if acc == nil {
		return &v
	}
	*acc += v
	return acc
}

// Buffer holds Points no older than Window, relative to the newest.
type Buffer struct {
	mu     sync.Mutex
	points []Point
}

// Add appends a Point and drops those that fell out of the window.
func (b *Buffer) Add(p Point) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.points = append(b.points, p)
	cut := p.Time.Add(-Window)
	i := 0
	for i < len(b.points)-1 && b.points[i].Time.Before(cut) {
		i++
	}
	if i > 0 {
		b.points = append(b.points[:0:0], b.points[i:]...) // copy so dropped Points are freed
	}
}

// Snapshot returns the buffered Points, oldest first.
func (b *Buffer) Snapshot() []Point {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Point(nil), b.points...)
}

// Shed drops all but the newest keep of the buffered Points, to give memory
// back when the program nears its own cap. The Graphs refill as samples arrive.
func (b *Buffer) Shed(keep time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.points) == 0 {
		return
	}
	cut := b.points[len(b.points)-1].Time.Add(-keep)
	i := 0
	for i < len(b.points)-1 && b.points[i].Time.Before(cut) {
		i++
	}
	b.points = append([]Point(nil), b.points[i:]...)
}
