package server

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
)

// applications serves GET /api/applications?sort=<field>&order=asc|desc&filter=<regex>.
// sort is any Application JSON total (name, kind, rss_bytes, pss_bytes,
// swap_bytes, cpu_percent, disk_read_bytes_per_sec, disk_write_bytes_per_sec,
// process_count) or pss_swap (the default, descending). filter is a
// case-insensitive regex over the Application name and its Processes' command
// lines. Missing values sort last in either order.
func (s *Server) applications(w http.ResponseWriter, r *http.Request) {
	sm, ap := s.latest.Load(), s.apps.Load()
	if sm == nil || ap == nil {
		http.Error(w, "no sample yet", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	key := q.Get("sort")
	if key == "" {
		key = "pss_swap"
	}
	cmp, textual, ok := applicationSort(key)
	if !ok {
		http.Error(w, "unknown sort column "+strconv.Quote(key), http.StatusBadRequest)
		return
	}
	desc := !textual
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
	var re *regexp.Regexp
	if f := strings.TrimSpace(q.Get("filter")); f != "" {
		var err error
		if re, err = regexp.Compile("(?i)" + f); err != nil {
			http.Error(w, "bad filter regex: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	list := make([]grouping.Application, 0, len(*ap))
	for _, a := range *ap {
		if re == nil || matchesApplication(re, a) {
			list = append(list, a)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		c := cmp(a, b) // -2: a missing, +2: b missing, else -1/0/1
		switch {
		case c == 0:
			return a.ID < b.ID
		case c == -2:
			return false
		case c == 2:
			return true
		case desc:
			return c > 0
		}
		return c < 0
	})
	writeJSON(w, map[string]any{"time": sm.Time, "applications": list, "missing": nonNil(sm.ProcessMissing)})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func matchesApplication(re *regexp.Regexp, a grouping.Application) bool {
	if re.MatchString(a.Name) {
		return true
	}
	for _, m := range a.Processes {
		if re.MatchString(m.Command) {
			return true
		}
	}
	return false
}

func applicationSort(key string) (cmp func(a, b grouping.Application) int, textual, ok bool) {
	switch key {
	case "name":
		return func(a, b grouping.Application) int { return cmpText(a.Name, b.Name) }, true, true
	case "kind":
		return func(a, b grouping.Application) int { return cmpText(a.Kind, b.Kind) }, true, true
	case "rss_bytes":
		return func(a, b grouping.Application) int { return cmpOpt(a.RSS, b.RSS) }, false, true
	case "pss_bytes":
		return func(a, b grouping.Application) int { return cmpOpt(a.PSS, b.PSS) }, false, true
	case "swap_bytes":
		return func(a, b grouping.Application) int { return cmpOpt(a.Swap, b.Swap) }, false, true
	case "pss_swap":
		return func(a, b grouping.Application) int { return cmpOpt(a.PSSPlusSwap(), b.PSSPlusSwap()) }, false, true
	case "cpu_percent":
		return func(a, b grouping.Application) int { return cmpOpt(a.CPU, b.CPU) }, false, true
	case "disk_read_bytes_per_sec":
		return func(a, b grouping.Application) int { return cmpOpt(a.Read, b.Read) }, false, true
	case "disk_write_bytes_per_sec":
		return func(a, b grouping.Application) int { return cmpOpt(a.Write, b.Write) }, false, true
	case "process_count":
		return func(a, b grouping.Application) int { return cmpOpt(&a.ProcessCount, &b.ProcessCount) }, false, true
	}
	return nil, false, false
}
