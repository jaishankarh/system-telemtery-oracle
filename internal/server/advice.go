package server

import (
	"net/http"

	"github.com/jaishankarh/system-telemtery-oracle/internal/advisor"
)

// SetAdvisor makes the Server feed every Sample to the Advisor and serve its
// Advice at /api/advice (Snapshot) and /api/advice/events (Events).
func (s *Server) SetAdvisor(a *advisor.Advisor) { s.advisor = a }

func (s *Server) adviceRoutes(mux *http.ServeMux) {
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
