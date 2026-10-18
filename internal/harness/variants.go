package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Degradation variants (ADR 0006). NewModern builds a machine that has every
// optional data source; each With*/As* method then takes one source away the
// way an old or minimal system would lack it, so a test can say exactly what
// is missing and assert through the JSON API and `report`.
//
//	m := harness.NewModern(t)   // PSI, cgroup v2, systemd, Docker socket, a GPU, Disk Swap + zram
//	m.AsCgroupV1()              // v1 hierarchy: /proc/<pid>/cgroup lines and /sys/fs/cgroup mounts
//	m.WithoutPSI()              // kernel without /proc/pressure
//	m.WithoutGPU()              // no NVIDIA driver, no DRM device
//	m.WithoutContainerRuntime() // no Docker or containerd socket
//	m.WithoutSystemd()          // no /run/systemd/system, cgroup paths without units
//	m.CompressedSwapOnly()      // zram only, no swap partition or file
//	m.OldKernelWithoutSmapsRollup() // kernel before 4.14: no /proc/<pid>/smaps_rollup
//
// Variants compose: call several on one machine.

// NewModern is the "dev-box-applications" machine plus every optional source.
func NewModern(t *testing.T) *Machine {
	t.Helper()
	m := New(t, "dev-box-applications")
	for _, r := range []string{"cpu", "io", "memory"} {
		m.WriteFile("/proc/pressure/"+r, "some avg10=0.00 avg60=0.00 avg300=0.00 total=2000000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=500000\n")
	}
	m.WriteFile("/proc/diskstats", "   259       0 nvme0n1 100 0 2000 10 100 0 4000 10 0 20 20\n")
	m.WriteFile("/sys/block/zram0/stat", "1 0 8 1 1 0 8 1 0 1 1\n") // lets the Advisor measure Compressed Swap traffic
	m.WriteFile("/sys/block/nvme0n1/size", "1000000\n")
	m.WriteFile("/sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory pids\n")
	m.WriteFile("/run/systemd/system/.keep", "")
	m.Docker.Add(DockerContainer{ID: strings.Repeat("ab", 32), Name: "web"}) // brings up the Docker socket
	m.WriteFile("/sys/class/drm/card0/device/vendor", "0x10de\n")
	return m
}

// eachCgroupFile calls fn with the path and content of every /proc/<pid>/cgroup.
func (m *Machine) eachCgroupFile(fn func(path, content string)) {
	m.t.Helper()
	entries, err := os.ReadDir(filepath.Join(m.Root, "proc"))
	if err != nil {
		m.t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := "/proc/" + e.Name() + "/cgroup"
		if b, err := os.ReadFile(filepath.Join(m.Root, p)); err == nil {
			fn(p, string(b))
		}
	}
}

// AsCgroupV1 replaces the unified hierarchy by cgroup v1: named controllers
// plus the "name=systemd" hierarchy, as on Ubuntu 18.04 or CentOS 7.
func (m *Machine) AsCgroupV1() {
	m.t.Helper()
	m.Remove("/sys/fs/cgroup/cgroup.controllers")
	for _, c := range []string{"memory", "cpu,cpuacct", "systemd"} {
		m.WriteFile("/sys/fs/cgroup/"+c+"/.keep", "")
	}
	m.eachCgroupFile(func(p, content string) {
		path := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(content, "\n", 2)[0], "0::"))
		m.WriteFile(p, "12:memory:"+path+"\n3:cpu,cpuacct:"+path+"\n1:name=systemd:"+path+"\n")
	})
}

// WithoutPSI removes /proc/pressure, as on a kernel older than 4.20.
func (m *Machine) WithoutPSI() { m.t.Helper(); m.Remove("/proc/pressure") }

// WithoutGPU removes every GPU a probe could find.
func (m *Machine) WithoutGPU() {
	m.t.Helper()
	m.Remove("/sys/class/drm")
	m.Remove("/proc/driver/nvidia")
}

// WithoutContainerRuntime removes the Docker and containerd sockets.
func (m *Machine) WithoutContainerRuntime() {
	m.t.Helper()
	m.socks.removeAll()
}

// WithoutSystemd makes the machine run a different init: no /run/systemd/system,
// and cgroup paths without any unit, scope or slice.
func (m *Machine) WithoutSystemd() {
	m.t.Helper()
	m.Remove("/run/systemd")
	m.Remove("/sys/fs/cgroup/systemd")
	m.eachCgroupFile(func(p, _ string) { m.WriteFile(p, "2:cpu,cpuacct:/\n1:memory:/\n") })
	m.Remove("/sys/fs/cgroup/cgroup.controllers")
	m.WriteFile("/sys/fs/cgroup/memory/.keep", "")
}

// CompressedSwapOnly drops every swap partition and file, keeping zram.
func (m *Machine) CompressedSwapOnly() {
	m.t.Helper()
	var keep []string
	for i, l := range strings.Split(m.ReadFile("/proc/swaps"), "\n") {
		if i == 0 || strings.HasPrefix(l, "/dev/zram") {
			keep = append(keep, l)
		}
	}
	m.WriteFile("/proc/swaps", strings.Join(keep, "\n")+"\n")
}

// OldKernelWithoutSmapsRollup removes /proc/<pid>/smaps_rollup (Linux before 4.14).
func (m *Machine) OldKernelWithoutSmapsRollup() {
	m.t.Helper()
	entries, err := os.ReadDir(filepath.Join(m.Root, "proc"))
	if err != nil {
		m.t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			m.Remove("/proc/" + e.Name() + "/smaps_rollup")
		}
	}
}
