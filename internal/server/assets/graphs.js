// Graphs view: live charts fed by /api/live (the last 5 minutes) and then
// /api/live/stream (one Point per sample). Streaming runs only while this page
// is visible, so a hidden tab costs the server nothing.
//
// To add a chart (for example GPU, ticket 9): add a field to live.Point on the
// server, then add one entry to CHARTS. Each series reads one Point key and
// may scale it. A chart whose series never have a value stays hidden (ADR 0006).
const GIB = 2 ** 30, MIB = 2 ** 20;
const CHARTS = [
  { id: "cpu", title: "CPU", unit: "%", max: 100, series: [
    { label: "CPU total", key: "cpu_percent", color: "#2a7de1" },
    { label: "CPU pressure", key: "cpu_pressure_some_percent", color: "#e08a00" }] },
  { id: "ram", title: "RAM vs swap", unit: "GiB", series: [
    { label: "RAM used", key: "ram_used_bytes", scale: 1 / GIB, color: "#2a7de1" },
    { label: "Disk Swap", key: "disk_swap_used_bytes", scale: 1 / GIB, color: "#d1342f" },
    { label: "Compressed Swap", key: "compressed_swap_used_bytes", scale: 1 / GIB, color: "#8a55d6" }] },
  { id: "mempsi", title: "Memory pressure", unit: "%", max: 100, series: [
    { label: "some", key: "memory_pressure_some_percent", color: "#e08a00" },
    { label: "full", key: "memory_pressure_full_percent", color: "#d1342f" }] },
  { id: "iopsi", title: "I/O pressure", unit: "%", max: 100, series: [
    { label: "some", key: "io_pressure_some_percent", color: "#e08a00" },
    { label: "full", key: "io_pressure_full_percent", color: "#d1342f" }] },
  { id: "disk", title: "Disk throughput", unit: "MiB/s", series: [
    { label: "read", key: "disk_read_bytes_per_sec", scale: 1 / MIB, color: "#2a7de1" },
    { label: "write", key: "disk_write_bytes_per_sec", scale: 1 / MIB, color: "#d1342f" }] },
  { id: "swapio", title: "Swap-in / swap-out", unit: "pages/s", series: [
    { label: "swap-in", key: "swap_in_pages_per_sec", color: "#2a7de1" },
    { label: "swap-out", key: "swap_out_pages_per_sec", color: "#d1342f" }] },
  { id: "gpu", title: "GPU", unit: "% / \u00b0C", series: [
    { label: "GPU utilisation %", key: "gpu_util_percent", color: "#2a7de1" },
    { label: "GPU temperature \u00b0C", key: "gpu_temperature_celsius", color: "#d1342f" }] },
  { id: "vram", title: "GPU memory (VRAM)", unit: "GiB", series: [
    { label: "VRAM used", key: "gpu_vram_used_bytes", scale: 1 / GIB, color: "#8a55d6" },
    { label: "VRAM total", key: "gpu_vram_total_bytes", scale: 1 / GIB, color: "#888888" }] },
];
const WINDOW_MS = 300 * 1000;
let points = [];
let charts = [];
let es = null;
let markerEvents = []; // Advice events: from /api/advice/events when Live, from History when in Replay
let markerList = [];   // the ones inside the charted window, ready to draw
const MARKER_COLORS = { advice_start: "#d1342f", advice_end: "#2a9d4b", advice_severity: "#e08a00" };
// Advice start/end markers on the swap-in/swap-out chart (and listed below the charts in words).
function markerPlugin() {
  return { hooks: { draw: [u => {
    const ctx = u.ctx, top = u.bbox.top, bottom = u.bbox.top + u.bbox.height;
    ctx.save();
    for (const m of markerList) {
      const x = Math.round(u.valToPos(m.t, "x", true));
      if (x < u.bbox.left || x > u.bbox.left + u.bbox.width) continue;
      ctx.strokeStyle = MARKER_COLORS[m.kind] || "#888";
      ctx.lineWidth = 2 * devicePixelRatio;
      ctx.setLineDash(m.kind === "advice_end" ? [4 * devicePixelRatio, 3 * devicePixelRatio] : []);
      ctx.beginPath(); ctx.moveTo(x, top); ctx.lineTo(x, bottom); ctx.stroke();
    }
    ctx.restore();
  }] } };
}
function refreshMarkers() {
  const xs = points.map(p => Date.parse(p.time));
  markerList = xs.length ? Replay.markers(markerEvents, Math.min(...xs), Math.max(...xs)) : [];
  document.getElementById("marker-list").replaceChildren(...markerList.map(m => {
    const li = document.createElement("li");
    li.textContent = m.label;
    li.style.color = MARKER_COLORS[m.kind] || "";
    return li;
  }));
}
async function loadLiveMarkers() {
  try {
    const r = await fetch("/api/advice/events", { cache: "no-store" });
    if (r.ok) markerEvents = (await r.json()).events || [];
  } catch (e) { /* markers are optional */ }
  render();
}

function seriesData(chart) {
  const xs = points.map(p => Date.parse(p.time) / 1000);
  return [xs, ...chart.series.map(s => points.map(p => {
    const v = p[s.key];
    return v == null ? null : v * (s.scale || 1);
  }))];
}
function textColor() { return getComputedStyle(document.body).color; }
function buildCharts() {
  const host = document.getElementById("graph-grid");
  host.replaceChildren();
  charts = CHARTS.map(c => {
    const box = document.createElement("div");
    box.className = "chart";
    box.hidden = true;
    box.innerHTML = `<h3>${c.title} <small>${c.unit}</small></h3>`;
    const plot = document.createElement("div");
    box.appendChild(plot);
    host.appendChild(box);
    const fg = textColor(), grid = "rgba(128,128,128,.25)";
    const u = new uPlot({
      width: Math.max(box.clientWidth, 260), height: 140,
      legend: { show: true },
      cursor: { drag: { x: false, y: false } },
      scales: { x: { time: true }, y: { range: (_, lo, hi) => [0, Math.max(c.max || 0, hi || 1) * (c.max ? 1 : 1.1)] } },
      axes: [{ stroke: fg, grid: { stroke: grid } }, { stroke: fg, grid: { stroke: grid }, size: 50 }],
      series: [{}, ...c.series.map(s => ({ label: s.label, stroke: s.color, width: 1.5, spanGaps: false }))],
      plugins: c.id === "swapio" ? [markerPlugin()] : [],
    }, seriesData(c), plot);
    return { c, box, u, plot };
  });
}
function render() {
  const now = points.length ? Date.parse(points[points.length - 1].time) : 0;
  if (!Replay.active()) points = points.filter(p => Date.parse(p.time) >= now - WINDOW_MS);
  refreshMarkers();
  for (const ch of charts) {
    ch.box.hidden = !ch.c.series.some(s => points.some(p => p[s.key] != null));
    if (ch.box.hidden) continue;
    const w = Math.max(ch.box.clientWidth, 260);
    if (ch.u.width !== w) ch.u.setSize({ width: w, height: 140 });
    ch.u.setData(seriesData(ch.c));
  }
  renderSplit();
}
// Top 10 Applications: RAM (PSS) and swapped-out memory as one stacked bar each.
function renderSplit() {
  const host = document.getElementById("split");
  const last = points[points.length - 1];
  let apps = last ? last.top_apps : [];
  if (Replay.active()) { // the Applications recorded in History at the selected time
    const at = Replay.state.atResult;
    apps = at && at.sample ? at.sample.applications.filter(a => a.pss_bytes != null).slice(0, 10).map(a => ({ name: a.name, pss_bytes: a.pss_bytes, swap_bytes: a.swap_bytes || 0 })) : [];
  }
  document.getElementById("split-box").hidden = apps.length === 0;
  const max = Math.max(1, ...apps.map(a => a.pss_bytes + a.swap_bytes));
  host.replaceChildren(...apps.map(a => {
    const row = document.createElement("div");
    row.className = "split-row";
    const name = document.createElement("span");
    name.className = "split-name";
    name.textContent = a.name;
    const bar = document.createElement("span");
    bar.className = "split-bar";
    const ram = document.createElement("i"), sw = document.createElement("b");
    ram.style.width = (100 * a.pss_bytes / max) + "%";
    sw.style.width = (100 * a.swap_bytes / max) + "%";
    ram.title = "RAM (PSS) " + (a.pss_bytes / MIB).toFixed(0) + " MiB";
    sw.title = "swapped out " + (a.swap_bytes / MIB).toFixed(0) + " MiB";
    bar.append(ram, sw);
    const num = document.createElement("span");
    num.className = "split-num";
    num.textContent = (a.pss_bytes / MIB).toFixed(0) + " + " + (a.swap_bytes / MIB).toFixed(0) + " MiB";
    row.append(name, bar, num);
    return row;
  }));
}
async function loadAndStream() {
  try {
    const d = await (await fetch("/api/live", { cache: "no-store" })).json();
    points = d.points || [];
    render();
  } catch (e) { /* retried on the next connect */ }
  loadLiveMarkers();
  openStream();
}
function openStream() {
  if (es || document.hidden || Replay.active()) return;
  es = new EventSource("/api/live/stream");
  es.onmessage = ev => {
    const p = JSON.parse(ev.data);
    const last = points[points.length - 1];
    if (last && Date.parse(p.time) <= Date.parse(last.time)) return; // already in the snapshot
    points.push(p);
    render();
  };
  document.getElementById("graph-status").textContent = "live";
  es.onerror = () => { document.getElementById("graph-status").textContent = "reconnecting"; };
}
function closeStream() {
  if (es) { es.close(); es = null; }
  document.getElementById("graph-status").textContent = "paused (tab hidden)";
}
buildCharts();
document.addEventListener("visibilitychange", () => document.hidden ? closeStream() : (Replay.active() ? null : loadAndStream()));
setInterval(() => { if (!Replay.active() && !document.hidden) loadLiveMarkers(); }, 10000);
// Called by timeslider.js. Replay draws the History series around the selected time; Live reloads the Live buffer.
function showReplayGraphs(series) {
  if (es) { es.close(); es = null; }
  document.getElementById("graph-status").textContent = "replay";
  points = series.points || [];
  markerEvents = series.events || [];
  render();
}
function showLiveGraphs() {
  document.getElementById("graph-status").textContent = "live";
  loadAndStream();
}
window.addEventListener("resize", render);
if (!document.hidden) loadAndStream();
