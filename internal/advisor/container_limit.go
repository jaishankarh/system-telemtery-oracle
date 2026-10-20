package advisor

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/availability"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// Thresholds of the Uncapped container rule. "High" is a Working Set (PSS +
// swap) of at least a GiB, or at least 256 MiB sitting in swap.
const (
	uncappedWorkingSet = 1 << 30
	uncappedSwap       = 256 << 20
	uncappedWarningPct = 25.0 // of installed RAM
	uncappedWarningSwp = 1 << 30
	limitHeadroom      = 1.25
	peakWindow         = 24 * time.Hour
	peakCacheFor       = 30 * time.Second
)

// UncappedContainerRule suggests a memory limit of the 24-hour peak plus 25%
// for a Container or Pod with no memory limit and high memory or swap. The
// peak comes from History, so it includes the time before a restart.
type UncappedContainerRule struct{}

func (UncappedContainerRule) ID() string    { return "container-limit" }
func (UncappedContainerRule) Title() string { return "Uncapped container" }

func (UncappedContainerRule) Needs(in *Input) string {
	if why := availability.DisabledReason(in.Sample.Availability.Unavailable,
		"container_runtime", "proc_cgroup", "smaps_rollup"); why != "" {
		return why
	}
	if in.Peaks == nil {
		return "needs History: it is not being recorded on this machine (the state directory is not writable) or no History file exists yet, so there is no 24-hour peak to base a limit on"
	}
	return ""
}

func (UncappedContainerRule) Check(in *Input) []Finding {
	var out []Finding
	var peaks *history.Peaks
	for _, a := range in.Apps {
		if (a.Kind != grouping.KindContainer && a.Kind != grouping.KindPod) || a.Container == nil || a.Container.MemoryLimit != nil {
			continue
		}
		ws := a.PSSPlusSwap()
		if ws == nil {
			continue
		}
		var swap uint64
		if a.Swap != nil {
			swap = *a.Swap
		}
		if *ws < uncappedWorkingSet && swap < uncappedSwap {
			continue
		}
		if peaks == nil {
			p, err := in.Peaks()
			if err != nil {
				return nil // no History to read: say nothing rather than guess a limit
			}
			peaks = &p
		}
		peak, basis := peakOf(a, *ws, *peaks)
		limit := uint64(math.Ceil(float64(peak)*limitHeadroom/(1<<20))) << 20
		sev := Info
		ram := 0.0
		if in.Sample.Memory != nil {
			ram = float64(in.Sample.Memory.TotalBytes)
		}
		if swap >= uncappedWarningSwp || (ram > 0 && float64(*ws) >= ram*uncappedWarningPct/100) {
			sev = Warning
		}
		noun := "Container"
		if a.Kind == grouping.KindPod {
			noun = "Pod"
		}
		out = append(out, Finding{
			Subject:  a.Kind + ":" + strings.TrimPrefix(a.ID, a.Kind+":"),
			Severity: sev, Category: SettingsFix,
			Title: fmt.Sprintf("%s %s has no memory limit", noun, a.Name),
			Summary: fmt.Sprintf("%s %s has no memory limit and uses %s (swap %s). Its highest Working Set was %s; "+
				"a limit of that plus 25%% is %s. This is advice only: nothing was changed.",
				noun, a.Name, byteText(*ws), byteText(swap), byteText(peak), byteText(limit)),
			Applications: []AppRef{{ID: a.ID, Name: a.Name}},
			Facts: []Fact{
				fnum("working_set_bytes", float64(*ws), "bytes"),
				fnum("swap_bytes", float64(swap), "bytes"),
				fnum("peak_24h_bytes", float64(peak), "bytes"),
				fnum("suggested_limit_bytes", float64(limit), "bytes"),
				{Name: "peak_basis", Text: basis},
			},
			Change: limitChange(a, limit),
		})
	}
	return out
}

// peakOf is the highest Working Set of the Application: History's, or the
// current reading when that is higher. basis says what the number covers.
func peakOf(a grouping.Application, now uint64, p history.Peaks) (uint64, string) {
	pk, ok := p.Apps[history.AppKey(a.Kind, a.Name)]
	peak := now
	fromNow := !ok || now > pk.Bytes
	if ok && pk.Bytes > peak {
		peak = pk.Bytes
	}
	const layout = "2006-01-02 15:04:05 MST"
	if p.Samples == 0 {
		return peak, "no History samples; the peak is the current reading only"
	}
	span := p.To.Sub(p.From).Round(time.Minute)
	b := fmt.Sprintf("Peak is the highest value History held, from %s to %s (%s of samples, including time before any restart). "+
		"The Application was recorded in %d of %d samples.", p.From.UTC().Format(layout), p.To.UTC().Format(layout), span, pk.Seen, p.Samples)
	if span < peakWindow-10*time.Minute {
		b += fmt.Sprintf(" History covers only %s so far, not a full 24 hours.", span)
	}
	b += " History keeps only the top 10 Applications per sample"
	if p.Truncated > 0 {
		b += fmt.Sprintf(" (%d samples had more), so the real peak may be higher than shown.", p.Truncated)
	} else {
		b += ", which did not cut any sample in this window."
	}
	if fromNow {
		b += " The current reading is higher than anything in History and is used."
	}
	return peak, b
}

func mib(limit uint64) uint64 { return limit >> 20 }

func limitChange(a grouping.Application, limit uint64) *Change {
	ci := a.Container
	m := mib(limit)
	name := a.Name
	switch ci.StartedBy {
	case "compose":
		svc, proj := "SERVICE_NAME", ""
		for _, c := range ci.Containers {
			if c.ComposeService != "" {
				svc, proj = c.ComposeService, c.ComposeProject
				break
			}
		}
		target := "the Compose file of service " + svc
		if proj != "" {
			target += " (project " + proj + ")"
		}
		return &Change{Kind: "compose", Target: target,
			Content: fmt.Sprintf("services:\n  %s:\n    deploy:\n      resources:\n        limits:\n          memory: %dM\n", svc, m),
			Note: "Merge this into the service in the Compose file, then recreate it with `docker compose up -d`. " +
				"deploy.resources.limits.memory is applied by Docker Compose v2 (`docker compose`); the old standalone " +
				"docker-compose v1 ignores deploy limits outside Swarm unless run with --compatibility, so there use the " +
				fmt.Sprintf("service-level alternative `mem_limit: %dm`. ", m) +
				"A container that exceeds its limit is killed by the kernel OOM killer. The limit is the 24-hour Working Set peak plus 25%."}
	case "kubernetes":
		return podChange(a, m)
	case "nerdctl":
		return &Change{Kind: "nerdctl", Target: "the nerdctl run command (or compose file) that starts " + name,
			Content: fmt.Sprintf("nerdctl run --memory %dm ...\n", m),
			Note: "Add the flag to the nerdctl run command and recreate the container; a limit cannot be added to a running container by editing the command. " +
				"If it was started with nerdctl compose, use deploy.resources.limits.memory: " + fmt.Sprintf("%dM", m) + " on the service instead. " +
				"A container that exceeds its limit is killed by the kernel OOM killer."}
	case "containerd":
		return &Change{Kind: "containerd", Target: "the command that starts " + name + " in containerd",
			Content: fmt.Sprintf("ctr run --memory-limit %d ...\n", limit),
			Note:    "ctr takes the limit in bytes (" + fmt.Sprintf("%d MiB", m) + "). Recreate the container with the flag; a limit cannot be added to a running container this way. A container that exceeds its limit is killed by the kernel OOM killer."}
	}
	return &Change{Kind: "docker-run", Target: "the docker run command that starts " + name,
		Content: fmt.Sprintf("docker run --memory=%dm ...\n", m),
		Note: "Add the flag to the docker run command and recreate the container. Without --memory-swap, Docker lets the container use as much swap again as the limit; " +
			fmt.Sprintf("add --memory-swap=%dm to forbid swap. ", m) +
			fmt.Sprintf("To change a running container, `docker update --memory=%dm --memory-swap=%dm %s` does it in place. ", m, m, name) +
			"A container that exceeds its limit is killed by the kernel OOM killer."}
}

// podChange gives resources.limits.memory for the Pod's containers without a
// limit. Kubernetes limits are per container, but History records the Pod as
// a whole, so the Pod limit is split by each container's current Working Set.
func podChange(a grouping.Application, m uint64) *Change {
	var unl []string
	use := map[string]uint64{}
	var total uint64
	for _, c := range a.Container.Containers {
		if c.Sandbox || c.MemoryLimit != nil {
			continue
		}
		unl = append(unl, c.Name)
	}
	sort.Strings(unl)
	for _, p := range a.Processes {
		var v uint64
		if p.PSS != nil {
			v += *p.PSS
		}
		if p.Swap != nil {
			v += *p.Swap
		}
		use[p.Subgroup] += v
	}
	for _, n := range unl {
		total += use[n]
	}
	var b strings.Builder
	b.WriteString("spec:\n  containers:\n")
	var left = m
	for i, n := range unl {
		share := m / uint64(len(unl))
		if total > 0 {
			share = uint64(float64(m) * float64(use[n]) / float64(total))
		}
		if i == len(unl)-1 && total == 0 {
			share = left
		}
		if share < 1 {
			share = 1
		}
		left -= min(left, share)
		fmt.Fprintf(&b, "  - name: %s\n    resources:\n      limits:\n        memory: %dMi\n", n, share)
	}
	ns, pod := "", a.Name
	if k := strings.TrimPrefix(a.ID, a.Kind+":"); strings.Contains(k, "/") {
		ns = k[:strings.Index(k, "/")]
	}
	note := "Merge into the container list of the workload that owns the Pod (Deployment, StatefulSet, ...), not the Pod itself, then roll it out; " +
		"resources.limits.memory cannot be changed on a running Pod. A container over its limit is OOM-killed and restarted. " +
		"Also consider resources.requests.memory. "
	if len(unl) > 1 {
		note += fmt.Sprintf("History records the Pod as a whole, so the Pod limit of %d Mi is split between its containers by their current Working Set.", m)
	}
	return &Change{Kind: "k8s", Target: "the workload of Pod " + ns + "/" + pod, Content: b.String(), Note: note}
}
