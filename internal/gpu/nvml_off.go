//go:build !nvml

package gpu

// The default build links no NVIDIA support, so it stays a fully static
// binary (ADR 0002 and its addendum). NVIDIA data needs the nvml build:
// `make build-nvml`.

// NVIDIAUnavailable is the reason given when NVIDIA hardware is present but
// this binary cannot read it.
const NVIDIAUnavailable = "NVIDIA GPU data needs the nvml build of sys-telemetry-app (make build-nvml; release asset sys-telemetry-app-nvml-linux-*); this static build reads AMD and Intel GPUs only"

type noNVML struct{}

// NewNVML returns a Reader that reports no NVIDIA data and says why.
func NewNVML() Reader { return noNVML{} }

func (noNVML) Read() []Device      { return nil }
func (noNVML) Unavailable() string { return NVIDIAUnavailable }
