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

// Processes view: sorting and filtering are done by /api/processes.
const COLUMNS = [
  ["pid", "PID", "text"], ["user", "User", "text"], ["name", "Name", "text"],
  ["rss_bytes", "RSS", "bytes"], ["pss_bytes", "PSS", "bytes"], ["swap_bytes", "Swap", "bytes"],
  ["cpu_percent", "CPU %", "pct"], ["disk_read_bytes_per_sec", "Read/s", "bytes"],
  ["disk_write_bytes_per_sec", "Write/s", "bytes"], ["threads", "Threads", "n"],
  ["open_fds", "FDs", "n"], ["command", "Command", "cmd"],
];
const procView = { sort: "pss_swap", order: "desc" };
const mib = b => (b / 2 ** 20).toFixed(1) + " MiB";
function cell(v, kind) {
  if (v == null) return "n/a";
  if (kind === "bytes") return mib(v);
  if (kind === "pct") return v.toFixed(1);
  return String(v);
}
function buildHeader() {
  const tr = document.querySelector("#proc-table thead tr");
  COLUMNS.forEach(([key, label, kind]) => {
    const th = document.createElement("th");
    th.textContent = label;
    th.dataset.key = key;
    if (kind === "text" || kind === "cmd") th.className = "text";
    th.onclick = () => {
      procView.order = procView.sort === key && procView.order === "desc" ? "asc" : (procView.sort === key ? "desc" : (kind === "text" || kind === "cmd" ? "asc" : "desc"));
      procView.sort = key;
      refreshProcesses();
    };
    tr.appendChild(th);
  });
}
async function refreshProcesses() {
  const q = new URLSearchParams({ sort: procView.sort, order: procView.order, filter: document.getElementById("filter").value });
  const err = document.getElementById("filter-error");
  try {
    const r = await fetch("/api/processes?" + q);
    if (!r.ok) { err.textContent = (await r.text()).trim(); return; }
    err.textContent = "";
    const l = await r.json();
    document.querySelectorAll("#proc-table th").forEach(th => {
      th.textContent = COLUMNS.find(c => c[0] === th.dataset.key)[1] + (th.dataset.key === procView.sort ? (procView.order === "desc" ? " \u25BE" : " \u25B4") : "");
    });
    const body = document.querySelector("#proc-table tbody");
    body.replaceChildren(...l.processes.map(p => {
      const tr = document.createElement("tr");
      COLUMNS.forEach(([key, , kind]) => {
        const td = document.createElement("td");
        td.textContent = cell(p[key], kind);
        if (kind === "text" || kind === "cmd") td.className = kind === "cmd" ? "text cmd" : "text";
        if (kind === "cmd") td.title = p[key];
        tr.appendChild(td);
      });
      return tr;
    }));
  } catch (e) { err.textContent = "disconnected"; }
}
buildHeader();
document.getElementById("filter").addEventListener("input", refreshProcesses);
refreshProcesses();
setInterval(refreshProcesses, 2000);
