package availability

import (
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Runtime sockets that name Containers and Pods (Docker, containerd, k3s).
var runtimeSockets = []string{
	"/var/run/docker.sock", "/run/docker.sock",
	"/run/containerd/containerd.sock", "/var/run/containerd/containerd.sock",
	"/run/k3s/containerd/containerd.sock",
}

func detect(m machine.Machine) Environment {
	env := Environment{Cgroup: cgroupMode(m), ContainerRuntimes: []string{}, GPUs: []string{}}
	_, err := m.ReadDir("/run/systemd/system")
	env.Systemd = err == nil
	for _, s := range runtimeSockets {
		if c, err := m.DialSocket(s); err == nil { // a runtime is there only if it answers
			c.Close()
			env.ContainerRuntimes = append(env.ContainerRuntimes, s)
		}
	}
	env.GPUs = append(env.GPUs, m.Glob("/sys/class/drm/card*/device/vendor")...)
	env.GPUs = append(env.GPUs, m.Glob("/proc/driver/nvidia/version")...)
	return env
}

// CgroupMode reports which cgroup hierarchy m has: "v2", "v1", "hybrid" or "none".
func CgroupMode(m machine.Machine) string { return cgroupMode(m) }

func cgroupMode(m machine.Machine) string {
	v2 := len(m.Glob("/sys/fs/cgroup/cgroup.controllers")) > 0
	v1 := len(m.Glob("/sys/fs/cgroup/memory")) > 0 || len(m.Glob("/sys/fs/cgroup/cpu")) > 0 || len(m.Glob("/sys/fs/cgroup/systemd")) > 0
	hybrid := len(m.Glob("/sys/fs/cgroup/unified/cgroup.controllers")) > 0
	switch {
	case v2 && !v1:
		return "v2"
	case v1 && hybrid, v1 && v2:
		return "hybrid"
	case v1:
		return "v1"
	}
	return "none"
}

func init() {
	RegisterProbe("cgroup", func(_ machine.Machine, env Environment) *Item {
		if env.Cgroup != "none" {
			return nil
		}
		return &Item{ID: "cgroup", Feature: "Control groups (cgroup)",
			Reason:  "no cgroup filesystem is mounted, so Processes cannot be placed in units, Containers or Pods",
			Affects: []string{"Container and Pod rows", "systemd unit names", "Uncapped container"}}
	})
	RegisterProbe("systemd", func(_ machine.Machine, env Environment) *Item {
		if env.Systemd {
			return nil
		}
		return &Item{ID: "systemd", Feature: "systemd",
			Reason:  "systemd is not the init system on this machine; Applications are named from the process tree instead of units, and install cannot enable a service",
			Affects: []string{"systemd unit names", "install and uninstall"}}
	})
	RegisterProbe("container_runtime", func(_ machine.Machine, env Environment) *Item {
		if len(env.ContainerRuntimes) > 0 {
			return nil
		}
		return &Item{ID: "container_runtime", Feature: "Container runtime",
			Reason:  "no Docker or containerd socket found, so Containers and Pods cannot be named; their processes appear as ordinary Applications",
			Affects: []string{"Container and Pod rows", "Uncapped container"}}
	})
	RegisterProbe("gpu", func(_ machine.Machine, env Environment) *Item {
		if len(env.GPUs) > 0 {
			return nil
		}
		return &Item{ID: "gpu", Feature: "GPU",
			Reason:  "no GPU was found (no NVIDIA driver and no DRM device in sysfs)",
			Affects: []string{"GPU graph"}}
	})
}
