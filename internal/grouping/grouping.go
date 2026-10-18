// Package grouping rolls Processes up into Applications (ADR 0005).
//
// A Grouper is an ordered chain of Layers. For each Process the first Layer
// that returns a Match wins; a Process no Layer can name falls into Other, so
// every Process belongs to exactly one Application.
//
// Adding a layer (containers and Pods, App Profiles, Dev & Build per Project,
// user rules from the config file) means writing a type with Name and Match
// and inserting it at the right position in DefaultLayers (layers.go). Nothing
// else changes: the Grouper, the Application shape and the API are layer
// agnostic. A layer that needs per-sample setup (for example one container
// socket query) also implements Preparer.
package grouping

import (
	"sort"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Kinds of Application, as served in the API "kind" field.
const (
	KindContainer = "container"
	KindPod       = "pod"
	KindProfile   = "profile"  // App Profile
	KindDevBuild  = "devbuild" // Dev & Build per Project
	KindSystemd   = "systemd"  // systemd unit or desktop scope
	KindSnap      = "snap"
	KindFlatpak   = "flatpak"
	KindKernel    = "kernel"
	KindAncestor  = "ancestor"
	KindOther     = "other"
)

// Match is a Layer's answer for one Process.
type Match struct {
	Kind string // one of the Kind constants
	Key  string // identity within the Kind; Processes with equal Kind and Key share an Application
	Name string // display name
	// Sub optionally names a sub-group inside the Application, such as the
	// container within a Pod; it is shown in the drilldown.
	Sub string
	// Role optionally names what this Process is for inside its Application,
	// such as "language-server" or "renderer" (see the Role constants in
	// profiles.go). Settings Fixes read it to tell an editor's watchers from
	// its language servers.
	Role string
}

// Layer recognises the Processes it can name. Match must be deterministic for
// a given Context and must not mutate the Process.
type Layer interface {
	Name() string
	Match(p *collector.Process, c *Context) (Match, bool)
}

// Preparer is implemented by a Layer that needs to look at the whole sample
// once before Match is called for each Process.
type Preparer interface {
	Prepare(c *Context)
}

// Context gives a Layer the rest of the sample and the Machine.
type Context struct {
	Machine machine.Machine
	ByPID   map[int]*collector.Process
}

// Parent returns the parent of p, or nil when it is not in the sample.
func (c *Context) Parent(p *collector.Process) *collector.Process {
	if p.PPID <= 0 || p.PPID == p.PID {
		return nil
	}
	return c.ByPID[p.PPID]
}

// Ancestors returns p's ancestors, nearest first.
func (c *Context) Ancestors(p *collector.Process) []*collector.Process {
	var out []*collector.Process
	for cur, n := c.Parent(p), 0; cur != nil && n < 256; cur, n = c.Parent(cur), n+1 {
		out = append(out, cur)
	}
	return out
}

// Member is a Process inside an Application. The embedded Process keeps the
// API's process fields, including command line, working directory and cgroup.
type Member struct {
	collector.Process
	Subgroup string `json:"subgroup,omitempty"`
	Role     string `json:"role,omitempty"`
}

// Application is a named group of Processes shown as one row. Totals are nil
// when no member has the figure (for example kernel threads have no memory).
type Application struct {
	ID           string   `json:"id"` // Kind + ":" + Key, unique per sample
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	PIDs         []int    `json:"pids"`         // every member, ascending
	PrimaryPIDs  []int    `json:"primary_pids"` // members whose parent is outside the Application, at most 5
	ProcessCount int      `json:"process_count"`
	RSS          *uint64  `json:"rss_bytes"`
	PSS          *uint64  `json:"pss_bytes"`
	Swap         *uint64  `json:"swap_bytes"`
	CPU          *float64 `json:"cpu_percent"`
	Read         *float64 `json:"disk_read_bytes_per_sec"`
	Write        *float64 `json:"disk_write_bytes_per_sec"`
	Processes    []Member `json:"processes"` // ascending PID
}

// PSSPlusSwap is the Application's contribution to the Working Set.
func (a Application) PSSPlusSwap() *uint64 {
	if a.PSS == nil && a.Swap == nil {
		return nil
	}
	var t uint64
	if a.PSS != nil {
		t += *a.PSS
	}
	if a.Swap != nil {
		t += *a.Swap
	}
	return &t
}

const maxPrimaryPIDs = 5

// Grouper is the ordered chain of Layers.
type Grouper struct {
	m      machine.Machine
	layers []Layer
}

// New builds a Grouper from layers, tried in the order given.
func New(m machine.Machine, layers ...Layer) *Grouper { return &Grouper{m: m, layers: layers} }

// Default builds the Grouper with the built-in chain (see DefaultLayers).
func Default(m machine.Machine) *Grouper { return New(m, DefaultLayers()...) }

var other = Match{Kind: KindOther, Key: "other", Name: "Other"}

// Group assigns every Process to exactly one Application. The result is
// ordered by PSS + swap, largest first, then by name.
func (g *Grouper) Group(procs []collector.Process) []Application {
	c := &Context{Machine: g.m, ByPID: make(map[int]*collector.Process, len(procs))}
	for i := range procs {
		c.ByPID[procs[i].PID] = &procs[i]
	}
	for _, l := range g.layers {
		if pr, ok := l.(Preparer); ok {
			pr.Prepare(c)
		}
	}
	apps := map[string]*Application{}
	appOf := map[int]string{}
	for i := range procs {
		p := &procs[i]
		match := other
		for _, l := range g.layers {
			if mm, ok := l.Match(p, c); ok {
				match = mm
				break
			}
		}
		id := match.Kind + ":" + match.Key
		a := apps[id]
		if a == nil {
			a = &Application{ID: id, Name: match.Name, Kind: match.Kind}
			apps[id] = a
		}
		a.Processes = append(a.Processes, Member{Process: *p, Subgroup: match.Sub, Role: match.Role})
		appOf[p.PID] = id
	}
	out := make([]Application, 0, len(apps))
	for id, a := range apps {
		finish(a, id, appOf)
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := total(out[i]), total(out[j])
		if a != b {
			return a > b
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func total(a Application) uint64 {
	if t := a.PSSPlusSwap(); t != nil {
		return *t
	}
	return 0
}

func finish(a *Application, id string, appOf map[int]string) {
	sort.Slice(a.Processes, func(i, j int) bool { return a.Processes[i].PID < a.Processes[j].PID })
	a.ProcessCount = len(a.Processes)
	a.PIDs = make([]int, 0, len(a.Processes))
	a.PrimaryPIDs = []int{}
	for _, m := range a.Processes {
		a.PIDs = append(a.PIDs, m.PID)
		if appOf[m.PPID] != id && len(a.PrimaryPIDs) < maxPrimaryPIDs {
			a.PrimaryPIDs = append(a.PrimaryPIDs, m.PID)
		}
		a.RSS = addU(a.RSS, m.RSS)
		a.PSS = addU(a.PSS, m.PSS)
		a.Swap = addU(a.Swap, m.Swap)
		a.CPU = addF(a.CPU, m.CPU)
		a.Read = addF(a.Read, m.Read)
		a.Write = addF(a.Write, m.Write)
	}
}

func addU(t, v *uint64) *uint64 {
	if v == nil {
		return t
	}
	n := *v
	if t != nil {
		n += *t
	}
	return &n
}

func addF(t, v *float64) *float64 {
	if v == nil {
		return t
	}
	n := *v
	if t != nil {
		n += *t
	}
	return &n
}
