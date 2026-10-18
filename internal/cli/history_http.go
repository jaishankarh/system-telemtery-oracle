package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// registerHistoryViews serves what the dashboard time slider needs, all read-only:
//
//	GET /api/history/span                          slider bounds and every Advice event
//	GET /api/history/at?time=RFC3339               the History sample nearest that time, with the Advice active then
//	GET /api/history/series?around=RFC3339&minutes=N   Graphs data (default: newest 60 minutes, at most 24 h)
func registerHistoryViews(mux *http.ServeMux, rec *history.Recorder) {
	serve := func(path string, do func(r *http.Request) (any, int, error)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if rec == nil {
				http.Error(w, "History is unavailable", http.StatusServiceUnavailable)
				return
			}
			v, code, err := do(r)
			if err != nil {
				http.Error(w, err.Error(), code)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			json.NewEncoder(w).Encode(v)
		})
	}
	serve("/api/history/span", func(r *http.Request) (any, int, error) {
		v, err := rec.Span()
		return v, http.StatusInternalServerError, err
	})
	serve("/api/history/at", func(r *http.Request) (any, int, error) {
		t, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
		if err != nil {
			return nil, http.StatusBadRequest, errBadTime
		}
		v, err := rec.At(t)
		return v, http.StatusInternalServerError, err
	})
	serve("/api/history/series", func(r *http.Request) (any, int, error) {
		var around time.Time
		if s := r.URL.Query().Get("around"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return nil, http.StatusBadRequest, errBadTime
			}
			around = t
		}
		mins := 0
		if s := r.URL.Query().Get("minutes"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				return nil, http.StatusBadRequest, errBadMinutes
			}
			mins = n
		}
		v, err := rec.Series(around, mins)
		return v, http.StatusInternalServerError, err
	})
}

type httpErr string

func (e httpErr) Error() string { return string(e) }

const (
	errBadTime    = httpErr("time must be RFC 3339, for example 2026-10-19T12:00:00Z")
	errBadMinutes = httpErr("minutes must be a positive integer")
)
