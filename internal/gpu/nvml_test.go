package gpu

import (
	"errors"
	"testing"
	"time"
)

// fakeAPI is a scripted NVML.
type fakeAPI struct {
	count    int
	countErr error
	handleEr error
	panicOn  string
	block    chan struct{}
	shut     int
}

func (f *fakeAPI) Shutdown() { f.shut++ }
func (f *fakeAPI) DeviceCount() (int, error) {
	if f.panicOn == "count" {
		panic("boom")
	}
	if f.block != nil {
		<-f.block
	}
	return f.count, f.countErr
}
func (f *fakeAPI) Handle(i int) (uintptr, error) { return uintptr(i + 1), f.handleEr }
func (f *fakeAPI) Name(uintptr) (string, error)  { return "RTX 3060", nil }
func (f *fakeAPI) Utilization(uintptr) (float64, error) {
	return 0, errors.New("not supported")
}
func (f *fakeAPI) Temperature(uintptr) (float64, error) { return 66, nil }
func (f *fakeAPI) Memory(uintptr) (uint64, uint64, error) {
	return 1 << 30, 12 << 30, nil
}

func reader(api nvmlAPI, err error) (*NVML, *int) {
	opens := 0
	return &NVML{timeout: 200 * time.Millisecond, open: func() (nvmlAPI, error) { opens++; return api, err }}, &opens
}

func TestPartialDataIsKept(t *testing.T) {
	r, _ := reader(&fakeAPI{count: 1}, nil)
	d := r.Read()
	if len(d) != 1 || d[0].Name != "RTX 3060" || d[0].UtilPercent != nil || *d[0].TempC != 66 || *d[0].VRAMTotalBytes != 12<<30 {
		t.Fatalf("%+v", d)
	}
}

func TestNoDriverMeansNoDataAndRareRetries(t *testing.T) {
	r, opens := reader(nil, errors.New("no library"))
	for i := 0; i < retryEvery+1; i++ {
		if r.Read() != nil {
			t.Fatal("want no data")
		}
	}
	if *opens != 1 {
		t.Fatalf("opened %d times; must not retry every sample", *opens)
	}
	r.Read()
	if *opens != 2 {
		t.Fatalf("want a retry after %d reads, opens=%d", retryEvery, *opens)
	}
}

func TestPanicIsNotACrashAndRecovers(t *testing.T) {
	api := &fakeAPI{count: 1, panicOn: "count"}
	r, _ := reader(api, nil)
	if r.Read() != nil {
		t.Fatal("want no data after a panic")
	}
	api.panicOn = ""
	r.skips = 0
	if len(r.Read()) != 1 {
		t.Fatal("want data once NVML works again")
	}
}

func TestNVMLErrorsGiveNoData(t *testing.T) {
	r, _ := reader(&fakeAPI{countErr: errors.New("driver/library version mismatch")}, nil)
	if r.Read() != nil {
		t.Fatal("want no data")
	}
	r2, _ := reader(&fakeAPI{count: 2, handleEr: errors.New("gpu lost")}, nil)
	if r2.Read() != nil {
		t.Fatal("want no data when no device can be opened")
	}
}

func TestHungNVMLDoesNotBlockSampling(t *testing.T) {
	api := &fakeAPI{count: 1, block: make(chan struct{})}
	r, _ := reader(api, nil)
	start := time.Now()
	if r.Read() != nil || time.Since(start) > 2*time.Second {
		t.Fatal("hung call must return promptly with no data")
	}
	start = time.Now()
	if r.Read() != nil || time.Since(start) > 50*time.Millisecond {
		t.Fatal("a read while the previous is stuck must return at once")
	}
	close(api.block)
	time.Sleep(50 * time.Millisecond)
	if len(r.Read()) != 1 {
		t.Fatal("want data after NVML un-hangs")
	}
}

func TestMachineWithoutDriverNeverLoadsLibrary(t *testing.T) {
	// openDriverLibrary checks for the driver first; on this machine there is none.
	if driverPresent() {
		t.Skip("NVIDIA driver present")
	}
	if _, err := openDriverLibrary(); err == nil {
		t.Fatal("want an error without a driver")
	}
}
