//go:build nvml

package gpu

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// nvmlAPI is the handful of NVML calls the program uses, already decoded. The
// real implementation (nvml_dl.go) calls the runtime-loaded library; tests
// substitute a Go fake. Every method reports NVML failure as an error.
type nvmlAPI interface {
	Shutdown()
	DeviceCount() (int, error)
	Handle(index int) (uintptr, error)
	Name(h uintptr) (string, error)
	Utilization(h uintptr) (float64, error)
	Temperature(h uintptr) (float64, error)
	Memory(h uintptr) (used, total uint64, err error)
}

// NVML reads NVIDIA GPUs through a runtime-loaded NVML (ADR 0002). It is built
// to never hurt the program: every failure (no driver, no library, an NVML
// error, a panic, a call that hangs) means "no NVIDIA data" for that read.
type NVML struct {
	open    func() (nvmlAPI, error) // loads the library; nil api and an error when the driver is absent
	timeout time.Duration           // longest one Read may block the sampler

	mu       sync.Mutex
	api      nvmlAPI
	skips    int  // reads left before trying to load again after a failure
	inFlight bool // a previous read is still stuck inside NVML
	lastErr  string
}

// Unavailable implements Explainer: why the last attempt to load NVML failed.
func (n *NVML) Unavailable() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.api != nil || n.lastErr == "" {
		return ""
	}
	return "NVIDIA GPU data could not be read: " + n.lastErr
}

// retryEvery is how many reads to skip before trying to load NVML again after
// a failure (about 5 minutes at the default interval).
const retryEvery = 150

// NewNVML returns the production Reader. Nothing is loaded until the first
// Read, and the library is only opened when the NVIDIA driver is present.
func NewNVML() *NVML {
	return &NVML{open: openDriverLibrary, timeout: 1500 * time.Millisecond}
}

// driverPresent reports whether the NVIDIA kernel driver is loaded, so a
// machine without one never even looks for the library.
func driverPresent() bool {
	for _, p := range []string{"/dev/nvidiactl", "/proc/driver/nvidia"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// Read implements Reader.
func (n *NVML) Read() []Device {
	n.mu.Lock()
	if n.inFlight {
		n.mu.Unlock()
		return nil
	}
	if n.api == nil {
		if n.skips > 0 {
			n.skips--
			n.mu.Unlock()
			return nil
		}
	}
	n.inFlight = true
	n.mu.Unlock()

	done := make(chan []Device, 1)
	go func() {
		var out []Device
		defer func() {
			if r := recover(); r != nil { // a panic is "no data", never a crash
				out = nil
				n.drop()
			}
			n.mu.Lock()
			n.inFlight = false
			n.mu.Unlock()
			done <- out
		}()
		out = n.read()
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(n.timeout):
		return nil // stuck in NVML: inFlight stays set until it returns
	}
}

// drop forgets the loaded library so the next successful opportunity reloads it.
func (n *NVML) drop() {
	n.mu.Lock()
	n.api, n.skips = nil, retryEvery
	n.mu.Unlock()
}

func (n *NVML) read() []Device {
	n.mu.Lock()
	api := n.api
	n.mu.Unlock()
	if api == nil {
		if n.open == nil {
			return nil
		}
		a, err := n.open()
		if err != nil || a == nil {
			n.mu.Lock()
			n.lastErr = "NVML did not load"
			if err != nil {
				n.lastErr = err.Error()
			}
			n.mu.Unlock()
			n.drop()
			return nil
		}
		n.mu.Lock()
		n.api = a
		n.mu.Unlock()
		api = a
	}
	count, err := api.DeviceCount()
	if err != nil || count < 0 || count > 64 {
		api.Shutdown()
		n.drop()
		return nil
	}
	var out []Device
	for i := 0; i < count; i++ {
		h, err := api.Handle(i)
		if err != nil {
			continue
		}
		d := Device{Vendor: "nvidia", Name: fmt.Sprintf("NVIDIA GPU %d", i)}
		if name, err := api.Name(h); err == nil && name != "" {
			d.Name = name
		}
		if v, err := api.Utilization(h); err == nil {
			d.UtilPercent = &v
		}
		if v, err := api.Temperature(h); err == nil {
			d.TempC = &v
		}
		if used, total, err := api.Memory(h); err == nil && total > 0 && used <= total {
			d.VRAMUsedBytes, d.VRAMTotalBytes = &used, &total
		}
		out = append(out, d)
	}
	if count > 0 && len(out) == 0 {
		api.Shutdown()
		n.drop()
	}
	return out
}
