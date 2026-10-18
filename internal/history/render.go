package history

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// Render prints a Replay as text: one line of system metrics per sample with
// its top Applications, Advice events in time order, and the Processes with
// full command lines for the last sample shown (every sample with allProcs).
func Render(w io.Writer, r Result, allProcs bool) {
	if r.Unclean {
		when := "an unknown time"
		if !r.EndedAt.IsZero() {
			when = r.EndedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(w, "notice: last session ended without clean shutdown at %s\n", when)
	}
	if len(r.Samples) == 0 {
		fmt.Fprintln(w, "no History samples in this window")
		for _, e := range r.Events {
			renderEvent(w, e)
		}
		return
	}
	fmt.Fprintf(w, "Replay %s to %s  (%d samples, %d Advice events)\n",
		r.From.Format(time.RFC3339), r.To.Format(time.RFC3339), len(r.Samples), len(r.Events))
	type line struct {
		t    time.Time
		rec  *Record
		ev   *Event
		last bool
	}
	var lines []line
	for i := range r.Samples {
		lines = append(lines, line{t: r.Samples[i].Time, rec: &r.Samples[i], last: i == len(r.Samples)-1})
	}
	for i := range r.Events {
		lines = append(lines, line{t: r.Events[i].Time, ev: &r.Events[i]})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if !lines[i].t.Equal(lines[j].t) {
			return lines[i].t.Before(lines[j].t)
		}
		return lines[i].ev == nil && lines[j].ev != nil
	})
	for _, l := range lines {
		if l.ev != nil {
			renderEvent(w, *l.ev)
			continue
		}
		renderSample(w, *l.rec, l.last || allProcs)
	}
}

func renderEvent(w io.Writer, e Event) {
	fmt.Fprintf(w, "%s  ADVICE %s  %s [%s %s]", e.Time.Format("15:04:05"), e.Kind, e.Title, e.Category, e.Severity)
	if len(e.Applications) > 0 {
		fmt.Fprintf(w, " apps=%v", e.Applications)
	}
	if e.Detail != "" {
		fmt.Fprintf(w, "  %s", e.Detail)
	}
	fmt.Fprintln(w)
}

func renderSample(w io.Writer, r Record, procs bool) {
	fmt.Fprintf(w, "%s  ", r.Time.Format("15:04:05"))
	if r.CPU.TotalPercent != nil {
		fmt.Fprintf(w, "cpu %.0f%%  ", *r.CPU.TotalPercent)
	}
	if m := r.Memory; m != nil {
		fmt.Fprintf(w, "ram %s/%s  ", gib(m.UsedBytes), gib(m.TotalBytes))
	}
	if d := r.Swap.Disk; d != nil {
		fmt.Fprintf(w, "disk-swap %s  ", gib(d.UsedBytes))
	}
	if c := r.Swap.Compressed; c != nil {
		fmt.Fprintf(w, "compressed-swap %s  ", gib(c.UsedBytes))
	}
	if r.Swap.InPagesPerSec != nil && r.Swap.OutPagesPerSec != nil {
		fmt.Fprintf(w, "swap-in/out %.0f/%.0f pg/s  ", *r.Swap.InPagesPerSec, *r.Swap.OutPagesPerSec)
	}
	if p := r.Pressure; p != nil && p.Some != nil {
		fmt.Fprintf(w, "mem-psi %.1f%%  ", *p.Some)
	}
	if r.WorkingSet != nil {
		fmt.Fprintf(w, "working-set %s  ", gib(*r.WorkingSet))
	}
	if r.Thrashing {
		fmt.Fprint(w, "THRASHING")
	}
	fmt.Fprintln(w)
	for _, a := range r.Apps {
		fmt.Fprintf(w, "    %-26s pss %s  swap %s  procs %d\n", a.Name, mib(a.PSS), mib(a.Swap), a.ProcessCount)
		if procs {
			for _, p := range a.Processes {
				fmt.Fprintf(w, "        %6d  pss %s  swap %s  %s\n", p.PID, mib(p.PSS), mib(p.Swap), p.Command)
			}
			if a.OmittedProcs > 0 {
				fmt.Fprintf(w, "        (+%d more processes not kept)\n", a.OmittedProcs)
			}
		}
	}
}

func gib(b uint64) string { return fmt.Sprintf("%.1fG", float64(b)/(1<<30)) }

func mib(p *uint64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%dM", *p>>20)
}
