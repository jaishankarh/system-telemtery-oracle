// Package gpu holds what the program knows about graphics processors and the
// reader for NVIDIA GPUs (NVML, loaded at runtime; see nvml.go). AMD and Intel
// GPUs are read from sysfs by the Collector through the Machine.
package gpu

// Device is one GPU. A nil field means the value is unavailable for it.
type Device struct {
	Vendor         string   `json:"vendor"` // "nvidia", "amd" or "intel"
	Name           string   `json:"name"`
	UtilPercent    *float64 `json:"util_percent"`
	TempC          *float64 `json:"temperature_celsius"`
	VRAMUsedBytes  *uint64  `json:"vram_used_bytes"`
	VRAMTotalBytes *uint64  `json:"vram_total_bytes"`
}

// Reader returns the NVIDIA GPUs present right now. It never fails and never
// panics: any problem means an empty result (ADR 0006). It is the GPU seam of
// the Machine; tests substitute a fake.
type Reader interface {
	Read() []Device
}

// None is a Reader for machines that have no NVIDIA GPU reader.
type None struct{}

// Read implements Reader.
func (None) Read() []Device { return nil }

// Explainer is optionally implemented by a Reader that can say why it has no
// NVIDIA data (the static build, or the driver library failed to load).
// Unavailable returns "" when nothing is wrong.
type Explainer interface {
	Unavailable() string
}
