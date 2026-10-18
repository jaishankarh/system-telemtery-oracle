package cli_test

import (
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

const ctrCfg = "advisor_window = \"30s\"\n"

// uncappedBox runs a machine with one Docker Container whose processes use
// peakGiB of memory at first (two History samples), then dropToGiB.
func uncappedBox(t *testing.T, c harness.DockerContainer, peakGiB, dropToGiB uint64) (*harness.Machine, *harness.Program) {
	t.Helper()
	m := harness.New(t, apps)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", ctrCfg)
	m.Docker.Add(c)
	m.AddProcess(900, 1, "java", "java -jar app.jar", "/system.slice/docker-"+c.ID+".scope", 1000)
	m.SetProcessMemory(900, peakGiB<<20, 0)
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	historyTicks(p, 6) // samples at 0 s and 10 s hold the peak
	m.SetProcessMemory(900, dropToGiB<<20, 0)
	historyTicks(p, 20)
	return m, p
}

func containerLimitAdvice(p *harness.Program) adviceDoc { return adviceForRule(p, "container-limit") }

func adviceForRule(p *harness.Program, rule string) adviceDoc {
	var out adviceDoc
	for _, a := range getAdvice(p).Advice {
		if a.Rule == rule {
			out.Advice = append(out.Advice, a)
		}
	}
	return out
}

func TestUncappedDockerRunContainerGetsPeakPlus25PercentFlag(t *testing.T) {
	_, p := uncappedBox(t, harness.DockerContainer{ID: id1, Name: "redis-dev"}, 4, 2)
	d := containerLimitAdvice(p)
	if len(d.Advice) != 1 {
		t.Fatalf("advice = %+v", getAdvice(p))
	}
	a := d.Advice[0]
	if a.ID != "container-limit:container:"+id1 || a.Category != "settings_fix" || a.Applications[0].Name != "redis-dev" {
		t.Fatalf("advice = %+v", a)
	}
	// 4 GiB peak (in History, since memory fell to 2 GiB) + 25% = 5 GiB.
	if a.Change == nil || a.Change.Kind != "docker-run" || !strings.Contains(a.Change.Content, "--memory=5120m") {
		t.Fatalf("change = %+v", a.Change)
	}
	var basis string
	for _, f := range a.Evidence.Facts {
		if f.Name == "peak_basis" {
			basis = f.Text
		}
	}
	if !strings.Contains(basis, "top 10") || !strings.Contains(basis, "2026-10-19") {
		t.Errorf("peak_basis should name the History window and the top-10 limit: %q", basis)
	}
	if !strings.Contains(a.Summary, "nothing was changed") {
		t.Errorf("summary = %s", a.Summary)
	}
}

func TestContainerWithLimitOrLowUsageGetsNoAdvice(t *testing.T) {
	_, p := uncappedBox(t, harness.DockerContainer{ID: id1, Name: "capped", MemoryLimit: 8 << 30}, 4, 2)
	if d := containerLimitAdvice(p); len(d.Advice) != 0 {
		t.Fatalf("limited container: %+v", d)
	}
	m := harness.New(t, apps)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", ctrCfg)
	m.Docker.Add(harness.DockerContainer{ID: id2, Name: "tiny"})
	m.AddProcess(901, 1, "sleep", "sleep 1000", "/docker/"+id2, 1000)
	q := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	historyTicks(q, 26)
	if d := containerLimitAdvice(q); len(d.Advice) != 0 {
		t.Fatalf("low usage: %+v", d)
	}
}

func TestComposeSnippetNamesTheServiceAndComposeV2(t *testing.T) {
	_, p := uncappedBox(t, harness.DockerContainer{ID: id1, Name: "shop-db-1",
		Labels: map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "db"}}, 4, 2)
	a := containerLimitAdvice(p).Advice[0]
	if a.Change.Kind != "compose" {
		t.Fatalf("change = %+v", a.Change)
	}
	for _, want := range []string{"services:", "  db:", "deploy:", "resources:", "limits:", "memory: 5120M"} {
		if !strings.Contains(a.Change.Content, want) {
			t.Errorf("snippet lacks %q:\n%s", want, a.Change.Content)
		}
	}
	if !strings.Contains(a.Change.Note, "Compose v2") || !strings.Contains(a.Change.Note, "mem_limit") {
		t.Errorf("note = %s", a.Change.Note)
	}
}

func TestKubernetesPodGetsResourcesLimitsMemory(t *testing.T) {
	m := harness.New(t, apps)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", ctrCfg)
	m.Containerd.Path = "/run/k3s/containerd/containerd.sock"
	k8s := func(ctr, kind string) map[string]string {
		l := map[string]string{"io.kubernetes.pod.name": "api-0", "io.kubernetes.pod.namespace": "prod", "io.cri-containerd.kind": kind}
		if ctr != "" {
			l["io.kubernetes.container.name"] = ctr
		}
		return l
	}
	m.Containerd.Add(harness.ContainerdContainer{ID: id1, Namespace: "k8s.io", Labels: k8s("", "sandbox")})
	m.Containerd.Add(harness.ContainerdContainer{ID: id2, Namespace: "k8s.io", Labels: k8s("app", "container")})
	cg := "/kubepods.slice/kubepods-pod1.slice/cri-containerd-"
	m.AddProcess(930, 1, "pause", "/pause", cg+id1+".scope", 100)
	m.AddProcess(931, 1, "app", "/app", cg+id2+".scope", 1000)
	m.SetProcessMemory(930, 0, 0)
	m.SetProcessMemory(931, 4<<20, 0)
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	historyTicks(p, 6)
	m.SetProcessMemory(931, 2<<20, 0)
	historyTicks(p, 20)
	a := containerLimitAdvice(p).Advice[0]
	if a.Change.Kind != "k8s" || !strings.Contains(a.Change.Content, "resources:") ||
		!strings.Contains(a.Change.Content, "memory: 5120Mi") || !strings.Contains(a.Change.Content, "name: app") {
		t.Fatalf("change = %+v", a.Change)
	}
	if !strings.Contains(a.Change.Target, "prod/api-0") {
		t.Errorf("target = %s", a.Change.Target)
	}
}

func TestNerdctlContainerGetsMemoryFlag(t *testing.T) {
	m := harness.New(t, apps)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", ctrCfg)
	m.Containerd.Add(harness.ContainerdContainer{ID: id3, Namespace: "default", Labels: map[string]string{"nerdctl/name": "web"}})
	m.AddProcess(920, 1, "nginx", "nginx", "/default/"+id3, 1000)
	m.SetProcessMemory(920, 4<<20, 0)
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	historyTicks(p, 26)
	a := containerLimitAdvice(p).Advice[0]
	if a.Change.Kind != "nerdctl" || !strings.Contains(a.Change.Content, "--memory 5120m") || !strings.Contains(a.Change.Content, "nerdctl run") {
		t.Fatalf("change = %+v", a.Change)
	}
}

func TestPeakIncludesHistoryFromBeforeARestart(t *testing.T) {
	m, p := uncappedBox(t, harness.DockerContainer{ID: id1, Name: "redis-dev"}, 4, 2)
	p.Stop()
	m.SetProcessMemory(900, 1<<20, 1<<20) // 2 GiB now
	q := m.Start("--config", "/etc/sys-telemetry-app/config.toml")
	historyTicks(q, 20)
	a := containerLimitAdvice(q).Advice[0]
	if !strings.Contains(a.Change.Content, "--memory=5120m") {
		t.Fatalf("peak from before the restart was lost: %+v", a.Change)
	}
}

func TestUncappedContainerRuleDisabledWithoutRuntimeOrHistory(t *testing.T) {
	m := harness.New(t, apps)
	m.WithoutContainerRuntime()
	p := m.Start()
	p.Tick()
	found := false
	for _, d := range getAdvice(p).Disabled {
		if d.Rule == "container-limit" {
			found = true
			if !strings.Contains(d.Reason, "Container runtime") {
				t.Errorf("reason = %q", d.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("container-limit not listed as disabled: %+v", getAdvice(p).Disabled)
	}
}
