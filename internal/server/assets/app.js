const gib = b => (b / 2 ** 30).toFixed(1) + " GiB";
const fmt = v => (v == null ? "n/a" : v.toFixed(2));
function swapLine(name, s) {
  return s ? `${name}: ${gib(s.used_bytes)} of ${gib(s.total_bytes)}` : `${name}: not present`;
}
async function refresh() {
  try {
    const s = await (await fetch("/api/system")).json();
    document.getElementById("status").textContent = "sampled " + s.time;
    document.getElementById("cpu").textContent =
      `total ${s.cpu.total_percent == null ? "n/a" : s.cpu.total_percent.toFixed(1) + "%"}  load ${fmt(s.cpu.load1)} ${fmt(s.cpu.load5)} ${fmt(s.cpu.load15)}  cores ${s.cpu.cores}`;
    const m = s.memory;
    document.getElementById("ram").textContent = m
      ? `total ${gib(m.total_bytes)}  available ${gib(m.available_bytes)}  used ${gib(m.used_bytes)}  cached ${gib(m.cached_bytes)}  buffers ${gib(m.buffers_bytes)}  dirty ${(m.dirty_bytes / 2 ** 20).toFixed(1)} MiB`
      : "unavailable";
    const w = s.swap;
    document.getElementById("swap").textContent = [
      swapLine("Disk Swap", w.disk), swapLine("Compressed Swap", w.compressed),
      `in ${w.in_pages_per_sec == null ? "n/a" : w.in_pages_per_sec.toFixed(0)} pages/s  out ${w.out_pages_per_sec == null ? "n/a" : w.out_pages_per_sec.toFixed(0)} pages/s`,
    ].join("\n");
  } catch (e) {
    document.getElementById("status").textContent = "disconnected";
  }
}
refresh();
setInterval(refresh, 2000);
