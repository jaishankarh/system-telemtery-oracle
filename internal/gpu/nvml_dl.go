//go:build nvml && !cgo

package gpu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/jaishankarh/system-telemtery-oracle/internal/third_party/purego"
)

// Library names tried in order. The bare names go through the dynamic linker's
// normal search; the absolute ones cover distributions whose search path
// misses the driver's directory.
var nvmlLibraries = []string{
	"libnvidia-ml.so.1",
	"libnvidia-ml.so",
	"/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1",
	"/usr/lib/aarch64-linux-gnu/libnvidia-ml.so.1",
	"/usr/lib64/libnvidia-ml.so.1",
	"/usr/lib/libnvidia-ml.so.1",
	"/lib64/libnvidia-ml.so.1",
	"/usr/lib/wsl/lib/libnvidia-ml.so.1",
}

func openDriverLibrary() (nvmlAPI, error) {
	if !driverPresent() {
		return nil, errors.New("no NVIDIA driver")
	}
	return openNVMLLibrary(nvmlLibraries)
}

// dlNVML calls NVML through purego's SyscallN. All out-parameters live in one
// heap buffer (Go heap objects do not move), guarded by mu.
type dlNVML struct {
	mu                            sync.Mutex
	buf                           *[128]byte
	shutdown, count, handle, name uintptr
	util, temp, mem               uintptr
}

const nvmlSuccess = 0

func openNVMLLibrary(names []string) (nvmlAPI, error) {
	var lib uintptr
	var lastErr error
	for _, n := range names {
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil && h != 0 {
			lib = h
			break
		}
		lastErr = err
	}
	if lib == 0 {
		return nil, fmt.Errorf("libnvidia-ml not loadable: %v", lastErr)
	}
	sym := func(names ...string) uintptr {
		for _, n := range names {
			if p, err := purego.Dlsym(lib, n); err == nil && p != 0 {
				return p
			}
		}
		return 0
	}
	d := &dlNVML{buf: new([128]byte)}
	init := sym("nvmlInit_v2", "nvmlInit")
	d.shutdown = sym("nvmlShutdown")
	d.count = sym("nvmlDeviceGetCount_v2", "nvmlDeviceGetCount")
	d.handle = sym("nvmlDeviceGetHandleByIndex_v2", "nvmlDeviceGetHandleByIndex")
	d.name = sym("nvmlDeviceGetName")
	d.util = sym("nvmlDeviceGetUtilizationRates")
	d.temp = sym("nvmlDeviceGetTemperature")
	d.mem = sym("nvmlDeviceGetMemoryInfo")
	if init == 0 || d.count == 0 || d.handle == 0 {
		return nil, errors.New("libnvidia-ml lacks the required functions")
	}
	if r, _, _ := purego.SyscallN(init); r != nvmlSuccess {
		return nil, fmt.Errorf("nvmlInit failed with code %d", r)
	}
	return d, nil
}

func (d *dlNVML) ptr() uintptr { return uintptr(unsafe.Pointer(&d.buf[0])) }

func (d *dlNVML) clear() { *d.buf = [128]byte{} }

func (d *dlNVML) call(fn uintptr, args ...uintptr) error {
	if fn == 0 {
		return errors.New("function not in this NVML")
	}
	r, _, _ := purego.SyscallN(fn, args...)
	if r != nvmlSuccess {
		return fmt.Errorf("NVML error %d", r)
	}
	return nil
}

func (d *dlNVML) Shutdown() {
	if d.shutdown != 0 {
		purego.SyscallN(d.shutdown)
	}
}

func (d *dlNVML) DeviceCount() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	if err := d.call(d.count, d.ptr()); err != nil {
		return 0, err
	}
	return int(binary.LittleEndian.Uint32(d.buf[:4])), nil
}

func (d *dlNVML) Handle(i int) (uintptr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	if err := d.call(d.handle, uintptr(i), d.ptr()); err != nil {
		return 0, err
	}
	h := uintptr(binary.LittleEndian.Uint64(d.buf[:8]))
	if h == 0 {
		return 0, errors.New("NVML returned a null device")
	}
	return h, nil
}

func (d *dlNVML) Name(h uintptr) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	if err := d.call(d.name, h, d.ptr(), 96); err != nil {
		return "", err
	}
	s := string(d.buf[:96])
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s), nil
}

func (d *dlNVML) Utilization(h uintptr) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	if err := d.call(d.util, h, d.ptr()); err != nil { // struct {unsigned gpu; unsigned memory}
		return 0, err
	}
	v := binary.LittleEndian.Uint32(d.buf[:4])
	if v > 100 {
		return 0, errors.New("implausible utilisation")
	}
	return float64(v), nil
}

func (d *dlNVML) Temperature(h uintptr) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	if err := d.call(d.temp, h, 0 /* NVML_TEMPERATURE_GPU */, d.ptr()); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(d.buf[:4])
	if v > 200 {
		return 0, errors.New("implausible temperature")
	}
	return float64(v), nil
}

func (d *dlNVML) Memory(h uintptr) (uint64, uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	if err := d.call(d.mem, h, d.ptr()); err != nil { // struct {ull total; ull free; ull used}
		return 0, 0, err
	}
	return binary.LittleEndian.Uint64(d.buf[16:24]), binary.LittleEndian.Uint64(d.buf[0:8]), nil
}
