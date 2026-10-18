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

// Applications view (default): sorting and filtering are done by /api/applications.
const APP_COLUMNS = [
  ["name", "Application", "text"], ["kind", "Kind", "text"],
  ["rss_bytes", "RSS", "bytes"], ["pss_bytes", "PSS", "bytes"], ["swap_bytes", "Swap", "bytes"],
  ["cpu_percent", "CPU %", "pct"], ["disk_read_bytes_per_sec", "Read/s", "bytes"],
  ["disk_write_bytes_per_sec", "Write/s", "bytes"], ["process_count", "Procs", "n"],
  ["primary_pids", "Primary PIDs", "pids"],
];
const appView = { sort: "pss_swap", order: "desc", open: new Set() };
function buildAppHeader() {
  const tr = document.querySelector("#app-table thead tr");
  APP_COLUMNS.forEach(([key, label, kind]) => {
    const th = document.createElement("th");
    th.textContent = label;
    th.dataset.key = key;
    if (kind === "text" || kind === "pids") th.className = "text";
    if (kind !== "pids") th.onclick = () => {
      appView.order = appView.sort === key && appView.order === "desc" ? "asc" : (appView.sort === key ? "desc" : (kind === "text" ? "asc" : "desc"));
      appView.sort = key;
      refreshApplications();
    };
    tr.appendChild(th);
  });
}
function appRow(a) {
  const tr = document.createElement("tr");
  tr.className = "app-row";
  tr.onclick = () => { appView.open.has(a.id) ? appView.open.delete(a.id) : appView.open.add(a.id); refreshApplications(); };
  APP_COLUMNS.forEach(([key, , kind]) => {
    const td = document.createElement("td");
    let v = key === "primary_pids" ? a.primary_pids.join(", ") : a[key];
    if (key === "name") v = (appView.open.has(a.id) ? "\u25BE " : "\u25B8 ") + v;
    td.textContent = kind === "pids" ? v : cell(v, kind);
    if (kind === "text" || kind === "pids") td.className = "text";
    tr.appendChild(td);
  });
  return tr;
}
function drilldown(a) {
  const tr = document.createElement("tr");
  tr.className = "drilldown";
  const td = document.createElement("td");
  td.colSpan = APP_COLUMNS.length;
  td.className = "text";
  const t = document.createElement("table");
  a.processes.forEach(p => {
    const r = document.createElement("tr");
    [p.pid, p.user, p.subgroup || "", cell(p.pss_bytes, "bytes"), cell(p.swap_bytes, "bytes"), p.cwd || "", p.command].forEach((v, i) => {
      const c = document.createElement("td");
      c.textContent = v;
      c.className = i >= 5 ? "text cmd-full" : (i === 1 || i === 2 ? "text" : "");
      if (i === 5) c.title = "working directory " + p.cwd + (p.cgroup ? "; cgroup " + p.cgroup : "");
      r.appendChild(c);
    });
    t.appendChild(r);
  });
  td.appendChild(t);
  tr.appendChild(td);
  return tr;
}
async function refreshApplications() {
  const q = new URLSearchParams({ sort: appView.sort, order: appView.order, filter: document.getElementById("app-filter").value });
  const err = document.getElementById("app-filter-error");
  try {
    const r = await fetch("/api/applications?" + q);
    if (!r.ok) { err.textContent = (await r.text()).trim(); return; }
    err.textContent = "";
    const l = await r.json();
    document.querySelectorAll("#app-table th").forEach(th => {
      th.textContent = APP_COLUMNS.find(c => c[0] === th.dataset.key)[1] + (th.dataset.key === appView.sort ? (appView.order === "desc" ? " \u25BE" : " \u25B4") : "");
    });
    const rows = [];
    l.applications.forEach(a => { rows.push(appRow(a)); if (appView.open.has(a.id)) rows.push(drilldown(a)); });
    document.querySelector("#app-table tbody").replaceChildren(...rows);
  } catch (e) { err.textContent = "disconnected"; }
}
buildAppHeader();
document.getElementById("app-filter").addEventListener("input", refreshApplications);
refreshApplications();
setInterval(refreshApplications, 2000);

// Advice panel: /api/advice is the whole story (Sustained is decided server side).
// Each item carries data-advice-id, the stable identity Hide/Snooze will key on.
function adviceFact(f) {
  if (f.value == null) return `${f.name}: ${f.text}`;
  const avg = f.avg == null ? "" : ` (avg ${f.avg.toFixed(1)})`;
  return `${f.name}: ${f.value.toFixed(1)} ${f.unit || ""}${avg}`;
}
function adviceItem(a) {
  const el = document.createElement("article");
  el.className = "advice sev-" + a.severity;
  el.dataset.adviceId = a.id;
  const h = document.createElement("h3");
  const badge = document.createElement("span");
  badge.className = "badge";
  badge.textContent = a.severity;
  h.append(badge, ` ${a.category === "hardware_limit" ? "Hardware Limit" : "Settings Fix"}: ${a.title}`);
  const p = document.createElement("p");
  p.textContent = a.summary;
  el.append(h, p);
  if (a.applications.length) {
    const ap = document.createElement("p");
    ap.textContent = "Applications: " + a.applications.map(x => x.name).join(", ");
    el.append(ap);
  }
  const d = document.createElement("details");
  const sm = document.createElement("summary");
  const e = a.evidence;
  sm.textContent = `Evidence over ${Math.round(e.window_seconds)}s (${new Date(e.start).toLocaleTimeString()} to ${new Date(e.end).toLocaleTimeString()})`;
  const ul = document.createElement("ul");
  e.facts.forEach(f => { const li = document.createElement("li"); li.textContent = adviceFact(f); ul.append(li); });
  d.append(sm, ul);
  el.append(d);
  if (a.change) {
    const pre = document.createElement("pre");
    pre.className = "change";
    pre.textContent = `# ${a.change.kind}: ${a.change.target}\n${a.change.content}`;
    el.append(pre);
  }
  return el;
}
async function refreshAdvice() {
  try {
    const r = await fetch("/api/advice");
    if (!r.ok) return;
    const s = await r.json();
    const list = document.getElementById("advice-list");
    list.replaceChildren(...(s.advice.length ? s.advice.map(adviceItem) : [Object.assign(document.createElement("p"), { textContent: "No active Advice." })]));
    document.getElementById("advice-pending").textContent = s.pending.length
      ? "Watching (not yet Sustained over " + Math.round(s.window_seconds) + "s): " + s.pending.map(p => `${p.title} (${Math.round(p.seconds_remaining)}s to go)`).join("; ")
      : "";
    document.getElementById("advice-disabled").replaceChildren(...s.disabled.map(x => Object.assign(document.createElement("li"), { textContent: `Disabled: ${x.title}: ${x.reason}` })));
  } catch (e) { /* the status line already shows a disconnect */ }
}
refreshAdvice();
setInterval(refreshAdvice, 2000);
