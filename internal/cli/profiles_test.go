package cli_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

const profiles = "dev-box-profiles"

type profileProc struct {
	PID  int    `json:"pid"`
	Role string `json:"role"`
}

type profileApp struct {
	Name         string        `json:"name"`
	Kind         string        `json:"kind"`
	PIDs         []int         `json:"pids"`
	ProcessCount int           `json:"process_count"`
	Processes    []profileProc `json:"processes"`
}

type profileList struct {
	Applications []profileApp `json:"applications"`
}

func (l profileList) app(t *testing.T, name string) profileApp {
	t.Helper()
	for _, a := range l.Applications {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no Application %q", name)
	return profileApp{}
}

func (a profileApp) pids() []int {
	p := append([]int(nil), a.PIDs...)
	sort.Ints(p)
	return p
}

func (a profileApp) roles() map[int]string {
	r := map[int]string{}
	for _, p := range a.Processes {
		r[p.PID] = p.Role
	}
	return r
}

func (l profileList) appOf(pid int) string {
	for _, a := range l.Applications {
		for _, p := range a.PIDs {
			if p == pid {
				return a.Name
			}
		}
	}
	return ""
}

func loadProfiles(t *testing.T) profileList {
	t.Helper()
	var l profileList
	harness.New(t, profiles).Start().GetJSON("/api/applications", &l)
	return l
}

func checkApp(t *testing.T, l profileList, name string, pids []int, roles map[int]string) {
	t.Helper()
	a := l.app(t, name)
	if a.Kind != "profile" {
		t.Errorf("%s kind = %s, want profile", name, a.Kind)
	}
	if !equalInts(a.pids(), pids) || a.ProcessCount != len(pids) {
		t.Errorf("%s pids = %v (count %d), want %v", name, a.pids(), a.ProcessCount, pids)
	}
	got := a.roles()
	for pid, role := range roles {
		if got[pid] != role {
			t.Errorf("%s pid %d role = %q, want %q", name, pid, got[pid], role)
		}
	}
}

func TestEditorsAreOneApplicationWithLanguageServersWatchersAndRipgrep(t *testing.T) {
	l := loadProfiles(t)
	// The dev server (111, 112) was started from the editor's terminal, so it is not the editor's.
	checkApp(t, l, "Cursor", []int{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110}, map[int]string{
		100: "main", 101: "zygote", 102: "gpu", 103: "renderer", 104: "extension-host",
		105: "language-server", 106: "language-server", 107: "ripgrep", 108: "file-watcher",
		109: "pty-host", 110: "terminal-shell",
	})
	checkApp(t, l, "Visual Studio Code", []int{200, 201, 202, 203}, map[int]string{
		200: "main", 201: "extension-host", 202: "language-server", 203: "ripgrep",
	})
	checkApp(t, l, "Windsurf", []int{250, 251, 252}, map[int]string{252: "language-server", 251: "extension-host"})
}

func TestDevServersLaunchedFromAnEditorTerminalAreNotTheEditors(t *testing.T) {
	l := loadProfiles(t)
	for _, pid := range []int{111, 112, 323} {
		if n := l.appOf(pid); n == "Cursor" || n == "IntelliJ IDEA" || n == "" {
			t.Errorf("pid %d (dev server in an editor terminal) is in %q", pid, n)
		}
	}
}

func TestJetBrainsIDEsFoldInHelpersAndTheDaemonsTheyStart(t *testing.T) {
	l := loadProfiles(t)
	checkApp(t, l, "Android Studio", []int{300, 301, 302, 303}, map[int]string{
		300: "main", 301: "file-watcher", 302: "gradle-daemon", 303: "kotlin-daemon",
	})
	checkApp(t, l, "IntelliJ IDEA", []int{320, 321, 322}, map[int]string{
		320: "main", 321: "maven-server", 322: "terminal-shell",
	})
}

func TestAndroidEmulatorAndStandaloneGradleDaemonAreTheirOwnApplications(t *testing.T) {
	l := loadProfiles(t)
	// The emulator was launched by Android Studio but is its own row; so is a Gradle daemon nobody hosts.
	checkApp(t, l, "Android emulator", []int{310, 311}, map[int]string{310: "main", 311: "emulator-vm"})
	checkApp(t, l, "Gradle daemon", []int{400, 401}, map[int]string{400: "gradle-daemon", 401: "kotlin-daemon"})
}

func TestEachBrowserIsOneApplicationWithAllRenderers(t *testing.T) {
	l := loadProfiles(t)
	checkApp(t, l, "Google Chrome", []int{500, 501, 502, 503, 504, 505, 506, 507}, map[int]string{
		500: "main", 501: "zygote", 502: "gpu", 503: "renderer", 504: "renderer",
		505: "utility", 506: "crash-reporter", 507: "extension",
	})
	checkApp(t, l, "Chromium", []int{550, 551}, map[int]string{550: "main", 551: "renderer"})
	checkApp(t, l, "Brave", []int{560, 561}, map[int]string{561: "renderer"})
	checkApp(t, l, "Microsoft Edge", []int{570, 571}, map[int]string{571: "renderer"})
	// Firefox lives in a flatpak scope; the App Profile still takes precedence over the cgroup layer.
	checkApp(t, l, "Firefox", []int{600, 601, 602, 603, 604}, map[int]string{
		600: "main", 601: "renderer", 602: "renderer", 603: "utility", 604: "gpu",
	})
}

func TestAppProfilesLeaveEveryProcessInExactlyOneApplication(t *testing.T) {
	m := harness.New(t, profiles)
	p := m.Start()
	var l profileList
	p.GetJSON("/api/applications", &l)
	var procs procList
	p.GetJSON("/api/processes", &procs)
	seen := map[int]int{}
	for _, a := range l.Applications {
		for _, p := range a.PIDs {
			seen[p]++
		}
	}
	for pid := range seen {
		if seen[pid] != 1 {
			t.Errorf("pid %d in %d Applications", pid, seen[pid])
		}
	}
	if len(seen) != len(procs.Processes) {
		t.Errorf("Applications hold %d processes, system has %d", len(seen), len(procs.Processes))
	}
	// Programs without a profile keep their old grouping; no role is invented for them.
	if n := l.appOf(710); n != "mystery-tool" {
		t.Errorf("pid 710 in %q", n)
	}
	for _, p := range l.app(t, "mystery-tool").Processes {
		if p.Role != "" {
			t.Errorf("unprofiled process has role %q", p.Role)
		}
	}
}

func TestReportListsProfiledApplicationsAsOneRowEach(t *testing.T) {
	out := harness.New(t, profiles).Report()
	for _, name := range []string{"Cursor", "Google Chrome", "Android Studio", "Firefox"} {
		if n := strings.Count(out, "\n"+name+" "); n != 1 {
			t.Errorf("report has %d rows for %s:\n%s", n, name, out)
		}
	}
}
