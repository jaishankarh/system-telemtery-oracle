package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/jaishankarh/system-telemtery-oracle/internal/advisor"
)

// SetAdvisor makes the Server feed every Sample to the Advisor and serve its
// Advice at /api/advice (Snapshot) and /api/advice/events (Events).
func (s *Server) SetAdvisor(a *advisor.Advisor) { s.advisor = a }

func (s *Server) adviceRoutes(mux *http.ServeMux) {
	s.adviceActionRoutes(mux)
	mux.HandleFunc("/api/advice", func(w http.ResponseWriter, r *http.Request) {
		if s.advisor == nil {
			http.Error(w, "advisor not running", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, s.advisor.Snapshot())
	})
	mux.HandleFunc("/api/advice/events", func(w http.ResponseWriter, r *http.Request) {
		if s.advisor == nil {
			http.Error(w, "advisor not running", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]any{"events": s.advisor.Events()})
	})
}

// The state-changing endpoints are POST-only and refuse anything a web page in
// the user's browser could send: the server has no auth (ADR 0003), so a
// cross-origin request must never change Hidden/Snoozed state.
func (s *Server) adviceActionRoutes(mux *http.ServeMux) {
	act := func(name string, do func(a *advisor.Advisor, req adviceAction) error) {
		mux.HandleFunc("/api/advice/"+name, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			if !sameOriginRequest(r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
			if s.advisor == nil {
				http.Error(w, "advisor not running", http.StatusServiceUnavailable)
				return
			}
			var req adviceAction
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ID == "" {
				http.Error(w, `body must be JSON like {"id": "..."}`, http.StatusBadRequest)
				return
			}
			switch err := do(s.advisor, req); {
			case err == nil:
				writeJSON(w, s.advisor.Snapshot())
			case errors.Is(err, advisor.ErrNoAdvice):
				http.Error(w, err.Error(), http.StatusNotFound)
			case errors.Is(err, advisor.ErrNoPreset):
				http.Error(w, err.Error(), http.StatusBadRequest)
			default:
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
	}
	act("hide", func(a *advisor.Advisor, q adviceAction) error { return a.Hide(q.ID) })
	act("unhide", func(a *advisor.Advisor, q adviceAction) error { return a.Unhide(q.ID) })
	act("snooze", func(a *advisor.Advisor, q adviceAction) error { return a.Snooze(q.ID, q.Preset) })
}

type adviceAction struct {
	ID     string `json:"id"`
	Preset string `json:"preset"`
}

// sameOriginRequest accepts a request only when it is JSON, addressed to a
// loopback Host (against DNS rebinding), and, if the browser says where it came
// from, came from this very origin.
func sameOriginRequest(r *http.Request) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		return false // a cross-site form or text/plain post cannot be JSON
	}
	if !loopbackHost(r.Host) {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	if f := r.Header.Get("Sec-Fetch-Site"); f != "" && f != "same-origin" && f != "none" {
		return false
	}
	return true
}
