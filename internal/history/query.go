package history

import (
	"errors"
	"os"
	"time"
)

// DefaultMinutes is the replay window when --minutes is not given.
const DefaultMinutes = 10

// Query selects what to replay. LastBoot shows the lead-up to the end of the
// last session that is not the running one; Around shows a window centred on a
// time; with neither, the newest Minutes are shown.
type Query struct {
	Around   time.Time
	LastBoot bool
	Minutes  int
}

// Result is a Replay: samples and Advice events in the window.
type Result struct {
	Live    bool      `json:"live"`    // answered by a running instance
	Session uint64    `json:"session"` // session the samples shown belong to (0 when mixed)
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	// Unclean: the session before the running one (or, reading the file with
	// no instance, the latest session) ended without a clean shutdown at EndedAt.
	Unclean bool      `json:"unclean_previous_shutdown"`
	EndedAt time.Time `json:"unclean_ended_at,omitempty"`
	Samples []Record  `json:"samples"`
	Events  []Event   `json:"events"`
}

// ErrNoHistory is returned when no History file exists or it is unreadable.
var ErrNoHistory = errors.New("no History recorded yet")

// ReadFile answers a Query straight from the ring file in stateDir.
func ReadFile(stateDir string, q Query) (Result, error) {
	snap, err := readSnapshot(Path(stateDir))
	if err != nil {
		if errors.Is(err, errNoHistory) || errors.Is(err, os.ErrNotExist) {
			return Result{}, ErrNoHistory
		}
		return Result{}, err
	}
	defer snap.Close()
	return answer(snap, q, 0), nil
}

// Query answers from the running instance: it flushes first so the answer
// includes samples not yet on disk.
func (r *Recorder) Query(q Query) (Result, error) {
	snap, release, err := r.snap()
	if err != nil {
		return Result{}, err
	}
	defer release()
	res := answer(snap, q, r.hdr.Session)
	res.Live = true
	return res, nil
}

// eventTailAfterLastSample bounds how far past the last sample Advice events are shown.
const eventTailAfterLastSample = 30 * time.Second

// answer: running is the live session number, or 0 when no instance runs.
// Only the samples in the window are decoded.
func answer(snap *snapshot, q Query, running uint64) Result {
	mins := q.Minutes
	if mins <= 0 {
		mins = DefaultMinutes
	}
	win := time.Duration(mins) * time.Minute
	n := len(snap.samples)

	res := Result{Samples: []Record{}, Events: []Event{}}
	if running > 0 {
		res.Unclean = snap.hdr.PrevState == stateRunning
		res.EndedAt = time.Unix(0, snap.hdr.PrevEndedNs).UTC()
	} else if snap.hdr.State == stateRunning {
		res.Unclean = true
		res.EndedAt = newestTime(snap, snap.hdr.Session)
	}
	if !res.Unclean {
		res.EndedAt = time.Time{}
	}

	toIdx := n // samples before this index are at or before res.To
	switch {
	case q.LastBoot:
		// The newest sample of the newest session that is not the running one.
		var last Record
		found := false
		for i := n - 1; i >= 0; i-- {
			if r, ok := snap.record(i); ok && (running == 0 || r.Session < running) {
				last, found, toIdx = r, true, i+1
				break
			}
		}
		if !found {
			return res
		}
		res.Session = last.Session
		res.To = last.Time
		res.From = res.To.Add(-win)
	case !q.Around.IsZero():
		res.From, res.To = q.Around.Add(-win/2), q.Around.Add(win/2)
		toIdx = snap.firstAfter(res.To)
	default:
		last, i, ok := snap.lastRecord(n)
		if !ok {
			return res
		}
		res.To, toIdx = last.Time, i+1
		res.From = res.To.Add(-win)
	}
	evs := snap.allEvents()
	eventsTo := res.To // end of the window for Advice events
	if q.Around.IsZero() {
		// Samples are taken every 10s but Advice is evaluated every sample interval, so
		// Advice that started or ended after the last sample, in the final seconds before a
		// crash, must still be part of the lead-up. Stop at the next session's first sample.
		limit := res.To.Add(eventTailAfterLastSample)
		for i := toIdx; i < n; i++ {
			r, ok := snap.record(i)
			if !ok {
				continue
			}
			if !r.Time.Before(limit) {
				break
			}
			if r.Time.After(res.To) && (q.LastBoot && r.Session != res.Session || !q.LastBoot) {
				limit = r.Time
				break
			}
		}
		for _, ev := range evs {
			if ev.Time.After(eventsTo) && ev.Time.Before(limit) {
				eventsTo = ev.Time
			}
		}
	}
	snap.eachRecord(snap.firstNotBefore(res.From), toIdx, func(r Record) {
		if r.Time.Before(res.From) || r.Time.After(res.To) || q.LastBoot && r.Session != res.Session {
			return
		}
		res.Samples = append(res.Samples, r)
	})
	for _, ev := range evs {
		if !ev.Time.Before(res.From) && !ev.Time.After(eventsTo) {
			res.Events = append(res.Events, ev)
		}
	}
	return res
}
