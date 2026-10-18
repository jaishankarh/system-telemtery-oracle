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
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// snapshot is everything valid in a ring file.
type snapshot struct {
	hdr     header
	samples []rawSlot
	events  []rawSlot
}

type rawSlot struct {
	seq     uint64
	payload []byte
}

var errNoHistory = errors.New("no History file")

func readSnapshot(path string) (*snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoHistory
		}
		return nil, err
	}
	defer f.Close()
	hb := make([]byte, HeaderSize)
	if _, err := io.ReadFull(f, hb); err != nil {
		return nil, errNoHistory
	}
	h, ok := decodeHeader(hb)
	if !ok {
		return nil, errNoHistory
	}
	s := &snapshot{hdr: h}
	buf := make([]byte, SlotSize)
	for i := 0; i < SlotCount; i++ {
		if _, err := f.ReadAt(buf, slotOffset(uint64(i))); err != nil {
			break
		}
		if seq, p, ok := decodeSlot(buf); ok {
			s.samples = append(s.samples, rawSlot{seq, append([]byte(nil), p...)})
		}
	}
	ebuf := make([]byte, EventSize)
	for i := 0; i < EventCount; i++ {
		if _, err := f.ReadAt(ebuf, eventOffset(uint64(i))); err != nil {
			break
		}
		if seq, p, ok := decodeSlot(ebuf); ok {
			s.events = append(s.events, rawSlot{seq, append([]byte(nil), p...)})
		}
	}
	sort.Slice(s.samples, func(i, j int) bool { return s.samples[i].seq < s.samples[j].seq })
	sort.Slice(s.events, func(i, j int) bool { return s.events[i].seq < s.events[j].seq })
	return s, nil
}
