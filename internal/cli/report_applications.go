package cli

import (
	"fmt"
	"io"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

const topApplicationCount = 15

// topApplications prints the Applications with the largest PSS + swap.
func topApplications(w io.Writer, env Env, s collector.Sample) {
	if len(s.Processes) == 0 {
		return
	}
	apps := newGrouper(env).Group(s.Processes) // already ordered by PSS + swap
	if len(apps) > topApplicationCount {
		apps = apps[:topApplicationCount]
	}
	fmt.Fprintf(w, "\nApplications by PSS + swap\n")
	fmt.Fprintf(w, "%-28s %-9s %10s %10s %10s %6s %5s  %s\n", "APPLICATION", "KIND", "PSS", "SWAP", "RSS", "CPU%", "PROCS", "PRIMARY PIDS")
	for _, a := range apps {
		cpu := "n/a"
		if a.CPU != nil {
			cpu = fmt.Sprintf("%.1f", *a.CPU)
		}
		fmt.Fprintf(w, "%-28s %-9s %10s %10s %10s %6s %5d  %v\n", clip(a.Name, 28), a.Kind, mibp(a.PSS), mibp(a.Swap), mibp(a.RSS), cpu, a.ProcessCount, a.PrimaryPIDs)
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
