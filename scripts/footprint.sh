#!/usr/bin/env bash
# Footprint budget check (SPEC): < 30 MB RAM, < 0.5% average CPU, dashboard closed.
# Measures an already-running sys-telemetry-app, or starts one, from outside the
# program by sampling /proc/<pid>. Prints PASS or FAIL per budget; exit 1 on FAIL.
#
#   scripts/footprint.sh [--duration SECONDS] [--interval SECONDS] [--pid PID | --binary PATH]
#
# Defaults: duration 1800 (30 min), interval 10, --binary dist/sys-telemetry-app-linux-<arch>.
# RAM is the peak resident set (VmRSS) seen; CPU is the average over the whole run
# as a percentage of one core. Override budgets with RAM_BUDGET_MB / CPU_BUDGET_PCT.
set -u
duration=1800; interval=10; pid=""; bin=""
ram_budget_mb=${RAM_BUDGET_MB:-30}; cpu_budget=${CPU_BUDGET_PCT:-0.5}
while [ $# -gt 0 ]; do
  case "$1" in
    --duration) duration=$2; shift 2;;
    --interval) interval=$2; shift 2;;
    --pid) pid=$2; shift 2;;
    --binary) bin=$2; shift 2;;
    -h|--help) sed -n 2,10p "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

started=""
cleanup() { [ -n "$started" ] && kill "$started" 2>/dev/null; }
trap cleanup EXIT
if [ -z "$pid" ]; then
  if [ -z "$bin" ]; then
    case "$(uname -m)" in aarch64|arm64) a=arm64;; *) a=amd64;; esac
    bin="$(dirname "$0")/../dist/sys-telemetry-app-linux-$a"
  fi
  [ -x "$bin" ] || { echo "binary not found: $bin (run make build)" >&2; exit 2; }
  "$bin" run --port "${PORT:-0}" >/dev/null 2>&1 &
  started=$!; pid=$started
  sleep 2
fi
[ -r "/proc/$pid/stat" ] || { echo "no process $pid" >&2; exit 2; }

hz=$(getconf CLK_TCK)
# utime+stime in clock ticks; comm may contain spaces/parens, so cut after the last ')'.
cpu_ticks() { sed 's/.*) //' "/proc/$pid/stat" | awk '{print $12+$13}'; }
rss_kb() { awk '/^VmRSS:/{print $2}' "/proc/$pid/status"; }
now() { date +%s.%N; }

t0=$(now); c0=$(cpu_ticks); peak=0; n=0; sum=0; end=$(( $(date +%s) + duration ))
echo "measuring pid $pid for ${duration}s (every ${interval}s)"
while [ "$(date +%s)" -lt "$end" ]; do
  sleep "$interval"
  [ -r "/proc/$pid/stat" ] || { echo "FAIL: process $pid exited during the run"; exit 1; }
  r=$(rss_kb); [ -n "$r" ] || continue
  n=$((n+1)); sum=$((sum+r)); [ "$r" -gt "$peak" ] && peak=$r
done
t1=$(now); c1=$(cpu_ticks)
[ "$n" -gt 0 ] || { echo "no samples taken" >&2; exit 2; }

cpu=$(awk -v c0="$c0" -v c1="$c1" -v t0="$t0" -v t1="$t1" -v hz="$hz" 'BEGIN{printf "%.3f", (c1-c0)/hz/(t1-t0)*100}')
peak_mb=$(awk -v k="$peak" 'BEGIN{printf "%.1f", k/1024}')
avg_mb=$(awk -v s="$sum" -v n="$n" 'BEGIN{printf "%.1f", s/n/1024}')
rc=0
if awk -v v="$peak_mb" -v b="$ram_budget_mb" 'BEGIN{exit !(v<b)}'; then ramv=PASS; else ramv=FAIL; rc=1; fi
if awk -v v="$cpu" -v b="$cpu_budget" 'BEGIN{exit !(v<b)}'; then cpuv=PASS; else cpuv=FAIL; rc=1; fi
echo "samples: $n over $(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.0f", b-a}')s"
echo "RAM  peak ${peak_mb} MB (avg ${avg_mb} MB), budget < ${ram_budget_mb} MB: $ramv"
echo "CPU  avg ${cpu}% of one core, budget < ${cpu_budget}%: $cpuv"
[ $rc -eq 0 ] && echo "PASS" || echo "FAIL"
exit $rc
