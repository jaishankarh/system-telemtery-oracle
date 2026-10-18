# sys-telemetry-app — Specification (revised)

Revised 2026-10-18 after the design grilling. Terms in **bold capitals** are defined in `GLOSSARY.md`; the reasoning behind non-obvious choices is in `docs/adr/`. The original brief is kept at `docs/original-spec.md`.

## 1. Problem

Developers running Docker/Kubernetes stacks, heavy editors and many browser tabs on one machine hit memory exhaustion and swap Thrashing. Existing monitors (htop, btop, GNOME System Monitor):

1. Fragment one program into dozens of processes, so no one can see what an Application really costs.
2. Show RSS but hide PSS and per-process swap.
3. Give numbers without saying whether the fix is a setting or more RAM.
4. Are heavy themselves, or lose everything when the machine freezes, which is exactly when the data matters.

## 2. Goal

A single static binary that, on any Linux machine, records system and per-process telemetry continuously and cheaply, groups processes into Applications, keeps a 24-hour History that survives crashes, serves a live and replayable dashboard on demand, and produces Advice classified as a **Settings Fix** or a **Hardware Limit**.

## 3. Scope

| In phase 1 | Deferred to phase 2 | Out of scope |
|---|---|---|
| Any Linux, x86_64 and arm64, any kernel (degrades on old ones) | macOS (own collector, reduced metrics, Docker-in-VM handling) | Windows |
| Docker, containerd, k3s, kind | Podman, LXC/LXD | Changing the system in any way |
| NVIDIA, AMD and Intel GPUs | Alerts outside the dashboard | Remote access / auth |

## 4. Runtime model

### 4.1 Binary
- Go, `CGO_ENABLED=0`, stripped static binary; `make build` builds both amd64 and arm64 (ADR 0002).
- Dashboard assets (vanilla JS, CSS, uPlot) embedded with `//go:embed`; no CDN, works offline.
- No shelling out to `ps`, `top`, `nvidia-smi`, `docker` etc. Data comes from `/proc`, `/sys`, cgroup files, container-runtime sockets and runtime-loaded GPU libraries.
- NVML is loaded at runtime if present; AMD and Intel GPU data come from sysfs. No GPU → GPU section hidden.
- Released on GitHub Releases for amd64 and arm64, with a one-line install script.

### 4.2 Privileges and safety (ADR 0003)
- Runs as root so it can read every process's memory maps.
- **Advise only:** never writes sysctl, never changes container limits, never kills or renices. Every Settings Fix is shown as the exact command or config to copy.
- Exception, its own process only: sets its own `oom_score_adj` to -1000 so the kernel kills something else first, and runs under a hard memory cap of its own (systemd `MemoryMax` when installed as a service, plus an internal self-check that drops caches and logs if it nears the cap). This does not prevent a freeze or reboot; it only keeps the recorder alive longer.
- Dashboard binds to `127.0.0.1` only, no auth. Remote viewing is via SSH tunnel.

### 4.3 Lifecycle and CLI
```
sys-telemetry-app [run] [--port 9191] [--interval 2s] [--config PATH]   # foreground collector + dashboard
sys-telemetry-app report      # one-shot table of Applications and current Advice in the terminal
sys-telemetry-app replay [--around TIME | --last-boot] [--minutes N]   # read History, e.g. lead-up to the last crash
sys-telemetry-app install     # write and enable a systemd unit
sys-telemetry-app uninstall   # disable and remove it
```
`report` and `replay` read from the running instance if there is one, otherwise directly from the History file.

### 4.4 Sampling and budget
- System-wide metrics every `--interval` (default 2s, minimum 1s).
- Per-process memory detail (PSS, swap from `smaps_rollup`) every 10s, because reading it for hundreds of processes is the expensive part. CPU and I/O per process still follow `--interval`.
- In RAM: the last 5 minutes at full sampling resolution for the live view.
- Budget: **< 30MB RAM** total including the History write buffer, **< 0.5% average CPU**.
- When no dashboard client is connected, no serialisation or streaming happens; the HTTP server just waits.

### 4.5 History (ADR 0004)
- 24 hours of 10-second samples in one preallocated, fixed-size ring file, overwritten in place: `/var/lib/sys-telemetry-app/history.ring`, mode 0600, root-owned. Never grows.
- Each sample holds system metrics plus per-Application totals for the top Applications by Working Set, and the processes inside them with **full command lines** (accepted risk: may include secrets; root-only file).
- Advice start, end and Severity changes are recorded as events in History.
- Flushed every 30 seconds, and immediately when memory pressure or Thrashing crosses its warning threshold, so at most ~30 seconds is lost in a hard crash. On start, a previous unclean shutdown is detected and surfaced ("last session ended without clean shutdown at …").

### 4.6 Graceful degradation (ADR 0006)
Every data source is optional: cgroup v1 or v2, PSI present or not, GPU present or not, container runtime present or not, systemd present or not. Missing data is hidden from the UI, and any Advice depending on it is disabled with a one-line reason shown in the Advice panel.

## 5. Metrics

### 5.1 System
- **CPU:** total and per-core %, load averages, CPU PSI.
- **RAM:** total, available, used, cached, buffers, dirty.
- **Swap:** used/total split into **Disk Swap** and **Compressed Swap** (zram/zswap), swap-in and swap-out rates from `/proc/vmstat`.
- **Memory PSI:** some/full.
- **Disk:** read/write MB/s per device, queue depth, I/O PSI.
- **GPU:** utilisation %, temperature, VRAM used/total, per vendor.

### 5.2 Per Process
PID, user, command line, RSS, PSS, swap, CPU %, disk read/write rate, threads, open file descriptors, cgroup path, working directory.

### 5.3 Per Application
Sums of the above, plus primary PIDs and process count.

## 6. Grouping processes into Applications (ADR 0005)

Each Process belongs to exactly one Application. Layers are tried in order; first match wins:

1. **Container / Pod** — from the cgroup path. Names come from the Docker API socket and the containerd socket (including k3s and kind). Kubernetes containers are grouped per **Pod**; the pod's containers appear in the drilldown.
2. **App Profiles** (built in):
   - Cursor, VS Code, Windsurf — Electron shell, extension hosts, language servers (tsserver, pyright, gopls, …), file watchers and ripgrep.
   - JetBrains IDEs — IDE JVM, its helper processes and Gradle/Maven daemons it starts.
   - Chrome, Chromium, Brave, Edge, Firefox — one row per browser with all tabs and helpers.
   - Android emulator and the Gradle daemon (when not owned by an IDE row).
3. **Dev & Build per Project** — dev servers, compilers and build tools (node/npm/pnpm/yarn dev servers, cargo, go build, gradle, make, tsc, vite, webpack, …) grouped by **Project**, wherever they were launched from, including an editor's built-in terminal. Project = nearest ancestor of the process's working directory that is a VCS root, otherwise the working directory itself.
4. **systemd unit, snap or flatpak** — from the cgroup path (`app-*.scope`, `*.service`, `snap.*`, `app-flatpak-*`), giving names to programs the tool has never heard of.
5. **Top ancestor** — the topmost parent below the user session or init, named by its executable.
6. **Other** — only what none of the above can name.

The config file can add rules (match by executable, command-line regex, cgroup path or working directory) and rename groups; user rules run before layer 2.

## 7. Advisor

### 7.1 Definitions
- **Working Set** = Σ over all Applications of (PSS + swap).
- **Memory Deficit** = Working Set − physical RAM.
- **Thrashing** = swap-in rate and swap-out rate both above threshold at the same time. Tracked separately for Disk Swap and Compressed Swap.
- **Sustained** = the condition held for the whole window, default 5 minutes, configurable.

### 7.2 Advice
Each piece of Advice has: category (Settings Fix / Hardware Limit), Severity (info / warning / critical), the Applications it concerns, the evidence (numbers and the time window), and for a Settings Fix the exact change to copy.

**Settings Fixes** (initial rule set):
- **Editor file-watcher load** — Sustained high CPU or memory in an editor's watcher/ripgrep/language-server processes → suggested exclude settings (`files.watcherExclude`, `search.exclude` for `node_modules`, `dist`, `.cache`, build output), naming the Project if known.
- **Uncapped container** — a Container or Pod with no memory limit and high memory or swap → suggested limit = **24-hour peak + 25%**, as a Compose `deploy.resources.limits.memory` snippet, `docker run --memory` flag, or Kubernetes `resources.limits.memory`, whichever matches how it was started.
- **Swappiness too aggressive** — swapping to Disk Swap while page cache is large → suggested `/etc/sysctl.d/` file (`vm.swappiness`, `vm.vfs_cache_pressure`) with values and the reason.
- **Compressed Swap thrashing** — Thrashing on zram/zswap only → CPU cost explanation and zram size/algorithm suggestion; does not count as disk wear.
- **Browser process sprawl** — high total PSS across many renderer processes → tab-unloading / process-limit settings for that browser.
- **Swap wear** — sustained Disk Swap write volume → daily write estimate and suggestion to prefer Compressed Swap.

**Hardware Limit** fires only when all hold, Sustained:
- memory PSI some/full above threshold (default 15%),
- Working Set > 90% of physical RAM,
- Thrashing on Disk Swap.

Verdict text states the Working Set, the installed RAM, the Memory Deficit, and the next standard sizes that cover it (from 8, 16, 24, 32, 48, 64, 96, 128 GB), e.g. *"Your applications need ~46 GB at once; you have 32 GB and are thrashing to disk. Settings alone won't fix this without stopping services. 48 GB or 64 GB would cover it."* The board's maximum supported RAM is **not** checked.

### 7.3 Advice states
- **Active** — shown.
- **Hidden** — marked manually; stays hidden until its Severity rises above the level at which it was hidden. Can be unhidden.
- **Snoozed** — hidden for a chosen period (configurable presets, e.g. 1 day / 7 days), then returns if still active.
Hidden/snoozed state persists in `/var/lib/sys-telemetry-app/state.json`.

### 7.4 Configuration
Optional file `/etc/sys-telemetry-app/config.toml` (or `--config`): thresholds, Sustained window, snooze presets, sampling interval, port, and grouping rules. Every value has a working default; the file never needs to exist.

## 8. Dashboard

Served at `http://127.0.0.1:<port>`. Single page, responsive down to laptop half-screen width.

- **Advice panel** (always visible at top): active Advice by Severity, with hide/snooze controls; disabled rules listed with their reason.
- **View 1 — Applications (default):** Application, RSS, PSS, swap, CPU %, disk read/write, process count, primary PIDs. Sortable. Expanding a row shows its Processes (or a Pod's containers) with full command lines.
- **View 2 — Processes:** flat sortable table, filter by PID, name or regex.
- **View 3 — Graphs:** CPU, RAM vs Disk/Compressed Swap, memory PSI, disk I/O, GPU; memory composition across the top 10 Applications (RAM vs swap); swap-in/out rate with Advice start/end markers.
- **Time slider:** live by default; drag back through History to Replay any moment in the last 24 hours. All views show the selected time.
- Live updates via SSE only while at least one client is connected.

## 9. Deliverables

1. Go source tree with the platform collector behind an interface (macOS slot for phase 2).
2. `Makefile` with `make build` (static, stripped, amd64 + arm64) and `make test`.
3. Embedded dashboard assets, fully offline.
4. systemd unit generated by `install`.
5. GitHub Actions release workflow publishing both binaries and the install script.
6. Tests: parsers against captured `/proc` fixtures (including old-kernel and cgroup v1 variants), grouping layers against recorded process trees, advisor rules against recorded History scenarios.

## 10. Phase 2

macOS collector (memory footprint instead of PSS, system pressure level instead of PSI, Docker Desktop containers via the Docker API since they live in one VM); Podman and LXC; optional alerts (system journal, desktop notifications).
