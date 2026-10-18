package advisor

import (
	"sort"
	"sync"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/config"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

const maxEvents = 1000

// Advisor evaluates rules on every observation and applies Sustained.
type Advisor struct {
	m     machine.Machine
	cfg   config.Advisor
	rules []Rule

	mu       sync.Mutex
	swap     swapTracker
	tracks   map[string]*track
	disabled []Disabled
	now      time.Time
	events   []Event
	sink     func(Event)

	marked   map[string]Marked // Hidden and Snoozed Advice by ID (state.go)
	writable bool
}

type track struct {
	rule     Rule
	finding  Finding
	id       string
	since    time.Time // condition began
	lastSeen time.Time
	active   *Advice // non-nil once Sustained
	sums     map[string]float64
	n        int
	nowFacts []Fact
}

// New creates an Advisor with the given rules (use DefaultRules).
func New(m machine.Machine, cfg config.Advisor, rules []Rule) *Advisor {
	a := &Advisor{m: m, cfg: cfg, rules: rules, tracks: map[string]*track{}}
	a.loadState()
	return a
}

// SetEventSink registers a function called (under the Advisor's lock; it must
// not call back into the Advisor) for every Event, so History (#11) can persist them.
func (a *Advisor) SetEventSink(f func(Event)) {
	a.mu.Lock()
	a.sink = f
	a.mu.Unlock()
}

// Observe evaluates every rule against one Sample taken at sample.Time.
func (a *Advisor) Observe(s collector.Sample, apps []grouping.Application) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := s.Time
	a.now = now
	in := &Input{Now: now, Window: a.cfg.Window, Cfg: a.cfg, Sample: s, Apps: apps}
	in.PSI, in.PSIReason = readPSI(a.m)
	in.Swap = a.swap.observe(a.m, now, s.Swap.Disk != nil, s.Swap.Compressed != nil,
		s.Swap.ZswapEnabled != nil && *s.Swap.ZswapEnabled)

	seen := map[string]bool{}
	a.disabled = a.disabled[:0]
	for _, r := range a.rules {
		if reason := r.Needs(in); reason != "" {
			a.disabled = append(a.disabled, Disabled{Rule: r.ID(), Title: r.Title(), Reason: reason})
			continue
		}
		for _, f := range r.Check(in) {
			id := r.ID()
			if f.Subject != "" {
				id += ":" + f.Subject
			}
			seen[id] = true
			t := a.tracks[id]
			if t == nil {
				t = &track{rule: r, id: id, since: now, sums: map[string]float64{}}
				a.tracks[id] = t
			}
			t.finding, t.lastSeen = f, now
			t.n++
			for _, fact := range f.Facts {
				if fact.Value != nil {
					t.sums[fact.Name] += *fact.Value
				}
			}
			a.update(t, now)
		}
	}
	for id, t := range a.tracks {
		if seen[id] {
			continue
		}
		if t.active != nil {
			a.emit(EventEnded, now, t.active, "")
		}
		delete(a.tracks, id)
	}
	a.releaseLocked(a.m.Clock.Now())
}

// update promotes a track to Advice once the condition held for the window and
// follows Severity changes.
func (a *Advisor) update(t *track, now time.Time) {
	f := t.finding
	if now.Sub(t.since) < a.cfg.Window {
		return
	}
	adv := Advice{
		ID: t.id, Rule: t.rule.ID(), Category: f.Category, Severity: f.Severity, Title: f.Title,
		Summary: f.Summary, Applications: f.Applications, Change: f.Change, ConditionSince: t.since,
		Evidence: Evidence{WindowSeconds: now.Sub(t.since).Seconds(), Start: t.since, End: now, Facts: t.facts()},
	}
	if adv.Applications == nil {
		adv.Applications = []AppRef{}
	}
	if t.active == nil {
		adv.Since = now
		t.active = &adv
		a.emit(EventStarted, now, &adv, "")
		return
	}
	adv.Since = t.active.Since
	prev := t.active.Severity
	t.active = &adv
	if prev != adv.Severity {
		a.emit(EventSeverityChanged, now, &adv, prev)
	}
}

func (t *track) facts() []Fact {
	out := make([]Fact, len(t.finding.Facts))
	for i, f := range t.finding.Facts {
		if f.Value != nil && t.n > 0 {
			avg := t.sums[f.Name] / float64(t.n)
			f.Avg = &avg
		}
		out[i] = f
	}
	return out
}

func (a *Advisor) emit(typ string, at time.Time, adv *Advice, prev Severity) {
	e := Event{Type: typ, Time: at, AdviceID: adv.ID, Rule: adv.Rule, Category: adv.Category,
		Severity: adv.Severity, PreviousSeverity: prev, Title: adv.Title, Applications: adv.Applications,
		ConditionSince: adv.ConditionSince}
	a.events = append(a.events, e)
	if len(a.events) > maxEvents {
		a.events = a.events[len(a.events)-maxEvents:]
	}
	if a.sink != nil {
		a.sink(e)
	}
}

// Snapshot returns the current Advice, pending conditions and disabled rules.
// Advice is ordered by Severity (critical first), then ID.
func (a *Advisor) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Snapshot{Time: a.now, WindowSeconds: a.cfg.Window.Seconds(),
		Advice: []Advice{}, Pending: []Pending{}, Disabled: append([]Disabled{}, a.disabled...),
		Hidden: []Marked{}, SnoozePresets: a.presets()}
	clock := a.m.Clock.Now()
	for _, mk := range a.markedList() {
		if !a.visibleAt(mk, clock) {
			s.Hidden = append(s.Hidden, mk)
		}
	}
	for _, t := range a.tracks {
		if mk, ok := a.marked[t.id]; ok && t.active != nil && !a.visibleAt(mk, clock) {
			continue
		}
		if t.active != nil {
			s.Advice = append(s.Advice, *t.active)
			continue
		}
		rem := a.cfg.Window - a.now.Sub(t.since)
		s.Pending = append(s.Pending, Pending{ID: t.id, Rule: t.rule.ID(), Title: t.finding.Title,
			ConditionSince: t.since, SecondsRemaining: rem.Seconds()})
	}
	sort.Slice(s.Advice, func(i, j int) bool {
		x, y := s.Advice[i], s.Advice[j]
		if x.Severity.rank() != y.Severity.rank() {
			return x.Severity.rank() > y.Severity.rank()
		}
		return x.ID < y.ID
	})
	sort.Slice(s.Pending, func(i, j int) bool { return s.Pending[i].ID < s.Pending[j].ID })
	return s
}

// Events returns the recorded start/severity-change/end events, oldest first.
func (a *Advisor) Events() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event{}, a.events...)
}
