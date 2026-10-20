package advisor

import (
	"fmt"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/availability"
	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// oomMemoryBuffer is added to the configured Sustained window (in.Window) to
// decide how long an OOM kill keeps being reported as a Finding after it
// happens. Advice is defined (GLOSSARY.md) as a continuous condition, and the
// framework (advisor.go) only promotes a condition to visible Advice once it
// has held for the Sustained window; a kernel OOM kill is a one-shot past
// event, true for exactly one sample. Remembering it for window+buffer gives
// the Sustained window time to see it and emit a normal start/end Advice pair
// (recorded to History, ADR 0004) before it expires on its own; the buffer is
// slack for the next sample to land before the deadline.
const oomMemoryBuffer = 30 * time.Second

// OOMKillRule reports a kernel OOM kill as Advice in its own right, separate
// from HardwareLimitRule (which folds the same fact in as evidence when its
// own, broader condition is already true). This rule fires even when the
// machine is not otherwise Sustained over the Hardware Limit thresholds -- for
// example a container killed by its own cgroup memory.max while the rest of
// the machine has headroom.
type OOMKillRule struct {
	last *oomEvent
}

type oomEvent struct {
	at      time.Time
	kills   uint64
	victims []collector.OOMVictim
}

func (*OOMKillRule) ID() string    { return "oom-kill" }
func (*OOMKillRule) Title() string { return "Kernel out-of-memory kill" }

func (*OOMKillRule) Needs(in *Input) string {
	return availability.DisabledReason(in.Sample.Availability.Unavailable, "oom_kill_count")
}

func (r *OOMKillRule) Check(in *Input) []Finding {
	if in.Sample.OOMKills != nil && *in.Sample.OOMKills > 0 {
		r.last = &oomEvent{at: in.Now, kills: *in.Sample.OOMKills, victims: in.Sample.OOMVictims}
	}
	if r.last == nil || in.Now.Sub(r.last.at) > in.Window+oomMemoryBuffer {
		return nil
	}
	e := r.last
	facts := []Fact{fnum("oom_kills", float64(e.kills), "count")}
	for _, v := range e.victims {
		facts = append(facts, Fact{Name: "oom_killed_process", Text: oomVictimText(v)})
	}
	return []Finding{{
		Category: HardwareLimit,
		Severity: Critical,
		Title:    "The kernel killed a process to free memory",
		Summary:  oomSummary(e),
		Facts:    facts,
	}}
}

func oomSummary(e *oomEvent) string {
	if len(e.victims) == 0 {
		return fmt.Sprintf("The kernel's OOM killer fired %d time(s) around %s; which process it killed could not be matched to one seen the sample before.",
			e.kills, e.at.Format("15:04:05"))
	}
	names := make([]string, len(e.victims))
	for i, v := range e.victims {
		names[i] = v.Name
	}
	return fmt.Sprintf("The kernel's OOM killer fired to free memory, most likely killing %s. This machine ran out of memory at that moment.", joinNames(names))
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// oomVictimText renders one OOM victim for a Fact: name, pid and its last
// known memory use (omitted when PSS was never read for it).
func oomVictimText(v collector.OOMVictim) string {
	if v.PSSBytes == nil {
		return fmt.Sprintf("%s (pid %d)", v.Name, v.PID)
	}
	mem := fmt.Sprintf("%.0f MiB", float64(*v.PSSBytes)/(1<<20))
	if v.SwapBytes != nil && *v.SwapBytes > 0 {
		mem += fmt.Sprintf(" + %.0f MiB swap", float64(*v.SwapBytes)/(1<<20))
	}
	return fmt.Sprintf("%s (pid %d) (%s)", v.Name, v.PID, mem)
}
