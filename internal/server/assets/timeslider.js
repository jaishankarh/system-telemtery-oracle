// Time slider: Live by default; dragging back selects a moment in the last 24 hours of
// History and every view (Applications, Processes, Graphs, Advice panel) shows that moment.
// Data: /api/history/span (slider bounds), /api/history/at (the sample and the Advice active
// then), /api/history/series (Graphs). The logic lives in replay.js; this file only wires the page.
(() => {
  const MAX = 1000;
  const slider = document.getElementById("time-slider");
  const label = document.getElementById("time-label");
  const liveBtn = document.getElementById("live-button");
  const banner = document.getElementById("replay-banner");
  let span = null, timer = null, ticket = 0;

  const bounds = () => span && span.oldest && span.newest ? [Date.parse(span.oldest), Date.parse(span.newest)] : null;

  async function loadSpan() {
    try {
      const r = await fetch("/api/history/span", { cache: "no-store" });
      if (!r.ok) throw new Error("History unavailable");
      span = await r.json();
      Replay.state.span = span;
      const b = bounds();
      slider.disabled = !b;
      document.getElementById("time-range").textContent = b
        ? `History: ${Replay.whenLabel(b[0], Date.now())} to ${Replay.whenLabel(b[1], Date.now())}`
        : "History: nothing recorded yet";
      if (b && Replay.active()) slider.value = Replay.timeToPosition(Replay.state.at, MAX, b[0], b[1]);
    } catch (e) {
      slider.disabled = true;
      document.getElementById("time-range").textContent = "History unavailable on this machine";
    }
  }

  function setAllViews() {
    const replaying = Replay.active();
    document.body.classList.toggle("replaying", replaying);
    liveBtn.hidden = !replaying;
    const at = Replay.state.atResult, notes = Replay.truncationNotes(at);
    const show = (id, text) => { const el = document.getElementById(id); el.hidden = !text; el.textContent = text || ""; };
    show("app-note", replaying ? (notes.applications || Replay.noHistoryMessage(at)) : "");
    show("proc-note", replaying ? (notes.processes || Replay.noHistoryMessage(at)) : "");
    show("advice-note", replaying ? "Replay: Advice that was active at this time. Hide and Snooze apply to the present, so they are available in Live only." : "");
    document.getElementById("graph-note").textContent = replaying
      ? "Replay graphs show one hour around the selected time. History records CPU, memory, swap and memory pressure only: disk throughput, I/O pressure and GPU are not shown."
      : "";
    if (replaying) {
      banner.hidden = false;
      banner.textContent = at && at.found
        ? `Replay of ${Replay.whenLabel(Date.parse(at.sample.time), Date.now())}. Nothing on this page is current.`
        : `Replay of ${Replay.whenLabel(Replay.state.at, Date.now())}. ${Replay.noHistoryMessage(at)}`;
      document.getElementById("status").textContent = "replaying " + Replay.whenLabel(Replay.state.at, Date.now());
    } else {
      banner.hidden = true;
    }
    refreshApplications(); refreshProcesses(); refreshAdvice();
    if (!replaying) refresh();
  }

  async function enterReplay(ms) {
    const mine = ++ticket;
    const iso = new Date(ms).toISOString();
    try {
      const [at, series] = await Promise.all([
        fetch("/api/history/at?time=" + encodeURIComponent(iso), { cache: "no-store" }).then(r => r.ok ? r.json() : Promise.reject(new Error("at"))),
        fetch("/api/history/series?minutes=60&around=" + encodeURIComponent(iso), { cache: "no-store" }).then(r => r.ok ? r.json() : Promise.reject(new Error("series"))),
      ]);
      if (mine !== ticket) return; // the slider moved on
      Replay.state.at = ms; Replay.state.atResult = at; Replay.state.series = series;
      label.textContent = Replay.whenLabel(ms, Date.now());
      setAllViews();
      showReplayGraphs(series);
    } catch (e) {
      if (mine === ticket) banner.hidden = false, banner.textContent = "Could not read History: " + e.message;
    }
  }

  function goLive() {
    ticket++;
    Replay.state.at = null; Replay.state.atResult = null; Replay.state.series = null;
    slider.value = MAX;
    label.textContent = "Live";
    setAllViews();
    showLiveGraphs();
    loadSpan();
  }

  slider.addEventListener("input", () => {
    const b = bounds();
    if (!b) return;
    const ms = Replay.positionToTime(Number(slider.value), MAX, b[0], b[1]);
    clearTimeout(timer);
    if (ms === null) { timer = setTimeout(() => Replay.active() && goLive(), 150); label.textContent = "Live"; return; }
    label.textContent = Replay.whenLabel(ms, Date.now());
    timer = setTimeout(() => enterReplay(ms), 150);
  });
  liveBtn.addEventListener("click", () => { clearTimeout(timer); goLive(); });
  loadSpan();
  setInterval(() => { if (!Replay.active()) loadSpan(); }, 30000);
})();
