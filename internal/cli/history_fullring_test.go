package cli_test

import (
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// Dragging the time slider on a full 24-hour ring must not decode the whole
// ring per request: the dashboard asks Span, At and Series continuously and the
// program runs under a small memory cap.
func TestHistoryQueriesOnAFullRingStayWithinTheMemoryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("fills a 24-hour ring")
	}
	m := harness.New(t, histFx)
	p := m.Start()
	p.Interval = history.SampleEvery // one History sample per tick
	historyTicks(p, history.SlotCount+200) // wraps: the oldest 200 samples are overwritten

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	var span struct {
		Oldest, Newest time.Time
	}
	p.GetJSON("/api/history/span", &span)
	mid := span.Newest.Add(-12 * time.Hour)
	for i := 0; i < 5; i++ { // dragging the slider
		a := at(t, p, mid.Add(time.Duration(i)*time.Minute))
		if !a.Found {
			t.Fatalf("no sample at %v", mid)
		}
		var s struct {
			Points []struct{} `json:"points"`
		}
		p.GetJSON("/api/history/series?minutes=30&around="+url.QueryEscape(mid.Format(time.RFC3339)), &s)
		if len(s.Points) < 100 {
			t.Fatalf("series points = %d", len(s.Points))
		}
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	alloc := after.TotalAlloc - before.TotalAlloc
	if span.Newest.Sub(span.Oldest) < 23*time.Hour {
		t.Fatalf("span = %v .. %v", span.Oldest, span.Newest)
	}
	// Whole-ring decoding cost ~180 MB each request. A window is a few MB.
	if alloc > 40<<20 {
		t.Fatalf("11 slider requests allocated %d MB, want under 40 MB", alloc>>20)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("11 slider requests took %v", elapsed)
	}
}
