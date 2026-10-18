package history

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// EventKind says what happened to a piece of Advice.
type EventKind string

const (
	AdviceStarted         EventKind = "advice_start"
	AdviceEnded           EventKind = "advice_end"
	AdviceSeverityChanged EventKind = "advice_severity"
)

// Event is one Advice transition kept in History so Replay can show when each
// piece of Advice started and ended. Category is "settings_fix" or
// "hardware_limit"; Severity is "info", "warning" or "critical".
type Event struct {
	Time         time.Time `json:"time"`
	Kind         EventKind `json:"kind"`
	AdviceID     string    `json:"advice_id"`
	Title        string    `json:"title"`
	Category     string    `json:"category,omitempty"`
	Severity     string    `json:"severity,omitempty"`
	Applications []string  `json:"applications,omitempty"`
	Detail       string    `json:"detail,omitempty"`
}

// EventAppender is what the Advisor uses to record Advice start, end and
// Severity changes. It is safe for concurrent use and never fails the caller.
// Events reach disk with the next flush; a critical Advice start flushes at once.
type EventAppender interface {
	AppendEvent(Event)
}

// Recorder writes History. A nil *Recorder is valid and does nothing, so a
// Machine whose state dir cannot be written still runs (ADR 0006).
type Recorder struct {
	mu      sync.Mutex
	gate    chan struct{} // one History query at a time
	m       machine.Machine
	f       *os.File
	grouper *grouping.Grouper
	hdr     header
	seq     uint64 // last used sample sequence number
	eseq    uint64
	dirty   map[int64][]byte

	lastSample time.Time
	lastFlush  time.Time
	warning    bool

	prevUnclean bool
	prevEndedAt time.Time
	closed      bool
	movedAside  string
	lock        *os.File // flock on the state dir while this instance owns History
}

// Open opens or creates the ring file in the Machine state dir, detects an
// unclean previous shutdown and starts a new session.
func Open(m machine.Machine, g *grouping.Grouper) (*Recorder, error) {
	path := Path(m.StateDir)
	lock, err := lockState(m.StateDir)
	if err != nil {
		return nil, err
	}
	r, err := open(m, g, path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	r.lock = lock
	return r, nil
}

func open(m machine.Machine, g *grouping.Grouper, path string) (*Recorder, error) {
	r := &Recorder{m: m, grouper: g, dirty: map[int64][]byte{}, gate: make(chan struct{}, 1)}
	now := m.Clock.Now()
	h := header{Session: 1, State: stateRunning, StartedNs: now.UnixNano()}
	if st, err := os.Stat(path); err == nil && st.Size() != FileSize {
		if err := r.moveAside(path); err != nil {
			return nil, err
		}
	}
	snap, err := readSnapshot(path)
	if errors.Is(err, errBadHeader) {
		if err := r.moveAside(path); err != nil {
			return nil, err
		}
		snap, err = nil, errNoHistory
	}
	if err != nil && !errors.Is(err, errNoHistory) {
		return nil, err // unreadable for another reason (permissions, I/O): never overwrite it
	}
	if err == nil {
		defer snap.Close()
		h.Session = snap.hdr.Session + 1
		if snap.hdr.State == stateRunning {
			h.PrevState = stateRunning
			r.prevUnclean = true
			r.prevEndedAt = newestTime(snap, snap.hdr.Session)
			h.PrevEndedNs = r.prevEndedAt.UnixNano()
		}
		if n := len(snap.samples); n > 0 {
			r.seq = snap.samples[n-1]
		}
		if n := len(snap.events); n > 0 {
			r.eseq = snap.events[n-1]
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		f.Chmod(0o600)
		r.f = f
	} else {
		f, err := create(path, h)
		if err != nil {
			return nil, err
		}
		r.f = f
	}
	r.hdr = h
	if err := r.writeHeader(); err != nil {
		r.f.Close()
		return nil, err
	}
	r.lastFlush = now
	return r, nil
}

// MovedAside is where an unreadable ring file was kept when this session
// started a fresh one; empty when nothing was moved.
func (r *Recorder) MovedAside() string {
	if r == nil {
		return ""
	}
	return r.movedAside
}

// moveAside keeps a ring file this version cannot read as <name>.corrupt
// (replacing an earlier one) instead of writing over it.
func (r *Recorder) moveAside(path string) error {
	if err := os.Rename(path, path+".corrupt"); err != nil {
		return err
	}
	r.movedAside = path + ".corrupt"
	return nil
}

func newestTime(s *snapshot, session uint64) time.Time {
	for i := len(s.samples) - 1; i >= 0; i-- {
		if rec, ok := s.record(i); ok && rec.Session == session {
			return rec.Time
		}
	}
	return time.Time{}
}

func (r *Recorder) writeHeader() error {
	if _, err := r.f.WriteAt(r.hdr.encode(), 0); err != nil {
		return err
	}
	return r.f.Sync()
}

// PreviousSession reports whether the session before this one ended without a
// clean shutdown, and the time of its last recorded sample.
func (r *Recorder) PreviousSession() (unclean bool, endedAt time.Time) {
	if r == nil {
		return false, time.Time{}
	}
	return r.prevUnclean, r.prevEndedAt
}

// Observe is called with every collected Sample. It records a History sample
// every SampleEvery, flushes every FlushEvery, and flushes at once when memory
// pressure or Thrashing crosses its warning threshold.
func (r *Recorder) Observe(s collector.Sample) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	p := readPressure(r.m)
	warn := thrashing(s.Swap) || (p != nil && p.Some != nil && *p.Some >= warningPressure)
	spike := warn && !r.warning
	r.warning = warn
	if spike || r.lastSample.IsZero() || s.Time.Sub(r.lastSample) >= SampleEvery {
		r.record(buildRecord(r.hdr.Session, s, r.grouper.Group(s.Processes), p))
		r.lastSample = s.Time
	}
	if spike || s.Time.Sub(r.lastFlush) >= FlushEvery {
		r.flush(s.Time)
	}
}

func (r *Recorder) record(rec Record) {
	r.seq++
	b, _ := encodeSlot(SlotSize, r.seq, encodeRecord(rec))
	r.dirty[slotOffset(r.seq)] = b
}

// AppendEvent implements EventAppender.
func (r *Recorder) AppendEvent(e Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if e.Time.IsZero() {
		e.Time = r.m.Clock.Now()
	}
	var payload []byte
	for {
		j, _ := json.Marshal(e)
		payload = compress(j)
		if slotHead+len(payload) <= EventSize || len(e.Detail) == 0 {
			break
		}
		e.Detail = e.Detail[:len(e.Detail)/2]
	}
	r.eseq++
	if b, ok := encodeSlot(EventSize, r.eseq, payload); ok {
		r.dirty[eventOffset(r.eseq)] = b
	}
	if e.Kind == AdviceStarted && e.Severity == "critical" {
		r.flush(e.Time)
	}
}

func (r *Recorder) flush(now time.Time) {
	for off, b := range r.dirty {
		if _, err := r.f.WriteAt(b, off); err != nil {
			return // keep the slot pending; try again at the next flush
		}
		delete(r.dirty, off)
	}
	r.f.Sync()
	r.lastFlush = now
}

// Flush writes everything pending.
func (r *Recorder) Flush() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flush(r.m.Clock.Now())
}

// Close flushes and marks the session as cleanly ended.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.flush(r.m.Clock.Now())
	r.hdr.State = stateClean
	r.writeHeader()
	r.f.Close()
	r.lock.Close()
	r.closed = true
}

// Abandon closes the file without flushing or marking a clean end, as a
// killed process would leave it. Used by tests through the harness.
func (r *Recorder) Abandon() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.f.Close()
	r.lock.Close()
}

// readPressure reads memory PSI avg10; nil when the kernel has no PSI.
func readPressure(m machine.Machine) *Pressure {
	b, err := m.ReadFile("/proc/pressure/memory")
	if err != nil {
		return nil
	}
	p := &Pressure{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, kv := range f[1:] {
			if v, ok := strings.CutPrefix(kv, "avg10="); ok {
				if x, err := strconv.ParseFloat(v, 64); err == nil {
					switch f[0] {
					case "some":
						p.Some = &x
					case "full":
						p.Full = &x
					}
				}
			}
		}
	}
	return p
}
