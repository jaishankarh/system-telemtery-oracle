package cli_test

import (
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

const devBuild = "dev-box-dev-build"

func TestDevServerInEditorTerminalLeavesTheEditorRow(t *testing.T) {
	m := newDevBuildMachine(t)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)

	shop := l.find(t, "Dev & Build: shop") // Project = VCS root above the working directory (web/ and api/ are inside shop)
	if shop.Kind != "devbuild" || !equalInts(sortedPIDs(shop), []int{1003, 1004, 1005, 1007}) {
		t.Errorf("shop = kind %s pids %v", shop.Kind, shop.PIDs)
	}
	// the editor keeps only its own processes, including its language server
	editor := l.find(t, "cursor")
	if !equalInts(sortedPIDs(editor), []int{1000, 1001, 1002, 1006}) {
		t.Errorf("editor row = %v, want the dev server removed", editor.PIDs)
	}
	// a Gradle daemon is not claimed: the App Profiles own it
	for _, a := range l.Applications {
		if a.Kind == "devbuild" {
			for _, pid := range a.PIDs {
				if pid == 1020 {
					t.Errorf("Gradle daemon claimed by %s", a.Name)
				}
			}
		}
	}
}

func TestTwoProjectsBuildsAreTwoRows(t *testing.T) {
	m := newDevBuildMachine(t)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)

	tool := l.find(t, "Dev & Build: tool")
	if !equalInts(sortedPIDs(tool), []int{1011, 1012}) {
		t.Errorf("tool = %v", tool.PIDs)
	}
	shop := l.find(t, "Dev & Build: shop")
	if shop.ID == tool.ID {
		t.Error("two projects share one row")
	}
	// no VCS root: the working directory itself is the Project
	scratch := l.find(t, "Dev & Build: scratch")
	if !equalInts(sortedPIDs(scratch), []int{1013}) {
		t.Errorf("scratch = %v", scratch.PIDs)
	}
}

func TestDevBuildInReport(t *testing.T) {
	m := newDevBuildMachine(t)
	if out := m.Report(); !strings.Contains(out, "Dev & Build: shop") {
		t.Errorf("report lacks Dev & Build row:\n%s", out)
	}
}

func TestConfigRuleCreatesAnApplication(t *testing.T) {
	m := newDevBuildMachine(t)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", `
# user rules run first
[[group]]
name = "Mystery stack"
executable = "mystery-tool"

[[group]]
name = "Shop frontend"
command = 'vite|nodemon'   # a # inside quotes stays: '#x'
cwd = "/home/dev/shop"

[[group]]
name = "Scoped"
cgroup = 'app-flatpak-org\.mozilla'
`)
	p := m.Start()
	var l appList
	p.GetJSON("/api/applications", &l)

	if a := l.find(t, "Mystery stack"); !equalInts(sortedPIDs(a), []int{700}) {
		t.Errorf("executable rule = %v", a.PIDs)
	}
	// user rules run before Dev & Build, and require every given field to match
	if a := l.find(t, "Shop frontend"); !equalInts(sortedPIDs(a), []int{1004, 1005, 1007}) {
		t.Errorf("command + cwd rule = %v", a.PIDs)
	}
	if a := l.find(t, "Scoped"); !equalInts(sortedPIDs(a), []int{200, 201}) {
		t.Errorf("cgroup rule = %v", a.PIDs)
	}
	// the rest of the dev server is still Dev & Build
	if a := l.find(t, "Dev & Build: shop"); !equalInts(sortedPIDs(a), []int{1003}) {
		t.Errorf("shop after rule = %v", a.PIDs)
	}
}

func TestConfigRuleRenamesAnApplication(t *testing.T) {
	m := newDevBuildMachine(t)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", `
[[rename]]
from = "org.mozilla.firefox"
to = "Firefox"

[[rename]]
from = "Dev & Build: shop"
to = "Shop dev servers"
`)
	var l appList
	m.Start().GetJSON("/api/applications", &l)
	if a := l.find(t, "Firefox"); a.Kind != "flatpak" || !equalInts(sortedPIDs(a), []int{200, 201}) {
		t.Errorf("renamed = %+v", a)
	}
	l.find(t, "Shop dev servers")
	if out := m.Report(); !strings.Contains(out, "Shop dev servers") {
		t.Errorf("report ignores rename:\n%s", out)
	}
}

func TestBadGroupRuleFailsClearly(t *testing.T) {
	m := newDevBuildMachine(t)
	for name, cfg := range map[string]string{
		"bad regex":      "[[group]]\nname = \"x\"\ncommand = \"(\"\n",
		"no name":        "[[group]]\nexecutable = \"x\"\n",
		"no match field": "[[group]]\nname = \"x\"\n",
		"unknown key":    "[[group]]\nname = \"x\"\nexecutable = \"x\"\nbogus = \"1\"\n",
		"unknown table":  "[nope]\nport = 1\n",
		"rename partial": "[[rename]]\nfrom = \"x\"\n",
	} {
		m.WriteFile("/bad.toml", cfg)
		if code, _, stderr := m.Run("report", "--config", "/bad.toml"); code == 0 || stderr == "" {
			t.Errorf("%s: want failure with message, got code %d", name, code)
		}
	}
}

func TestWhichCommandsAreDevAndBuildJobs(t *testing.T) {
	cases := []struct {
		cmd     string
		claimed bool
	}{
		{"make -j4", true},
		{"go build ./...", true},
		{"go env GOPATH", false},
		{"gopls serve", false},
		{"pnpm dev", true},
		{"yarn", false},
		{"npm install", false},
		{"npx vite --host", true},
		{"node /x/node_modules/webpack-cli/bin/cli.js serve", true},
		{"node /usr/share/cursor/resources/tsserver.js", false},
		{"/usr/lib/jvm/java/bin/java org.gradle.launcher.daemon.bootstrap.GradleDaemon 8.5", false},
		{"/usr/lib/jvm/java/bin/java -classpath gradle-wrapper.jar org.gradle.wrapper.GradleWrapperMain build", true},
		{"/usr/lib/gcc/x86_64-linux-gnu/13/cc1 -quiet main.c", true},
	}
	for _, c := range cases {
		m := newDevBuildMachine(t)
		m.WriteFile("/proc/1013/cmdline", strings.ReplaceAll(c.cmd, " ", "\x00")+"\x00")
		var l appList
		m.Start().GetJSON("/api/applications", &l)
		got := false
		for _, a := range l.Applications {
			if a.Name == "Dev & Build: scratch" {
				got = true
			}
		}
		if got != c.claimed {
			t.Errorf("%q: claimed = %v, want %v", c.cmd, got, c.claimed)
		}
	}
}

// newDevBuildMachine loads the dev-box-dev-build fixture and creates the project
// directories at test time. Git refuses to track anything under a path component
// named ".git" and drops empty directories, so the VCS markers cannot live in the
// fixture tree: shop and tool are VCS roots, scratch is not.
func newDevBuildMachine(t *testing.T) *harness.Machine {
	t.Helper()
	m := harness.New(t, devBuild)
	for _, root := range []string{"/home/dev/shop", "/home/dev/tool"} {
		m.WriteFile(root+"/.git/HEAD", "ref: refs/heads/main\n")
	}
	for _, dir := range []string{"/home/dev/shop/web", "/home/dev/shop/api", "/home/dev/tool/crates/core", "/home/dev/scratch"} {
		m.WriteFile(dir+"/.keep", "")
	}
	return m
}
