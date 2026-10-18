// Package machine is the single boundary between the program and the outside
// world: a filesystem root (where /proc, /sys and cgroup files are read from),
// a clock and a state directory. Production code uses Real(); tests use the
// fake built by package harness. Everything else reaches the world only
// through a Machine.
package machine

import (
	"os"
	"path/filepath"
	"time"
)

// Clock abstracts time so tests can control it.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
	// Sleep blocks for d (a fake clock advances instead of blocking).
	Sleep(d time.Duration)
}

// Ticker is the subset of time.Ticker the program uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Machine is the world as the program sees it.
type Machine struct {
	// Root is prepended to every absolute path read ("/" in production).
	Root string
	// Clock is the time source.
	Clock Clock
	// StateDir holds persistent state (/var/lib/sys-telemetry-app in production).
	StateDir string
	// Host is what the program may change (its own OOM score and systemd unit).
	Host Host
}

// Real returns the Machine backed by the actual system.
func Real() Machine {
	return Machine{Root: "/", Clock: realClock{}, StateDir: "/var/lib/sys-telemetry-app", Host: realHost{}}
}

// Path maps an absolute system path such as "/proc/meminfo" under Root.
func (m Machine) Path(p string) string { return filepath.Join(m.Root, p) }

// ReadFile reads an absolute system path through the Machine.
func (m Machine) ReadFile(p string) ([]byte, error) { return os.ReadFile(m.Path(p)) }

// Glob lists absolute system paths matching pattern, relative to Root.
func (m Machine) Glob(pattern string) []string {
	matches, _ := filepath.Glob(m.Path(pattern))
	out := make([]string, 0, len(matches))
	for _, p := range matches {
		rel, err := filepath.Rel(m.Root, p)
		if err == nil {
			out = append(out, "/"+filepath.ToSlash(rel))
		}
	}
	return out
}

type realClock struct{}

func (realClock) Now() time.Time                   { return time.Now() }
func (realClock) Sleep(d time.Duration)            { time.Sleep(d) }
func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
