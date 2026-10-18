package history

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
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
	return answer(snap, q, 0), nil
}

// Query answers from the running instance: it flushes first so the answer
// includes samples not yet on disk.
func (r *Recorder) Query(q Query) (Result, error) {
	r.Flush()
	snap, err := readSnapshot(Path(r.m.StateDir))
	if err != nil {
		return Result{}, err
	}
	res := answer(snap, q, r.hdr.Session)
	res.Live = true
	return res, nil
}

// answer: running is the live session number, or 0 when no instance runs.
func answer(snap *snapshot, q Query, running uint64) Result {
	mins := q.Minutes
	if mins <= 0 {
		mins = DefaultMinutes
	}
	win := time.Duration(mins) * time.Minute
	var all []Record
	for _, s := range snap.samples {
		if rec, ok := decodeRecord(s.payload); ok {
			all = append(all, rec)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })

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

	switch {
	case q.LastBoot:
		var target uint64
		for _, r := range all {
			if (running == 0 || r.Session < running) && r.Session > target {
				target = r.Session
			}
		}
		res.Session = target
		var sess []Record
		for _, r := range all {
			if r.Session == target {
				sess = append(sess, r)
			}
		}
		if len(sess) == 0 {
			return res
		}
		res.To = sess[len(sess)-1].Time
		res.From = res.To.Add(-win)
	case !q.Around.IsZero():
		res.From, res.To = q.Around.Add(-win/2), q.Around.Add(win/2)
	default:
		if len(all) == 0 {
			return res
		}
		res.To = all[len(all)-1].Time
		res.From = res.To.Add(-win)
	}
	for _, r := range all {
		if r.Time.Before(res.From) || r.Time.After(res.To) {
			continue
		}
		if q.LastBoot && r.Session != res.Session {
			continue
		}
		res.Samples = append(res.Samples, r)
	}
	for _, e := range snap.events {
		var ev Event
		if j, err := decompress(e.payload); err == nil && json.Unmarshal(j, &ev) == nil {
			if !ev.Time.Before(res.From) && !ev.Time.After(res.To) {
				res.Events = append(res.Events, ev)
			}
		}
	}
	sort.SliceStable(res.Events, func(i, j int) bool { return res.Events[i].Time.Before(res.Events[j].Time) })
	return res
}
