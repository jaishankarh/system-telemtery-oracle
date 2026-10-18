package cli_test

import (
	"fmt"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
)

// Footprint budget (SPEC): < 30 MB RAM and < 0.5% average CPU. The real-machine
// benchmark is scripts/footprint.sh; this is its fast stand-in: a busy machine
// (1500 Applications' worth of processes) must not make the program leak memory
// across samples or spend gross CPU per sample.

const (
	syntheticProcesses = 1500
	warmTicks          = 12 // past the 10s PSS refresh
	measuredTicks      = 60
	// Generous so a loaded CI box cannot flake it: the budget is 10 ms per 2 s
	// sample on real /proc; the fake tree adds file-system and harness polling cost.
	maxCPUPerTick   = 400 * time.Millisecond
	maxHeapGrowth   = 8 << 20
	maxHeapInUseCap = 30 << 20
)

func cpuTime() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func heapAfterGC() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func TestManyProcessesStayWithinFootprintBudget(t *testing.T) {
	m := harness.New(t, tree)
	for i := 0; i < syntheticProcesses; i++ {
		pid := 1000 + i
		m.WriteFile(fmt.Sprintf("/proc/%d/stat", pid), fmt.Sprintf("%d (worker%d) S 1 %d %d 0 -1 4194304 100 0 0 0 4000 1000 0 0 20 0 8 0 1100 4595712 20000 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n", pid, i%50, pid, pid))
		m.WriteFile(fmt.Sprintf("/proc/%d/status", pid), fmt.Sprintf("Name:\tworker%d\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t 80000 kB\nVmSwap:\t 0 kB\nThreads:\t8\n", i%50))
		m.WriteFile(fmt.Sprintf("/proc/%d/cmdline", pid), fmt.Sprintf("/opt/app%d/bin/worker --id=%d", i%50, i))
		m.WriteFile(fmt.Sprintf("/proc/%d/io", pid), "read_bytes: 1000\nwrite_bytes: 1000\n")
		m.WriteFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid), "Rss: 80000 kB\nPss: 60000 kB\nSwap: 0 kB\n")
	}
	p := m.Start()
	for i := 0; i < warmTicks; i++ {
		p.Tick()
	}
	var l procList
	p.GetJSON("/api/processes", &l)
	if len(l.Processes) < syntheticProcesses {
		t.Fatalf("synthetic load not seen: %d processes", len(l.Processes))
	}

	heap0, cpu0 := heapAfterGC(), cpuTime()
	for i := 0; i < measuredTicks; i++ {
		p.Tick()
	}
	cpu1 := cpuTime()
	heap1 := heapAfterGC()

	if per := (cpu1 - cpu0) / measuredTicks; per > maxCPUPerTick {
		t.Errorf("CPU per sample = %v, want <= %v", per, maxCPUPerTick)
	}
	if heap1 > heap0 && heap1-heap0 > maxHeapGrowth {
		t.Errorf("live heap grew %d bytes over %d samples (leak?)", heap1-heap0, measuredTicks)
	}
	if heap1 > maxHeapInUseCap {
		t.Errorf("live heap %d bytes exceeds the 30 MB budget", heap1)
	}
	t.Logf("%d processes: cpu/sample %v, live heap %d -> %d bytes", syntheticProcesses, (cpu1-cpu0)/measuredTicks, heap0, heap1)
}
