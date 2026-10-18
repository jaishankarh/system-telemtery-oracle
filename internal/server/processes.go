package server

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// processes serves GET /api/processes?sort=<field>&order=asc|desc&filter=<text>.
// sort is any Process JSON field name, or pss_swap (the default, descending).
// filter matches a PID exactly, or is a case-insensitive regex over the name
// and the full command line. Missing values sort last in either order.
func (s *Server) processes(w http.ResponseWriter, r *http.Request) {
	sm := s.latest.Load()
	if sm == nil {
		http.Error(w, "no sample yet", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	key := q.Get("sort")
	if key == "" {
		key = "pss_swap"
	}
	col, ok := processKeys[key]
	if !ok {
		http.Error(w, "unknown sort column "+strconv.Quote(key), http.StatusBadRequest)
		return
	}
	desc := !col.textual
	switch q.Get("order") {
	case "asc":
		desc = false
	case "desc":
		desc = true
	case "":
	default:
		http.Error(w, "order must be asc or desc", http.StatusBadRequest)
		return
	}

	list := make([]collector.Process, 0, len(sm.Processes))
	var re *regexp.Regexp
	pid := -1
	if f := strings.TrimSpace(q.Get("filter")); f != "" {
		var err error
		if re, err = regexp.Compile("(?i)" + f); err != nil {
			http.Error(w, "bad filter regex: "+err.Error(), http.StatusBadRequest)
			return
		}
		if n, err := strconv.Atoi(f); err == nil {
			pid = n
		}
	}
	for _, p := range sm.Processes {
		if re == nil || p.PID == pid || re.MatchString(p.Name) || re.MatchString(p.Command) {
			list = append(list, p)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		c := col.cmp(a, b) // -2: a missing, +2: b missing, else -1/0/1
		switch {
		case c == 0:
			return a.PID < b.PID
		case c == -2:
			return false
		case c == 2:
			return true
		case desc:
			return c > 0
		}
		return c < 0
	})
	missing := sm.ProcessMissing
	if missing == nil {
		missing = []string{}
	}
	writeJSON(w, map[string]any{"time": sm.Time, "processes": list, "missing": missing})
}

type column struct {
	cmp     func(a, b collector.Process) int
	textual bool
}

func cmpOpt[T int | uint64 | float64](a, b *T) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -2
	case b == nil:
		return 2
	case *a < *b:
		return -1
	case *a > *b:
		return 1
	}
	return 0
}

func cmpText(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }

func sum(p collector.Process) *uint64 {
	if p.PSS == nil && p.Swap == nil {
		return nil
	}
	var t uint64
	if p.PSS != nil {
		t += *p.PSS
	}
	if p.Swap != nil {
		t += *p.Swap
	}
	return &t
}

// processKeys has one entry per Process JSON field, plus pss_swap.
var processKeys = map[string]column{
	"pid":                      {func(a, b collector.Process) int { return cmpOpt(&a.PID, &b.PID) }, true},
	"user":                     {func(a, b collector.Process) int { return cmpText(a.User, b.User) }, true},
	"name":                     {func(a, b collector.Process) int { return cmpText(a.Name, b.Name) }, true},
	"command":                  {func(a, b collector.Process) int { return cmpText(a.Command, b.Command) }, true},
	"rss_bytes":                {func(a, b collector.Process) int { return cmpOpt(a.RSS, b.RSS) }, false},
	"pss_bytes":                {func(a, b collector.Process) int { return cmpOpt(a.PSS, b.PSS) }, false},
	"swap_bytes":               {func(a, b collector.Process) int { return cmpOpt(a.Swap, b.Swap) }, false},
	"pss_swap":                 {func(a, b collector.Process) int { return cmpOpt(sum(a), sum(b)) }, false},
	"cpu_percent":              {func(a, b collector.Process) int { return cmpOpt(a.CPU, b.CPU) }, false},
	"disk_read_bytes_per_sec":  {func(a, b collector.Process) int { return cmpOpt(a.Read, b.Read) }, false},
	"disk_write_bytes_per_sec": {func(a, b collector.Process) int { return cmpOpt(a.Write, b.Write) }, false},
	"threads":                  {func(a, b collector.Process) int { return cmpOpt(&a.Threads, &b.Threads) }, false},
	"open_fds":                 {func(a, b collector.Process) int { return cmpOpt(a.OpenFDs, b.OpenFDs) }, false},
}
