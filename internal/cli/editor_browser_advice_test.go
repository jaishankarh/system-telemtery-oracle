package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

const quickWindow = "advisor_window = \"30s\"\n"

func profilesBox(t *testing.T) (*harness.Machine, *harness.Program) {
	t.Helper()
	m := harness.New(t, profiles)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", quickWindow)
	m.WriteFile("/home/dev/proj/.git/HEAD", "ref: refs/heads/main\n") // the Project root
	m.WriteFile("/home/dev/proj/node_modules/.keep", "")
	m.WriteFile("/home/dev/proj/.next/.keep", "")
	return m, m.Start("--config", "/etc/sys-telemetry-app/config.toml")
}

// ticks runs n intervals, giving each pid in busy a full core (200 ticks per 2 s) first.
func ticks(m *harness.Machine, p *harness.Program, n int, busy ...int) {
	for i := 0; i < n; i++ {
		for _, pid := range busy {
			m.AddProcessCPU(pid, 200)
		}
		p.Tick()
	}
}

func adviceFor(t *testing.T, p *harness.Program, rule string) adviceDoc {
	t.Helper()
	d := getAdvice(p)
	var out adviceDoc
	for _, a := range d.Advice {
		if a.Rule == rule {
			out.Advice = append(out.Advice, a)
		}
	}
	return out
}

func TestEditorWatcherLoadNamesTheProjectAndGivesExcludeSettings(t *testing.T) {
	m, p := profilesBox(t)
	ticks(m, p, 17, 108, 107) // Cursor's file watcher and ripgrep, over 30 s
	d := adviceFor(t, p, "editor-file-watcher")
	if len(d.Advice) != 1 {
		t.Fatalf("advice = %+v", getAdvice(p))
	}
	a := d.Advice[0]
	if a.Category != "settings_fix" || a.Severity != "warning" || len(a.Applications) != 1 || a.Applications[0].Name != "Cursor" {
		t.Fatalf("advice = %+v", a)
	}
	if !strings.Contains(a.Summary, "proj") || !strings.Contains(a.Summary, "file watchers") {
		t.Errorf("summary = %s", a.Summary)
	}
	if a.Change == nil || a.Change.Kind != "editor-settings" || a.Change.Target != "/home/dev/proj/.vscode/settings.json" {
		t.Fatalf("change = %+v", a.Change)
	}
	var settings map[string]map[string]bool
	if err := json.Unmarshal([]byte(a.Change.Content), &settings); err != nil {
		t.Fatalf("change content is not JSON: %v\n%s", err, a.Change.Content)
	}
	for _, key := range []string{"files.watcherExclude", "search.exclude"} {
		for _, glob := range []string{"**/node_modules/**", "**/dist/**", "**/build/**", "**/.cache/**", "**/.next/**"} {
			if !settings[key][glob] {
				t.Errorf("%s lacks %s: %s", key, glob, a.Change.Content)
			}
		}
		if settings[key]["**/target/**"] {
			t.Errorf("%s excludes target/, which this Project does not have", key)
		}
	}
	if m.ReadFileOrEmpty("/home/dev/proj/.vscode/settings.json") != "" {
		t.Error("the settings file was written; advice must change nothing")
	}
}

func TestEditorWatcherLoadWithoutProjectPointsAtUserSettingsOfThatEditor(t *testing.T) {
	m, p := profilesBox(t)
	// a language server of Visual Studio Code with no working directory that is a Project
	m.AddProcess(910, 200, "rust-analyzer", "/home/dev/.vscode/extensions/x/server/rust-analyzer", "/user.slice/x.scope", 3*1024*1024)
	ticks(m, p, 17, 910)
	d := adviceFor(t, p, "editor-file-watcher")
	if len(d.Advice) != 1 || d.Advice[0].Applications[0].Name != "Visual Studio Code" {
		t.Fatalf("advice = %+v", getAdvice(p))
	}
	if c := d.Advice[0].Change; c == nil || c.Target != "~/.config/Code/User/settings.json" {
		t.Fatalf("change = %+v", c)
	}
}

func TestEditorSettingsPathsForCursorAndWindsurf(t *testing.T) {
	for _, tc := range []struct{ name, comm, cmd, ppid, target string }{
		{"Cursor", "rg", "/usr/share/cursor/resources/app/node_modules/@vscode/ripgrep/bin/rg --files /", "100", "~/.config/Cursor/User/settings.json"},
		{"Windsurf", "ls", "/home/dev/.windsurf/extensions/x/server/gopls", "250", "~/.config/Windsurf/User/settings.json"},
	} {
		m, p := profilesBox(t)
		m.AddProcess(920, atoi(tc.ppid), tc.comm, tc.cmd, "/user.slice/x.scope", 3*1024*1024)
		ticks(m, p, 17, 920)
		d := adviceFor(t, p, "editor-file-watcher")
		if len(d.Advice) != 1 || d.Advice[0].Applications[0].Name != tc.name || d.Advice[0].Change.Target != tc.target {
			t.Errorf("%s: advice = %+v", tc.name, getAdvice(p).Advice)
		}
	}
}

func atoi(s string) int { var n int; fmt.Sscan(s, &n); return n }

func TestQuietEditorOrBriefSpikeGivesNoEditorAdvice(t *testing.T) {
	m, p := profilesBox(t)
	ticks(m, p, 40) // nothing busy
	ticks(m, p, 10, 108)
	ticks(m, p, 10) // spike ended before 30 s
	if d := adviceFor(t, p, "editor-file-watcher"); len(d.Advice) != 0 {
		t.Fatalf("advice = %+v", d.Advice)
	}
}

func TestEditorMemoryAloneIsEnough(t *testing.T) {
	m, p := profilesBox(t)
	m.SetProcessMemory(105, 3*1024*1024, 0) // rust-analyzer at 3 GiB; memory is re-read every 10 s
	ticks(m, p, 25)
	if d := adviceFor(t, p, "editor-file-watcher"); len(d.Advice) != 1 || d.Advice[0].Severity != "info" {
		t.Fatalf("advice = %+v", getAdvice(p).Advice)
	}
}

func addRenderers(m *harness.Machine, parent, first, n int, cmd string, pssKiB int) {
	for i := 0; i < n; i++ {
		m.AddProcess(first+i, parent, "renderer", fmt.Sprintf(cmd, i), "/user.slice/x.scope", pssKiB)
	}
}

func TestBrowserSprawlGivesSettingsForTheSpecificBrowser(t *testing.T) {
	for _, tc := range []struct {
		name, parentCmd, cmd string
		parent               int
		must                 []string
		mustNot              []string
	}{
		{"Google Chrome", "", "/opt/google/chrome/chrome --type=renderer --renderer-client-id=%d", 500,
			[]string{"chrome://settings/performance", "Memory Saver", "--renderer-process-limit=", "NOT a preference", "Preference"}, []string{"dom.ipc"}},
		{"Brave", "", "/opt/brave.com/brave/brave --type=renderer --renderer-client-id=%d", 560,
			[]string{"brave://settings/system", "Memory Saver", "--renderer-process-limit="}, []string{"dom.ipc", "chrome://"}},
		{"Microsoft Edge", "", "/opt/microsoft/msedge/msedge --type=renderer --renderer-client-id=%d", 570,
			[]string{"edge://settings/system", "sleeping tabs", "--renderer-process-limit="}, []string{"dom.ipc", "Memory Saver"}},
		{"Chromium", "", "/usr/lib/chromium/chromium --type=renderer --renderer-client-id=%d", 550,
			[]string{"#high-efficiency-mode-available", "--renderer-process-limit="}, []string{"dom.ipc"}},
		{"Firefox", "", "/usr/lib/firefox/firefox -contentproc -childID %d -isForBrowser tab", 600,
			[]string{"dom.ipc.processCount", "about:config", "Content process limit"}, []string{"--renderer-process-limit", "Memory Saver"}},
	} {
		m, p := profilesBox(t)
		addRenderers(m, tc.parent, 1000, 10, tc.cmd, 400*1024) // 10 renderers, ~4 GiB
		ticks(m, p, 17)
		d := adviceFor(t, p, "browser-process-sprawl")
		found := false
		for _, a := range d.Advice {
			if a.Applications[0].Name != tc.name {
				continue
			}
			found = true
			if a.Category != "settings_fix" || a.Change == nil || a.Change.Kind != "browser-settings" {
				t.Errorf("%s: advice = %+v", tc.name, a)
				continue
			}
			text := a.Change.Content + "\n" + a.Change.Note
			for _, s := range tc.must {
				if !strings.Contains(text, s) {
					t.Errorf("%s: change lacks %q:\n%s", tc.name, s, text)
				}
			}
			for _, s := range tc.mustNot {
				if strings.Contains(text, s) {
					t.Errorf("%s: change must not contain %q:\n%s", tc.name, s, text)
				}
			}
			if !strings.Contains(a.Summary, "renderer processes") {
				t.Errorf("%s: summary = %s", tc.name, a.Summary)
			}
		}
		if !found {
			t.Errorf("%s: no browser advice in %+v", tc.name, getAdvice(p).Advice)
		}
	}
}

func TestFewOrSmallRenderersAreNotSprawl(t *testing.T) {
	m, p := profilesBox(t)
	addRenderers(m, 500, 1000, 10, "/opt/google/chrome/chrome --type=renderer --renderer-client-id=%d", 10*1024)      // many but small
	addRenderers(m, 600, 1100, 3, "/usr/lib/firefox/firefox -contentproc -childID %d -isForBrowser tab", 2*1024*1024) // big but few
	ticks(m, p, 17)
	if d := adviceFor(t, p, "browser-process-sprawl"); len(d.Advice) != 0 {
		t.Fatalf("advice = %+v", d.Advice)
	}
}

func TestReportShowsTheEditorFixAndItsNote(t *testing.T) {
	m := harness.New(t, profiles)
	m.WriteFile("/etc/sys-telemetry-app/config.toml", quickWindow)
	m.WriteFile("/home/dev/proj/.git/HEAD", "ref: refs/heads/main\n")
	port := freePort(t)
	p := m.Start("--config", "/etc/sys-telemetry-app/config.toml", "--port", port)
	ticks(m, p, 17, 108)
	out := m.Report("--port", port, "--config", "/etc/sys-telemetry-app/config.toml")
	for _, want := range []string{"Settings Fix", "Cursor is spending", "files.watcherExclude", "/home/dev/proj/.vscode/settings.json", "Note: "} {
		if !strings.Contains(out, want) && !strings.Contains(out, strings.ToLower(want)) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
