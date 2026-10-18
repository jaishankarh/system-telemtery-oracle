package advisor

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
)

// Thresholds of the editor file-watcher rule. A watcher, ripgrep or language
// server is normally quiet; these are totals over all of them in one editor.
const (
	watcherCPUPercent  = 50.0    // percent of one core
	watcherMemoryBytes = 2 << 30 // PSS
	watcherCPUWarning  = 100.0   // a full core: warning instead of info
	watcherMemWarning  = 4 << 30
)

// vsCodeFamily maps the App Profile key of an editor that reads VS Code
// settings.json to the folder under ~/.config where it keeps user settings.
var vsCodeFamily = map[string]string{
	"cursor":   "Cursor",
	"vscode":   "Code",
	"vscodium": "VSCodium",
	"windsurf": "Windsurf",
}

// watcherRoles are the Roles whose load this rule is about.
var watcherRoles = map[string]bool{
	grouping.RoleFileWatcher: true, grouping.RoleRipgrep: true, grouping.RoleLanguageServer: true,
}

// buildOutput are directories excluded for every Project; extraExcludes only
// when the Project has them.
var (
	baseExcludes  = []string{"node_modules", "dist", "build", ".cache"}
	extraExcludes = []string{".next", ".nuxt", ".turbo", "out", "target", ".gradle", ".venv", "coverage"}
)

// EditorWatcherRule is the Settings Fix for Sustained high CPU or memory in an
// editor's file watchers, ripgrep and language servers: exclude the folders
// that hold no source from watching and searching.
type EditorWatcherRule struct{}

func (EditorWatcherRule) ID() string    { return "editor-file-watcher" }
func (EditorWatcherRule) Title() string { return "Editor file-watcher load" }

func (EditorWatcherRule) Needs(in *Input) string {
	if len(in.Sample.Processes) == 0 {
		return "no per-process data: /proc/<pid> could not be read"
	}
	return ""
}

func (EditorWatcherRule) Check(in *Input) []Finding {
	var out []Finding
	for _, a := range in.Apps {
		if a.Kind != grouping.KindProfile {
			continue
		}
		key := strings.TrimPrefix(a.ID, grouping.KindProfile+":")
		cfgDir, ok := vsCodeFamily[key]
		if !ok {
			continue
		}
		var cpu float64
		var haveCPU bool
		var pss uint64
		roleCPU := map[string]float64{}
		var heavy []grouping.Member
		var all []grouping.Member
		for _, p := range a.Processes {
			if !watcherRoles[p.Role] {
				continue
			}
			all = append(all, p)
			if p.CPU != nil {
				cpu += *p.CPU
				roleCPU[p.Role] += *p.CPU
				haveCPU = true
			}
			if p.PSS != nil {
				pss += *p.PSS
			}
		}
		highCPU := haveCPU && cpu >= watcherCPUPercent
		highMem := pss >= watcherMemoryBytes
		if !highCPU && !highMem {
			continue
		}
		// The Project is the one of the heaviest Processes only, so a quiet
		// language server of another Project does not get blamed.
		var top float64
		for _, p := range all {
			if l := load(p); l > top {
				top = l
			}
		}
		for _, p := range all {
			if load(p) >= top/2 {
				heavy = append(heavy, p)
			}
		}
		projects := projectsOf(in, heavy)

		sev := Info
		if cpu >= watcherCPUWarning || pss >= watcherMemWarning {
			sev = Warning
		}
		facts := []Fact{
			fnum("watcher_search_language_server_cpu_percent", cpu, "percent"),
			fnum("watcher_search_language_server_pss_bytes", float64(pss), "bytes"),
		}
		for _, r := range []string{grouping.RoleFileWatcher, grouping.RoleRipgrep, grouping.RoleLanguageServer} {
			if v, ok := roleCPU[r]; ok {
				facts = append(facts, fnum(strings.ReplaceAll(r, "-", "_")+"_cpu_percent", v, "percent"))
			}
		}
		if len(projects) > 0 {
			var names []string
			for _, p := range projects {
				names = append(names, path.Base(p))
			}
			facts = append(facts, Fact{Name: "project", Text: strings.Join(names, ", ")})
		}
		out = append(out, Finding{
			Subject:      a.ID,
			Severity:     sev,
			Category:     SettingsFix,
			Title:        a.Name + " is spending CPU or memory watching and indexing files",
			Summary:      watcherSummary(a.Name, cpu, pss, roleCPU, projects),
			Applications: []AppRef{{ID: a.ID, Name: a.Name}},
			Facts:        facts,
			Change:       watcherChange(in, a.Name, cfgDir, projects),
		})
	}
	return out
}

func watcherSummary(name string, cpu float64, pss uint64, roleCPU map[string]float64, projects []string) string {
	s := fmt.Sprintf("%s's file watchers, search (ripgrep) and language servers use %.0f%% of a core and %s together. ",
		name, cpu, byteText(pss))
	if len(projects) > 0 {
		var names []string
		for _, p := range projects {
			names = append(names, path.Base(p))
		}
		s += "Project: " + strings.Join(names, ", ") + ". "
	}
	s += "Exclude folders that hold no source (node_modules, dist, build output, caches) from watching and searching. " +
		"This is advice only: nothing was changed."
	return s
}

func byteText(b uint64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
}

// projectsOf returns the Project roots of the given Processes, largest
// contributors first, omitting working directories that are not a Project
// (the filesystem root and home directories).
func projectsOf(in *Input, ms []grouping.Member) []string {
	seen := map[string]bool{}
	var out []string
	sorted := append([]grouping.Member(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return load(sorted[i]) > load(sorted[j]) })
	for _, m := range sorted {
		if m.Cwd == "" || isHomeOrRoot(m.Cwd) {
			continue
		}
		root := grouping.ProjectRoot(in.Machine, m.Cwd)
		if isHomeOrRoot(root) || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func load(m grouping.Member) float64 {
	var v float64
	if m.CPU != nil {
		v += *m.CPU
	}
	if m.PSS != nil {
		v += float64(*m.PSS) / (1 << 30)
	}
	return v
}

var homeRe = regexp.MustCompile(`^(/root|/home/[^/]+|/Users/[^/]+)?/?$`)

func isHomeOrRoot(p string) bool { return homeRe.MatchString(strings.TrimSuffix(p, " (deleted)")) }

// watcherChange is the settings.json fragment to merge. With a known Project
// it targets that Project's .vscode/settings.json (Cursor, VS Code, VSCodium
// and Windsurf all read it); otherwise the editor's user settings.json.
func watcherChange(in *Input, name, cfgDir string, projects []string) *Change {
	dirs := append([]string(nil), baseExcludes...)
	if len(projects) > 0 {
		for _, e := range extraExcludes {
			if in.Machine.Exists(path.Join(projects[0], e)) {
				dirs = append(dirs, e)
			}
		}
	}
	var b strings.Builder
	b.WriteString("{\n")
	for i, key := range []string{"files.watcherExclude", "search.exclude"} {
		fmt.Fprintf(&b, "  %q: {\n", key)
		for j, d := range dirs {
			comma := ","
			if j == len(dirs)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %q: true%s\n", "**/"+d+"/**", comma)
		}
		if i == 0 {
			b.WriteString("  },\n")
		} else {
			b.WriteString("  }\n")
		}
	}
	b.WriteString("}\n")
	target := "~/.config/" + cfgDir + "/User/settings.json"
	note := "Merge these two keys into the existing settings.json (do not replace the file); " +
		"restart the editor or reload the window afterwards. Values are glob patterns set to true. " +
		"files.watcherExclude is added to the editor's built-in default (which only covers .git internals and node_modules/*/); " +
		"search.exclude also keeps these folders out of ripgrep. " +
		"For a snap or flatpak install the user settings.json lives under the package's own config directory."
	if len(projects) > 0 {
		target = path.Join(projects[0], ".vscode/settings.json")
		note = "Workspace settings for Project " + path.Base(projects[0]) + ". " + note +
			" To apply this to every Project, put the same keys in ~/.config/" + cfgDir + "/User/settings.json instead."
		if len(projects) > 1 {
			var others []string
			for _, p := range projects[1:] {
				others = append(others, p+"/.vscode/settings.json")
			}
			note += " Other Projects with load: " + strings.Join(others, ", ") + "."
		}
	}
	return &Change{Kind: "editor-settings", Target: target, Content: b.String(), Note: note}
}
