// Package harness is the fake-Machine test harness. Every ticket's tests use
// it; extend it, do not fork it.
//
// Idea: run the REAL program (cli.Run) in-process against a fake Machine and
// assert only through what a user sees: the JSON API and `report` output.
//
// API:
//
//	m := harness.New(t, "laptop-disk-and-zram") // copy testdata/fixtures/<name> to a temp root
//	m.WriteFile("/proc/vmstat", "pswpin 5\n")   // change the machine (also: Remove, ReadFile)
//	m.Clock.Advance(2 * time.Second)            // controllable clock (Clock.OnSleep hook too)
//
//	out := m.Report()                            // run `report`, return stdout
//	p := m.Start("--interval", "2s")             // run the program in the background
//	p.GetJSON("/api/system", &v)                 // GET + decode JSON from 127.0.0.1
//	p.Tick()                                     // advance one collection interval, wait for it
//	p.Stop()                                     // cancel (also registered with t.Cleanup)
//	m.Run(args...)                               // run any CLI invocation, returns code, stdout, stderr
//
//	m.Host.OOMScoreAdj / Units / Systemctls / SetRSS(n)  // what the program changed on the fake Host
//
// Fixture "dev-box-process-tree" is the laptop fixture plus a recorded process
// tree: /proc/<pid>/{stat,status,cmdline,io,smaps_rollup,fd/*} for five
// processes (init, a kernel thread, an editor extension host, a browser content
// process and a node server) and /etc/passwd. Edit those files with WriteFile
// to move a process's counters between Ticks. Process endpoint: GET
// /api/processes?sort=<json field>&order=asc|desc&filter=<pid or regex>.
//
// Fixture "dev-box-applications" (regenerate with scripts/gen-applications-fixture.py)
// is the same machine with 18 processes forming a desktop session: editor and
// language server in a desktop scope, a flatpak browser, a snap, a service, a
// terminal with a dev server, a kernel thread pair, orphans and one process
// nothing can name. Each has /proc/<pid>/{cgroup,cwd (symlink),stat,status,...}.
// Application endpoint: GET /api/applications?sort=<json field>&order=asc|desc&filter=<regex>.
//
// Container runtimes: m.Docker.Add(harness.DockerContainer{...}) and
// m.Containerd.Add(harness.ContainerdContainer{...}) create fake sockets
// (Docker Engine API; containerd gRPC) on first use. m.AddProcess(pid, ppid,
// comm, cmdline, cgroup, pssKB) adds a recorded process, for example one
// whose cgroup path names a container. Container and Pod Applications carry
// a "container" object in /api/applications (runtime, started_by,
// memory_limit_bytes, containers).
//
// Machine.Root is a temp dir, so config files live at e.g. m.WriteFile("/etc/sys-telemetry-app/config.toml", ...).
// Later tickets add fixtures under testdata/fixtures and helpers here (fake
// container sockets, fake GPU reader) as new fields on Machine.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/cli"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Machine is a fake machine plus the means to run the program on it.
type Machine struct {
	t     *testing.T
	Root  string
	Clock *FakeClock
	// Host records what the program would change (OOM score, systemd unit); nothing is really written.
	Host *FakeHost
	// Docker and Containerd are fake container-runtime sockets. A runtime
	// nothing was added to has no socket, like a machine without it.
	Docker     *FakeDocker
	Containerd *FakeContainerd
	socks      *sockets
}

// New creates a fake machine from testdata/fixtures/<fixture>.
func New(t *testing.T, fixture string) *Machine {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures", fixture)
	root := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(root, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 { // recorded /proc/<pid>/cwd links
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatalf("load fixture %q: %v", fixture, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	socks := newSockets(t)
	return &Machine{t: t, Root: root, Clock: NewFakeClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)), Host: NewFakeHost(),
		Docker: &FakeDocker{s: socks}, Containerd: &FakeContainerd{s: socks}, socks: socks}
}

func (m *Machine) machine() machine.Machine {
	return machine.Machine{Root: m.Root, Clock: m.Clock, StateDir: filepath.Join(m.Root, "state"), Host: m.Host, Dial: m.socks.dial}
}

// WriteFile creates or replaces a file at an absolute system path.
func (m *Machine) WriteFile(path, content string) {
	m.t.Helper()
	p := filepath.Join(m.Root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

// ReadFile returns the current content of a file on the fake machine, so a
// test can edit a recorded file (for example bump a counter in /proc/<pid>/stat).
func (m *Machine) ReadFile(path string) string {
	m.t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Root, path))
	if err != nil {
		m.t.Fatal(err)
	}
	return string(b)
}

// Remove deletes a file, simulating a missing data source.
func (m *Machine) Remove(path string) {
	m.t.Helper()
	if err := os.RemoveAll(filepath.Join(m.Root, path)); err != nil {
		m.t.Fatal(err)
	}
}

// Run executes one CLI invocation to completion and returns exit code, stdout, stderr.
func (m *Machine) Run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := cli.Run(context.Background(), cli.Env{Machine: m.machine(), Stdout: &out, Stderr: &errb}, args)
	return code, out.String(), errb.String()
}

// Report runs `report` and returns its stdout, failing the test on error.
func (m *Machine) Report(args ...string) string {
	m.t.Helper()
	code, out, errs := m.Run(append([]string{"report"}, args...)...)
	if code != 0 {
		m.t.Fatalf("report exited %d: %s", code, errs)
	}
	return out
}

// Program is a running instance of the program.
type Program struct {
	t      *testing.T
	m      *Machine
	Addr   string
	cancel context.CancelFunc
	done   chan int
	once   sync.Once
	// Interval used to advance the clock on Tick.
	Interval time.Duration
}

// Start runs `run` in the background on port 0 (override with --port) and
// waits until it is listening. Pass flags such as "--interval", "2s".
func (m *Machine) Start(args ...string) *Program {
	m.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	p := &Program{t: m.t, m: m, cancel: cancel, done: make(chan int, 1), Interval: 2 * time.Second}
	full := append([]string{"run", "--port", "0"}, args...)
	go func() {
		p.done <- cli.Run(ctx, cli.Env{Machine: m.machine(), Stdout: io.Discard, Stderr: io.Discard,
			OnListen: func(a string) { ready <- a }}, full)
	}()
	select {
	case p.Addr = <-ready:
	case code := <-p.done:
		cancel()
		m.t.Fatalf("program exited early with %d", code)
	case <-time.After(5 * time.Second):
		cancel()
		m.t.Fatal("program did not start listening")
	}
	m.t.Cleanup(p.Stop)
	return p
}

// Stop cancels the program and waits for it to exit.
func (p *Program) Stop() {
	p.once.Do(func() {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			p.t.Error("program did not stop")
		}
	})
}

// Get fetches a path over HTTP and returns status and body.
func (p *Program) Get(path string) (int, []byte) {
	p.t.Helper()
	resp, err := http.Get("http://" + p.Addr + path)
	if err != nil {
		p.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// GetJSON fetches a path and decodes the JSON body into v.
func (p *Program) GetJSON(path string, v any) {
	p.t.Helper()
	code, b := p.Get(path)
	if code != 200 {
		p.t.Fatalf("GET %s: status %d: %s", path, code, b)
	}
	if err := json.Unmarshal(b, v); err != nil {
		p.t.Fatalf("GET %s: bad JSON: %v\n%s", path, err, b)
	}
}

// Tick advances the fake clock by one interval and waits until the program
// has published a sample taken at the new time (observed via /api/system).
func (p *Program) Tick() {
	p.t.Helper()
	p.m.Clock.Advance(p.Interval)
	want := p.m.Clock.Now().Format(time.RFC3339Nano)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var s struct {
			Time time.Time `json:"time"`
		}
		p.GetJSON("/api/system", &s)
		if s.Time.Format(time.RFC3339Nano) == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	p.t.Fatal("program did not take a sample after Tick")
}
