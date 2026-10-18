package history

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/live"
)

// The dashboard time slider reads History through three small views of the
// ring: Span (how far back the slider goes, with every Advice event), At (the
// one sample shown at a chosen moment, with the Advice active then) and Series
// (system metrics over a window, for the Graphs). All are answered by the
// running instance; they flush first, like Query.

// MaxSeriesMinutes is the longest Series window: the whole 24 hours.
const MaxSeriesMinutes = 24 * 60

// DefaultSeriesMinutes is the Series window when none is asked for.
const DefaultSeriesMinutes = 60

// SpanResult bounds the slider. Oldest and Newest are zero when there is no History yet.
type SpanResult struct {
	Oldest          time.Time `json:"oldest"`
	Newest          time.Time `json:"newest"`
	IntervalSeconds float64   `json:"interval_seconds"` // spacing of History samples
	Events          []Event   `json:"events"`           // every retained Advice event, oldest first
}

// ActiveAdvice is Advice that was active at the requested moment, rebuilt from Advice events.
type ActiveAdvice struct {
	AdviceID     string    `json:"advice_id"`
	Title        string    `json:"title"`
	Category     string    `json:"category,omitempty"`
	Severity     string    `json:"severity,omitempty"`
	Applications []string  `json:"applications,omitempty"`
	Since        time.Time `json:"since"`
}

// AtResult is History at one moment. Sample is the recorded sample nearest the
// requested time; Found is false when none lies within two sampling intervals
// (the program was not running), and Previous/Next then name the neighbours.
type AtResult struct {
	Requested time.Time `json:"requested"`
	Found     bool      `json:"found"`
	Sample    *Record   `json:"sample"`
	Previous  time.Time `json:"previous_sample,omitempty"`
	Next      time.Time `json:"next_sample,omitempty"`
	// Truncation, stated so views can say what is not shown: History keeps the top
	// Applications with their biggest Processes only.
	TotalApplications int            `json:"total_applications"`
	ShownApplications int            `json:"shown_applications"`
	Advice            []ActiveAdvice `json:"advice"`
}

// SeriesResult is the Graphs data for a window. Points use the Live buffer's
// field names, so the same charts draw both; only what History records is
// filled (no disk throughput, I/O pressure or GPU).
type SeriesResult struct {
	From   time.Time    `json:"from"`
	To     time.Time    `json:"to"`
	Points []live.Point `json:"points"`
	Events []Event      `json:"events"`
}

func (s *snapshot) records() []Record {
	var all []Record
	for _, sl := range s.samples {
		if rec, ok := decodeRecord(sl.payload); ok {
			all = append(all, rec)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })
	return all
}

func (s *snapshot) allEvents() []Event {
	evs := []Event{}
	for _, e := range s.events {
		var ev Event
		if j, err := decompress(e.payload); err == nil && json.Unmarshal(j, &ev) == nil {
			evs = append(evs, ev)
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time.Before(evs[j].Time) })
	return evs
}

func (r *Recorder) snap() (*snapshot, error) {
	r.Flush()
	return readSnapshot(Path(r.m.StateDir))
}

// Span answers how far back the slider can go.
func (r *Recorder) Span() (SpanResult, error) {
	snap, err := r.snap()
	if err != nil {
		return SpanResult{}, err
	}
	res := SpanResult{IntervalSeconds: SampleEvery.Seconds(), Events: snap.allEvents()}
	if all := snap.records(); len(all) > 0 {
		res.Oldest, res.Newest = all[0].Time, all[len(all)-1].Time
	}
	return res, nil
}

// At answers History at one moment.
func (r *Recorder) At(t time.Time) (AtResult, error) {
	snap, err := r.snap()
	if err != nil {
		return AtResult{}, err
	}
	return at(snap.records(), snap.allEvents(), t), nil
}

func at(all []Record, evs []Event, t time.Time) AtResult {
	res := AtResult{Requested: t, Advice: activeAt(evs, t)}
	best := -1
	for i, rec := range all {
		if rec.Time.After(t) {
			res.Next = rec.Time
			break
		}
		best = i
	}
	if best >= 0 {
		res.Previous = all[best].Time
	}
	near := func(x time.Time) bool { d := t.Sub(x); return d < 2*SampleEvery && d > -2*SampleEvery }
	pick := -1
	if best >= 0 && near(all[best].Time) {
		pick = best
	}
	if !res.Next.IsZero() && near(res.Next) && (pick < 0 || res.Next.Sub(t) < t.Sub(all[pick].Time)) {
		for i := best + 1; i < len(all); i++ {
			if all[i].Time.Equal(res.Next) {
				pick = i
				break
			}
		}
	}
	if pick >= 0 {
		rec := all[pick]
		res.Found, res.Sample = true, &rec
		res.ShownApplications = len(rec.Apps)
		res.TotalApplications = len(rec.Apps) + rec.OmittedApps
	}
	return res
}

// activeAt replays Advice events up to t: start adds, a Severity change updates, end removes.
func activeAt(evs []Event, t time.Time) []ActiveAdvice {
	act := map[string]*ActiveAdvice{}
	for _, e := range evs {
		if e.Time.After(t) {
			break
		}
		switch e.Kind {
		case AdviceStarted:
			act[e.AdviceID] = &ActiveAdvice{AdviceID: e.AdviceID, Title: e.Title, Category: e.Category, Severity: e.Severity, Applications: e.Applications, Since: e.Time}
		case AdviceSeverityChanged:
			if a := act[e.AdviceID]; a != nil {
				a.Severity = e.Severity
			}
		case AdviceEnded:
			delete(act, e.AdviceID)
		}
	}
	out := []ActiveAdvice{}
	for _, a := range act {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := sevRank(out[i].Severity), sevRank(out[j].Severity); ri != rj {
			return ri > rj
		}
		return out[i].AdviceID < out[j].AdviceID
	})
	return out
}

func sevRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	}
	return 1
}

// Series answers Graphs data for the window of the given minutes centred on
// around (the newest window when around is zero).
func (r *Recorder) Series(around time.Time, minutes int) (SeriesResult, error) {
	snap, err := r.snap()
	if err != nil {
		return SeriesResult{}, err
	}
	if minutes <= 0 {
		minutes = DefaultSeriesMinutes
	}
	if minutes > MaxSeriesMinutes {
		minutes = MaxSeriesMinutes
	}
	all, evs := snap.records(), snap.allEvents()
	win := time.Duration(minutes) * time.Minute
	res := SeriesResult{Points: []live.Point{}, Events: []Event{}}
	switch {
	case !around.IsZero():
		res.From, res.To = around.Add(-win/2), around.Add(win/2)
	case len(all) > 0:
		res.To = all[len(all)-1].Time
		res.From = res.To.Add(-win)
	}
	for _, rec := range all {
		if !rec.Time.Before(res.From) && !rec.Time.After(res.To) {
			res.Points = append(res.Points, pointOf(rec))
		}
	}
	for _, e := range evs {
		if !e.Time.Before(res.From) && !e.Time.After(res.To) {
			res.Events = append(res.Events, e)
		}
	}
	return res, nil
}

func pointOf(r Record) live.Point {
	p := live.Point{Time: r.Time, CPUPercent: r.CPU.TotalPercent, Load1: r.CPU.Load1,
		SwapInPages: r.Swap.InPagesPerSec, SwapOutPages: r.Swap.OutPagesPerSec, TopApps: []live.AppSplit{}}
	if m := r.Memory; m != nil {
		t, u := m.TotalBytes, m.UsedBytes
		p.RAMTotalBytes, p.RAMUsedBytes = &t, &u
	}
	if d := r.Swap.Disk; d != nil {
		u := d.UsedBytes
		p.DiskSwapUsed = &u
	}
	if c := r.Swap.Compressed; c != nil {
		u := c.UsedBytes
		p.CompressedSwapUse = &u
	}
	if pr := r.Pressure; pr != nil {
		p.MemoryPressureSome, p.MemoryPressureFull = pr.Some, pr.Full
	}
	return p
}
