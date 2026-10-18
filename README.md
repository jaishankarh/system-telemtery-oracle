# sys-telemetry-app

A lightweight Linux system monitor that rolls hundreds of processes up into the **Applications** people actually think about (a Docker container, Cursor, Chrome, a Project's dev server), keeps a 24-hour **History** that survives crashes, and tells you whether memory trouble is fixable by **settings** or needs **more RAM**.

It is a single static binary. It records continuously and cheaply, serves a live and replayable dashboard on `127.0.0.1`, and never changes your system: it only advises.

- **One row per Application**, with real PSS and per-process swap, not just RSS.
- **Advice, not just numbers:** a *Settings Fix* (with the exact command or config to copy) or a *Hardware Limit* ("48 or 64 GB would cover it").
- **Crash-surviving History:** a fixed-size ring file records the lead-up to a freeze. Replay it afterwards.
- **Small:** budget under 30 MB RAM and 0.5% CPU.
- **Runs anywhere on Linux** (x86_64 and arm64, cgroup v1 or v2, with or without PSI, GPU, containers or systemd) and hides what it cannot measure.

> Status: phase 1 (Linux). macOS, Podman and LXC are phase 2. See [Roadmap](#roadmap).

Terms in *italics* and **bold** (Application, Working Set, Thrashing, Sustained, ...) are defined in [GLOSSARY.md](GLOSSARY.md). The full design is in [SPEC.md](SPEC.md).

---

## Contents

1. [Install](#install)
2. [Quick start](#quick-start)
3. [Commands](#commands)
4. [The dashboard](#the-dashboard)
5. [How processes become Applications](#how-processes-become-applications)
6. [Advice](#advice)
7. [History and Replay](#history-and-replay)
8. [Configuration](#configuration)
9. [HTTP API](#http-api)
10. [Running as a service](#running-as-a-service)
11. [GPU support](#gpu-support)
12. [Safety and privacy](#safety-and-privacy)
13. [Graceful degradation](#graceful-degradation)
14. [Performance budget](#performance-budget)
15. [Building and testing](#building-and-testing)
16. [Project layout](#project-layout)
17. [Releases](#releases)
18. [Verifying on a real machine](#verifying-on-a-real-machine)
19. [Design documents](#design-documents)
20. [Roadmap](#roadmap)

---

## Install

### From GitHub Releases (recommended)

```sh
curl -fsSL https://github.com/jaishankarh/system-telemtery-oracle/releases/latest/download/install.sh | sudo sh
```

The script detects x86_64 or aarch64 and installs the static binary to `/usr/local/bin/sys-telemetry-app`.

| Environment variable | Meaning |
|---|---|
| `INSTALL_DIR` | Target directory (default `/usr/local/bin`) |
| `SYS_TELEMETRY_VERSION` | Release tag, e.g. `v0.1.0` (default: latest) |
| `SYS_TELEMETRY_VARIANT` | `nvml` installs the build with NVIDIA GPU support (not static, needs glibc) |

### From source

Requires Go and `make`.

```sh
git clone https://github.com/jaishankarh/system-telemtery-oracle
cd system-telemtery-oracle
make build            # dist/sys-telemetry-app-linux-{amd64,arm64}
sudo install -m 0755 dist/sys-telemetry-app-linux-amd64 /usr/local/bin/sys-telemetry-app
```

`make build` verifies the binaries are fully static (no ELF interpreter, no shared libraries) and fails if they are not.

## Quick start

```sh
sudo sys-telemetry-app run
```

Open <http://127.0.0.1:9191>. Root is needed to read every process's memory maps (`smaps_rollup`); without it the tool still runs but with partial data.

Prefer a terminal? In another shell:

```sh
sudo sys-telemetry-app report
```

To keep it running across reboots, see [Running as a service](#running-as-a-service).

## Commands

```
sys-telemetry-app [run] [--port 9191] [--interval 2s] [--config PATH]
sys-telemetry-app report
sys-telemetry-app replay [--around TIME | --last-boot] [--minutes N] [--processes]
sys-telemetry-app install
sys-telemetry-app uninstall
```

| Command | What it does |
|---|---|
| `run` (default) | Foreground collector plus dashboard. Sampling continues whether or not a browser is connected. |
| `report` | One-shot table of Applications and current Advice in the terminal. |
| `replay` | Reads History: by default the newest 10 minutes. `--around 14:30` centres a window on a time, `--last-boot` shows the lead-up to the last session that ended, `--minutes N` sets the window, `--processes` lists Processes with full command lines for every sample, not only the last. Asks the running instance if there is one (its address is in `<state dir>/instance.addr`), otherwise reads the ring file directly. |
| `install` / `uninstall` | Write and enable, or disable and remove, a systemd unit. |

Flags for `run`:

| Flag | Default | Notes |
|---|---|---|
| `--port` | `9191` | The dashboard always binds to `127.0.0.1`. |
| `--interval` | `2s` | System sampling interval, minimum `1s`. |
| `--config` | `/etc/sys-telemetry-app/config.toml` | A missing file is fine. |

## The dashboard

Served at `http://127.0.0.1:<port>`; single page, usable at laptop half-screen width, fully offline (all assets are embedded).

- **Advice panel** (always at the top): active Advice by Severity with Hide and Snooze controls; disabled rules are listed with their reason.
- **Applications** (default view): Application, RSS, PSS, swap, CPU %, disk read/write, process count, primary PIDs. Sortable. Expand a row to see its Processes (or a Pod's containers) with full command lines.
- **Processes:** flat sortable table; filter by PID, name or regex.
- **Graphs:** CPU, RAM versus Disk Swap and Compressed Swap, memory pressure (PSI), disk I/O, GPU; memory composition across the top 10 Applications; swap-in/out rate with markers where Advice started and ended.
- **Time slider:** live by default. Drag back through the last 24 hours to Replay any moment; every view shows the selected time. Hide and Snooze only work in Live mode.

Live updates use Server-Sent Events and only happen while at least one client is connected; with no dashboard open, nothing is serialised or streamed.

## How processes become Applications

Every Process belongs to exactly one Application. Layers are tried in order and the first match wins:

1. **Container / Pod:** from the cgroup path. Names come from the Docker API socket and the containerd socket (including k3s and kind). Kubernetes containers roll up per Pod. Docker Compose labels are read for context.
2. **User rules:** `[[group]]` tables in the config file (they run before built-in profiles).
3. **Dev & Build per Project:** dev servers, compilers and build tools (node/npm/pnpm/yarn dev servers, cargo, go build, gradle, make, tsc, vite, webpack, ...) grouped by **Project**, wherever they were launched from, including an editor's built-in terminal. The Project is the nearest ancestor of the process's working directory that is a VCS root, otherwise the working directory itself.
4. **App Profiles** (built in): Cursor / VS Code / Windsurf (Electron shell, extension hosts, language servers, file watchers, ripgrep); JetBrains IDEs and the Gradle/Maven daemons they start; Chrome, Chromium, Brave, Edge and Firefox (one row per browser with all tabs and helpers); Android emulator and the Gradle daemon.
5. **Kernel threads**, then **systemd unit, snap or flatpak** from the cgroup path (`*.service`, `app-*.scope`, `snap.*`, `app-flatpak-*`), which names programs the tool has never heard of.
6. **Top ancestor:** the topmost parent below the user session or init, named by its executable.
7. **Other:** only what nothing above could name.

Discovery is dynamic: unknown programs are named from their cgroup or ancestry rather than from a fixed list (ADR 0005).

## Advice

Each piece of Advice has a category (Settings Fix or Hardware Limit), a **Severity** (info, warning, critical), the Applications it concerns, the evidence (numbers and time window), and for a Settings Fix the exact change to copy. The tool never applies a change itself.

A condition must be **Sustained** (held for the whole advisor window, default 5 minutes) before it produces Advice, so short spikes never fire.

### Settings Fixes

| Rule ID | What it notices | What it suggests |
|---|---|---|
| `editor-file-watcher` | Sustained high CPU or memory in an editor's watcher, ripgrep or language-server processes | `files.watcherExclude` / `search.exclude` for `node_modules`, `dist`, `.cache`, build output; names the Project when known |
| `container-limit` | A Container or Pod with no memory limit and a large Working Set (>= 1 GiB) or swap (>= 256 MiB) | A memory limit of the 24-hour peak plus 25%, as a Compose `deploy.resources.limits.memory` snippet, `docker run --memory`, or Kubernetes `resources.limits.memory` |
| `swappiness-too-aggressive` | Swapping to Disk Swap while the page cache is large | A `/etc/sysctl.d/` file with `vm.swappiness` and the reason |
| `compressed-swap-thrashing` | Thrashing on zram/zswap only | The CPU cost explained, and a zram size or algorithm suggestion (no disk wear) |
| `browser-process-sprawl` | High total PSS across many renderer processes | Tab-unloading or process-limit settings for that browser |
| `swap-wear` | Sustained Disk Swap write volume | A daily write estimate and a suggestion to prefer Compressed Swap |

### Hardware Limit

`hardware-limit` fires only when all of these hold, Sustained:

- memory PSI (`some` or `full`) is above the threshold (default 15%),
- the **Working Set** (sum over Applications of PSS plus swap) is above 90% of physical RAM,
- there is **Thrashing** on Disk Swap (swap-in and swap-out both above the threshold at once).

The verdict states the Working Set, the installed RAM, the **Memory Deficit**, and the next standard sizes that cover it (8, 16, 24, 32, 48, 64, 96, 128 GB). The motherboard's maximum RAM is not checked.

### Hide and Snooze

- **Hidden:** stays out of view until its Severity rises above the level at which it was hidden. Can be unhidden.
- **Snoozed:** hidden for a chosen period (presets `1d` and `7d` by default), then returns if still active.

State is saved in `<state dir>/state.json`.

## History and Replay

History is a **fixed-size ring file** (`/var/lib/sys-telemetry-app/history.ring`, mode `0600`, root-owned, about 8.6 MB) that never grows:

- 24 hours of 10-second samples (8640 slots) plus 1024 Advice-event slots, overwritten in place.
- Each sample holds system metrics, memory PSI, a Thrashing flag, the Working Set and the top 10 Applications, each with up to 8 Processes with **full command lines**.
- Every slot carries a CRC32 and sequence number, so a slot torn by a crash is skipped rather than corrupting the file.
- Flushed (write plus fsync) every 30 seconds and immediately when memory PSI crosses its warning level or Thrashing begins, so at most about 30 seconds are lost in a hard crash.
- Advice start, end and Severity changes are recorded as events.

On start, a previous unclean shutdown is detected and surfaced on stderr:

```
notice: last session ended without clean shutdown at 2026-10-19T03:14:07Z
```

Typical use after a freeze and reboot:

```sh
sudo sys-telemetry-app replay --last-boot --minutes 15 --processes
```

If the state directory is not writable, `run` warns and continues without History.

## Configuration

Everything has a working default; the config file never needs to exist. Default location: `/etc/sys-telemetry-app/config.toml` (override with `--config`). A small TOML subset is supported (`key = value`, `[[group]]` and `[[rename]]` tables, comments, strings, numbers).

```toml
# Runtime
port     = 9191
interval = "2s"            # minimum 1s

# Advisor thresholds
advisor_window               = "5m"   # how long a condition must hold to be Sustained
advisor_psi_percent          = 15     # memory pressure treated as high
advisor_working_set_percent  = 90     # Working Set as a share of physical RAM
advisor_thrash_pages_per_sec = 100    # swap-in AND swap-out above this = Thrashing
snooze_presets               = "1d, 7d"

# Grouping: user rules run before the built-in App Profiles; first match wins.
# Every field you set must match; set at least one of executable/command/cgroup/cwd.
[[group]]
name       = "Billing stack"
cwd        = "/home/me/work/billing"        # directory or anything below it

[[group]]
name       = "Nightly ETL"
command    = "etl_runner .*--nightly"       # regular expression over the full command line

[[group]]
name       = "Game"
executable = "steam"
cgroup     = "app-steam.*"                  # regular expression over the cgroup path

# Rename an Application in the display
[[rename]]
from = "Google Chrome"
to   = "Browser"
```

Invalid values are reported with the line number.

## HTTP API

The dashboard is a client of this JSON API, which is also handy for scripting. All routes are served on `127.0.0.1:<port>` only.

| Route | Returns |
|---|---|
| `GET /api/system` | Latest system metrics (CPU, RAM, Disk Swap and Compressed Swap, PSI, disk, GPU) |
| `GET /api/info` | Static information about the host and the program |
| `GET /api/availability` | Which data sources exist on this machine and which are unavailable, each with a one-line reason |
| `GET /api/processes` | Every Process with PID, user, command line, RSS, PSS, swap, CPU %, I/O rate, threads, file descriptors, cgroup, working directory |
| `GET /api/applications` | Applications with sums, primary PIDs, and their Processes (a Pod's containers included) |
| `GET /api/live` | The last 5 minutes at full sampling resolution |
| `GET /api/live/stream` | Server-Sent Events, one point per sample |
| `GET /api/live/stats` | Connected stream count and how many points were encoded |
| `GET /api/advice` | Active Advice, `pending` conditions not yet Sustained, and `disabled` rules with reasons |
| `GET /api/advice/events` | Advice start/end/Severity events |
| `POST /api/advice/hide`, `unhide`, `snooze` | Change Advice state (same-origin only) |
| `GET /api/history` | History samples for a window (query parameters such as `minutes`) |
| `GET /api/history/at`, `/span`, `/series` | A single moment, the available span, and graph series for the time slider |

Examples:

```sh
curl -s 127.0.0.1:9191/api/applications | python3 -m json.tool | head -40
curl -s 127.0.0.1:9191/api/advice | python3 -m json.tool
curl -s '127.0.0.1:9191/api/history?minutes=5' | head -c 600
```

## Running as a service

```sh
sudo sys-telemetry-app install      # writes and enables the systemd unit
sudo systemctl status sys-telemetry-app
sudo sys-telemetry-app uninstall
```

The unit gives the recorder a hard memory cap (`MemoryMax=64M`) and the program sets its own `oom_score_adj` to -1000, so the kernel kills something else first when memory runs out. This does not prevent a freeze or reboot; it only keeps the recorder alive longer so the lead-up gets written. Without systemd, `run` works the same in the foreground.

## GPU support

- **AMD and Intel:** read from sysfs; no extra setup.
- **NVIDIA:** the default binary is fully static and cannot load NVIDIA's libraries. For NVIDIA metrics (utilisation, temperature, VRAM), use the `nvml` build, which loads NVML at runtime through a vendored dlopen loader (see `docs/adr/0002-addendum-vendored-purego.md`). It is not static and needs glibc:

  ```sh
  make build-nvml        # dist/sys-telemetry-app-nvml-linux-{amd64,arm64}
  # or install the release variant
  curl -fsSL .../install.sh | sudo SYS_TELEMETRY_VARIANT=nvml sh
  ```

No GPU or no NVML: the GPU section is hidden.

## Safety and privacy

- **Advise only** (ADR 0003). The tool never writes sysctl, changes container limits, kills or renices anything. The only things it writes are its own `oom_score_adj`, its systemd unit (on `install`), and its own files in the state directory (`history.ring`, `state.json`, `instance.addr`).
- **Loopback only.** The dashboard binds to `127.0.0.1` with no authentication. For remote viewing use an SSH tunnel: `ssh -L 9191:127.0.0.1:9191 host`.
- **Command lines can contain secrets.** History stores full command lines (accepted risk, ADR 0004); the ring file is `0600` and root-owned. Anyone who can reach the local port can also read the API, so treat a shared machine accordingly.
- **No shelling out.** Data comes from `/proc`, `/sys`, cgroup files, container-runtime sockets and runtime-loaded GPU libraries; there are no calls to `ps`, `top`, `nvidia-smi` or `docker`. (`systemctl` is used only by `install` and `uninstall`.)

## Graceful degradation

Every data source is optional (ADR 0006): cgroup v1 or v2, PSI, GPU, a container runtime, systemd, zram. Missing data is hidden from the UI rather than shown as zero, and any Advice that depends on it is disabled with a one-line reason. `GET /api/availability`, the dashboard's "Unavailable on this machine" box and the bottom of `report` all list what is missing and why.

## Performance budget

- Under **30 MB** RAM in total, including the History write buffer.
- Under **0.5%** average CPU.
- System metrics every `--interval`; the expensive per-process `smaps_rollup` (PSS, swap) every 10 s.

To measure it on your machine (as root, with no dashboard open):

```sh
sudo scripts/footprint.sh --duration 1800
```

It runs the binary, reads its RSS and CPU time from `/proc` from outside, and prints PASS or FAIL against both budgets (`--interval`, `--pid` and `--binary` are also accepted; `RAM_BUDGET_MB` and `CPU_BUDGET_PCT` override the budgets). A synthetic-load test, `TestManyProcessesStayWithinFootprintBudget`, guards against gross regressions in CI.

## Building and testing

```sh
make build         # static amd64 + arm64 in dist/, with a static check
make build-nvml    # NVIDIA-capable build (not static)
make test          # go vet + go test, plus the nvml-tagged packages
make clean
```

`CGO_ENABLED=0` is set by the Makefile.

### How tests work

Tests run the **real program** against a fake **Machine**: a recorded `/proc`, `/sys` and cgroup tree, fake Docker and containerd sockets, a fake GPU reader, a controllable clock and a temporary state directory (`internal/harness`). They assert only through the JSON API and the `report` and `replay` output, not through internals. The application itself always reads the real system; fakes exist only in tests.

Fixture generators live in `scripts/` (`gen-*-fixture.py`, `record-fixture.sh`). Git cannot track empty directories or anything under a `.git` path, so fixtures that need those create them at test time.

## Project layout

```
cmd/sys-telemetry-app/   main entry point
internal/
  cli/         commands: run, report, replay, install, uninstall
  collector/   /proc, /sys, cgroup readers (behind an interface for a future macOS collector)
  containers/  Docker and containerd discovery
  grouping/    layers that turn Processes into Applications
  advisor/     rules, Sustained windows, Hide/Snooze state
  history/     the crash-surviving ring file, queries, Replay
  server/      HTTP API, SSE and embedded dashboard assets
  live/        in-memory 5-minute buffer for the live view
  gpu/         AMD/Intel sysfs readers and the NVML build
  selfguard/   own memory cap and oom_score_adj
  availability/ graceful-degradation reporting
  config/      optional TOML config
  machine/     the seam between the program and the OS
  harness/     the fake Machine used by tests
  third_party/ vendored purego (nvml build only)
scripts/       install.sh, footprint.sh, fixture generators
docs/adr/      architecture decision records
```

## Releases

Pushing a version tag runs `.github/workflows/release.yml`, which builds the static amd64 and arm64 binaries (and the NVML variants) and publishes them with `install.sh` to GitHub Releases.

## Verifying on a real machine

[VERIFY.md](VERIFY.md) is a checklist grouped by ticket with the exact commands to run as root on an Ubuntu laptop and what you should see, including the NVIDIA check and the footprint budget check.

## Design documents

| Document | Purpose |
|---|---|
| [SPEC.md](SPEC.md) | Full specification |
| [GLOSSARY.md](GLOSSARY.md) | Domain terms used in code, docs and commits |
| [docs/adr/](docs/adr/) | Why: Linux first (0001), Go with runtime-loaded GPU libraries (0002, plus the vendored purego addendum), advise-only despite root (0003), crash-surviving History (0004), dynamic Application discovery (0005), run anywhere and degrade (0006) |
| [docs/original-spec.md](docs/original-spec.md) | The original brief |
| [AGENTS.md](AGENTS.md) | Notes for coding agents working in this repo |

## Roadmap

Phase 2: a macOS collector (memory footprint instead of PSS, pressure level instead of PSI, Docker Desktop containers via the Docker API), Podman and LXC, and optional alerts outside the dashboard (system journal, desktop notifications). Out of scope: Windows, remote access and authentication, and changing the system in any way.
