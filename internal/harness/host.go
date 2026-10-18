package harness

import (
	"errors"
	"github.com/jaishankarh/system-telemtery-oracle/internal/gpu"
	"sync"
)

// FakeHost records every change the program makes to its host, so tests can
// assert on what WOULD be written. Nothing real is touched.
type FakeHost struct {
	mu          sync.Mutex
	oom         *int
	oomWrites   int
	units       map[string]string
	systemctls  [][]string
	rss         uint64
	exe         string
	OOMErr      error // set to make the OOM score write fail (e.g. not root)
	SystemctlEr error // set to make systemctl fail
}

// NewFakeHost returns a FakeHost whose binary lives at /usr/local/bin/sys-telemetry-app.
func NewFakeHost() *FakeHost {
	return &FakeHost{units: map[string]string{}, rss: 10 << 20, exe: "/usr/local/bin/sys-telemetry-app"}
}

// SetRSS sets the program's own resident memory as the fake host reports it.
func (h *FakeHost) SetRSS(n uint64) { h.mu.Lock(); h.rss = n; h.mu.Unlock() }

// OOMScoreAdj returns the score written for the program's own process, if any.
func (h *FakeHost) OOMScoreAdj() (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.oom == nil {
		return 0, false
	}
	return *h.oom, true
}

// OOMWrites is how many OOM score writes were attempted.
func (h *FakeHost) OOMWrites() int { h.mu.Lock(); defer h.mu.Unlock(); return h.oomWrites }

// Units returns the unit files currently "installed" (path to content).
func (h *FakeHost) Units() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]string{}
	for k, v := range h.units {
		out[k] = v
	}
	return out
}

// Systemctls returns every systemctl invocation, in order.
func (h *FakeHost) Systemctls() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]string(nil), h.systemctls...)
}

func (h *FakeHost) SetOOMScoreAdj(v int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.oomWrites++
	if h.OOMErr != nil {
		return h.OOMErr
	}
	h.oom = &v
	return nil
}
func (h *FakeHost) SelfRSSBytes() (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rss, nil
}
func (h *FakeHost) WriteUnit(path string, content []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.units[path] = string(content)
	return nil
}
func (h *FakeHost) RemoveUnit(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.units, path)
	return nil
}
func (h *FakeHost) Systemctl(args ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.systemctls = append(h.systemctls, append([]string(nil), args...))
	if h.SystemctlEr != nil {
		return errors.New(h.SystemctlEr.Error())
	}
	return nil
}
func (h *FakeHost) Executable() (string, error) { return h.exe, nil }

// FakeGPU is the fake NVML reader: it returns whatever devices the test set.
type FakeGPU struct {
	mu   sync.Mutex
	devs []gpu.Device
}

// Set replaces the NVIDIA GPUs on the fake machine (none by default).
func (f *FakeGPU) Set(d ...gpu.Device) { f.mu.Lock(); f.devs = d; f.mu.Unlock() }

// Read implements gpu.Reader.
func (f *FakeGPU) Read() []gpu.Device {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gpu.Device(nil), f.devs...)
}
