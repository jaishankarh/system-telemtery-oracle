package grouping

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/containers"
)

// ContainerMember is one container inside a Container or Pod Application.
type ContainerMember struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Runtime     string  `json:"runtime"`    // docker or containerd
	StartedBy   string  `json:"started_by"` // compose, docker run, kubernetes, nerdctl, containerd
	MemoryLimit *uint64 `json:"memory_limit_bytes"`
	Sandbox     bool    `json:"sandbox,omitempty"` // a Pod's pause container
	// ComposeProject and ComposeService name the Compose service of a Compose-started container.
	ComposeProject string `json:"compose_project,omitempty"`
	ComposeService string `json:"compose_service,omitempty"`
}

// ContainerInfo is what the runtimes say about a Container or Pod. MemoryLimit
// is nil when there is no limit; a Pod has one only when every one of its
// containers does (the sum). Settings Fix for an uncapped container reads it.
type ContainerInfo struct {
	Runtime     string            `json:"runtime"`
	StartedBy   string            `json:"started_by"`
	MemoryLimit *uint64           `json:"memory_limit_bytes"`
	Containers  []ContainerMember `json:"containers"`
}

// ContainerPod groups the Processes inside a Docker or containerd container
// into a Container, and those in Kubernetes containers into one Pod per pod.
// Containers are found by the container ID in a Process's cgroup path (Docker,
// containerd, CRI and systemd cgroup drivers all embed it); names and limits
// come from the runtime sockets, asked once per sample.
//
// Asking the runtimes can be slow (one Docker call per container, a hung socket),
// so it never happens on the sampling path: a background refresh repopulates the
// map every containerTTL and Prepare reads the last answer. Only the very first
// Prepare waits, briefly, so the first sample already names its containers.
type ContainerPod struct {
	byID map[string]containers.Info // used by Match; copied from cache in Prepare

	mu       sync.Mutex
	cache    map[string]containers.Info
	fetched  time.Time
	have     bool
	inflight chan struct{} // non-nil while a refresh runs; closed when it ends
}

const (
	containerTTL       = 15 * time.Second
	containerFirstWait = 500 * time.Millisecond
)

func (*ContainerPod) Name() string { return "container / pod" }

// Prepare reads the cached running containers, starting a background refresh
// when they are older than containerTTL. No runtime, no match.
func (l *ContainerPod) Prepare(c *Context) {
	now := c.Machine.Clock.Now()
	l.mu.Lock()
	if l.inflight == nil && (!l.have || now.Sub(l.fetched) >= containerTTL) {
		done := make(chan struct{})
		l.inflight = done
		go func() {
			found := containers.Discover(c.Machine)
			l.mu.Lock()
			l.cache, l.have, l.fetched = found, true, c.Machine.Clock.Now()
			l.inflight = nil
			l.mu.Unlock()
			close(done)
		}()
	}
	wait := l.inflight
	first := !l.have
	l.mu.Unlock()
	if first && wait != nil {
		select {
		case <-wait:
		case <-time.After(containerFirstWait):
		}
	}
	l.mu.Lock()
	l.byID = l.cache
	l.mu.Unlock()
}

func (l *ContainerPod) Match(p *collector.Process, _ *Context) (Match, bool) {
	if len(l.byID) == 0 {
		return Match{}, false
	}
	in, ok := l.lookup(p.Cgroup)
	if !ok {
		return Match{}, false
	}
	m := &ContainerMember{ID: in.ID, Name: in.Name, Runtime: in.Runtime, StartedBy: in.StartedBy,
		MemoryLimit: in.MemoryLimit, Sandbox: in.Sandbox,
		ComposeProject: in.ComposeProject, ComposeService: in.ComposeService}
	if in.PodName != "" {
		return Match{Kind: KindPod, Key: in.PodNamespace + "/" + in.PodName, Name: in.PodName, Sub: in.Name, Container: m}, true
	}
	return Match{Kind: KindContainer, Key: in.ID, Name: in.Name, Container: m}, true
}

// lookup finds the deepest cgroup path component naming a known container, so
// a container inside a kind node resolves to the inner one when known and to
// the node's own Docker container otherwise.
func (l *ContainerPod) lookup(cgroup string) (containers.Info, bool) {
	parts := strings.Split(cgroup, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		id := strings.TrimSuffix(parts[i], ".scope")
		for _, pre := range []string{"docker-", "cri-containerd-", "nerdctl-", "containerd-"} {
			id = strings.TrimPrefix(id, pre)
		}
		if in, ok := l.byID[id]; ok {
			return in, true
		}
	}
	return containers.Info{}, false
}

func (a *Application) noteContainer(m *ContainerMember) {
	if m == nil {
		return
	}
	if a.Container == nil {
		a.Container = &ContainerInfo{Containers: []ContainerMember{}}
	}
	for _, e := range a.Container.Containers {
		if e.ID == m.ID {
			return
		}
	}
	a.Container.Containers = append(a.Container.Containers, *m)
}

func (a *Application) finishContainer() {
	ci := a.Container
	if ci == nil {
		return
	}
	sort.Slice(ci.Containers, func(i, j int) bool { return ci.Containers[i].Name < ci.Containers[j].Name })
	ci.Runtime, ci.StartedBy = ci.Containers[0].Runtime, ci.Containers[0].StartedBy
	var sum uint64
	limited := false
	for _, m := range ci.Containers {
		if m.Sandbox {
			continue
		}
		if m.MemoryLimit == nil {
			ci.MemoryLimit = nil
			return
		}
		sum += *m.MemoryLimit
		limited = true
	}
	if limited {
		ci.MemoryLimit = &sum
	}
}
