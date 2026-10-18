# Project Specification: Lightweight Local System Telemetry & Bottleneck Analyzer (`sys-telemetry-app`)

## 1. Background & Problem Statement
The developer runs high-density local workloads on an **Ubuntu 26.04 LTS (x86_64, AMD Ryzen 5, 32GB DDR4/DDR5 RAM, NVIDIA RTX 3060 Laptop 4GB VRAM)** environment. Workloads simultaneously include:
- Multiple multi-container Docker stacks (relational/NoSQL databases, queue runners, API runtimes).
- Heavy developer IDEs (e.g., Cursor / VS Code) with LSP language servers and file watchers (`ripgrep`).
- Multiple browser windows with heavy concurrent tab counts.
- Heavy swap usage reaching **up to 48GB on disk swap**, causing significant disk I/O latency, swap thrashing, and storage wear.

### Current Friction with Standard Tools (`htop`, `btop`, GNOME System Monitor):
1. **Process Fragmentation:** Standard process monitors split workloads into dozens of disconnected child processes, threads, and container worker forks (e.g., Cursor forks, Docker shims, browser content sandboxes). It is impossible to see the aggregate footprint of an application at the root boundary.
2. **Missing Swap & Memory Detail:** Most tools report basic Resident Set Size (RSS), hiding Proportional Set Size (PSS) and actual per-process swap allocation (`VmSwap`).
3. **No Advisory Layer:** Standard utilities provide raw numbers without contextual diagnosis—they do not distinguish between software misconfigurations (e.g., unbounded watchers, uncapped dev servers) and genuine physical hardware exhaustion.
4. **Observer Effect (Heaviness):** Running continuous monitoring stacks (like Prometheus + Grafana or heavy Electron dashboards) consumes significant memory and CPU, worsening the very bottleneck being investigated.

---

## 2. Objective
Build an ultra-lightweight, zero-runtime-dependency, portable single-binary CLI & live dashboard tool. It must sample system and per-process telemetry in the background, aggregate child processes into high-level application bundles, expose an interactive live browser UI only on demand, and produce actionable recommendations differentiating software settings from hardware limits.

---

## 3. Architecture & Efficiency Constraints (Strict)

### 3.1 Binary & Deployment Model
- **Language Choice:** **Go (Golang)** or **Rust**.
  - Single statically compiled binary (`CGO_ENABLED=0` for Go, or `musl` target for Rust).
  - No external runtime dependencies (no Python runtime, no Node.js, no Docker required to run this monitoring tool).
- **Embedded Frontend:** The HTML, CSS, and JS bundle must be embedded directly inside the compiled binary (e.g., using `//go:embed` in Go or `rust-embed` in Rust).
- **Zero-Footprint Baseline:**
  - Background collector footprint: **< 15MB RAM** and **< 0.5% average CPU usage**.
  - Must read directly from Linux kernel virtual filesystems (`/proc`, `/sys/class`, `/sys/fs/cgroup`) using low-allocation stream parsing—do not shell out to external commands like `ps`, `top`, or `nvidia-smi` repeatedly.

### 3.2 Telemetry & On-Demand UI Model
- **Decoupled Telemetry vs. Visualization:**
  - Background telemetry loop collects and rolls metrics into an in-memory ring buffer (e.g., 10-second downsampling over 1 hour, 1-second rolling window for the last 5 minutes).
  - Web server serves the dashboard over localhost (default `http://127.0.0.1:9191`).
  - **Zero Streaming Overhead When Inactive:** WebSockets or Server-Sent Events (SSE) must pause broadcasting when no active browser tabs are connected. If zero clients are attached, the HTTP server remains dormant, consuming no network or serialization CPU cycles.

---

## 4. Key Metrics to Capture

### 4.1 System-Wide Metrics
1. **CPU:** Total usage %, per-core breakdown, load averages (1m, 5m, 15m), and **CPU Pressure Stall Information (PSI)** (`/proc/pressure/cpu`).
2. **System RAM:** Total, Available, Used, Cached, Buffers, Dirty memory.
3. **Swap & Thrashing:** Total swap, Used swap, Free swap, Swap In/Out rates (pages/sec via `/proc/vmstat`).
4. **Memory Pressure (PSI):** Some/Full memory pressure stall percentages (`/proc/pressure/memory`) to detect kernel-level memory wait bottlenecks.
5. **Disk I/O & Pressure:** Read/Write MB/s, average queue depth, and I/O Pressure (`/proc/pressure/io`).
6. **NVIDIA GPU/VRAM:** Temperature, GPU Core % utilization, VRAM Used / Free (via direct NVML C-bindings or dynamic fallback to NVML shared library).

### 4.2 Application & Process Metrics
- **PID Aggregation Hierarchy:** Group child processes under unified application parents using process group IDs, `/proc/[pid]/cgroup` hierarchies (identifying Docker container names/IDs), and command-line binary detection.
- **Accurate Memory Accounting:**
  - **PSS (Proportional Set Size):** Avoid double-counting shared libraries across multiple forks.
  - **Swap Paged (`VmSwap`):** Exact amount of virtual memory evicted to disk for each process from `/proc/[pid]/smaps_rollup`.
  - **Thread Count & File Descriptors:** Open file handles and thread pools.

---

## 5. UI Views & Visualizations

The live dashboard must provide a single-page responsive interface (using vanilla JS + lightweight Canvas-based charting like uPlot or Chart.js) with the following views:

### View 1: Consolidated Application View (Default)
- Groups all running processes into macro-applications:
  - **Docker Containers:** Individual containers identified by container name/ID (e.g., `postgres:16`, `redis`, `api-server`), rolling up all internal processes.
  - **Cursor / VS Code:** Core Electron shell + language servers (e.g., `tsserver`, `pyright`, `gopls`) + background indexing/file watchers (`ripgrep`).
  - **Browsers (Firefox / Chrome):** Main UI process + GPU process + individual tabs/sandboxes consolidated into a single application row.
  - **Build Systems / Terminal Tooling:** Aggregated compilation jobs (`cargo`, `go build`, `npm run build`).
  - **System Services:** OS daemons, display server (`wayland`/`xorg`), audio/network daemons.
- **Columns per Row:** Application Name, Total RSS, Total PSS, Total Swap on Disk, CPU %, Disk I/O Read/Write rate, Primary PIDs.
- **Expandable Drilldown:** Clicking an application expands the list to show raw child PIDs and exact sub-commands.

### View 2: Raw Process Inspector
- Flat, sortable table for all individual processes (PID, User, PSS, RSS, Swap, CPU %, Command string).
- Instant search filter by PID, process name, or regex.

### View 3: Visual Real-Time Graph View
- **System Resource Strip:** Real-time mini-charts for CPU, RAM vs. Swap, Memory PSI, Disk I/O, and GPU VRAM.
- **Memory Composition Treemap / Stacked Area:** Visual distribution of physical RAM vs. disk swap allocated across the top 10 applications.
- **Swap Eviction Rate Chart:** Visualizing spikes in disk swap activity correlating with container restarts or IDE operations.

---

## 6. Optimization & Diagnostic Engine (Rules-Based Advisor)

The application must include an automated advisory engine that classifies bottlenecks into two actionable categories: **"Fix with Settings/Configuration"** vs. **"Requires Physical RAM Upgrade"**.

### Category A: Settings & Configuration Tweaks
The engine should flag specific, actionable adjustments when matching telemetry rules:
- **Unbounded IDE File Watchers:** High CPU/RSS in background `rg` or language servers $\rightarrow$ Suggest adding ignore paths (`node_modules`, `dist`, `.cache`) in editor settings.
- **Docker Container Memory Over-Allocation:** High idle RSS or runaway swap usage in Docker $\rightarrow$ Flag containers without resource constraints and generate suggested `deploy.resources.limits` or `--memory` caps.
- **Aggressive Linux Kernel Swappiness:** System swaps aggressively while cache/buffers are large $\rightarrow$ Recommend tuning `/etc/sysctl.d/` (`vm.swappiness = 10`, `vm.vfs_cache_pressure = 50`).
- **Browser Multi-Process Sprawl:** High PSS across numerous browser renderers $\rightarrow$ Recommend lowering content process limits or activating strict inactive tab unloading flags.

### Category B: Physical Hardware Bottleneck Indicators
The engine must flag a hardware bottleneck when the following conditions are met simultaneously:
- **Sustained Memory PSI (Some/Full > 15-20%)** alongside active container workloads.
- **Cumulative Active Working Set (PSS minus caches)** across essential applications exceeds 90% of physical capacity (30GB+).
- **High Swap I/O Thrashing:** Sustained page-in/page-out rates causing disk I/O wait during standard compilation or multi-service runs.
- **Diagnostic Verdict:** Explicitly calculate the memory deficit: *"Your active container and IDE working set requires ~48GB of concurrent memory. You are experiencing swap thrashing on 32GB RAM. This cannot be resolved by configuration alone without stopping services; a 64GB RAM upgrade is recommended."*

---

## 7. Deliverable Requirements

1. **Complete Source Code:** Fully functional Go or Rust project structure.
2. **Single Build Script / Makefile:** A one-command build target (`make build`) that compiles a stripped, static binary.
3. **Embedded Web Assets:** Self-contained HTML/CSS/JS with zero external CDN dependencies (works entirely offline).
4. **Execution Simplicity:**
   ```bash
   # Run directly with standard user permissions (escalating only if reading restricted /proc entries)
   ./sys-telemetry-app --port 9191 --interval 2s
