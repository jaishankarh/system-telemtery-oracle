package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

type ctrMember struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Runtime     string  `json:"runtime"`
	StartedBy   string  `json:"started_by"`
	MemoryLimit *uint64 `json:"memory_limit_bytes"`
	Sandbox     bool    `json:"sandbox"`
}

type ctrApp struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	PIDs      []int  `json:"pids"`
	Container *struct {
		Runtime     string      `json:"runtime"`
		StartedBy   string      `json:"started_by"`
		MemoryLimit *uint64     `json:"memory_limit_bytes"`
		Containers  []ctrMember `json:"containers"`
	} `json:"container"`
	Processes []struct {
		PID      int    `json:"pid"`
		Subgroup string `json:"subgroup"`
	} `json:"processes"`
}

func ctrApps(t *testing.T, m *harness.Machine) map[string]ctrApp {
	t.Helper()
	p := m.Start()
	var l struct {
		Applications []ctrApp `json:"applications"`
	}
	p.GetJSON("/api/applications", &l)
	out := map[string]ctrApp{}
	for _, a := range l.Applications {
		out[a.Name] = a
	}
	return out
}

var (
	id1 = strings.Repeat("a1", 32)
	id2 = strings.Repeat("b2", 32)
	id3 = strings.Repeat("c3", 32)
	id4 = strings.Repeat("d4", 32)
	id5 = strings.Repeat("e5", 32)
)

func TestDockerContainersAppearNamedWithLimitAndStartMethod(t *testing.T) {
	m := harness.New(t, apps)
	m.Docker.Add(harness.DockerContainer{ID: id1, Name: "shop-db-1", MemoryLimit: 512 << 20,
		Labels: map[string]string{"com.docker.compose.project": "shop"}})
	m.Docker.Add(harness.DockerContainer{ID: id2, Name: "redis-dev"})
	m.AddProcess(900, 1, "postgres", "postgres -D /data", "/system.slice/docker-"+id1+".scope", 100000)
	m.AddProcess(901, 900, "postgres", "postgres: writer", "/system.slice/docker-"+id1+".scope", 20000)
	m.AddProcess(910, 1, "redis-server", "redis-server *:6379", "/docker/"+id2, 5000)

	a := ctrApps(t, m)
	db := a["shop-db-1"]
	if db.Kind != "container" || len(db.PIDs) != 2 {
		t.Fatalf("shop-db-1: %+v", db)
	}
	if db.Container.StartedBy != "compose" || db.Container.Runtime != "docker" ||
		db.Container.MemoryLimit == nil || *db.Container.MemoryLimit != 512<<20 {
		t.Fatalf("shop-db-1 container info: %+v", db.Container)
	}
	r := a["redis-dev"]
	if r.Kind != "container" || r.Container.MemoryLimit != nil || r.Container.StartedBy != "docker run" {
		t.Fatalf("redis-dev (no limit, docker run): %+v", r.Container)
	}
}

func TestContainerdContainersAppearNamed(t *testing.T) {
	m := harness.New(t, apps)
	m.Containerd.Add(harness.ContainerdContainer{ID: id3, Namespace: "default",
		Labels: map[string]string{"nerdctl/name": "web"}, MemoryLimit: 256 << 20})
	m.Containerd.Add(harness.ContainerdContainer{ID: "plainctr", Namespace: "default"})
	m.AddProcess(920, 1, "nginx", "nginx -g daemon off", "/default/"+id3, 8000)
	m.AddProcess(921, 1, "sleep", "sleep 1000", "/default/plainctr", 100)

	a := ctrApps(t, m)
	w := a["web"]
	if w.Kind != "container" || w.Container.Runtime != "containerd" || w.Container.StartedBy != "nerdctl" ||
		w.Container.MemoryLimit == nil || *w.Container.MemoryLimit != 256<<20 {
		t.Fatalf("web: %+v", w)
	}
	if p := a["plainctr"]; p.Kind != "container" || p.Container.MemoryLimit != nil || p.Container.StartedBy != "containerd" {
		t.Fatalf("plainctr: %+v", p)
	}
}

func TestKubernetesPodsGroupTheirContainersWithSummedLimit(t *testing.T) {
	m := harness.New(t, apps)
	k8s := func(pod, ctr string, kind string) map[string]string {
		l := map[string]string{"io.kubernetes.pod.name": pod, "io.kubernetes.pod.namespace": "prod", "io.cri-containerd.kind": kind}
		if ctr != "" {
			l["io.kubernetes.container.name"] = ctr
		}
		return l
	}
	m.Containerd.Path = "/run/k3s/containerd/containerd.sock" // k3s
	m.Containerd.Add(harness.ContainerdContainer{ID: id1, Namespace: "k8s.io", Labels: k8s("api-0", "", "sandbox")})
	m.Containerd.Add(harness.ContainerdContainer{ID: id2, Namespace: "k8s.io", Labels: k8s("api-0", "app", "container"), MemoryLimit: 300 << 20})
	m.Containerd.Add(harness.ContainerdContainer{ID: id3, Namespace: "k8s.io", Labels: k8s("api-0", "sidecar", "container"), MemoryLimit: 100 << 20})
	m.Containerd.Add(harness.ContainerdContainer{ID: id4, Namespace: "k8s.io", Labels: k8s("db-0", "pg", "container")})
	pod := "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod1234.slice/cri-containerd-"
	m.AddProcess(930, 1, "pause", "/pause", pod+id1+".scope", 100)
	m.AddProcess(931, 1, "app", "/app", pod+id2+".scope", 10000)
	m.AddProcess(932, 1, "side", "/side", pod+id3+".scope", 2000)
	m.AddProcess(940, 1, "postgres", "postgres", pod+id4+".scope", 9000)

	a := ctrApps(t, m)
	api := a["api-0"]
	if api.Kind != "pod" || len(api.PIDs) != 3 || len(api.Container.Containers) != 3 {
		t.Fatalf("api-0: %+v", api)
	}
	if api.Container.StartedBy != "kubernetes" || api.Container.MemoryLimit == nil || *api.Container.MemoryLimit != 400<<20 {
		t.Fatalf("api-0 limit should be the containers' sum, got %+v", api.Container)
	}
	subs := map[int]string{}
	for _, p := range api.Processes {
		subs[p.PID] = p.Subgroup
	}
	if subs[931] != "app" || subs[932] != "sidecar" {
		t.Fatalf("drilldown containers: %v", subs)
	}
	if db := a["db-0"]; db.Kind != "pod" || db.Container.MemoryLimit != nil {
		t.Fatalf("db-0 has an unlimited container so no pod limit: %+v", db.Container)
	}
}

func TestContainerNestedInKindNodeResolvesToTheNode(t *testing.T) {
	m := harness.New(t, apps)
	m.Docker.Add(harness.DockerContainer{ID: id5, Name: "kind-control-plane"})
	m.AddProcess(950, 1, "kubelet", "kubelet", "/docker/"+id5+"/kubelet.slice/kubelet.service", 40000)
	m.AddProcess(951, 1, "etcd", "etcd", "/docker/"+id5+"/kubepods/burstable/pod9/"+strings.Repeat("f6", 32), 40000)
	a := ctrApps(t, m)
	if n := a["kind-control-plane"]; len(n.PIDs) != 2 {
		t.Fatalf("kind node: %+v", n)
	}
}

func TestNoRuntimeInstalledChangesNothing(t *testing.T) {
	m := harness.New(t, apps)
	m.AddProcess(960, 1, "orphan", "/opt/x/orphan", "/docker/"+id1, 100) // cgroup looks like a container, no runtime to name it
	a := ctrApps(t, m)
	for _, x := range a {
		if x.Container != nil || x.Kind == "container" || x.Kind == "pod" {
			t.Fatalf("unexpected container Application %+v", x)
		}
	}
	if _, ok := a["orphan"]; !ok {
		t.Fatalf("process should fall through to top ancestor, got %v", a)
	}
}

func TestReportListsContainers(t *testing.T) {
	m := harness.New(t, apps)
	m.Docker.Add(harness.DockerContainer{ID: id1, Name: "shop-db-1"})
	m.AddProcess(900, 1, "postgres", "postgres", "/docker/"+id1, 100000)
	if out := m.Report(); !strings.Contains(out, "shop-db-1") || !strings.Contains(out, "container") {
		t.Fatalf("report should list the Container:\n%s", out)
	}
}

func TestAHungDockerSocketDoesNotDelaySamples(t *testing.T) {
	m := harness.New(t, apps)
	m.Docker.Add(harness.DockerContainer{ID: id1, Name: "shop-db-1"})
	m.Docker.Hung.Store(true)
	m.AddProcess(900, 1, "postgres", "postgres", "/docker/"+id1, 100000)
	p := m.Start()
	start := time.Now()
	for i := 0; i < 5; i++ {
		p.Tick()
		var l struct {
			Applications []struct{} `json:"applications"`
		}
		p.GetJSON("/api/applications", &l)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("5 samples with a hung Docker socket took %v", d)
	}
}
