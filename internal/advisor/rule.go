package advisor

import (
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/config"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// Rule is one kind of Advice. To add a rule: implement Rule, add it to
// DefaultRules. Check reports only what is true NOW; the framework applies the
// Sustained window, identity, Severity changes and events.
type Rule interface {
	// ID is the stable rule identifier, e.g. "hardware-limit".
	ID() string
	// Title is a short name for the disabled list.
	Title() string
	// Needs returns "" when the data this rule depends on exists on this
	// machine, otherwise a one-line reason it is disabled (ADR 0006).
	Needs(in *Input) string
	// Check returns the findings that hold in this observation.
	Check(in *Input) []Finding
}

// Finding is a rule's answer for one subject in one observation.
type Finding struct {
	// Subject distinguishes findings of one rule, e.g. a container name; "" if the rule has one.
	Subject      string
	Severity     Severity
	Category     Category
	Title        string
	Summary      string
	Applications []AppRef
	Facts        []Fact // Value is the current reading; the framework fills Avg
	Change       *Change
}

// Input is everything a rule may look at in one observation.
type Input struct {
	Now     time.Time
	Machine machine.Machine // for rules that look at the Project on disk (read only)
	Window  time.Duration
	Cfg     config.Advisor
	Sample  collector.Sample
	Apps    []grouping.Application // ordered by Working Set contribution, largest first
	// PSI is nil when /proc/pressure/memory is missing; PSIReason says why.
	PSI       *PSI
	PSIReason string
	Swap      SwapActivity
	// Swappiness is vm.swappiness; nil when /proc/sys/vm/swappiness is unreadable.
	Swappiness *int
}

// WorkingSet is the sum over Applications of PSS + swap. ok is false when no
// Application has a PSS or swap figure.
func (in *Input) WorkingSet() (bytes uint64, ok bool) {
	for _, a := range in.Apps {
		if t := a.PSSPlusSwap(); t != nil {
			bytes += *t
			ok = true
		}
	}
	return
}

// TopApps returns up to n Applications by Working Set contribution.
func (in *Input) TopApps(n int) []AppRef {
	var out []AppRef
	for _, a := range in.Apps {
		if t := a.PSSPlusSwap(); t == nil || *t == 0 {
			continue
		}
		if len(out) == n {
			break
		}
		out = append(out, AppRef{ID: a.ID, Name: a.Name})
	}
	if out == nil {
		out = []AppRef{}
	}
	return out
}

// DefaultRules lists every rule. Tickets #13-#17 add theirs here.
func DefaultRules() []Rule {
	return []Rule{HardwareLimitRule{}, SwappinessRule{}, CompressedSwapThrashingRule{}, SwapWearRule{}, EditorWatcherRule{}, BrowserSprawlRule{}}
}

func fnum(name string, v float64, unit string) Fact { return Fact{Name: name, Value: &v, Unit: unit} }
