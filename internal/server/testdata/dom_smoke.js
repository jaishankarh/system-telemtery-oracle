// Run with: node dom_smoke.js <assets dir> <base url>. Loads the dashboard scripts against a tiny
// DOM stand-in and the real program's API; prints JSON describing what the page rendered.
const fs = require("fs"), vm = require("vm");
const [assets, base] = process.argv.slice(2);
class El {
  constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.listeners = {}; this.dataset = {}; this.style = {}; this._text = ""; this.hidden = false; this.value = ""; this.className = ""; this.disabled = false; this.classList = { toggle: () => {} }; }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(""); }
  set textContent(v) { this._text = String(v); this.children = []; }
  append(...n) { n.forEach(x => this.children.push(typeof x === "string" ? Object.assign(new El("#text"), { _text: x }) : x)); }
  appendChild(n) { this.append(n); return n; }
  replaceChildren(...n) { this._text = ""; this.children = []; this.append(...n); }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener(t, f) { (this.listeners[t] = this.listeners[t] || []).push(f); }
  fire(t) { (this.listeners[t] || []).forEach(f => f({})); }
  get parentElement() { return this._parent || (this._parent = new El("div")); }
  set innerHTML(v) { this._text = v.replace(/<[^>]*>/g, ""); }
  all(pred, out = []) { this.children.forEach(c => { if (pred(c)) out.push(c); c.all(pred, out); }); return out; }
}
const byKey = {};
const get = k => byKey[k] || (byKey[k] = new El(k));
const document = { getElementById: id => get("#" + id), querySelector: s => get(s), body: new El("body"), hidden: false,
  createElement: t => new El(t), addEventListener() {}, activeElement: null };
document.body.style = {};
const ctx = { document, window: {}, console, setTimeout, clearTimeout, setInterval: () => 0, URLSearchParams, Date, Promise, JSON, Math, Number, String, Set, Error, RegExp, Object, Array,
  encodeURIComponent, fetch: (u, o) => fetch(base + u, o), alert: m => { throw new Error("alert: " + m); }, navigator: {}, devicePixelRatio: 1,
  showReplayGraphs: s => { ctx.replayGraphPoints = s.points.length; ctx.replayGraphEvents = s.events.length; }, showLiveGraphs: () => { ctx.liveGraphs = true; } };
vm.createContext(ctx);
const load = f => vm.runInContext(fs.readFileSync(assets + "/" + f, "utf8"), ctx, { filename: f });
const sleep = ms => new Promise(r => setTimeout(r, ms));
const rowsOf = id => get(id).children.filter(r => r.className === "app-row").map(r => r.children.map(c => c.textContent));
(async () => {
  ["replay.js", "app.js", "timeslider.js"].forEach(load);
  await sleep(400);
  const out = { liveRows: rowsOf("#app-table tbody"), liveAdviceControls: get("#advice-list").all(e => e.className === "advice-controls").length };
  const slider = get("#time-slider");
  out.sliderEnabled = !slider.disabled;
  slider.value = Number(process.env.SLIDER || 300);
  slider.fire("input");
  await sleep(700);
  out.replayRows = rowsOf("#app-table tbody");
  out.replayLabel = get("#time-label").textContent;
  out.banner = get("#replay-banner").textContent;
  out.appNote = get("#app-note").textContent;
  out.procNote = get("#proc-note").textContent;
  out.procRows = get("#proc-table tbody").children.length;
  out.adviceTitles = get("#advice-list").all(e => e.tag === "h3").map(e => e.textContent);
  out.replayAdviceControls = get("#advice-list").all(e => e.tag === "button" || e.tag === "select").length;
  out.replayGraphPoints = ctx.replayGraphPoints;
  out.replayGraphEvents = ctx.replayGraphEvents;
  out.liveButtonHidden = get("#live-button").hidden;
  get("#live-button").fire("click");
  await sleep(400);
  out.backToLiveLabel = get("#time-label").textContent;
  out.backToLiveRows = rowsOf("#app-table tbody");
  out.backToLiveBannerHidden = get("#replay-banner").hidden;
  console.log(JSON.stringify(out));
  process.exit(0);
})().catch(e => { console.error(e); process.exit(1); });
