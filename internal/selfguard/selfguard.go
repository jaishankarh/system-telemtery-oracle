// Package selfguard protects the program from the kernel OOM killer and keeps
// it under its own memory cap. These are the only things the program ever
// changes (ADR 0003), and only about itself. All effects go through
// machine.Host so tests never write anything real.
package selfguard

import (
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

const (
	// OOMScoreAdj exempts the process from the OOM killer.
	OOMScoreAdj = -1000
	// DefaultCap is the internal memory cap when systemd supplies none.
	DefaultCap uint64 = 64 << 20
	// ShedFraction of the cap at which caches are shed.
	ShedFraction = 0.8
	// UnitName is the installed systemd unit.
	UnitName = "sys-telemetry-app.service"
	// UnitPath is where the unit file lives.
	UnitPath = "/etc/systemd/system/" + UnitName
	// UnitMemoryMax is written into the unit as MemoryMax.
	UnitMemoryMax = "64M"
)

// Status is what /api/self reports.
type Status struct {
	OOMScoreAdj    *int   `json:"oom_score_adj"`
	OOMProtected   bool   `json:"oom_protected"`
	OOMError       string `json:"oom_error,omitempty"`
	MemoryCapBytes uint64 `json:"memory_cap_bytes"`
	CapSource      string `json:"memory_cap_source"`
	RSSBytes       uint64 `json:"rss_bytes"`
	ShedCount      int    `json:"shed_count"`
}

// Guard enforces the program's own limits.
type Guard struct {
	m        machine.Machine
	mu       sync.Mutex
	st       Status
	shedders []func()
}

// Start sets the own OOM score and works out the memory cap. A failure to set
// the score (for example, not root) is recorded, not fatal (ADR 0006).
func Start(m machine.Machine) *Guard {
	g := &Guard{m: m}
	if err := m.Host.SetOOMScoreAdj(OOMScoreAdj); err != nil {
		g.st.OOMError = err.Error()
	} else {
		v := OOMScoreAdj
		g.st.OOMScoreAdj, g.st.OOMProtected = &v, true
	}
	g.st.MemoryCapBytes, g.st.CapSource = DefaultCap, "internal"
	if c, ok := systemdCap(m); ok && c < DefaultCap {
		g.st.MemoryCapBytes, g.st.CapSource = c, "systemd"
	}
	return g
}

// OnShed registers a function that drops a cache when memory nears the cap.
func (g *Guard) OnShed(f func()) { g.mu.Lock(); g.shedders = append(g.shedders, f); g.mu.Unlock() }

// Check compares own memory to the cap and sheds caches near it.
func (g *Guard) Check() {
	rss, err := g.m.Host.SelfRSSBytes()
	if err != nil {
		return
	}
	g.mu.Lock()
	g.st.RSSBytes = rss
	if float64(rss) < ShedFraction*float64(g.st.MemoryCapBytes) {
		g.mu.Unlock()
		return
	}
	g.st.ShedCount++
	fs := append([]func(){}, g.shedders...)
	g.mu.Unlock()
	for _, f := range fs {
		f()
	}
	debug.FreeOSMemory()
}

// Status returns a snapshot.
func (g *Guard) Status() Status { g.mu.Lock(); defer g.mu.Unlock(); return g.st }

// systemdCap reads this process's cgroup v2 memory.max, if set.
func systemdCap(m machine.Machine) (uint64, bool) {
	b, err := m.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			mb, err := m.ReadFile("/sys/fs/cgroup" + strings.TrimSpace(rest) + "/memory.max")
			if err != nil {
				return 0, false
			}
			n, err := strconv.ParseUint(strings.TrimSpace(string(mb)), 10, 64)
			return n, err == nil && n > 0
		}
	}
	return 0, false
}

// Unit renders the systemd unit for the binary at exe.
func Unit(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=sys-telemetry-app: system telemetry and bottleneck analyzer
After=local-fs.target

[Service]
ExecStart=%s run
Restart=on-failure
MemoryMax=%s
OOMScoreAdjust=%d

[Install]
WantedBy=multi-user.target
`, exe, UnitMemoryMax, OOMScoreAdj)
}

// Install writes and enables the unit.
func Install(m machine.Machine) error {
	exe, err := m.Host.Executable()
	if err != nil {
		return err
	}
	if err := m.Host.WriteUnit(UnitPath, []byte(Unit(exe))); err != nil {
		return err
	}
	if err := m.Host.Systemctl("daemon-reload"); err != nil {
		return err
	}
	return m.Host.Systemctl("enable", "--now", UnitName)
}

// Uninstall disables the unit and removes its file.
func Uninstall(m machine.Machine) error {
	disableErr := m.Host.Systemctl("disable", "--now", UnitName)
	if err := m.Host.RemoveUnit(UnitPath); err != nil {
		return err
	}
	if err := m.Host.Systemctl("daemon-reload"); err != nil {
		return err
	}
	return disableErr
}
