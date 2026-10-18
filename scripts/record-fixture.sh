#!/bin/sh
# Record the files the Collector reads from this machine into a fixture dir.
# Usage: scripts/record-fixture.sh testdata/fixtures/<name>
set -eu
d=${1:?fixture dir}
mkdir -p "$d/proc" "$d/sys/block"
for f in meminfo stat loadavg vmstat swaps; do cp "/proc/$f" "$d/proc/$f" 2>/dev/null || true; done
for z in /sys/block/zram*; do [ -d "$z" ] || continue; n=$(basename "$z"); mkdir -p "$d/sys/block/$n"
  for f in mm_stat disksize; do cp "$z/$f" "$d/sys/block/$n/$f" 2>/dev/null || true; done; done
mkdir -p "$d/sys/module/zswap/parameters"; cp /sys/module/zswap/parameters/enabled "$d/sys/module/zswap/parameters/" 2>/dev/null || true
