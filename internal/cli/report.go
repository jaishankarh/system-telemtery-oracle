package cli

import (
	"fmt"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// report samples twice, one second apart so rates exist, and prints a table.
func report(env Env, col collector.Collector) int {
	if _, err := col.Collect(); err != nil {
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
	for _, p := range s.Missing {
		fmt.Fprintf(w, "missing data source: %s\n", p)
	}
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
