package machine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Host is the part of the world the program may CHANGE. It is deliberately
// tiny (ADR 0003): only the program's own OOM score and its own systemd unit.
// Tests substitute a recording fake, so nothing is ever really written.
type Host interface {
	// SetOOMScoreAdj sets the OOM score adjustment of the program's own process.
	SetOOMScoreAdj(v int) error
	// SelfRSSBytes is the resident memory of the program's own process.
	SelfRSSBytes() (uint64, error)
	// WriteUnit creates or replaces the program's systemd unit file.
	WriteUnit(path string, content []byte) error
	// RemoveUnit deletes the program's systemd unit file (absent is fine).
	RemoveUnit(path string) error
	// Systemctl runs systemctl with args.
	Systemctl(args ...string) error
	// Executable is the absolute path of the running binary.
	Executable() (string, error)
}

type realHost struct{}

func (realHost) SetOOMScoreAdj(v int) error {
	return os.WriteFile("/proc/self/oom_score_adj", []byte(strconv.Itoa(v)), 0o644)
}

func (realHost) SelfRSSBytes() (uint64, error) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				kb, err := strconv.ParseUint(f[0], 10, 64)
				return kb * 1024, err
			}
		}
	}
	return 0, fmt.Errorf("VmRSS not found")
}

func (realHost) WriteUnit(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func (realHost) RemoveUnit(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (realHost) Systemctl(args ...string) error {
	var out bytes.Buffer
	c := exec.Command("systemctl", args...)
	c.Stdout, c.Stderr = &out, &out
	if err := c.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}

func (realHost) Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
