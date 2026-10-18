package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/advisor"
	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/config"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// adviceSection prints current Advice. Sustained needs a window of history, so
// it asks the running instance on the configured port first; without one it
// evaluates the two samples it just took and lists only conditions that hold
// right now (not yet Sustained).
func adviceSection(w io.Writer, env Env, cfg config.Config, first, second collector.Sample) {
	fmt.Fprintf(w, "\nAdvice\n")
	if snap, ok := fetchAdvice(cfg.Port); ok {
		fmt.Fprintf(w, "(from the running instance on 127.0.0.1:%d, window %.0fs)\n", cfg.Port, snap.WindowSeconds)
		printSnapshot(w, snap)
		return
	}
	adv := advisor.New(env.Machine, cfg.Advisor, advisor.DefaultRules())
	if env.Machine.Exists(history.Path(env.Machine.StateDir)) {
		adv.SetPeakSource(func(since time.Time) (history.Peaks, error) { return history.ReadPeaks(env.Machine.StateDir, since) })
	}
	g := newGrouper(env)
	adv.Observe(first, g.Group(first.Processes))
	adv.Observe(second, g.Group(second.Processes))
	snap := adv.Snapshot()
	fmt.Fprintf(w, "(no running instance: a one-shot report cannot verify the %.0fs Sustained window; run the program to get Sustained Advice)\n", snap.WindowSeconds)
	printSnapshot(w, snap)
}

func fetchAdvice(port int) (advisor.Snapshot, bool) {
	var snap advisor.Snapshot
	if port <= 0 {
		return snap, false
	}
	c := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/advice", port))
	if err != nil {
		return snap, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&snap) != nil {
		return snap, false
	}
	return snap, true
}

func printSnapshot(w io.Writer, s advisor.Snapshot) {
	if len(s.Advice) == 0 {
		fmt.Fprintln(w, "no active Advice")
	}
	for _, a := range s.Advice {
		kind := "Settings Fix"
		if a.Category == advisor.HardwareLimit {
			kind = "Hardware Limit"
		}
		fmt.Fprintf(w, "[%s] %s: %s\n", a.Severity, kind, a.Title)
		fmt.Fprintf(w, "  %s\n", a.Summary)
		if len(a.Applications) > 0 {
			names := ""
			for i, ap := range a.Applications {
				if i > 0 {
					names += ", "
				}
				names += ap.Name
			}
			fmt.Fprintf(w, "  Applications: %s\n", names)
		}
		fmt.Fprintf(w, "  Evidence over %.0fs (%s to %s)\n", a.Evidence.WindowSeconds,
			a.Evidence.Start.Format("15:04:05"), a.Evidence.End.Format("15:04:05"))
		for _, f := range a.Evidence.Facts {
			switch {
			case f.Value != nil && f.Avg != nil:
				fmt.Fprintf(w, "    %s: %.1f %s (average %.1f)\n", f.Name, *f.Value, f.Unit, *f.Avg)
			case f.Value != nil:
				fmt.Fprintf(w, "    %s: %.1f %s\n", f.Name, *f.Value, f.Unit)
			default:
				fmt.Fprintf(w, "    %s: %s\n", f.Name, f.Text)
			}
		}
		if a.Change != nil {
			fmt.Fprintf(w, "  Change (%s, %s):\n%s\n", a.Change.Kind, a.Change.Target, a.Change.Content)
			if a.Change.Note != "" {
				fmt.Fprintf(w, "  Note: %s\n", a.Change.Note)
			}
		}
	}
	for _, p := range s.Pending {
		fmt.Fprintf(w, "pending (not yet Sustained): %s, %.0fs to go\n", p.Title, p.SecondsRemaining)
	}
	for _, h := range s.Hidden {
		if h.State == advisor.StateSnoozed {
			fmt.Fprintf(w, "snoozed until %s: %s (id %s)\n", h.Until.Format("2006-01-02 15:04"), h.Title, h.ID)
		} else {
			fmt.Fprintf(w, "hidden (until Severity rises above %s): %s (id %s)\n", h.Severity, h.Title, h.ID)
		}
	}
	for _, d := range s.Disabled {
		fmt.Fprintf(w, "disabled: %s: %s\n", d.Title, d.Reason)
	}
}
