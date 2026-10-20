package grouping

import "github.com/jaishankarh/system-telemtery-oracle/internal/collector"

// KernelThreads groups kthreadd and its children into one Application.
type KernelThreads struct{}

func (KernelThreads) Name() string { return "kernel threads" }

func (KernelThreads) Match(p *collector.Process, _ *Context) (Match, bool) {
	if p.PID == 2 || p.PPID == 2 {
		return Match{Kind: KindKernel, Key: "kernel", Name: "Kernel threads"}, true
	}
	return Match{}, false
}
