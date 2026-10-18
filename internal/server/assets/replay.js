// Replay: the time slider's logic as small plain functions with no DOM access,
// so they can be run with node (see replay_check_test.go, which runs them when node
// is installed). The page scripts (app.js, graphs.js) call them; nothing here
// fetches or touches the page.
//
// History keeps, per 10-second sample, the top 10 Applications by Working Set
// (PSS + swap) with up to 8 Processes each. Everything built here from a sample
// says so (truncationNotes) instead of presenting the rest as absent.
const Replay = (() => {
  const state = { at: null, atResult: null, span: null, series: null };

  // The slider runs from the oldest recorded sample (0) to Live (max).
  function positionToTime(pos, max, oldestMs, newestMs) {
    if (pos >= max || !(newestMs > oldestMs)) return null; // null means Live
    return Math.round(oldestMs + (newestMs - oldestMs) * pos / max);
  }
  function timeToPosition(ms, max, oldestMs, newestMs) {
    if (!(newestMs > oldestMs)) return max;
    return Math.max(0, Math.min(max, Math.round((ms - oldestMs) / (newestMs - oldestMs) * max)));
  }
  const active = () => state.at !== null;

  const pad = n => String(n).padStart(2, "0");
  const clock = ms => { const d = new Date(ms); return pad(d.getHours()) + ":" + pad(d.getMinutes()); };
  const clockSeconds = ms => clock(ms) + ":" + pad(new Date(ms).getSeconds());
  function whenLabel(ms, nowMs) {
    const d = new Date(ms), n = new Date(nowMs);
    const sameDay = d.getFullYear() === n.getFullYear() && d.getMonth() === n.getMonth() && d.getDate() === n.getDate();
    return sameDay ? clockSeconds(ms) : d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) + " " + clockSeconds(ms);
  }

  function filterRegex(f) {
    if (!f) return null;
    try { return new RegExp(f, "i"); } catch (e) { throw new Error("invalid filter: " + e.message); }
  }
  const wsOf = o => (o.pss_bytes || 0) + (o.swap_bytes || 0);
  function sortRows(rows, sort, order) {
    const key = sort === "pss_swap" ? wsOf : r => r[sort];
    const dir = order === "asc" ? 1 : -1;
    return rows.slice().sort((a, b) => {
      const x = key(a), y = key(b);
      if (x == null && y == null) return 0;
      if (x == null) return 1; // missing values last in either order
      if (y == null) return -1;
      return (x < y ? -1 : x > y ? 1 : 0) * dir;
    });
  }

  // Applications as /api/applications shapes them, built from one History sample.
  function applicationsFrom(sample, opts) {
    if (!sample) return [];
    const re = filterRegex(opts.filter);
    let rows = sample.applications.map(a => ({
      id: a.name, name: a.name, kind: a.kind, rss_bytes: a.rss_bytes, pss_bytes: a.pss_bytes, swap_bytes: a.swap_bytes,
      cpu_percent: a.cpu_percent, process_count: a.process_count, primary_pids: null,
      omitted_processes: a.omitted_processes || 0,
      processes: (a.processes || []).map(p => ({ pid: p.pid, user: p.user || "", command: p.command, pss_bytes: p.pss_bytes, swap_bytes: p.swap_bytes, cpu_percent: p.cpu_percent })),
    }));
    if (re) rows = rows.filter(a => re.test(a.name) || a.processes.some(p => re.test(p.command)));
    return sortRows(rows, opts.sort, opts.order);
  }
  const programName = cmd => { const first = (cmd || "").split(" ")[0]; return first.slice(first.lastIndexOf("/") + 1); };
  // Processes as /api/processes shapes them: only the recorded ones (up to 8 in each of the top 10 Applications).
  function processesFrom(sample, opts) {
    if (!sample) return [];
    const re = filterRegex(opts.filter);
    let rows = [];
    sample.applications.forEach(a => (a.processes || []).forEach(p => rows.push({
      pid: p.pid, user: p.user || "", name: programName(p.command), rss_bytes: null, pss_bytes: p.pss_bytes, swap_bytes: p.swap_bytes,
      cpu_percent: p.cpu_percent, command: p.command, application: a.name,
    })));
    if (re) rows = rows.filter(p => re.test(String(p.pid)) || re.test(p.name) || re.test(p.command));
    return sortRows(rows, opts.sort, opts.order);
  }

  // What the selected sample leaves out, in words. at is the /api/history/at answer.
  function truncationNotes(at) {
    if (!at || !at.found) return { applications: "", processes: "" };
    const s = at.sample, shown = at.shown_applications, total = at.total_applications;
    const procs = s.applications.reduce((n, a) => n + (a.processes || []).length, 0);
    const all = s.applications.reduce((n, a) => n + a.process_count, 0);
    const trimmed = s.applications.filter(a => (a.omitted_processes || 0) > 0).length;
    return {
      applications: total > shown
        ? `History keeps only the top ${shown} Applications by Working Set (RAM + swap). Showing ${shown} of ${total}; the other ${total - shown} were not recorded.`
        : `Showing all ${total} Applications recorded at this time.`,
      processes: `History keeps up to 8 Processes per Application, biggest first, for the top ${shown} Applications only. Showing ${procs} of the ${all} Processes in those Applications` +
        (trimmed ? ` (${trimmed} Application${trimmed === 1 ? "" : "s"} cut to 8)` : "") +
        (total > shown ? `, and none from the ${total - shown} unrecorded Applications` : "") + ".",
    };
  }
  const appProcessNote = a => a.processes.length < a.process_count
    ? `Showing ${a.processes.length} of ${a.process_count} Processes: History keeps the biggest ones only.`
    : "";

  function noHistoryMessage(at) {
    if (!at || at.found) return "";
    const gap = [];
    if (at.previous_sample) gap.push("after " + clockSeconds(Date.parse(at.previous_sample)));
    if (at.next_sample) gap.push("before " + clockSeconds(Date.parse(at.next_sample)));
    return "No History was recorded at this time (the program was not running" + (gap.length ? ", nothing " + gap.join(" and ") : "") + ").";
  }

  // Advice start/end markers (and Severity changes) for the swap-in/swap-out chart.
  const VERB = { advice_start: "started", advice_end: "ended" };
  function markers(events, fromMs, toMs) {
    return (events || []).map(e => ({ ms: Date.parse(e.time), e }))
      .filter(m => m.ms >= fromMs && m.ms <= toMs)
      .map(m => ({
        t: m.ms / 1000, kind: m.e.kind, severity: m.e.severity,
        label: `${m.e.title} ${VERB[m.e.kind] || "now " + m.e.severity} ${clock(m.ms)}`,
      }));
  }

  // The Hide/Snooze controls change state, so they exist only in Live.
  const controlsAllowed = () => !active();

  return { state, positionToTime, timeToPosition, active, clock, clockSeconds, whenLabel, applicationsFrom, processesFrom,
    truncationNotes, appProcessNote, noHistoryMessage, markers, controlsAllowed };
})();
if (typeof module !== "undefined") module.exports = Replay;
