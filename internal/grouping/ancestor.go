package grouping

import (
	"path"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// TopAncestor names a Process after the topmost parent below the user session
// or init, using that parent's executable name.
type TopAncestor struct{}

func (TopAncestor) Name() string { return "top ancestor" }

// sessionManagers are parents the walk stops below: they hold a session, not a program.
var sessionManagers = map[string]bool{
	"systemd": true, "init": true, "login": true, "sshd": true,
	"gdm-session-wor": true, "lightdm": true, "sddm-helper": true,
}

func boundary(p *collector.Process) bool {
	return p.PID <= 2 || sessionManagers[p.Name]
}

func (TopAncestor) Match(p *collector.Process, c *Context) (Match, bool) {
	top := p
	for _, a := range c.Ancestors(p) {
		if boundary(a) {
			break
		}
		top = a
	}
	name := executableName(top)
	if name == "" {
		return Match{}, false
	}
	return Match{Kind: KindAncestor, Key: name, Name: name}, true
}

// executableName is the basename of argv[0], or the process name when there
// is no command line.
func executableName(p *collector.Process) string {
	if strings.HasPrefix(p.Command, "[") { // no command line
		return p.Name
	}
	first, _, _ := strings.Cut(p.Command, " ")
	first = strings.TrimLeft(strings.TrimSuffix(first, ":"), "-")
	if n := path.Base(first); n != "." && n != "/" {
		return n
	}
	return p.Name
}
