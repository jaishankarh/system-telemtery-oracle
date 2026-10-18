package grouping

import (
	"regexp"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/config"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// KindUserRule is the Kind of an Application created by a rule in the config file.
const KindUserRule = "rule"

// UserRules applies the grouping rules from the config file. The zero value
// has no rules and matches nothing; it holds the slot in DefaultLayers that
// Configured fills. A rule's fields are all required to match; the first rule
// that matches a Process names its Application.
type UserRules struct {
	rules []compiledRule
}

type compiledRule struct {
	name, executable, cwd string
	command, cgroup       *regexp.Regexp
}

func (UserRules) Name() string { return "user rules" }

func newUserRules(rs []config.GroupRule) UserRules {
	var u UserRules
	for _, r := range rs {
		c := compiledRule{name: r.Name, executable: r.Executable, cwd: strings.TrimRight(r.Cwd, "/")}
		if r.Command != "" {
			c.command = regexp.MustCompile(r.Command) // validated when the config was parsed
		}
		if r.Cgroup != "" {
			c.cgroup = regexp.MustCompile(r.Cgroup)
		}
		u.rules = append(u.rules, c)
	}
	return u
}

func (u UserRules) Match(p *collector.Process, _ *Context) (Match, bool) {
	for _, r := range u.rules {
		if r.matches(p) {
			return Match{Kind: KindUserRule, Key: r.name, Name: r.name}, true
		}
	}
	return Match{}, false
}

func (r compiledRule) matches(p *collector.Process) bool {
	if r.executable != "" && r.executable != executableName(p) && r.executable != p.Name {
		return false
	}
	if r.command != nil && !r.command.MatchString(p.Command) {
		return false
	}
	if r.cgroup != nil && !r.cgroup.MatchString(p.Cgroup) {
		return false
	}
	if r.cwd != "" && !(p.Cwd == r.cwd || strings.HasPrefix(p.Cwd, r.cwd+"/")) {
		return false
	}
	return true
}

// renamed wraps a Layer and gives the Applications it names their configured display names.
type renamed struct {
	Layer
	to map[string]string
}

func (r renamed) Match(p *collector.Process, c *Context) (Match, bool) {
	m, ok := r.Layer.Match(p, c)
	if ok {
		if to, found := r.to[m.Name]; found {
			m.Name = to
		}
	}
	return m, ok
}

func (r renamed) Prepare(c *Context) {
	if pr, ok := r.Layer.(Preparer); ok {
		pr.Prepare(c)
	}
}

// Configured builds the default chain with the user's rules in the user-rules
// slot and the user's renames applied to every Application. Rules take part in
// the chain, so a rule runs before the layers after its slot and a process a
// rule names never reaches them.
func Configured(m machine.Machine, g config.Grouping) *Grouper {
	layers := DefaultLayers()
	for i, l := range layers {
		if _, ok := l.(UserRules); ok {
			layers[i] = newUserRules(g.Rules)
		}
	}
	if len(g.Renames) > 0 {
		to := map[string]string{}
		for _, r := range g.Renames {
			to[r.From] = r.To
		}
		for i, l := range layers {
			layers[i] = renamed{Layer: l, to: to}
		}
	}
	return New(m, layers...)
}
