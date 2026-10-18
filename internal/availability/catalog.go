package availability

// What is lost when each file the Collector reads is missing. Tickets that add
// a source describe it in their own file with Describe (see the package doc).
func init() {
	Describe("/proc/meminfo", Item{ID: "meminfo", Feature: "RAM figures",
		Reason:  "/proc/meminfo is not readable",
		Affects: []string{"RAM", "RAM vs swap graph", "Hardware Limit"}})
	Describe("/proc/loadavg", Item{ID: "loadavg", Feature: "Load averages",
		Reason: "/proc/loadavg is not readable", Affects: []string{"CPU load"}})
	Describe("/proc/stat", Item{ID: "cpu", Feature: "CPU utilisation",
		Reason: "/proc/stat is not readable", Affects: []string{"CPU", "CPU graph"}})
	Describe("/proc/vmstat", Item{ID: "swap_rates", Feature: "Swap-in and swap-out rates",
		Reason:  "/proc/vmstat has no pswpin and pswpout counters",
		Affects: []string{"Swap-in/out graph", "Hardware Limit", "Compressed Swap thrashing", "Swap wear"}})
	Describe("/proc/swaps", Item{ID: "swaps", Feature: "Swap devices",
		Reason:  "/proc/swaps is not readable",
		Affects: []string{"Disk Swap and Compressed Swap", "RAM vs swap graph", "Hardware Limit", "Compressed Swap thrashing", "Swappiness too aggressive", "Swap wear"}})
	Describe("/proc/pressure", Item{ID: "pressure", Feature: "Pressure stall information (PSI)",
		Reason:  "/proc/pressure/memory is missing: this kernel has no pressure stall information (PSI needs Linux 4.20 or newer built with CONFIG_PSI)",
		Affects: []string{"CPU, memory and I/O pressure graphs", "Hardware Limit"}})
	Describe("/proc/diskstats", Item{ID: "diskstats", Feature: "Disk throughput",
		Reason: "/proc/diskstats is not readable", Affects: []string{"Disk throughput graph"}})
	Describe("/proc/PID/io", Item{ID: "proc_io", Feature: "Per-process disk reads and writes",
		Reason:  "this kernel has no /proc/<pid>/io (built without task I/O accounting)",
		Affects: []string{"Read/s and Write/s columns"}})
	Describe("/proc/PID/smaps_rollup", Item{ID: "smaps_rollup", Feature: "Per-process PSS",
		Reason:  "this kernel has no /proc/<pid>/smaps_rollup (needs Linux 4.14 or newer); swap is read from VmSwap instead",
		Affects: []string{"PSS columns", "Working Set", "Top 10 Applications split", "Hardware Limit", "Browser process sprawl"}})
	Describe("/proc/PID/cgroup", Item{ID: "proc_cgroup", Feature: "Process cgroup paths",
		Reason:  "this kernel has no /proc/<pid>/cgroup",
		Affects: []string{"systemd unit names", "Container and Pod rows"}})

	// Found absent without a failed read: /proc/swaps lists no Disk Swap.
	Describe("disk_swap", Item{ID: "disk_swap", Feature: "Disk Swap",
		Reason:  "no swap partition or swap file is configured on this machine (Compressed Swap only, or no swap at all)",
		Affects: []string{"Disk Swap rows", "Hardware Limit", "Swap wear", "Swappiness too aggressive"}})
}
