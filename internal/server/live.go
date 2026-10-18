package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
	"github.com/jaishankarh/system-telemtery-oracle/internal/live"
)

// liveFeed is the Live buffer plus the stream of new Points to dashboards.
// Chart API:
//
//	GET /api/live         {window_seconds, interval_seconds, points: [live.Point...]} oldest first
//	GET /api/live/stream  Server-Sent Events, one "data: <live.Point JSON>" per sample
//	GET /api/live/stats   {clients, stream_encodes}: connected streams, and how many Points were
//	                      ever encoded for the stream (stays flat while no client is connected)
//
// Publishing with no client connected does no serialisation at all.
type liveFeed struct {
	buf     live.Buffer
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	encodes atomic.Int64
}

func newLiveFeed() *liveFeed { return &liveFeed{clients: map[chan []byte]struct{}{}} }

func (f *liveFeed) publish(s collector.Sample, apps []grouping.Application) {
	p := live.NewPoint(s, apps)
	f.buf.Add(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.clients) == 0 {
		return // nobody is watching: no serialisation
	}
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	f.encodes.Add(1)
	for c := range f.clients {
		select {
		case c <- b:
		default: // slow client: drop this event, it catches up with the next
		}
	}
}

func (f *liveFeed) subscribe() chan []byte {
	c := make(chan []byte, 4)
	f.mu.Lock()
	f.clients[c] = struct{}{}
	f.mu.Unlock()
	return c
}

func (f *liveFeed) unsubscribe(c chan []byte) {
	f.mu.Lock()
	delete(f.clients, c)
	f.mu.Unlock()
}

func (s *Server) registerLive(mux *http.ServeMux) {
	mux.HandleFunc("/api/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"window_seconds": int(live.Window.Seconds()),
			"interval_seconds": s.interval.Seconds(), "points": s.live.buf.Snapshot()})
	})
	mux.HandleFunc("/api/live/stats", func(w http.ResponseWriter, r *http.Request) {
		s.live.mu.Lock()
		n := len(s.live.clients)
		s.live.mu.Unlock()
		writeJSON(w, map[string]any{"clients": n, "stream_encodes": s.live.encodes.Load()})
	})
	mux.HandleFunc("/api/live/stream", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		c := s.live.subscribe()
		defer s.live.unsubscribe(c)
		fmt.Fprint(w, "retry: 2000\n\n")
		fl.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case b := <-c:
				fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			}
		}
	})
}
