// Run with: node replay_logic.js <path to assets dir>. Exercises replay.js with no DOM.
const assert = require("assert");
const Replay = require(process.argv[2] + "/replay.js");

// slider <-> time
assert.strictEqual(Replay.positionToTime(1000, 1000, 1000, 2000), null, "far right is Live");
assert.strictEqual(Replay.positionToTime(0, 1000, 1000, 2000), 1000);
assert.strictEqual(Replay.positionToTime(500, 1000, 1000, 2000), 1500);
assert.strictEqual(Replay.positionToTime(10, 1000, 5, 5), null, "no span means Live");
assert.strictEqual(Replay.timeToPosition(1500, 1000, 1000, 2000), 500);
assert.strictEqual(Replay.timeToPosition(0, 1000, 1000, 2000), 0, "clamped");

const proc = (pid, command, pss, swap) => ({ pid, user: "u", command, pss_bytes: pss, swap_bytes: swap, cpu_percent: 1 });
const app = (name, pss, swap, count, procs, omitted) => ({ name, kind: "program", rss_bytes: pss, pss_bytes: pss, swap_bytes: swap, cpu_percent: 2, process_count: count, processes: procs, omitted_processes: omitted });
const sample = { time: "2026-10-19T23:10:00Z", applications: [
  app("browser", 500, 0, 30, Array.from({ length: 8 }, (_, i) => proc(100 + i, "/usr/bin/browser --tab " + i, 60 - i, 0)), 22),
  app("editor", 100, 400, 2, [proc(7, "/opt/editor/bin/editor", 100, 400), proc(8, "node server.js", 5, 0)], 0),
] };

let rows = Replay.applicationsFrom(sample, { sort: "pss_swap", order: "desc" });
assert.deepStrictEqual(rows.map(r => r.name), ["browser", "editor"]);
rows = Replay.applicationsFrom(sample, { sort: "swap_bytes", order: "desc" });
assert.strictEqual(rows[0].name, "editor");
rows = Replay.applicationsFrom(sample, { sort: "pss_swap", order: "desc", filter: "server\\.js" });
assert.deepStrictEqual(rows.map(r => r.name), ["editor"], "filter matches command lines");
assert.throws(() => Replay.applicationsFrom(sample, { sort: "name", order: "asc", filter: "(" }), /invalid filter/);
assert.deepStrictEqual(Replay.applicationsFrom(null, { sort: "name", order: "asc" }), []);

let procs = Replay.processesFrom(sample, { sort: "pss_swap", order: "desc" });
assert.strictEqual(procs.length, 10, "only the recorded Processes");
assert.strictEqual(procs[0].name, "editor", "named after the program");
assert.strictEqual(Replay.processesFrom(sample, { sort: "pid", order: "asc", filter: "^107$" })[0].pid, 107);

// truncation is stated, not hidden
let at = { found: true, sample, shown_applications: 2, total_applications: 23 };
let n = Replay.truncationNotes(at);
assert.match(n.applications, /top 2 Applications/);
assert.match(n.applications, /2 of 23/);
assert.match(n.applications, /other 21 were not recorded/);
assert.match(n.processes, /10 of the 32 Processes/);
assert.match(n.processes, /1 Application cut to 8/);
assert.match(n.processes, /none from the 21 unrecorded/);
at = { found: true, sample, shown_applications: 2, total_applications: 2 };
assert.match(Replay.truncationNotes(at).applications, /all 2 Applications/);
assert.match(Replay.appProcessNote(rows.find(r => r.name === "browser") || Replay.applicationsFrom(sample, { sort: "name", order: "asc" })[0]), /8 of 30 Processes/);
assert.strictEqual(Replay.appProcessNote(Replay.applicationsFrom(sample, { sort: "pss_swap", order: "desc" }).find(r => r.name === "editor")), "");
assert.match(Replay.noHistoryMessage({ found: false, previous_sample: "2026-10-19T10:00:00Z", next_sample: "2026-10-19T11:00:00Z" }), /not running, nothing after .* and before/);
assert.strictEqual(Replay.noHistoryMessage({ found: true }), "");

// Advice markers
const ev = (time, kind, severity) => ({ time, kind, severity, title: "Thrashing" });
const evs = [ev("2026-10-19T23:10:00Z", "advice_start", "warning"), ev("2026-10-19T23:30:00Z", "advice_severity", "critical"), ev("2026-10-19T23:40:00Z", "advice_end", "critical"), ev("2026-10-19T01:00:00Z", "advice_start", "warning")];
const ms = s => Date.parse(s);
const mk = Replay.markers(evs, ms("2026-10-19T23:00:00Z"), ms("2026-10-19T23:59:00Z"));
assert.deepStrictEqual(mk.map(m => m.kind), ["advice_start", "advice_severity", "advice_end"], "only events inside the window");
assert.ok(/^Thrashing started \d\d:\d\d$/.test(mk[0].label), mk[0].label);
assert.ok(/^Thrashing ended \d\d:\d\d$/.test(mk[2].label), mk[2].label);
assert.ok(/^Thrashing now critical \d\d:\d\d$/.test(mk[1].label), mk[1].label);
if ((process.env.TZ || "") === "UTC") assert.strictEqual(mk[0].label, "Thrashing started 23:10");

// state-changing controls are Live only
assert.strictEqual(Replay.controlsAllowed(), true);
Replay.state.at = 123;
assert.strictEqual(Replay.controlsAllowed(), false);
assert.strictEqual(Replay.active(), true);
console.log("ok");
