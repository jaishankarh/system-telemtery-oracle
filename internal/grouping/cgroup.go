package grouping

import (
	"regexp"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// CgroupUnit names a Process after the systemd unit, snap or flatpak its
// cgroup path belongs to. Modern desktops create one scope per launched
// program, so this names programs the tool has never heard of. Where systemd
// is absent the cgroup path has no such components and the layer does not match.
type CgroupUnit struct{}

func (CgroupUnit) Name() string { return "systemd unit, snap, flatpak" }

var (
	snapScope    = regexp.MustCompile(`^snap\.([^.]+)\..+\.scope$`)
	trailingPID  = regexp.MustCompile(`-\d+$`)
	launcherHead = regexp.MustCompile(`^(gnome|kde|xfce|lxqt|cinnamon|mate|dbus)-`)
)

// plumbing units hold sessions and managers, not programs.
func plumbing(c string) bool {
	return c == "init.scope" || strings.HasPrefix(c, "session-") || strings.HasPrefix(c, "vte-spawn-") ||
		strings.HasPrefix(c, "user@") || strings.HasPrefix(c, "user-runtime-dir@")
}

func (CgroupUnit) Match(p *collector.Process, _ *Context) (Match, bool) {
	parts := strings.Split(p.Cgroup, "/")
	for i := len(parts) - 1; i >= 0; i-- { // deepest unit first
		c := parts[i]
		if !(strings.HasSuffix(c, ".scope") || strings.HasSuffix(c, ".service")) || plumbing(c) {
			continue
		}
		if m := snapScope.FindStringSubmatch(c); m != nil {
			return Match{Kind: KindSnap, Key: m[1], Name: m[1]}, true
		}
		base := strings.NewReplacer(`\x2d`, "-").Replace(strings.TrimSuffix(strings.TrimSuffix(c, ".scope"), ".service"))
		if rest, ok := strings.CutPrefix(base, "app-"); ok {
			rest, _, _ = strings.Cut(rest, "@")
			rest = trailingPID.ReplaceAllString(rest, "")
			if id, ok := strings.CutPrefix(rest, "flatpak-"); ok && id != "" {
				return Match{Kind: KindFlatpak, Key: id, Name: id}, true
			}
			rest = launcherHead.ReplaceAllString(rest, "")
			if rest != "" {
				return Match{Kind: KindSystemd, Key: rest, Name: rest}, true
			}
			continue
		}
		return Match{Kind: KindSystemd, Key: base, Name: base}, true
	}
	return Match{}, false
}
