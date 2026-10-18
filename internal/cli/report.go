package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/config"
)

// report samples twice, one second apart so rates exist, and prints a table.
func report(env Env, cfg config.Config, col collector.Collector) int {
	first, err := col.Collect()
	if err != nil {
		fmt.Fprintln(env.Stderr, "error:", err)
		return 1
	}
	env.Machine.Clock.Sleep(time.Second)
	s, err := col.Collect()
	if err != nil {
		fmt.Fprintln(env.Stderr, "error:", err)
		return 1
	}
	w := env.Stdout
	fmt.Fprintf(w, "sys-telemetry-app report  %s\n\n", s.Time.Format(time.RFC3339))

	c := s.CPU
	fmt.Fprintf(w, "CPU  %sload %s %s %s  cores %d\n", pct(c.TotalPercent), num(c.Load1), num(c.Load5), num(c.Load15), c.Cores)
	if m := s.Memory; m != nil {
		fmt.Fprintf(w, "RAM  total %s  available %s  used %s  cached %s  buffers %s  dirty %s\n",
			gib(m.TotalBytes), gib(m.AvailableBytes), gib(m.UsedBytes), gib(m.CachedBytes), gib(m.BuffersBytes), mib(m.DirtyBytes))
	} else {
		fmt.Fprintln(w, "RAM  unavailable")
	}
	sw := s.Swap
	if sw.Disk != nil {
		fmt.Fprintf(w, "Disk Swap        used %s of %s\n", gib(sw.Disk.UsedBytes), gib(sw.Disk.TotalBytes))
	} else {
		fmt.Fprintln(w, "Disk Swap        none")
	}
	if c := sw.Compressed; c != nil {
		cost := ""
		if c.RAMUsedBytes != nil {
			cost = fmt.Sprintf("  (RAM cost %s)", gib(*c.RAMUsedBytes))
		}
		fmt.Fprintf(w, "Compressed Swap  used %s of %s%s\n", gib(c.UsedBytes), gib(c.TotalBytes), cost)
	} else {
		fmt.Fprintln(w, "Compressed Swap  none")
	}
	if sw.InPagesPerSec != nil && sw.OutPagesPerSec != nil {
		fmt.Fprintf(w, "Swap-in %.0f pages/s  Swap-out %.0f pages/s\n", *sw.InPagesPerSec, *sw.OutPagesPerSec)
	} else {
		fmt.Fprintln(w, "Swap-in/out rates unavailable")
	}
	adviceSection(w, env, cfg, first, s)
	topApplications(w, env, s)
	topProcesses(w, s)
	unavailableSection(w, s)
	return 0
}

func gib(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }
func mib(b uint64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }
func num(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *v)
}
func pct(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("total %.1f%%  ", *v)
}

const topProcessCount = 15

// topProcesses prints the processes with the largest PSS + swap.
func topProcesses(w io.Writer, s collector.Sample) {
	if len(s.Processes) == 0 {
		return
	}
	total := func(p collector.Process) uint64 {
		var t uint64
		if p.PSS != nil {
			t += *p.PSS
		}
		if p.Swap != nil {
			t += *p.Swap
		}
		return t
	}
	ps := append([]collector.Process(nil), s.Processes...)
	sort.SliceStable(ps, func(i, j int) bool {
		if a, b := total(ps[i]), total(ps[j]); a != b {
			return a > b
		}
		return ps[i].PID < ps[j].PID
	})
	if len(ps) > topProcessCount {
		ps = ps[:topProcessCount]
	}
	fmt.Fprintf(w, "\nTop processes by PSS + swap\n")
	fmt.Fprintf(w, "%7s %-10s %10s %10s %10s %6s  %s\n", "PID", "USER", "PSS", "SWAP", "RSS", "CPU%", "COMMAND")
	for _, p := range ps {
		cpu := "n/a"
		if p.CPU != nil {
			cpu = fmt.Sprintf("%.1f", *p.CPU)
		}
		fmt.Fprintf(w, "%7d %-10s %10s %10s %10s %6s  %s\n", p.PID, p.User, mibp(p.PSS), mibp(p.Swap), mibp(p.RSS), cpu, oneLine(p.Command))
	}
}

func mibp(b *uint64) string {
	if b == nil {
		return "n/a"
	}
	return mib(*b)
}

// oneLine keeps a command line with embedded newlines on one report row.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// unavailableSection lists the data this machine cannot provide, with the
// reason, (ADR 0006). Disabled Advice rules are listed by the Advice section.
func unavailableSection(w io.Writer, s collector.Sample) {
	a := s.Availability
	if len(a.Unavailable) == 0 {
		return
	}
	fmt.Fprintf(w, "\nUnavailable on this machine\n")
	for _, u := range a.Unavailable {
		fmt.Fprintf(w, "  %s: %s\n", u.Feature, u.Reason)
	}
}
