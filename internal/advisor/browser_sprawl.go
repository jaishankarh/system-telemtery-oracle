package advisor

import (
	"fmt"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
)

// Thresholds of the browser process sprawl rule.
const (
	sprawlMinRenderers  = 8
	sprawlRendererBytes = 3 << 30 // total PSS of the renderers
)

// BrowserSprawlRule is the Settings Fix for a browser whose many renderer
// processes add up to a large PSS: that browser's tab-unloading or
// process-limit settings. Each setting is labelled as a preference (set in the
// browser, persists) or a flag (command line, applies only to browsers started
// with it).
type BrowserSprawlRule struct{}

func (BrowserSprawlRule) ID() string    { return "browser-process-sprawl" }
func (BrowserSprawlRule) Title() string { return "Browser process sprawl" }

func (BrowserSprawlRule) Needs(in *Input) string {
	if len(in.Sample.Processes) == 0 {
		return "no per-process data: /proc/<pid> could not be read"
	}
	if _, ok := in.WorkingSet(); !ok {
		return "per-process PSS is unavailable (smaps_rollup), so renderer memory cannot be added up"
	}
	return ""
}

var browserNames = map[string]bool{"chrome": true, "chromium": true, "brave": true, "edge": true, "firefox": true}

func (BrowserSprawlRule) Check(in *Input) []Finding {
	var out []Finding
	for _, a := range in.Apps {
		key := strings.TrimPrefix(a.ID, grouping.KindProfile+":")
		if a.Kind != grouping.KindProfile || !browserNames[key] {
			continue
		}
		var n int
		var pss uint64
		for _, p := range a.Processes {
			if p.Role == grouping.RoleRenderer {
				n++
				if p.PSS != nil {
					pss += *p.PSS
				}
			}
		}
		if n < sprawlMinRenderers || pss < sprawlRendererBytes {
			continue
		}
		sev := Warning
		if m := in.Sample.Memory; m != nil && m.TotalBytes > 0 && float64(pss) > float64(m.TotalBytes)/2 {
			sev = Critical
		}
		facts := []Fact{
			fnum("renderer_count", float64(n), "processes"),
			fnum("renderer_pss_bytes", float64(pss), "bytes"),
		}
		out = append(out, Finding{
			Subject:  a.ID,
			Severity: sev,
			Category: SettingsFix,
			Title:    a.Name + " has too many renderer processes",
			Summary: fmt.Sprintf("%s runs %d renderer processes that add up to %s (PSS). %s",
				a.Name, n, byteText(pss), browserAdvice(key)),
			Applications: []AppRef{{ID: a.ID, Name: a.Name}},
			Facts:        facts,
			Change:       browserChange(key, n),
		})
	}
	return out
}

func browserAdvice(key string) string {
	if key == "firefox" {
		return "Lower Firefox's content process limit, and unload idle tabs. Advice only: nothing was changed."
	}
	return "Turn on tab unloading (Memory Saver or sleeping tabs) and optionally cap renderer processes. Advice only: nothing was changed."
}

func browserChange(key string, renderers int) *Change {
	limit := renderers / 2
	if limit < 4 {
		limit = 4
	}
	switch key {
	case "firefox":
		return &Change{
			Kind:   "browser-settings",
			Target: "about:config  (or user.js in your Firefox profile folder, about:profiles)",
			Content: fmt.Sprintf("// Preference (about:config, integer; default is 8). Settings > General > Performance shows it as "+
				"\"Content process limit\" once \"Use recommended performance settings\" is unticked.\n"+
				"user_pref(\"dom.ipc.processCount\", %d);\n", limit),
			Note: "A preference: it persists and applies after a Firefox restart. Fewer content processes means tabs share a process, so one heavy page can slow others. " +
				"To free memory from idle tabs without closing them, open about:unloads and unload the biggest ones.",
		}
	}
	names := map[string][3]string{
		// settings page, tab-unloading feature, where the launcher flag goes
		"chrome":   {"chrome://settings/performance", "Memory Saver", "google-chrome"},
		"chromium": {"chrome://flags/#high-efficiency-mode-available", "Memory Saver mode (a flag in Chromium; the feature may be absent from some builds)", "chromium"},
		"brave":    {"brave://settings/system", "Memory Saver", "brave-browser"},
		"edge":     {"edge://settings/system", "Save resources with sleeping tabs", "microsoft-edge"},
	}
	n := names[key]
	var b strings.Builder
	fmt.Fprintf(&b, "# 1. Preference (set in the browser, persists): open %s and turn on \"%s\".\n", n[0], n[1])
	if key == "edge" {
		b.WriteString("#    The same page sets how long a tab is idle before it sleeps.\n")
	} else if key != "chromium" {
		b.WriteString("#    Memory Saver discards tabs you have not used for a while; they reload when you return.\n")
	}
	fmt.Fprintf(&b, "# 2. Flag (command line, NOT a preference; only applies to a browser started with it). Optional:\n"+
		"#    add to the Exec= line of a copy of the launcher in ~/.local/share/applications/%s.desktop\n"+
		"--renderer-process-limit=%d\n", n[2], limit)
	return &Change{
		Kind:    "browser-settings",
		Target:  n[0],
		Content: b.String(),
		Note: "Step 1 is a preference. Step 2 is a Chromium command-line switch: Chromium-based browsers have no preference for a process limit, " +
			"and the limit is a soft cap (above it, tabs share renderers, which weakens isolation between sites). " +
			"The browser's own task manager (Shift+Esc) shows which tabs are heaviest.",
	}
}
