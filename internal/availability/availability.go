// Package availability is the one place that says which data sources this
// machine cannot provide, and why (ADR 0006). Every data source is optional:
// a source that is missing is hidden from the views, and anything that
// depends on it is listed here with a one-line reason.
//
// How a source takes part (this is the pattern every data-source ticket
// follows, without editing a shared file):
//
//  1. Read the file or API through the Machine. When it is absent, leave that
//     part of the Sample nil (never zero) and append the path to Sample.Missing
//     (or ProcessMissing for per-process files, with the PID written "PID").
//  2. Describe the source once, in your own file: Describe("/proc/foo", Item{...})
//     in an init function. The Item names the feature, gives the one-line
//     reason and lists the views and Advice rules that lose data.
//  3. For a source that is detected rather than read (a socket, a library, a
//     device), register a Probe in an init function: RegisterProbe("gpu", fn).
//     A probe returns an Item when the source is absent, nil when present.
//     Registering an id that already exists replaces the default probe.
//  4. Let a view hide itself when its field is nil; never print 0 or an error.
//
// An Advice rule lists the Item IDs it needs and, in its Needs method, returns
// DisabledReason(in.Sample.Availability.Unavailable, ids...) when that is not
// empty; the Advisor then lists the rule as disabled with that line.
package availability

import (
	"sort"
	"strings"
	"sync"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Item is one unavailable source. JSON field names are the API.
type Item struct {
	// ID is stable and short ("pressure", "smaps_rollup", "gpu"); rules refer to it.
	ID string `json:"id"`
	// Source is the file or interface that is missing ("" for a detected environment).
	Source string `json:"source"`
	// Feature is what the user loses, in words ("Pressure stall information (PSI)").
	Feature string `json:"feature"`
	// Reason is one line saying why it is unavailable here.
	Reason string `json:"reason"`
	// Affects lists the views and Advice rules that are hidden or disabled.
	Affects []string `json:"affects"`
}

// Environment is what kind of machine this is. A value here is information,
// not a problem: cgroup v1 is fully supported.
type Environment struct {
	Cgroup            string   `json:"cgroup"` // "v2", "v1", "hybrid" or "none"
	Systemd           bool     `json:"systemd"`
	ContainerRuntimes []string `json:"container_runtimes"` // socket paths found
	GPUs              []string `json:"gpus"`               // sysfs or driver paths found
}

// Report is served inside every Sample as "availability".
type Report struct {
	Environment Environment `json:"environment"`
	Unavailable []Item      `json:"unavailable"`
}

// Probe returns an Item when its source is absent on m, otherwise nil. env is
// the already-detected Environment.
type Probe func(m machine.Machine, env Environment) *Item

var (
	mu      sync.RWMutex
	catalog = map[string]Item{} // key: a Missing path, or an Item ID
	probes  = map[string]Probe{}
)

// Describe registers what is lost when key is missing. key is a path as it
// appears in Sample.Missing ("/proc/pressure") or an Item ID. If it.ID is empty
// the key is used; if it.Source is empty and key is a path, the key is used.
func Describe(key string, it Item) {
	if it.ID == "" {
		it.ID = key
	}
	if it.Source == "" && strings.HasPrefix(key, "/") {
		it.Source = key
	}
	if it.Affects == nil {
		it.Affects = []string{}
	}
	mu.Lock()
	catalog[key] = it
	mu.Unlock()
}

// RegisterProbe adds (or replaces, by id) a detected source.
func RegisterProbe(id string, p Probe) {
	mu.Lock()
	probes[id] = p
	mu.Unlock()
}

// Inputs is what a Collector knows after one sample.
type Inputs struct {
	// Missing are the paths from Sample.Missing and Sample.ProcessMissing.
	Missing []string
	// Absent are Item IDs the Collector found absent without a failed read
	// (for example "disk_swap" when /proc/swaps lists no Disk Swap).
	Absent []string
}

// Build assembles the Report for one sample.
func Build(m machine.Machine, in Inputs) Report {
	mu.RLock()
	defer mu.RUnlock()
	env := detect(m)
	byID := map[string]Item{}
	add := func(it Item) {
		if _, dup := byID[it.ID]; !dup {
			byID[it.ID] = it
		}
	}
	for _, p := range in.Missing {
		it, ok := catalog[p]
		if !ok { // a source nobody described: still say what is missing
			it = Item{ID: p, Source: p, Feature: p, Reason: "not provided by this kernel", Affects: []string{}}
		}
		add(it)
	}
	for _, id := range in.Absent {
		if it, ok := catalog[id]; ok {
			add(it)
		}
	}
	for _, p := range probes {
		if it := p(m, env); it != nil {
			if it.Affects == nil {
				it.Affects = []string{}
			}
			add(*it)
		}
	}
	r := Report{Environment: env, Unavailable: make([]Item, 0, len(byID))}
	for _, it := range byID {
		r.Unavailable = append(r.Unavailable, it)
	}
	sort.Slice(r.Unavailable, func(i, j int) bool { return r.Unavailable[i].ID < r.Unavailable[j].ID })
	return r
}

// DisabledReason is the contract for Advice rules: it returns "" when every
// source in needs (Item IDs) is available, otherwise one line, ready to show
// next to the disabled rule, naming each missing feature and why.
func DisabledReason(unavailable []Item, needs ...string) string {
	var parts []string
	for _, id := range needs {
		for _, it := range unavailable {
			if it.ID == id {
				parts = append(parts, it.Feature+" ("+it.Reason+")")
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "needs " + strings.Join(parts, "; ")
}
