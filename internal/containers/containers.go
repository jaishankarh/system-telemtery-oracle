// Package containers asks the container runtimes what the containers on this
// machine are called and what memory limit each has. It talks to the Docker
// API socket and the containerd socket (including k3s) through the Machine,
// never to a CLI (SPEC section 3). A runtime that is not installed, or whose
// socket cannot be read, is skipped silently (ADR 0006).
package containers

import (
	"sort"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// How a Container was started, as far as the runtime reveals it.
const (
	StartedCompose    = "compose"
	StartedDockerRun  = "docker run"
	StartedKubernetes = "kubernetes"
	StartedNerdctl    = "nerdctl"
	StartedContainerd = "containerd"
)

// Runtimes.
const (
	RuntimeDocker     = "docker"
	RuntimeContainerd = "containerd"
)

// Info is what a runtime knows about one container.
type Info struct {
	ID          string
	Name        string
	Runtime     string
	StartedBy   string
	MemoryLimit *uint64 // nil: the container has no memory limit
	// Pod is set for a Kubernetes container; its containers form one Pod.
	PodName      string
	PodNamespace string
	// ComposeProject and ComposeService are set for a Compose-started container.
	ComposeProject string
	ComposeService string
	// Sandbox marks the pause container that holds a Pod's namespaces.
	Sandbox bool
}

const (
	labelPodName      = "io.kubernetes.pod.name"
	labelPodNamespace = "io.kubernetes.pod.namespace"
	labelContainer    = "io.kubernetes.container.name"
	labelCRIKind      = "io.cri-containerd.kind"
	labelCompose      = "com.docker.compose.project"
	labelNerdctlName  = "nerdctl/name"
	labelComposeSvc   = "com.docker.compose.service"
)

// Socket locations, tried in order; the first that answers is used.
var (
	dockerSockets     = []string{"/var/run/docker.sock", "/run/docker.sock"}
	containerdSockets = [][]string{
		{"/run/containerd/containerd.sock", "/var/run/containerd/containerd.sock"},
		{"/run/k3s/containerd/containerd.sock"},
	}
)

// Discover returns every running container the runtimes report, keyed by
// container ID. A container known to Docker is reported by Docker, because
// Docker also keeps its containers in containerd's "moby" namespace.
func Discover(m machine.Machine) map[string]Info {
	out := map[string]Info{}
	for _, group := range containerdSockets {
		for _, sock := range group {
			if list, ok := containerdList(m, sock); ok {
				for _, in := range list {
					out[in.ID] = in
				}
				break
			}
		}
	}
	for _, sock := range dockerSockets {
		if list, ok := dockerList(m, sock); ok {
			for _, in := range list {
				out[in.ID] = in
			}
			break
		}
	}
	return out
}

// SortedIDs is a helper for deterministic iteration.
func SortedIDs(m map[string]Info) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func startedBy(labels map[string]string, def string) string {
	switch {
	case labels[labelPodName] != "":
		return StartedKubernetes
	case labels[labelCompose] != "":
		return StartedCompose
	}
	return def
}
