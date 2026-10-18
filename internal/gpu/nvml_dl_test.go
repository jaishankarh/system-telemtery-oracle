//go:build !cgo

package gpu

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const stubC = `
typedef void* dev;
static int fake;
int nvmlInit_v2(void){return 0;}
int nvmlShutdown(void){return 0;}
int nvmlDeviceGetCount_v2(unsigned*n){*n=1;return 0;}
int nvmlDeviceGetHandleByIndex_v2(unsigned i, dev*d){*d=(dev)&fake;return i?2:0;}
int nvmlDeviceGetName(dev d, char*s, unsigned len){const char*n="Stub RTX 3060";unsigned i=0;for(;n[i]&&i+1<len;i++)s[i]=n[i];s[i]=0;return 0;}
int nvmlDeviceGetUtilizationRates(dev d, unsigned*u){u[0]=42;u[1]=7;return 0;}
int nvmlDeviceGetTemperature(dev d, int sensor, unsigned*t){*t=sensor==0?64:0;return 0;}
int nvmlDeviceGetMemoryInfo(dev d, unsigned long long*m){m[0]=12884901888ULL;m[1]=9663676416ULL;m[2]=3221225472ULL;return 0;}
`

// Exercises the real runtime-loading path (dlopen through the vendored loader)
// against a stub library built with the system C compiler. Skipped without one.
func TestRealDlopenAgainstStubLibrary(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	dir := t.TempDir()
	src, so := filepath.Join(dir, "stub.c"), filepath.Join(dir, "libnvidia-ml.so.1")
	if err := os.WriteFile(src, []byte(stubC), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", so, src).CombinedOutput(); err != nil {
		t.Skipf("cannot build stub: %v %s", err, out)
	}
	api, err := openNVMLLibrary([]string{"/nonexistent/libnvidia-ml.so.1", so})
	if err != nil {
		t.Fatal(err)
	}
	r := &NVML{timeout: 5e9, open: func() (nvmlAPI, error) { return api, nil }}
	d := r.Read()
	if len(d) != 1 {
		t.Fatalf("%+v", d)
	}
	g := d[0]
	if g.Name != "Stub RTX 3060" || *g.UtilPercent != 42 || *g.TempC != 64 || *g.VRAMUsedBytes != 3<<30 || *g.VRAMTotalBytes != 12<<30 {
		t.Fatalf("%+v util=%v temp=%v", g, *g.UtilPercent, *g.TempC)
	}
}

func TestMissingLibraryIsAnError(t *testing.T) {
	if _, err := openNVMLLibrary([]string{"/nonexistent/libnvidia-ml.so.1"}); err == nil {
		t.Fatal("want error")
	}
}
