const gib = b => (b / 2 ** 30).toFixed(1) + " GiB";
const fmt = v => (v == null ? "n/a" : v.toFixed(2));
const rates = (i, o) => (i == null || o == null ? null : `in ${i.toFixed(0)} pages/s  out ${o.toFixed(0)} pages/s`);
// A missing source is hidden (ADR 0006): its box disappears instead of showing zero or an error.
function show(id, text) {
  const pre = document.getElementById(id);
  pre.parentElement.hidden = text == null;
  if (text != null) pre.textContent = text;
}
function renderUnavailable(a) {
  const list = document.getElementById("unavailable-list");
  const rows = (a.unavailable || []).map(u => [u.feature, u.reason]);
  document.getElementById("unavailable").hidden = rows.length === 0;
  list.replaceChildren(...rows.map(([what, why]) => {
    const li = document.createElement("li");
    const b = document.createElement("b");
    b.textContent = what + ": ";
    li.append(b, why);
    return li;
  }));
}
async function refresh() {
  try {
    const s = await (await fetch("/api/system")).json();
    document.getElementById("status").textContent = "sampled " + s.time;
    renderUnavailable(s.availability || {});
    const c = s.cpu, cpu = [];
    if (c.total_percent != null) cpu.push(`total ${c.total_percent.toFixed(1)}%`);
    if (c.load1 != null) cpu.push(`load ${fmt(c.load1)} ${fmt(c.load5)} ${fmt(c.load15)}`);
    if (c.cores) cpu.push(`cores ${c.cores}`);
    show("cpu", cpu.length ? cpu.join("  ") : null);
    const m = s.memory;
    show("ram", m ? `total ${gib(m.total_bytes)}  available ${gib(m.available_bytes)}  used ${gib(m.used_bytes)}  cached ${gib(m.cached_bytes)}  buffers ${gib(m.buffers_bytes)}  dirty ${(m.dirty_bytes / 2 ** 20).toFixed(1)} MiB` : null);
    const w = s.swap, lines = [];
    if (w.disk) lines.push(`Disk Swap: ${gib(w.disk.used_bytes)} of ${gib(w.disk.total_bytes)}`);
    if (w.compressed) lines.push(`Compressed Swap: ${gib(w.compressed.used_bytes)} of ${gib(w.compressed.total_bytes)}`);
    const r = rates(w.in_pages_per_sec, w.out_pages_per_sec);
    if (r && lines.length) lines.push(r);
    show("swap", lines.length ? lines.join("\n") : null);
    const gs = s.gpus || [];
    show("gpu", gs.length ? gs.map(g =>
      `${g.name} (${g.vendor})  util ${g.util_percent == null ? "n/a" : g.util_percent.toFixed(0) + "%"}  temp ${g.temperature_celsius == null ? "n/a" : g.temperature_celsius.toFixed(0) + " C"}  VRAM ${g.vram_total_bytes == null ? "n/a" : gib(g.vram_used_bytes) + " of " + gib(g.vram_total_bytes)}`).join("\n") : null);
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
// A column no row has a value for is hidden (ADR 0006), not filled with "n/a".
const visible = (cols, rows) => cols.filter(([key]) => rows.length === 0 || rows.some(r => r[key] != null));
function renderHead(sel, cols, view, again) {
  document.querySelector(sel + " thead tr").replaceChildren(...cols.map(([key, label, kind]) => {
    const th = document.createElement("th");
    th.textContent = label + (key === view.sort ? (view.order === "desc" ? " \u25BE" : " \u25B4") : "");
    const textual = kind === "text" || kind === "cmd" || kind === "pids";
    if (textual) th.className = "text";
    if (kind !== "pids") th.onclick = () => {
      view.order = view.sort === key ? (view.order === "desc" ? "asc" : "desc") : (textual ? "asc" : "desc");
      view.sort = key;
      again();
    };
    return th;
  }));
}
async function refreshProcesses() {
  const q = new URLSearchParams({ sort: procView.sort, order: procView.order, filter: document.getElementById("filter").value });
  const err = document.getElementById("filter-error");
  try {
    const r = await fetch("/api/processes?" + q);
    if (!r.ok) { err.textContent = (await r.text()).trim(); return; }
    err.textContent = "";
    const l = await r.json();
    const cols = visible(COLUMNS, l.processes);
    renderHead("#proc-table", cols, procView, refreshProcesses);
    const body = document.querySelector("#proc-table tbody");
    body.replaceChildren(...l.processes.map(p => {
      const tr = document.createElement("tr");
      cols.forEach(([key, , kind]) => {
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
function appRow(a, cols) {
  const tr = document.createElement("tr");
  tr.className = "app-row";
  tr.onclick = () => { appView.open.has(a.id) ? appView.open.delete(a.id) : appView.open.add(a.id); refreshApplications(); };
  cols.forEach(([key, , kind]) => {
    const td = document.createElement("td");
    let v = key === "primary_pids" ? a.primary_pids.join(", ") : a[key];
    if (key === "name") v = (appView.open.has(a.id) ? "\u25BE " : "\u25B8 ") + v;
    td.textContent = kind === "pids" ? v : cell(v, kind);
    if (kind === "text" || kind === "pids") td.className = "text";
    tr.appendChild(td);
  });
  return tr;
}
function drilldown(a, cols) {
  const tr = document.createElement("tr");
  tr.className = "drilldown";
  const td = document.createElement("td");
  td.colSpan = cols.length;
  td.className = "text";
  const t = document.createElement("table");
  a.processes.forEach(p => {
    const r = document.createElement("tr");
    [p.pid, p.user, p.role || p.subgroup || "", cell(p.pss_bytes, "bytes"), cell(p.swap_bytes, "bytes"), p.cwd || "", p.command].forEach((v, i) => {
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
    const cols = visible(APP_COLUMNS, l.applications);
    renderHead("#app-table", cols, appView, refreshApplications);
    const rows = [];
    l.applications.forEach(a => { rows.push(appRow(a, cols)); if (appView.open.has(a.id)) rows.push(drilldown(a, cols)); });
    document.querySelector("#app-table tbody").replaceChildren(...rows);
  } catch (e) { err.textContent = "disconnected"; }
}
document.getElementById("app-filter").addEventListener("input", refreshApplications);
refreshApplications();
setInterval(refreshApplications, 2000);

// Advice panel: /api/advice is the whole story (Sustained is decided server side).
// Each item carries data-advice-id, the stable identity Hide/Snooze key on.
function adviceFact(f) {
  if (f.value == null) return `${f.name}: ${f.text}`;
  const avg = f.avg == null ? "" : ` (avg ${f.avg.toFixed(1)})`;
  return `${f.name}: ${f.value.toFixed(1)} ${f.unit || ""}${avg}`;
}
async function adviceAction(name, body) {
  try {
    const r = await fetch("/api/advice/" + name, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (!r.ok) alert((await r.text()).trim());
  } catch (e) { alert("could not reach the program"); }
  refreshAdvice();
}
function adviceControls(a, presets) {
  const c = document.createElement("div");
  c.className = "advice-controls";
  const hide = Object.assign(document.createElement("button"), { textContent: "Hide", title: "Hidden until the Severity rises above " + a.severity });
  hide.onclick = () => adviceAction("hide", { id: a.id });
  const sel = document.createElement("select");
  sel.setAttribute("aria-label", "Snooze period");
  presets.forEach(p => sel.append(Object.assign(document.createElement("option"), { value: p.label, textContent: "Snooze " + p.label })));
  const sn = Object.assign(document.createElement("button"), { textContent: "Snooze" });
  sn.onclick = () => adviceAction("snooze", { id: a.id, preset: sel.value });
  c.append(hide, sel, sn);
  return c;
}
function hiddenItem(h) {
  const li = document.createElement("li");
  li.dataset.adviceId = h.id;
  li.append(h.state === "snoozed"
    ? `Snoozed until ${new Date(h.until).toLocaleString()}: ${h.title} `
    : `Hidden (until Severity rises above ${h.severity}): ${h.title} `);
  const b = Object.assign(document.createElement("button"), { textContent: "Unhide" });
  b.onclick = () => adviceAction("unhide", { id: h.id });
  li.append(b);
  return li;
}
function adviceItem(a, presets) {
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
  el.append(adviceControls(a, presets));
  return el;
}
async function refreshAdvice() {
  try {
    const r = await fetch("/api/advice");
    if (!r.ok) return;
    const s = await r.json();
    const list = document.getElementById("advice-list");
    if (document.activeElement && document.activeElement.tagName === "SELECT" && list.contains(document.activeElement)) return; // keep the open snooze choice
    list.replaceChildren(...(s.advice.length ? s.advice.map(a => adviceItem(a, s.snooze_presets)) : [Object.assign(document.createElement("p"), { textContent: "No active Advice." })]));
    document.getElementById("advice-pending").textContent = s.pending.length
      ? "Watching (not yet Sustained over " + Math.round(s.window_seconds) + "s): " + s.pending.map(p => `${p.title} (${Math.round(p.seconds_remaining)}s to go)`).join("; ")
      : "";
    document.getElementById("advice-hidden").replaceChildren(...s.hidden.map(hiddenItem));
    document.getElementById("advice-disabled").replaceChildren(...s.disabled.map(x => Object.assign(document.createElement("li"), { textContent: `Disabled: ${x.title}: ${x.reason}` })));
  } catch (e) { /* the status line already shows a disconnect */ }
}
refreshAdvice();
setInterval(refreshAdvice, 2000);
