package harness

import (
	"os"
	"path/filepath"

	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// HistoryPath is the ring file on the fake machine.
func (m *Machine) HistoryPath() string { return history.Path(filepath.Join(m.Root, "state")) }

// TearNewestHistorySlot simulates a crash in the middle of a flush: the most
// recently written sample slot is left half written. Call it after Kill.
func (m *Machine) TearNewestHistorySlot() {
	m.t.Helper()
	f, err := os.OpenFile(m.HistoryPath(), os.O_RDWR, 0)
	if err != nil {
		m.t.Fatal(err)
	}
	defer f.Close()
	var newest int64 = -1
	var best uint64
	buf := make([]byte, 16)
	for i := 0; i < history.SlotCount; i++ {
		off := int64(history.HeaderSize + i*history.SlotSize)
		if _, err := f.ReadAt(buf, off); err != nil {
			m.t.Fatal(err)
		}
		seq := uint64(buf[4]) | uint64(buf[5])<<8 | uint64(buf[6])<<16 | uint64(buf[7])<<24
		if seq > best {
			best, newest = seq, off
		}
	}
	if newest < 0 {
		m.t.Fatal("no sample slot to tear")
	}
	garbage := make([]byte, 24)
	for i := range garbage {
		garbage[i] = 0xAB
	}
	if _, err := f.WriteAt(garbage, newest+20); err != nil {
		m.t.Fatal(err)
	}
}
