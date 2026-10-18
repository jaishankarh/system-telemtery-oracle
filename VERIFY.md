# VERIFY.md: real-machine checklist

Run on your Ubuntu laptop, as root, from a checkout of `integration/phase-1`. One section per ticket; each ticket's "Verified on a real machine" criterion stays unticked until you have done its section.

Setup once: `sudo apt-get install -y golang-go make` if needed, then `make build`.

## Ticket 2: Walking skeleton

- [ ] `make build` then `file dist/sys-telemetry-app-linux-amd64 dist/sys-telemetry-app-linux-arm64` shows "statically linked" and "stripped" for both
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 report` prints CPU (total %, load, cores), RAM (total/available/used/cached/buffers/dirty), Disk Swap and Compressed Swap lines, and Swap-in/out rates. Compare RAM and swap with `free -h` and `swapon --show`; Disk Swap must match the swap file/partition and Compressed Swap the zram device (or "none")
- [ ] `sudo ./dist/sys-telemetry-app-linux-amd64 run --port 9191 --interval 2s`, then `curl -s 127.0.0.1:9191/api/system` returns JSON with real values; `missing` is empty (or lists only sources your kernel lacks)
- [ ] `ss -ltn | grep 9191` shows 127.0.0.1:9191 only (not 0.0.0.0)
- [ ] Open http://127.0.0.1:9191 in a browser: CPU, RAM and Swap sections update every ~2s; with the network disconnected it still works
- [ ] Generate swap activity (e.g. `stress-ng --vm 2 --vm-bytes 90%` for 30s) and check swap-in/out rates and CPU % move in the dashboard
- [ ] Config: write `/etc/sys-telemetry-app/config.toml` with `port = 9292` and `interval = "3s"`; a plain `run` uses them (`curl 127.0.0.1:9292/api/info` shows `interval_seconds: 3`); `--port`/`--interval` flags override it; `--interval 500ms` is rejected
