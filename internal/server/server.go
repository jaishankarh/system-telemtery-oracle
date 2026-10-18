// Package server serves the JSON API and the embedded dashboard on 127.0.0.1.
package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

//go:embed assets
var assets embed.FS

// Server holds the latest Sample and serves it.
type Server struct {
	latest   atomic.Pointer[collector.Sample]
	interval time.Duration
}

// New creates a Server that reports the given sampling interval.
func New(interval time.Duration) *Server { return &Server{interval: interval} }

// Update publishes a new latest Sample.
func (s *Server) Update(sample collector.Sample) { s.latest.Store(&sample) }

// Handler returns the HTTP handler: /api/system, /api/info and the dashboard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		if sm := s.latest.Load(); sm != nil {
			writeJSON(w, sm)
			return
		}
		http.Error(w, "no sample yet", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"interval_seconds": s.interval.Seconds()})
	})
	sub, _ := fs.Sub(assets, "assets")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
