package advisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StateFile is the one file this tool writes besides its own process settings
// (ADR 0003): the Hidden and Snoozed state, in the Machine's state dir.
const StateFile = "state.json"

// Hidden/Snoozed kinds.
const (
	StateHidden  = "hidden"
	StateSnoozed = "snoozed"
)

// Marked is Advice the user has marked Hidden or Snoozed. Title is kept so it
// can be listed (and unhidden) while the Advice is not active.
type Marked struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Title string `json:"title"`
	// Severity is the level at which the Advice was marked; Hidden Advice
	// returns only when its Severity rises above it.
	Severity Severity  `json:"severity"`
	MarkedAt time.Time `json:"marked_at"`
	Until    time.Time `json:"until,omitzero"` // Snoozed only
}

// Preset is a Snoozed period offered to the user.
type Preset struct {
	Label   string  `json:"label"`
	Seconds float64 `json:"seconds"`
}

// ErrNoAdvice is returned when the Advice to mark is not active.
var ErrNoAdvice = errors.New("no such active Advice")

// ErrNoPreset is returned for a snooze period that is not a configured preset.
var ErrNoPreset = errors.New("not a configured snooze preset")

type stateDoc struct {
	Version int      `json:"version"`
	Marked  []Marked `json:"marked"`
}

// loadState reads state.json; a missing or unreadable file means nothing is marked.
func (a *Advisor) loadState() {
	a.marked = map[string]Marked{}
	b, err := a.m.ReadFileAt(a.statePath())
	if err != nil {
		return
	}
	var d stateDoc
	if json.Unmarshal(b, &d) != nil {
		return
	}
	for _, mk := range d.Marked {
		if mk.ID != "" && (mk.State == StateHidden || mk.State == StateSnoozed) {
			a.marked[mk.ID] = mk
		}
	}
}

func (a *Advisor) statePath() string { return filepath.Join(a.m.StateDir, StateFile) }

// AllowStateWrites lets this Advisor write state.json. Only the running
// instance calls it; a one-shot `report` only reads.
func (a *Advisor) AllowStateWrites() {
	a.mu.Lock()
	a.writable = true
	a.mu.Unlock()
}

// saveLocked writes state.json atomically (a temporary file beside it, then a
// rename). Without write permission it succeeds without touching the disk.
func (a *Advisor) saveLocked() error {
	if !a.writable {
		return nil
	}
	d := stateDoc{Version: 1, Marked: a.markedList()}
	b, _ := json.MarshalIndent(d, "", "  ")
	path := a.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (a *Advisor) markedList() []Marked {
	out := make([]Marked, 0, len(a.marked))
	for _, mk := range a.marked {
		out = append(out, mk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Hide marks active Advice Hidden at its current Severity.
func (a *Advisor) Hide(id string) error {
	return a.mark(id, StateHidden, "")
}

// Snooze marks active Advice Snoozed for the configured preset with that label.
func (a *Advisor) Snooze(id, preset string) error {
	return a.mark(id, StateSnoozed, preset)
}

func (a *Advisor) mark(id, state, preset string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tracks[id]
	if t == nil || t.active == nil {
		return fmt.Errorf("%w: %s", ErrNoAdvice, id)
	}
	now := a.m.Clock.Now()
	mk := Marked{ID: id, State: state, Title: t.active.Title, Severity: t.active.Severity, MarkedAt: now}
	if state == StateSnoozed {
		var d time.Duration
		for _, p := range a.cfg.SnoozePresets {
			if p.Label == preset {
				d = p.Duration
			}
		}
		if d == 0 {
			return fmt.Errorf("%w: %q", ErrNoPreset, preset)
		}
		mk.Until = now.Add(d)
	}
	return a.commit(id, mk, true)
}

// Unhide returns Hidden or Snoozed Advice to view.
func (a *Advisor) Unhide(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.marked[id]; !ok {
		return fmt.Errorf("%w: %s is not hidden or snoozed", ErrNoAdvice, id)
	}
	return a.commit(id, Marked{}, false)
}

// commit applies a change and saves it; on a save failure nothing changes.
func (a *Advisor) commit(id string, mk Marked, set bool) error {
	old, had := a.marked[id]
	if set {
		a.marked[id] = mk
	} else {
		delete(a.marked, id)
	}
	if err := a.saveLocked(); err != nil {
		if had {
			a.marked[id] = old
		} else {
			delete(a.marked, id)
		}
		return fmt.Errorf("save %s: %w", StateFile, err)
	}
	return nil
}

// releaseLocked returns marked Advice to view when its time or Severity says so:
// a Snoozed period that has passed, or Hidden Advice whose Severity rose above
// the level at which it was hidden. It saves when anything changed.
func (a *Advisor) releaseLocked(now time.Time) {
	changed := false
	for id, mk := range a.marked {
		if a.visibleAt(mk, now) {
			delete(a.marked, id)
			changed = true
		}
	}
	if changed {
		a.saveLocked() // best effort; the in-memory state is already right
	}
}

// visibleAt reports whether the mark no longer applies.
func (a *Advisor) visibleAt(mk Marked, now time.Time) bool {
	if mk.State == StateSnoozed {
		return !now.Before(mk.Until)
	}
	if t := a.tracks[mk.ID]; t != nil && t.active != nil {
		return t.active.Severity.rank() > mk.Severity.rank()
	}
	return false
}

func (a *Advisor) presets() []Preset {
	out := make([]Preset, 0, len(a.cfg.SnoozePresets))
	for _, p := range a.cfg.SnoozePresets {
		out = append(out, Preset{p.Label, p.Duration.Seconds()})
	}
	return out
}
