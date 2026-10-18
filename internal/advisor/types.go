// Package advisor turns Samples and Applications into Advice.
//
// The framework (advisor.go) owns everything rules should not repeat:
// Sustained windows on the machine's clock, stable Advice identity, Severity
// changes, start/end events and the list of rules disabled for lack of data
// (ADR 0006). A rule (rule.go) only says, for the current observation, which
// findings are true right now. Rules are advise-only (ADR 0003).
package advisor

import "time"

// Severity is how serious a piece of Advice is right now.
type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

func (s Severity) rank() int {
	switch s {
	case Critical:
		return 3
	case Warning:
		return 2
	}
	return 1
}

// Category is the glossary split of Advice.
type Category string

const (
	SettingsFix   Category = "settings_fix"
	HardwareLimit Category = "hardware_limit"
)

// AppRef names an Application a piece of Advice concerns.
type AppRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Fact is one number of the evidence. Value is the latest reading and Avg the
// mean over the Sustained window; Text carries a non-numeric fact.
type Fact struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value,omitempty"`
	Avg   *float64 `json:"avg,omitempty"`
	Unit  string   `json:"unit,omitempty"`
	Text  string   `json:"text,omitempty"`
}

// Evidence is the numbers behind Advice and the window they cover.
type Evidence struct {
	WindowSeconds float64   `json:"window_seconds"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	Facts         []Fact    `json:"facts"`
}

// Change is the exact change of a Settings Fix, to be copied by the user.
// Kind examples: "sysctl.d", "compose", "docker-run", "k8s", "editor-settings".
type Change struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`  // file or place to put it, e.g. /etc/sysctl.d/99-swappiness.conf
	Content string `json:"content"` // the text to copy
	Note    string `json:"note,omitempty"`
}

// Advice is a single finding about the running system.
//
// ID is stable while the finding persists and across restarts: the rule ID,
// plus ":" and the rule's subject when one rule can fire for several things
// (for example "container-limit:docker:db"). Hide and Snooze (#17) key on ID
// and Severity.
type Advice struct {
	ID             string    `json:"id"`
	Rule           string    `json:"rule"`
	Category       Category  `json:"category"`
	Severity       Severity  `json:"severity"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	Applications   []AppRef  `json:"applications"`
	Evidence       Evidence  `json:"evidence"`
	Change         *Change   `json:"change"` // nil for a Hardware Limit
	Since          time.Time `json:"since"`  // when it became Sustained and active
	ConditionSince time.Time `json:"condition_since"`
}

// Pending is a condition that is true now but not yet Sustained.
type Pending struct {
	ID               string    `json:"id"`
	Rule             string    `json:"rule"`
	Title            string    `json:"title"`
	ConditionSince   time.Time `json:"condition_since"`
	SecondsRemaining float64   `json:"seconds_remaining"`
}

// Disabled is a rule that cannot run on this machine, with a one-line reason.
type Disabled struct {
	Rule   string `json:"rule"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// Snapshot is what /api/advice serves.
type Snapshot struct {
	Time          time.Time  `json:"time"`
	WindowSeconds float64    `json:"window_seconds"`
	Advice        []Advice   `json:"advice"`
	Pending       []Pending  `json:"pending"`
	Disabled      []Disabled `json:"disabled"`
	// Hidden lists Advice marked Hidden or Snoozed, active or not, so it can be unhidden.
	Hidden        []Marked `json:"hidden"`
	SnoozePresets []Preset `json:"snooze_presets"`
}

// Event types emitted for History (#11).
const (
	EventStarted         = "advice_started"
	EventSeverityChanged = "advice_severity_changed"
	EventEnded           = "advice_ended"
)

// Event is the record of a change in Advice, for History and Replay. Time is
// the sample time at which it became true (for advice_started, the moment the
// condition had been Sustained for the whole window; ConditionSince is when it
// began). Severity is the Severity after the event (for advice_ended, the last
// one held); PreviousSeverity is set only for advice_severity_changed.
type Event struct {
	Type             string    `json:"type"`
	Time             time.Time `json:"time"`
	AdviceID         string    `json:"advice_id"`
	Rule             string    `json:"rule"`
	Category         Category  `json:"category"`
	Severity         Severity  `json:"severity"`
	PreviousSeverity Severity  `json:"previous_severity,omitempty"`
	Title            string    `json:"title"`
	Applications     []AppRef  `json:"applications"`
	ConditionSince   time.Time `json:"condition_since"`
}
