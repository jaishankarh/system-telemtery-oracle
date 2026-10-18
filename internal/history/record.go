package history

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
)

// How much of a sample is kept before the slot size forces it to shrink.
const (
	maxApps         = 10
	maxProcsPerApp  = 8
	warningPressure = 15.0  // memory PSI some avg10 percent (SPEC default)
	thrashPagesSec  = 100.0 // swap-in and swap-out both above this = Thrashing
)

// Record is one 10-second History sample: system metrics plus per-Application
// totals for the top Applications by Working Set (PSS + swap) and the
// Processes inside them with full command lines.
type Record struct {
	Session     uint64            `json:"session"`
	Time        time.Time         `json:"time"`
	CPU         collector.CPU     `json:"cpu"`
	Memory      *collector.Memory `json:"memory"`
	Swap        collector.Swap    `json:"swap"`
	Pressure    *Pressure         `json:"memory_pressure,omitempty"`
	Thrashing   bool              `json:"thrashing,omitempty"`
	WorkingSet  *uint64           `json:"working_set_bytes,omitempty"`
	Apps        []AppRecord       `json:"applications"`
	OmittedApps int               `json:"omitted_applications,omitempty"`
}

// Pressure is memory PSI (avg10, percent). Nil fields: the kernel has no PSI.
type Pressure struct {
	Some *float64 `json:"some"`
	Full *float64 `json:"full"`
}

// AppRecord is one Application's totals and its recorded Processes.
type AppRecord struct {
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	PSS          *uint64      `json:"pss_bytes"`
	Swap         *uint64      `json:"swap_bytes"`
	RSS          *uint64      `json:"rss_bytes"`
	CPU          *float64     `json:"cpu_percent"`
	ProcessCount int          `json:"process_count"`
	Processes    []ProcRecord `json:"processes,omitempty"`
	OmittedProcs int          `json:"omitted_processes,omitempty"`
}

// ProcRecord is a Process with its full command line.
type ProcRecord struct {
	PID     int      `json:"pid"`
	User    string   `json:"user,omitempty"`
	Command string   `json:"command"`
	PSS     *uint64  `json:"pss_bytes"`
	Swap    *uint64  `json:"swap_bytes"`
	CPU     *float64 `json:"cpu_percent"`
}

func ws(p ProcRecord) uint64 {
	var t uint64
	if p.PSS != nil {
		t += *p.PSS
	}
	if p.Swap != nil {
		t += *p.Swap
	}
	return t
}

// buildRecord keeps the biggest maxApps Applications (apps arrive ordered by
// Working Set) and their biggest Processes.
func buildRecord(session uint64, s collector.Sample, apps []grouping.Application, p *Pressure) Record {
	r := Record{Session: session, Time: s.Time, CPU: s.CPU, Memory: s.Memory, Swap: s.Swap, Pressure: p, Apps: []AppRecord{}}
	r.Thrashing = thrashing(s.Swap)
	var total uint64
	have := false
	for _, a := range apps {
		if t := a.PSSPlusSwap(); t != nil {
			total += *t
			have = true
		}
	}
	if have {
		r.WorkingSet = &total
	}
	for i, a := range apps {
		if i >= maxApps {
			r.OmittedApps = len(apps) - maxApps
			break
		}
		ar := AppRecord{Name: a.Name, Kind: a.Kind, PSS: a.PSS, Swap: a.Swap, RSS: a.RSS, CPU: a.CPU, ProcessCount: a.ProcessCount}
		for _, m := range a.Processes {
			ar.Processes = append(ar.Processes, ProcRecord{PID: m.PID, User: m.User, Command: m.Command, PSS: m.PSS, Swap: m.Swap, CPU: m.CPU})
		}
		sort.SliceStable(ar.Processes, func(i, j int) bool { return ws(ar.Processes[i]) > ws(ar.Processes[j]) })
		if len(ar.Processes) > maxProcsPerApp {
			ar.OmittedProcs = len(ar.Processes) - maxProcsPerApp
			ar.Processes = ar.Processes[:maxProcsPerApp]
		}
		r.Apps = append(r.Apps, ar)
	}
	return r
}

func thrashing(s collector.Swap) bool {
	return s.InPagesPerSec != nil && s.OutPagesPerSec != nil && *s.InPagesPerSec > thrashPagesSec && *s.OutPagesPerSec > thrashPagesSec
}

// shrink reduces a Record one step so it fits a slot: first fewer Processes per
// Application (totals stay), then fewer Applications. False when nothing is left to drop.
func shrink(r *Record) bool {
	most := 0
	for _, a := range r.Apps {
		if len(a.Processes) > most {
			most = len(a.Processes)
		}
	}
	if most > 0 {
		for i := range r.Apps {
			if n := len(r.Apps[i].Processes); n >= most {
				r.Apps[i].OmittedProcs += n - (most - 1)
				r.Apps[i].Processes = r.Apps[i].Processes[:most-1]
			}
		}
		return true
	}
	if len(r.Apps) > 1 {
		r.Apps = r.Apps[:len(r.Apps)-1]
		r.OmittedApps++
		return true
	}
	return false
}

func compress(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func decompress(b []byte) ([]byte, error) {
	return io.ReadAll(flate.NewReader(bytes.NewReader(b)))
}

// encodeRecord compresses r into a slot payload, shrinking it until it fits.
func encodeRecord(r Record) []byte {
	for {
		j, _ := json.Marshal(r)
		if c := compress(j); slotHead+len(c) <= SlotSize {
			return c
		}
		if !shrink(&r) {
			r.Apps = nil
			j, _ = json.Marshal(r)
			return compress(j)
		}
	}
}

func decodeRecord(p []byte) (Record, bool) {
	j, err := decompress(p)
	if err != nil {
		return Record{}, false
	}
	var r Record
	return r, json.Unmarshal(j, &r) == nil
}
