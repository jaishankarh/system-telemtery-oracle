# VERIFY.md: real-machine checklist

Run on your Ubuntu laptop, as root, from a checkout of `integration/phase-1`. One section per ticket; each ticket's "Verified on a real machine" criterion stays unticked until you have done its section.

Setup once: `sudo apt-get install -y golang-go make` if needed, then `make build`.

## Ticket 10: Graceful degradation

Build and run as root: `make build && sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`

- [ ] `curl -s 127.0.0.1:9191/api/availability | python3 -m json.tool` shows `environment` (your cgroup mode "v2"/"hybrid"/"v1", `systemd` true, the Docker/containerd sockets found, GPU paths) and `unavailable` listing only what your laptop truly lacks, each with a one-line `reason`; on a modern Ubuntu with Docker and a GPU it is empty or short
- [ ] Dashboard at http://127.0.0.1:9191: if anything is unavailable, an "Unavailable on this machine" box lists it with reasons; sections and table columns without data are hidden rather than showing 0 or n/a
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report | tail -20` prints the same under "Unavailable on this machine"
- [ ] Take a source away and see it listed within a few seconds, no crash: `sudo systemctl stop docker docker.socket` lists "Container runtime"; start them again and it disappears. If you have no swap partition or file, "Disk Swap" is listed and the Advice panel lists Hardware Limit as disabled with "needs Disk Swap (...)"
- [ ] Optional, kernel without PSI: boot with `psi=0`, or compare `cat /proc/pressure/memory` failing: "Pressure stall information (PSI)" is listed, the pressure charts are hidden, `curl -s 127.0.0.1:9191/api/system | python3 -m json.tool | grep -A2 '"pressure"'` shows null

## Ticket 11: History and replay

- [ ] Build and run as root: `make build && sudo ./dist/sys-telemetry-app-linux-amd64 run`
- [ ] `sudo ls -l /var/lib/sys-telemetry-app/history.ring` shows mode `-rw-------`, owner root, size 9,011,200 bytes (about 8.6 MB); it stays that size after minutes of running.
- [ ] After ~1 minute: `sudo ./dist/sys-telemetry-app-linux-amd64 replay` (asks the running instance) lists a line every 10 s with real cpu, ram, swap and working-set figures and your real top Applications; the last sample lists Processes with full command lines.
- [ ] `curl -s 127.0.0.1:9191/api/history?minutes=5 | head -c 600` returns JSON with `"live": true` and real samples.
- [ ] Crash test: `sudo pkill -9 sys-telemetry-app`, restart it: stderr prints `notice: last session ended without clean shutdown at <time within ~30s of the kill>`.
- [ ] With the instance stopped (or killed): `sudo ./dist/sys-telemetry-app-linux-amd64 replay --last-boot --minutes 5` shows the lead-up to the kill, ending within ~30 s of it.
- [ ] `replay --around 14:30 --minutes 10` shows a window centred on that time (use a time within the running period).
- [ ] Stop with Ctrl-C and restart: no unclean notice.
- [ ] Optional: `stress-ng --vm 4 --vm-bytes 90%` (or fill RAM) and see a flush plus a `mem-psi` / `THRASHING` sample in `replay` at the spike.

## Ticket 12: Advisor and Hardware Limit

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`. `curl -s 127.0.0.1:9191/api/advice | python3 -m json.tool` returns `window_seconds: 300`, `advice: []`, `pending`, and `disabled`. `disabled` is empty if your kernel has `/proc/pressure/memory`, `/proc/vmstat` and (when you have both zram and a swap file) `/sys/block/zram0/stat`; otherwise it lists the Hardware Limit rule with a one-line reason
- [ ] On a normal day (no memory trouble) there is no Advice; the dashboard at http://127.0.0.1:9191 shows an Advice panel at the very top reading "No active Advice."
- [ ] Hardware Limit must not fire on a short spike: run `stress-ng --vm 2 --vm-bytes 90% --timeout 60s`; `/api/advice` may show a `pending` entry during the run but never an entry in `advice`, and `pending` empties afterwards
- [ ] To see a real verdict without buying RAM, use a short window: write `/etc/sys-telemetry-app/config.toml` with `advisor_window = "20s"` and `advisor_working_set_percent = 20`, `advisor_psi_percent = 1`, `advisor_thrash_pages_per_sec = 5`, restart, then run `stress-ng --vm 4 --vm-bytes 110% --timeout 120s`. Expect a critical or warning Hardware Limit in the panel with Working Set, installed RAM, Memory Deficit and "would cover it" sizes; compare Working Set with `free` plus swap in use, and that `swap_attribution` evidence matches your setup. Remove the config afterwards
- [ ] `curl -s 127.0.0.1:9191/api/advice/events` shows `advice_started` and, after the stress ends, `advice_ended`
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` (with the instance above running) prints an "Advice" section from the running instance; with it stopped, it says it cannot verify the Sustained window and lists current conditions and disabled rules
- [ ] Swap attribution on a machine with both zram and a swap file: `cat /sys/block/zram0/stat` counters increase when swapping, and `disk_swap_in/out` in the evidence is what is left after subtracting them from `grep -E 'pswp(in|out)' /proc/vmstat`

## Ticket 13: Dashboard time slider and Replay

Run as root on the Ubuntu laptop. Let the program run at least 15 minutes (a few hours is better) so History has something to slide through.

- [ ] `make build && sudo ./dist/sys-telemetry-app-linux-amd64 run`, open http://127.0.0.1:9191
- [ ] A "Time" slider sits at the top, at the far right, labelled "Live"; beside it a line "History: <oldest> to <newest>"
- [ ] `curl -s localhost:9191/api/history/span | head -c 400` shows `oldest`, `newest`, `interval_seconds: 10`, `events`
- [ ] Drag the slider left: the label shows the time, an orange banner says "Replay of <time>. Nothing on this page is current."
- [ ] Applications, Processes, Graphs and the Advice panel all change to that time; the Applications and Processes notes say "History keeps only the top 10 Applications ... Showing 10 of N" and "up to 8 Processes per Application"
- [ ] Expanding an Application with more than 8 Processes says "Showing 8 of N Processes"
- [ ] Graphs: one hour around the selected time; the swap-in/swap-out chart has vertical lines for Advice start (red), Severity change (orange), end (green, dashed), and the list under "Graphs" reads like "Thrashing started 23:10" (cause some Advice, for example `stress-ng --vm 4 --vm-bytes 90% -t 10m` on a small-RAM machine, then wait for the Sustained window)
- [ ] While in the past, the Advice panel shows no Hide, Snooze or Unhide controls, and says why
- [ ] Slide to a time before the program first ran or during a stop: the banner says no History was recorded then
- [ ] "Back to Live" (or dragging fully right) returns to current data, controls return, graphs stream again
- [ ] Restart the program, slide back to before the restart: the Advice events from before it are still shown
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 replay --minutes 30` shows `ADVICE advice_start` lines for the same Advice

## Ticket 14: swap Settings Fixes

- [ ] Build and run as root: `make build && sudo ./bin/sys-telemetry-app` (use the binary name from earlier sections), open the dashboard.
- [ ] `curl -s localhost:<port>/api/advice | jq '.disabled'` lists swap rules only if a source is missing (for example `/proc/sys/vm/swappiness`); on a normal Ubuntu laptop none of the three swap rules are disabled.
- [ ] Swappiness: `cat /proc/sys/vm/swappiness` is 60. With a large page cache (read a few big files, e.g. `cat` a multi-GB file to /dev/null until Cached is above 25% of RAM) and Disk Swap being written (`vmstat 1`, so column), after 5 minutes Advice `swappiness-too-aggressive` appears with `vm.swappiness = 10` and a reason. Copy it into `/etc/sysctl.d/99-swappiness.conf` yourself; the app must not create it.
- [ ] Compressed Swap Thrashing: with zram in use and in/out both above 100 pages/s for 5 minutes, `compressed-swap-thrashing` appears, explained as CPU cost, no hardware-limit entry.
- [ ] Swap wear: with Disk Swap written steadily (for example a stress job that overcommits RAM for 35+ minutes) `swap-wear` appears with a GB/day figure in the title and in Evidence (`disk_swap_write_gb_per_day`) that matches `vmstat` pswpout x 4 KiB x 86400.
- [ ] Dashboard Advice panel shows the text in a box with a Copy button; pasting it gives only the file content.
- [ ] After all of this: `ls /etc/sysctl.d/99-swappiness.conf /etc/sysctl.d/99-zram-swap.conf /etc/systemd/zram-generator.conf` shows files you did not create do not exist, and `cat /proc/sys/vm/swappiness` is unchanged.

## Ticket 15: Uncapped container (suggested memory limit)

- [ ] `make build && sudo ./dist/sys-telemetry-app-linux-amd64 run` (let it run at least 10 minutes with Docker running so History has samples).
- [ ] Start a container with no limit that uses over 1 GiB (or 256 MiB in swap), for example `docker run -d --name hog python:3 python -c "x=bytearray(1500*1024*1024); import time; time.sleep(99999)"`.
- [ ] After the advisor window (5 minutes), `curl -s 127.0.0.1:<port>/api/advice | jq '.advice[] | select(.rule=="container-limit")'` shows Advice `container-limit:container:<id>` with category `settings_fix`.
- [ ] `change.content` is `docker run --memory=NNNNm ...` where NNNN is about 1.25 x the container's highest Working Set (PSS + swap); the evidence fact `peak_basis` states the History window (from/to times) and the top-10 caveat.
- [ ] For a Compose service, the change is a `services: <service>: deploy: resources: limits: memory:` snippet and the note mentions Compose v2 and `mem_limit`. For a Kubernetes (k3s/kind-less) Pod, a `resources.limits.memory: NNNMi` snippet naming the container.
- [ ] Recreate the container with `--memory` set: the Advice ends. Nothing was changed by the tool (`docker inspect` shows no limit before you apply it).
- [ ] With no Docker/containerd socket (or `sudo mv /var/run/docker.sock` away in a VM), the Advice panel lists "Uncapped container" as disabled with a reason about the container runtime.
- [ ] `./dist/sys-telemetry-app-linux-amd64 report` prints the same Advice when an instance is running.

## Ticket 16: Editor file-watcher and browser process sprawl Settings Fixes

Thresholds (fixed in code, window is `advisor_window`, default 5 min): editor rule fires when file-watcher + ripgrep + language-server processes of one Cursor / VS Code / VSCodium / Windsurf together use >= 50% of one core or >= 2 GB PSS. Browser rule fires at >= 8 renderer processes totalling >= 3 GB PSS. To test quickly, add `advisor_window = "30s"` to `/etc/sys-telemetry-app/config.toml`.

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`
- [ ] Editor: open a large git repository in Cursor (or VS Code / Windsurf) with a big `node_modules` and no `files.watcherExclude`; run a project-wide search or a TypeScript/Rust language server indexing so the watcher/ripgrep/language server burn CPU. Check roles with `curl -s localhost:9191/api/applications | jq '.applications[] | select(.name=="Cursor") | .processes[] | {pid, role, cpu_percent, pss_bytes}'`
- [ ] After the window: `curl -s localhost:9191/api/advice | jq '.advice[] | select(.rule=="editor-file-watcher")'` shows category `settings_fix`, the editor in `applications`, evidence facts, and a `change` whose `target` is `<your repo>/.vscode/settings.json` and `content` is JSON with `files.watcherExclude` and `search.exclude` (node_modules, dist, build, .cache, plus extras like .next / target only if they exist in the repo). The summary names the repo folder as the Project.
- [ ] Paste the content into that settings.json (merging keys), reload the window: watcher/ripgrep CPU drops and the Advice ends after the editor is quiet.
- [ ] `sudo ls <repo>/.vscode` before you paste anything: the tool created no file (advice only).
- [ ] Browser: open 15+ tabs in Chrome/Chromium/Brave/Edge/Firefox so renderers total > 3 GB. `curl -s localhost:9191/api/advice | jq '.advice[] | select(.rule=="browser-process-sprawl") | {summary, change}'` names that browser and gives its own settings: Chrome `chrome://settings/performance` Memory Saver, Brave `brave://settings/system`, Edge `edge://settings/system` sleeping tabs, Chromium the `#high-efficiency-mode-available` flag, Firefox `dom.ipc.processCount`. The text labels each item as a preference or a flag (`--renderer-process-limit=N` is a command-line flag).
- [ ] Check the named setting really exists in your browser version at the named place (the Memory Saver location moves between releases).
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` shows the same Advice with the change and its Note.
- [ ] Dashboard Advice panel shows the change in a copyable block and the note under it.
- [ ] With quiet editors and few tabs, neither rule shows up (and neither appears under "Disabled").

## Ticket 17: Hide and Snooze Advice

Build and run as root: `make build && sudo ./dist/sys-telemetry-app-linux-amd64 run` (a config with `advisor_window = "30s"` and `snooze_presets = "1m, 1d, 7d"` makes this quicker).

- [ ] Create Sustained Advice (for example fill RAM until the Advice panel shows an item) and open http://127.0.0.1:9191. The item shows Hide, a Snooze select (presets from config) and Snooze.
- [ ] Click Hide: the item leaves the panel and appears under "Hidden (until Severity rises above ...)" with an Unhide button. `curl -s localhost:9191/api/advice | jq .hidden` lists it.
- [ ] Click Unhide: it returns.
- [ ] Snooze for 1m: it disappears, shows "Snoozed until ..."; after a minute, if the condition still holds, it is back.
- [ ] `sudo cat /var/lib/sys-telemetry-app/state.json` shows the entry; restart the program: the entry is still hidden.
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` lists hidden/snoozed Advice with its id; `ls /var/lib/sys-telemetry-app` shows state.json (plus the History files from ticket 11), nothing else from this ticket.
- [ ] Browser safety: `curl -i -X POST localhost:9191/api/advice/hide -H 'Content-Type: application/json' -H 'Origin: http://evil.example' -d '{"id":"x"}'` returns 403; `curl -i localhost:9191/api/advice/hide` returns 405.

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

## Ticket 20: Footprint budget check

Budget: < 30 MB RAM (peak VmRSS) and < 0.5% average CPU over 30 minutes, no dashboard open. `scripts/footprint.sh` measures from outside by sampling /proc/<pid>; it starts the binary itself unless you pass `--pid`.

- [ ] `make build` and `sudo true` ready; put the laptop under normal heavy load (Docker stacks up, Cursor, browsers with many tabs) and do NOT open the dashboard
- [ ] Quick smoke first (1 minute): `sudo scripts/footprint.sh --duration 60 --interval 5` prints `samples: ...`, a `RAM  peak ... MB` line, a `CPU  avg ...%` line, and ends with PASS or FAIL (first minute includes startup, so CPU may read slightly high)
- [ ] The real run (30 min): `sudo scripts/footprint.sh` (defaults: 1800 s, every 10 s, `dist/sys-telemetry-app-linux-amd64`). Expected: `RAM peak < 30 MB: PASS`, `CPU avg < 0.5%: PASS`, final line `PASS`. Paste the output into issue #20
- [ ] Cross-check against an already running instance: `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191 &` then `sudo scripts/footprint.sh --pid $(pgrep -f 'sys-telemetry-app.* run' | head -1) --duration 300`; same verdict expected
- [ ] Confirm no dashboard client was attached: `ss -tnp | grep 9191` shows no established connections during the run
- [ ] If FAIL: note which budget, and `sudo cat /proc/$(pgrep -f sys-telemetry-app | head -1)/status | grep -E 'VmRSS|VmHWM|Threads'`; a follow-up ticket is needed (the sandbox run of the same script read ~0.6% CPU over 20 s including startup, so CPU is the one to watch)

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

## Ticket 6: App Profiles

- [ ] `make build`, then `sudo ./dist/sys-telemetry-app-linux-amd64 report`: each program you have running appears as ONE row in "Applications by PSS + swap" with KIND `profile`: Cursor / Visual Studio Code / Windsurf, Android Studio / IntelliJ IDEA / other JetBrains IDEs, Google Chrome / Chromium / Brave / Microsoft Edge / Firefox, Android emulator, Gradle daemon. None of them is split into per-process rows or left in Other
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`, then list a browser's roles: `curl -s 127.0.0.1:9191/api/applications | python3 -c 'import json,sys,collections; [print(a["name"], dict(collections.Counter(p.get("role","") for p in a["processes"]))) for a in json.load(sys.stdin)["applications"] if a["kind"]=="profile"]'`. Browsers show `main`, `renderer` (many), `gpu`, `utility`, `zygote`; editors show `main`, `extension-host`, `language-server`, `file-watcher`, `ripgrep`, `pty-host`, `terminal-shell`, `renderer`
- [ ] Language servers count toward their editor: open a Rust, Go, Python or TypeScript project in Cursor/VS Code, then `curl -s 127.0.0.1:9191/api/applications | grep -o '"role":"language-server"' | wc -l` is >= 1, and `pgrep -a 'rust-analyzer|gopls|tsserver|pyright'` PIDs all appear in the editor's Application (`pids`), not in a separate row
- [ ] Editor terminal exclusion: run `npm run dev` (or any server) in the editor's built-in terminal; its PIDs are NOT in the editor's Application (the shell is, with role `terminal-shell`)
- [ ] Browser sprawl: with many tabs open, one row per browser; its `process_count` matches `pgrep -c -f '<browser binary>'` within a few; PSS is far below the sum of RSS (shared pages not double counted)
- [ ] Android: start an emulator from Android Studio: it is its own "Android emulator" row (roles `main`, `emulator-vm`), not part of Android Studio. A Gradle daemon started by Android Studio or IntelliJ (`pgrep -af GradleDaemon`) is inside that IDE's row; one started from a terminal (or whose IDE has quit) is the "Gradle daemon" row
- [ ] Dashboard at http://127.0.0.1:9191: expand an editor or browser row; the role (for example `language-server`, `renderer`) shows in place of the old subgroup column for each process
- [ ] Anything that is not in the list (a snap, a flatpak, a service) still gets its old systemd/snap/flatpak/ancestor row. Note any browser or editor build installed in a path with spaces or an unusual name that is NOT folded in; send the command line (`ps -o args= -p <pid>`) so a recogniser can be added to internal/grouping/profiles_table.go

## Ticket 7: Dev & Build per Project and user grouping rules

Run as root on the Ubuntu laptop. Start the binary: `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191`.

- [ ] In Cursor (or VS Code), open a project that is a git repository, open the built-in terminal and run `npm run dev` (or any dev server).
- [ ] `curl -s localhost:9191/api/applications | jq '.applications[] | {name, kind, pids}'` shows `Dev & Build: <repo folder name>` with kind `devbuild`, containing the npm/node dev server processes, and the Cursor row no longer contains them.
- [ ] Cursor's own language servers (tsserver, extension host) are still in the Cursor row (not in Dev & Build).
- [ ] Start a second build in another repository (for example `cargo build` or `make`): a second `Dev & Build: <other repo>` row appears.
- [ ] A build run in a directory with no VCS root appears as `Dev & Build: <that directory name>`.
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` lists the same Dev & Build rows under Applications.
- [ ] Create `/etc/sys-telemetry-app/config.toml` with:
  ```
  [[group]]
  name = "My terminal"
  executable = "gnome-terminal-server"

  [[rename]]
  from = "Dev & Build: <repo folder name>"
  to = "My dev server"
  ```
  Restart: an Application "My terminal" holds the terminal server, and the renamed row shows the new name. A typo in the file (unknown key, bad regex) makes the binary exit with a line-numbered error.

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

## Ticket 9: GPU metrics

AMD/Intel GPUs are read from sysfs in the default (static) binary. NVIDIA needs the separate nvml build, which loads libnvidia-ml.so.1 at runtime; it could only be tested against a stub library, so the NVIDIA checks matter most.

Static default build:
- [ ] `make build` prints "static OK" for both binaries; `ldd ./dist/sys-telemetry-app-linux-amd64` says "not a dynamic executable"; `file` says "statically linked"
- [ ] On the NVIDIA laptop with the default binary: `sudo ./dist/sys-telemetry-app-linux-amd64 report` ends with "Unavailable on this machine" listing "NVIDIA GPU: NVIDIA GPU data needs the nvml build ..." and no NVIDIA GPU line; `curl -s 127.0.0.1:9191/api/system | python3 -m json.tool | grep -B1 -A3 nvidia_gpu` shows the same item

NVIDIA (RTX 3060 laptop, proprietary driver installed), with the nvml build:
- [ ] `make build-nvml` succeeds; run THIS binary as root: `sudo ./dist/sys-telemetry-app-nvml-linux-amd64 run --port 9191` (expected: `ldd` shows libc/libdl/libpthread; that is fine for this variant)
- [ ] The driver is loaded: `ls /dev/nvidiactl /proc/driver/nvidia` both exist, and `ls /usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1` (or `ldconfig -p | grep nvidia-ml`) exists
- [ ] `curl -s 127.0.0.1:9191/api/system | python3 -m json.tool | grep -B2 -A12 '"gpus"'` shows an entry with vendor "nvidia", name containing "RTX 3060", util_percent, temperature_celsius, vram_used_bytes and vram_total_bytes (about 6 GiB on a laptop 3060, 12 GiB desktop)
- [ ] Compare with `nvidia-smi --query-gpu=name,utilization.gpu,temperature.gpu,memory.used,memory.total --format=csv` (run it only to compare): temperature within 1-2 C, VRAM used within a few MiB (nvidia-smi MiB x 1048576 = bytes), utilisation similar
- [ ] Generate load (a game, `glxgears -fullscreen`, or `python3 -c 'import torch;...'`): util_percent and temperature rise within a few seconds; VRAM used rises when a GPU program starts
- [ ] `sudo ./dist/sys-telemetry-app-nvml-linux-amd64 report` has a line `GPU  NVIDIA GeForce RTX 3060 (nvidia)  util N%  temp N C  VRAM x GiB of y GiB`
- [ ] Dashboard at http://127.0.0.1:9191: a "GPU" section under Swap, and in Graphs a "GPU" chart (utilisation and temperature) and a "GPU memory (VRAM)" chart that move every ~2s
- [ ] `curl -s 127.0.0.1:9191/api/live | python3 -c 'import json,sys; p=json.load(sys.stdin)["points"][-1]; print({k:v for k,v in p.items() if k.startswith("gpu")})'` prints the four gpu_* values
- [ ] Hybrid laptop: if the GPU is runtime-suspended (`cat /sys/bus/pci/devices/*/power/runtime_status`), reading may wake it; note whether this is a problem (battery). If it is, tell me
- [ ] Stability: leave it running 10 minutes under `watch -n1 nvidia-smi` in another terminal; the program keeps sampling (`/api/system` time advances) and `ps -o rss= -p $(pgrep sys-telemetry)` stays under 30000 (KiB)
- [ ] Failure is quiet: `sudo rmmod nvidia_uvm` is not needed; instead `sudo mv /usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1 /tmp/` and restart the nvml binary: no crash, no `gpus` key, and "NVIDIA GPU: NVIDIA GPU data could not be read: ..." under Unavailable (move it back afterwards)

AMD / Intel (only if you have such a machine or an iGPU):
- [ ] `ls /sys/class/drm/card*/device/vendor` and `cat` them: 0x1002 is AMD, 0x8086 is Intel. `gpus` in /api/system lists those (Intel iGPUs usually have only a temperature or nothing; AMD shows util from gpu_busy_percent and VRAM from mem_info_vram_*)

No GPU / no driver:
- [ ] On a machine without a GPU (a VM or server): the binary starts, `/api/system` has no `gpus` key, the dashboard has no GPU section or charts, `report` has no GPU line, stderr has no GPU errors
- [ ] `SYS_TELEMETRY_VARIANT=nvml` with install.sh (after a release) installs the nvml build; without it the static one
