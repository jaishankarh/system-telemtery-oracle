// Package history keeps the 24-hour History (ADR 0004): 10-second samples in a
// fixed-size, preallocated, root-only ring file that is overwritten in place,
// plus a smaller ring of Advice events. The file lives in the Machine state
// dir. It never grows: sample N goes into slot N mod SlotCount.
//
// Layout: one 512-byte header sector, SlotCount sample slots of SlotSize
// bytes, EventCount event slots of EventSize bytes. Each slot is
// [crc32 u32][seq u64][len u32][payload]; a slot torn by a crash fails its
// checksum and is skipped, so a crash mid-flush costs at most the slots being
// written.
package history

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Ring dimensions. SampleEvery * SlotCount = 24 hours.
const (
	SampleEvery = 10 * time.Second
	FlushEvery  = 30 * time.Second
	SlotCount   = 8640
	SlotSize    = 1024
	EventCount  = 1024
	EventSize   = 512
	HeaderSize  = 512
	slotHead    = 16
	// FileName is the ring file inside the Machine state dir.
	FileName = "history.ring"
	// InstanceFile holds the address of the running instance's server.
	InstanceFile = "instance.addr"
	// FileSize is the fixed size of the ring file.
	FileSize = HeaderSize + SlotCount*SlotSize + EventCount*EventSize
)

var magic = [8]byte{'S', 'T', 'O', 'H', 'I', 'S', 'T', '1'}

// Header states.
const (
	stateClean   = 0
	stateRunning = 1
)

type header struct {
	Session     uint64 // current (latest) session number
	State       uint8  // of the latest session
	PrevState   uint8  // state the previous session was left in (stateRunning = unclean)
	PrevEndedNs int64  // newest recorded time of the previous session when it was found unclean
	StartedNs   int64
}

func (h header) encode() []byte {
	b := make([]byte, HeaderSize)
	copy(b, magic[:])
	binary.LittleEndian.PutUint32(b[8:], SlotCount)
	binary.LittleEndian.PutUint32(b[12:], SlotSize)
	binary.LittleEndian.PutUint32(b[16:], EventCount)
	binary.LittleEndian.PutUint32(b[20:], EventSize)
	binary.LittleEndian.PutUint64(b[24:], h.Session)
	b[32], b[33] = h.State, h.PrevState
	binary.LittleEndian.PutUint64(b[40:], uint64(h.PrevEndedNs))
	binary.LittleEndian.PutUint64(b[48:], uint64(h.StartedNs))
	binary.LittleEndian.PutUint32(b[HeaderSize-4:], crc32.ChecksumIEEE(b[:HeaderSize-4]))
	return b
}

func decodeHeader(b []byte) (header, bool) {
	if len(b) < HeaderSize || [8]byte(b[:8]) != magic {
		return header{}, false
	}
	if binary.LittleEndian.Uint32(b[HeaderSize-4:]) != crc32.ChecksumIEEE(b[:HeaderSize-4]) {
		return header{}, false
	}
	if binary.LittleEndian.Uint32(b[8:]) != SlotCount || binary.LittleEndian.Uint32(b[12:]) != SlotSize ||
		binary.LittleEndian.Uint32(b[16:]) != EventCount || binary.LittleEndian.Uint32(b[20:]) != EventSize {
		return header{}, false
	}
	return header{
		Session: binary.LittleEndian.Uint64(b[24:]), State: b[32], PrevState: b[33],
		PrevEndedNs: int64(binary.LittleEndian.Uint64(b[40:])), StartedNs: int64(binary.LittleEndian.Uint64(b[48:])),
	}, true
}

func slotOffset(seq uint64) int64 { return HeaderSize + int64(seq%SlotCount)*SlotSize }
func eventOffset(seq uint64) int64 {
	return HeaderSize + SlotCount*SlotSize + int64(seq%EventCount)*EventSize
}

func encodeSlot(size int, seq uint64, payload []byte) ([]byte, bool) {
	if slotHead+len(payload) > size {
		return nil, false
	}
	b := make([]byte, size)
	binary.LittleEndian.PutUint64(b[4:], seq)
	binary.LittleEndian.PutUint32(b[12:], uint32(len(payload)))
	copy(b[slotHead:], payload)
	binary.LittleEndian.PutUint32(b, crc32.ChecksumIEEE(b[4:slotHead+len(payload)]))
	return b, true
}

// decodeSlot returns the sequence number (starting at 1) and payload of a
// valid slot.
func decodeSlot(b []byte) (uint64, []byte, bool) {
	if len(b) < slotHead {
		return 0, nil, false
	}
	seq := binary.LittleEndian.Uint64(b[4:])
	n := int(binary.LittleEndian.Uint32(b[12:]))
	if seq == 0 || n > len(b)-slotHead {
		return 0, nil, false
	}
	if binary.LittleEndian.Uint32(b) != crc32.ChecksumIEEE(b[4:slotHead+n]) {
		return 0, nil, false
	}
	return seq, b[slotHead : slotHead+n], true
}

// Path is the ring file inside a state dir.
func Path(stateDir string) string { return filepath.Join(stateDir, FileName) }

// create preallocates a zero-filled ring file with root-only permissions.
func create(path string, h header) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	zero := make([]byte, 1<<20)
	for left := int64(FileSize); left > 0; {
		n := int64(len(zero))
		if left < n {
			n = left
		}
		if _, err := f.Write(zero[:n]); err != nil {
			f.Close()
			return nil, err
		}
		left -= n
	}
	if _, err := f.WriteAt(h.encode(), 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, f.Sync()
}

// snapshot is an index over the valid slots of a ring file. It holds only the
// sequence numbers (oldest first); a slot's payload is read and decoded when
// asked for, so a query costs what its window costs, not the whole 24 hours.
// Close it when done.
type snapshot struct {
	hdr     header
	f       *os.File
	samples []uint64 // sequence numbers of valid sample slots, ascending
	events  []uint64 // same for Advice event slots
}

var errNoHistory = errors.New("no History file")

func (s *snapshot) Close() {
	if s != nil && s.f != nil {
		s.f.Close()
	}
}

var indexBufs = sync.Pool{New: func() any { b := make([]byte, 1<<20); return &b }}

// indexRing reads the ring in 1 MB chunks and keeps only the sequence numbers
// of slots whose checksum holds.
func indexRing(f *os.File, base int64, count, size int) ([]uint64, error) {
	per := (1 << 20) / size
	bp := indexBufs.Get().(*[]byte)
	defer indexBufs.Put(bp)
	buf := (*bp)[:per*size]
	var seqs []uint64
	for i := 0; i < count; i += per {
		n := per
		if i+n > count {
			n = count - i
		}
		if _, err := f.ReadAt(buf[:n*size], base+int64(i)*int64(size)); err != nil {
			return seqs, nil // a short file: keep what was read
		}
		for j := 0; j < n; j++ {
			if seq, _, ok := decodeSlot(buf[j*size : (j+1)*size]); ok {
				seqs = append(seqs, seq)
			}
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs, nil
}

func readSnapshot(path string) (*snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoHistory
		}
		return nil, err
	}
	hb := make([]byte, HeaderSize)
	if _, err := io.ReadFull(f, hb); err != nil {
		f.Close()
		return nil, errNoHistory
	}
	h, ok := decodeHeader(hb)
	if !ok {
		f.Close()
		return nil, errNoHistory
	}
	s := &snapshot{hdr: h, f: f}
	s.samples, _ = indexRing(f, HeaderSize, SlotCount, SlotSize)
	s.events, _ = indexRing(f, HeaderSize+SlotCount*SlotSize, EventCount, EventSize)
	return s, nil
}

// payload reads the slot holding seq (nil when it was overwritten or is torn).
func (s *snapshot) payload(off int64, size int, seq uint64) []byte {
	b := make([]byte, size)
	if _, err := s.f.ReadAt(b, off); err != nil {
		return nil
	}
	if got, p, ok := decodeSlot(b); ok && got == seq {
		return p
	}
	return nil
}

// record decodes sample number i of the index.
func (s *snapshot) record(i int) (Record, bool) {
	p := s.payload(slotOffset(s.samples[i]), SlotSize, s.samples[i])
	if p == nil {
		return Record{}, false
	}
	return decodeRecord(p)
}

// timeAt is the time of sample i, false when it cannot be decoded.
func (s *snapshot) timeAt(i int) (time.Time, bool) {
	r, ok := s.record(i)
	return r.Time, ok
}

// firstAfter is the index of the first sample strictly after t (len when none),
// found by binary search: sequence order is time order, so only about 14 slots
// are decoded.
func (s *snapshot) firstAfter(t time.Time) int {
	return sort.Search(len(s.samples), func(i int) bool {
		ti, ok := s.timeAt(i)
		return ok && ti.After(t)
	})
}

// firstNotBefore is the index of the first sample at or after t.
func (s *snapshot) firstNotBefore(t time.Time) int {
	return sort.Search(len(s.samples), func(i int) bool {
		ti, ok := s.timeAt(i)
		return ok && !ti.Before(t)
	})
}

// eachRecord streams samples lo..hi-1 in order, decoding one at a time.
func (s *snapshot) eachRecord(lo, hi int, fn func(Record)) {
	for i := lo; i < hi && i < len(s.samples); i++ {
		if r, ok := s.record(i); ok {
			fn(r)
		}
	}
}

// lastRecord is the newest decodable sample at or before index hi-1.
func (s *snapshot) lastRecord(hi int) (Record, int, bool) {
	for i := hi - 1; i >= 0; i-- {
		if r, ok := s.record(i); ok {
			return r, i, true
		}
	}
	return Record{}, -1, false
}

// firstRecord is the oldest decodable sample.
func (s *snapshot) firstRecord() (Record, bool) {
	for i := range s.samples {
		if r, ok := s.record(i); ok {
			return r, true
		}
	}
	return Record{}, false
}

// allEvents decodes the Advice events (at most EventCount, small), oldest first.
func (s *snapshot) allEvents() []Event {
	evs := []Event{}
	for _, seq := range s.events {
		p := s.payload(eventOffset(seq), EventSize, seq)
		if p == nil {
			continue
		}
		var ev Event
		if j, err := decompress(p); err == nil && json.Unmarshal(j, &ev) == nil {
			evs = append(evs, ev)
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time.Before(evs[j].Time) })
	return evs
}
