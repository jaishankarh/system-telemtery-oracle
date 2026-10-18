# VERIFY.md: real-machine checklist

Run on your Ubuntu laptop, as root, from a checkout of `integration/phase-1`. One section per ticket; each ticket's "Verified on a real machine" criterion stays unticked until you have done its section.

Setup once: `sudo apt-get install -y golang-go make` if needed, then `make build`.

## Ticket 18: Self-guard and systemd install

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`; in another shell `cat /proc/$(pgrep -f 'sys-telemetry-app.*run' | head -1)/oom_score_adj` prints `-1000`
- [ ] `curl -s 127.0.0.1:9191/api/self` shows `oom_protected: true`, `oom_score_adj: -1000`, `memory_cap_bytes: 67108864`, `memory_cap_source: "internal"`, and a plausible `rss_bytes` (well under 30 MB)
- [ ] `sudo install -m 755 dist/sys-telemetry-app-linux-amd64 /usr/local/bin/sys-telemetry-app && sudo sys-telemetry-app install` prints `install: done`
- [ ] `systemctl status sys-telemetry-app` is active (running) and enabled; `cat /etc/systemd/system/sys-telemetry-app.service` shows `MemoryMax=64M`, `OOMScoreAdjust=-1000`
- [ ] `systemctl show sys-telemetry-app -p MemoryMax` shows 67108864; `curl -s 127.0.0.1:9191/api/self` now reports `memory_cap_source: "systemd"`
- [ ] Reboot (or `sudo systemctl restart`), the service comes back by itself
- [ ] `sudo sys-telemetry-app uninstall` prints `uninstall: done`; then `ls /etc/systemd/system/sys-telemetry-app.service` fails, `systemctl status sys-telemetry-app` says not found, and `systemctl list-unit-files | grep sys-telemetry` is empty
- [ ] Nothing else changed: `sysctl -a | md5sum` and `cat /proc/*/oom_score_adj | sort | uniq -c` before and after differ only by the tool's own process

## Ticket 19: Release workflow and install script

- [ ] `git tag v0.0.1-test && git push origin v0.0.1-test`; the "release" workflow run in GitHub Actions goes green and the Release lists `sys-telemetry-app-linux-amd64`, `sys-telemetry-app-linux-arm64`, `SHA256SUMS`, `install.sh` (delete the test release and tag afterwards)
- [ ] Local dry run of the script without network: `make build && (cd dist && sha256sum sys-telemetry-app-linux-* > SHA256SUMS) && SYS_TELEMETRY_BASE_URL=$PWD/dist INSTALL_DIR=/tmp/sta sh scripts/install.sh` prints "Installed /tmp/sta/sys-telemetry-app (amd64)"
- [ ] On the laptop: `curl -fsSL https://github.com/jaishankarh/system-telemtery-oracle/releases/latest/download/install.sh | sudo sh` installs to /usr/local/bin
- [ ] `sudo sys-telemetry-app report` shows real data (CPU, RAM, Disk Swap, Compressed Swap) from the downloaded binary
- [ ] `file /usr/local/bin/sys-telemetry-app` says statically linked, x86-64 (aarch64 on an ARM box)
- [ ] Tampering check: edit a byte in SHA256SUMS locally and rerun the dry run; it fails with "checksum mismatch" and installs nothing

## Ticket 2: Walking skeleton

- [ ] `make build` then `file dist/sys-telemetry-app-linux-amd64 dist/sys-telemetry-app-linux-arm64` shows "statically linked" and "stripped" for both
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` prints CPU (total %, load, cores), RAM (total/available/used/cached/buffers/dirty), Disk Swap and Compressed Swap lines, and Swap-in/out rates. Compare RAM and swap with `free -h` and `swapon --show`; Disk Swap must match the swap file/partition and Compressed Swap the zram device (or "none")
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191 --interval 2s`, then `curl -s 127.0.0.1:9191/api/system` returns JSON with real values; `missing` is empty (or lists only sources your kernel lacks)
- [ ] `ss -ltn | grep 9191` shows 127.0.0.1:9191 only (not 0.0.0.0)
- [ ] Open http://127.0.0.1:9191 in a browser: CPU, RAM and Swap sections update every ~2s; with the network disconnected it still works
- [ ] Generate swap activity (e.g. `stress-ng --vm 2 --vm-bytes 90%` for 30s) and check swap-in/out rates and CPU % move in the dashboard
- [ ] Config: write `/etc/sys-telemetry-app/config.toml` with `port = 9292` and `interval = "3s"`; a plain `run` uses them (`curl 127.0.0.1:9292/api/info` shows `interval_seconds: 3`); `--port`/`--interval` flags override it; `--interval 500ms` is rejected

## Ticket 3: Processes view

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 report`: a "Top processes by PSS + swap" table lists 15 rows (PID, USER, PSS, SWAP, RSS, CPU%, COMMAND). Browser/editor/Docker processes you know are running appear near the top; usernames are names, not numbers
- [ ] Compare one row with `sudo grep -E '^(Pss|Swap):' /proc/<pid>/smaps_rollup` and `grep VmRSS /proc/<pid>/status`: PSS, SWAP and RSS match (in MiB)
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`, then `curl -s 127.0.0.1:9191/api/processes | head -c 1500`: every process has pid, user, name, full command line, rss/pss/swap bytes, threads, open_fds; `cpu_percent` and disk rates are null on the very first sample only; `missing` is empty (or lists only sources your kernel lacks)
- [ ] `curl -s '127.0.0.1:9191/api/processes?sort=cpu_percent&order=desc'` after ~4s: top entry matches the busiest process in `top`/`htop` (cpu_percent is percent of one core, so can exceed 100)
- [ ] Filters: `?filter=<a pid>` returns that process; `?filter=chrome|firefox` returns the browser processes; `?filter=(` returns HTTP 400
- [ ] Dashboard at http://127.0.0.1:9191: Processes table shows all 12 columns; clicking each header sorts (arrow shows direction, second click reverses); typing a PID, a name or a regex filters live; a bad regex shows an error beside the box
- [ ] Start something that writes to disk (`dd if=/dev/zero of=/tmp/x bs=1M count=2000 oflag=direct`) and watch its Write/s column rise; start `yes > /dev/null` and watch CPU % near 100
- [ ] PSS/Swap refresh only every ~10s (watch a process allocating memory: RSS moves every 2s, PSS/Swap in steps of 10s)

## Ticket 4: Applications view and grouping

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 report`: an "Applications by PSS + swap" table (APPLICATION, KIND, PSS, SWAP, RSS, CPU%, PROCS, PRIMARY PIDS) appears above the process list; programs you launched from the desktop (browser, editor, terminal, music player) each have their own named row with kind systemd, snap or flatpak
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`, then `curl -s 127.0.0.1:9191/api/applications | head -c 2000`: each Application has name, kind, pids, primary_pids, process_count, rss/pss/swap totals and a processes list with full command lines, cwd and cgroup
- [ ] Every process exactly once: `curl -s 127.0.0.1:9191/api/applications | python3 -c 'import json,sys; a=json.load(sys.stdin)["applications"]; n=sum(x["process_count"] for x in a); print(n)'` equals the length of `curl -s 127.0.0.1:9191/api/processes | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["processes"]))'` (allow +-a few for processes starting or exiting between calls)
- [ ] Totals add up: the pss_bytes of one Application equals the sum of pss_bytes in its processes; spot-check one against `sudo grep Pss /proc/<pid>/smaps_rollup`
- [ ] "Other" is small or absent: it holds only processes with no executable name; "Kernel threads" holds the [kworker]/[ksoftirqd] entries
- [ ] Sorting: `?sort=cpu_percent&order=desc` and `?sort=name&order=asc` reorder rows; `?sort=nope` is HTTP 400; `?filter=firefox|chrome` narrows to matching Applications
- [ ] Dashboard at http://127.0.0.1:9191: the Applications table is the first view and sorted by PSS + swap; clicking a header sorts (arrow shows direction); clicking a row expands its Processes with PID, user, PSS, swap, working directory and the full command line; the expansion survives the 2s refresh
- [ ] Launch a program the tool has never heard of (for example a snap or Flatpak app, or `systemd-run --user --scope sleep 600`) and see it appear as its own Application without any config

## Ticket 5: Containers and Pods

Build and run as root: `make build && sudo ./dist/sys-telemetry-app-linux-amd64 run --port 8080`

- [ ] Start a container: `docker run -d --name memtest --memory 256m alpine sleep 1000`. `curl -s localhost:8080/api/applications?filter=memtest | jq '.applications[] | {name,kind,container}'` shows kind "container", name "memtest", started_by "docker run", memory_limit_bytes 268435456.
- [ ] Start one without a limit (`docker run -d --name nolimit alpine sleep 1000`): memory_limit_bytes is null.
- [ ] If you use Compose: a compose container shows started_by "compose".
- [ ] If containerd/nerdctl or k3s is installed: its containers show runtime "containerd"; k3s pods show kind "pod" named by pod name, with `.container.containers[]` listing each container and each process's `subgroup` naming its container.
- [ ] kind: the kind node appears as one Docker Container (e.g. kind-control-plane); pods inside it are not split out (known limitation).
- [ ] With Docker stopped (`sudo systemctl stop docker docker.socket`) the program keeps running and the container rows disappear without errors.
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` lists the containers in the Applications table.

## Ticket 8: Live graphs and SSE

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`
- [ ] `curl -s 127.0.0.1:9191/api/system | python3 -m json.tool | grep -A12 '"pressure"'` shows cpu/memory/io `some_percent`/`full_percent` (null only on the first sample); `"disks"` lists whole devices (nvme0n1, sda; no partitions, loop, zram). `missing` lists `/proc/pressure` only if your kernel lacks PSI
- [ ] Idle cost: before opening a browser, `curl -s 127.0.0.1:9191/api/live/stats` shows `"clients":0,"stream_encodes":0`, and stays 0 after 30s
- [ ] After ~1 minute, `curl -s 127.0.0.1:9191/api/live | python3 -c 'import json,sys; d=json.load(sys.stdin); print(len(d["points"]), d["window_seconds"])'` prints about 30 points (at 2s) and 300; after 6+ minutes it stays at ~150 points
- [ ] Open http://127.0.0.1:9191: the Graphs section shows CPU, RAM vs swap, Memory pressure, I/O pressure, Disk throughput, Swap-in/out, and the Top 10 Applications RAM/swap bars. On open the charts already show the last 5 minutes; all move every ~2s. Charts for data your kernel lacks (for example no PSI) are hidden
- [ ] Generate load: `stress-ng --vm 2 --vm-bytes 90% --timeout 60s` (RAM and swap lines rise, memory pressure and swap-in/out rise); `dd if=/dev/zero of=/tmp/x bs=1M count=3000 oflag=direct` (disk write throughput rises); `yes >/dev/null` (CPU rises). Values agree with `vmstat 1` / `iostat -x 1` / `cat /proc/pressure/memory`
- [ ] While the page is open `curl -s 127.0.0.1:9191/api/live/stats` shows `clients` 1 and `stream_encodes` rising by 1 per sample. `curl -N 127.0.0.1:9191/api/live/stream` prints a `data: {...}` line every 2s
- [ ] Close the tab (or switch to another tab: streaming pauses while hidden): `clients` returns to 0 and `stream_encodes` stops rising; reopening resumes it and the graph backfills the gap
- [ ] Works offline: disconnect the network, reload; charts still draw (uPlot is embedded)
- [ ] Narrow the window to half a laptop screen: charts reflow to one column without horizontal page scroll
