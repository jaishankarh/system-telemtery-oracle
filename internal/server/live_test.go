package server_test

import (
	"bufio"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type sysPressure struct {
	Pressure struct {
		Memory struct {
			Some *float64 `json:"some_percent"`
			Full *float64 `json:"full_percent"`
		} `json:"memory"`
		IO struct {
			Some *float64 `json:"some_percent"`
		} `json:"io"`
	} `json:"pressure"`
	Disks []struct {
		Device string   `json:"device"`
		Read   *float64 `json:"read_bytes_per_sec"`
		Write  *float64 `json:"write_bytes_per_sec"`
	} `json:"disks"`
}

func near(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 0.01 }

// Pressure stall and per-disk throughput are rates over the sampling interval.
func TestSystemPressureAndDiskThroughput(t *testing.T) {
	m := harness.New(t, "laptop-live")
	p := m.Start()
	// 2s later: memory some stalled 200ms (10%), full 100ms (5%), io some 400ms (20%);
	// the disk read 2048 sectors (1 MiB) and wrote 4096 sectors (2 MiB).
	m.WriteFile("/proc/pressure/memory", "some avg10=0.00 avg60=0.00 avg300=0.00 total=2200000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=600000\n")
	m.WriteFile("/proc/pressure/io", "some avg10=0.00 avg60=0.00 avg300=0.00 total=3400000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=1000000\n")
	m.WriteFile("/proc/diskstats", " 259 0 nvme0n1 1000 0 82048 500 2000 0 164096 900 0 1000 1400 0 0 0 0 0 0\n 259 1 nvme0n1p1 1 0 1 0 1 0 1 0 0 0 0\n 7 0 loop0 10 0 100 1 0 0 0 0 0 1 1\n")
	p.Tick()
	var s sysPressure
	p.GetJSON("/api/system", &s)
	if !near(s.Pressure.Memory.Some, 10) || !near(s.Pressure.Memory.Full, 5) || !near(s.Pressure.IO.Some, 20) {
		t.Fatalf("pressure %+v", s.Pressure)
	}
	if len(s.Disks) != 1 || s.Disks[0].Device != "nvme0n1" {
		t.Fatalf("want only the whole device nvme0n1, got %+v", s.Disks)
	}
	if !near(s.Disks[0].Read, 512*1024) || !near(s.Disks[0].Write, 1024*1024) {
		t.Fatalf("disk rates %+v", s.Disks[0])
	}
}

// A kernel without pressure stall data omits it and says so (ADR 0006).
func TestSystemWithoutPressureStall(t *testing.T) {
	m := harness.New(t, "laptop-live")
	m.Remove("/proc/pressure")
	p := m.Start()
	p.Tick()
	var s struct {
		Pressure *struct{} `json:"pressure"`
		Missing  []string  `json:"missing"`
	}
	p.GetJSON("/api/system", &s)
	if s.Pressure != nil || !strings.Contains(strings.Join(s.Missing, ","), "/proc/pressure") {
		t.Fatalf("pressure=%v missing=%v", s.Pressure, s.Missing)
	}
}

type livePoint struct {
	Time   time.Time `json:"time"`
	CPU    *float64  `json:"cpu_percent"`
	SwapIn *float64  `json:"swap_in_pages_per_sec"`
	Apps   []struct {
		Name string `json:"name"`
		PSS  uint64 `json:"pss_bytes"`
		Swap uint64 `json:"swap_bytes"`
	} `json:"top_apps"`
}
type liveDoc struct {
	WindowSeconds   int         `json:"window_seconds"`
	IntervalSeconds float64     `json:"interval_seconds"`
	Points          []livePoint `json:"points"`
}

// The Live buffer holds the last 5 minutes and no more.
func TestLiveBufferHoldsFiveMinutes(t *testing.T) {
	m := harness.New(t, "laptop-live")
	p := m.Start()
	for i := 0; i < 200; i++ {
		p.Tick()
	}
	var d liveDoc
	p.GetJSON("/api/live", &d)
	if d.WindowSeconds != 300 || d.IntervalSeconds != 2 {
		t.Fatalf("doc %+v", d)
	}
	n := len(d.Points)
	span := d.Points[n-1].Time.Sub(d.Points[0].Time)
	if span > 300*time.Second || span < 296*time.Second {
		t.Fatalf("span %v over %d points", span, n)
	}
}

func TestLiveTopApplicationsSplitRAMAndSwap(t *testing.T) {
	m := harness.New(t, "dev-box-applications")
	p := m.Start()
	p.Tick()
	var d liveDoc
	p.GetJSON("/api/live", &d)
	last := d.Points[len(d.Points)-1]
	if len(last.Apps) == 0 || len(last.Apps) > 10 {
		t.Fatalf("top apps %d", len(last.Apps))
	}
	var apps struct {
		Applications []struct {
			Name string  `json:"name"`
			PSS  *uint64 `json:"pss_bytes"`
		} `json:"applications"`
	}
	p.GetJSON("/api/applications", &apps)
	if last.Apps[0].Name != apps.Applications[0].Name || last.Apps[0].PSS != *apps.Applications[0].PSS {
		t.Fatalf("top app %+v vs applications view %+v", last.Apps[0], apps.Applications[0])
	}
}

type stats struct {
	Clients       int `json:"clients"`
	StreamEncodes int `json:"stream_encodes"`
}

func (p stream) stats() stats { var s stats; p.p.GetJSON("/api/live/stats", &s); return s }

type stream struct {
	p    *harness.Program
	body *bufio.Scanner
	resp *http.Response
}

func connect(t *testing.T, p *harness.Program) stream {
	resp, err := http.Get("http://" + p.Addr + "/api/live/stream")
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	return stream{p: p, body: bufio.NewScanner(resp.Body), resp: resp}
}

func (s stream) nextData(t *testing.T) string {
	done := make(chan string, 1)
	go func() {
		for s.body.Scan() {
			if l := s.body.Text(); strings.HasPrefix(l, "data: ") {
				done <- strings.TrimPrefix(l, "data: ")
				return
			}
		}
		done <- ""
	}()
	select {
	case d := <-done:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return ""
	}
}

func waitClients(t *testing.T, p *harness.Program, n int) {
	for i := 0; i < 500; i++ {
		var s stats
		p.GetJSON("/api/live/stats", &s)
		if s.Clients == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("clients never reached %d", n)
}

// With no browser connected no serialisation happens; it starts on connect and
// stops again when the last client leaves.
func TestStreamingOnlyWhileAClientIsConnected(t *testing.T) {
	m := harness.New(t, "laptop-live")
	p := m.Start()
	for i := 0; i < 5; i++ {
		p.Tick()
	}
	var s stats
	p.GetJSON("/api/live/stats", &s)
	if s.Clients != 0 || s.StreamEncodes != 0 {
		t.Fatalf("idle: %+v", s)
	}

	c := connect(t, p)
	waitClients(t, p, 1)
	p.Tick()
	var pt livePoint
	if d := c.nextData(t); !strings.Contains(d, `"cpu_percent"`) {
		t.Fatalf("event %q", d)
	}
	_ = pt
	p.GetJSON("/api/live/stats", &s)
	if s.StreamEncodes == 0 {
		t.Fatal("expected encoding while a client is connected")
	}

	c.resp.Body.Close()
	waitClients(t, p, 0)
	p.GetJSON("/api/live/stats", &s)
	before := s.StreamEncodes
	for i := 0; i < 5; i++ {
		p.Tick()
	}
	p.GetJSON("/api/live/stats", &s)
	if s.StreamEncodes != before {
		t.Fatalf("encoded %d events with no client", s.StreamEncodes-before)
	}

	c2 := connect(t, p)
	waitClients(t, p, 1)
	p.Tick()
	c2.nextData(t)
	c2.resp.Body.Close()
}

func TestGraphsViewIsEmbedded(t *testing.T) {
	m := harness.New(t, "laptop-live")
	p := m.Start()
	for _, path := range []string{"/uplot.min.js", "/uplot.min.css", "/graphs.js"} {
		if code, b := p.Get(path); code != 200 || len(b) < 100 {
			t.Fatalf("%s: %d, %d bytes", path, code, len(b))
		}
	}
	_, html := p.Get("/")
	for _, id := range []string{`id="graphs"`, "uplot.min.js", "graphs.js"} {
		if !strings.Contains(string(html), id) {
			t.Fatalf("index.html lacks %s", id)
		}
	}
}
