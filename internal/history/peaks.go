package history

import (
	"errors"
	"os"
	"time"
)

// Peaks are the highest Working Set (PSS + swap) each Application had in the
// samples History holds. History keeps only the top 10 Applications per
// sample, so an Application missing from a sample may have been small or just
// outside the top 10; Truncated counts the samples where the cut applied.
type Peaks struct {
	From      time.Time // first and last sample considered
	To        time.Time
	Samples   int
	Truncated int
	Apps      map[string]AppPeak // by AppKey
}

// AppPeak is one Application's highest Working Set in the window.
type AppPeak struct {
	Bytes uint64
	At    time.Time
	Seen  int // samples that recorded it
}

// AppKey identifies an Application across samples and sessions.
func AppKey(kind, name string) string { return kind + "\x00" + name }

func peaksOf(snap *snapshot, since time.Time) Peaks {
	p := Peaks{Apps: map[string]AppPeak{}}
	for _, s := range snap.samples {
		rec, ok := decodeRecord(s.payload)
		if !ok || rec.Time.Before(since) {
			continue
		}
		if p.Samples == 0 || rec.Time.Before(p.From) {
			p.From = rec.Time
		}
		if rec.Time.After(p.To) {
			p.To = rec.Time
		}
		p.Samples++
		if rec.OmittedApps > 0 {
			p.Truncated++
		}
		for _, a := range rec.Apps {
			var b uint64
			if a.PSS == nil && a.Swap == nil {
				continue
			}
			if a.PSS != nil {
				b += *a.PSS
			}
			if a.Swap != nil {
				b += *a.Swap
			}
			k := AppKey(a.Kind, a.Name)
			e := p.Apps[k]
			e.Seen++
			if b >= e.Bytes {
				e.Bytes, e.At = b, rec.Time
			}
			p.Apps[k] = e
		}
	}
	return p
}

// ReadPeaks reads Application peaks straight from the ring file in stateDir.
func ReadPeaks(stateDir string, since time.Time) (Peaks, error) {
	snap, err := readSnapshot(Path(stateDir))
	if err != nil {
		if errors.Is(err, errNoHistory) || errors.Is(err, os.ErrNotExist) {
			return Peaks{}, ErrNoHistory
		}
		return Peaks{}, err
	}
	return peaksOf(snap, since), nil
}

// Peaks answers from the running instance, flushing first.
func (r *Recorder) Peaks(since time.Time) (Peaks, error) {
	r.Flush()
	return ReadPeaks(r.m.StateDir, since)
}
