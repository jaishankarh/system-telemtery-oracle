package cli_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/harness"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

type sliderAt struct {
	Requested time.Time `json:"requested"`
	Found     bool      `json:"found"`
	Sample    *struct {
		Time         time.Time `json:"time"`
		Applications []struct {
			Name         string `json:"name"`
			ProcessCount int    `json:"process_count"`
			Processes    []struct {
				Command string `json:"command"`
			} `json:"processes"`
		} `json:"applications"`
	} `json:"sample"`
	Previous          time.Time `json:"previous_sample"`
	Next              time.Time `json:"next_sample"`
	TotalApplications int       `json:"total_applications"`
	ShownApplications int       `json:"shown_applications"`
	Advice            []struct {
		ID       string    `json:"advice_id"`
		Title    string    `json:"title"`
		Severity string    `json:"severity"`
		Since    time.Time `json:"since"`
	} `json:"advice"`
}

func at(t *testing.T, p *harness.Program, when time.Time) sliderAt {
	t.Helper()
	var a sliderAt
	p.GetJSON("/api/history/at?time="+url.QueryEscape(when.Format(time.RFC3339)), &a)
	return a
}

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func TestHistorySpanBoundsTheSliderAndListsAdviceEvents(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	historyTicks(p, 30) // 60 s
	p.AppendEvent(history.Event{Time: t0.Add(20 * time.Second), Kind: history.AdviceStarted, AdviceID: "thrash", Title: "Thrashing", Category: "hardware_limit", Severity: "warning"})
	var s struct {
		Oldest   time.Time       `json:"oldest"`
		Newest   time.Time       `json:"newest"`
		Interval float64         `json:"interval_seconds"`
		Events   []history.Event `json:"events"`
	}
	p.GetJSON("/api/history/span", &s)
	if !s.Oldest.Equal(t0) || !s.Newest.Equal(t0.Add(60*time.Second)) || s.Interval != 10 {
		t.Fatalf("span = %+v", s)
	}
	if len(s.Events) != 1 || s.Events[0].Title != "Thrashing" {
		t.Fatalf("events = %+v", s.Events)
	}
}

func TestHistoryAtGivesTheNearestSampleAndAdviceActiveThen(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	historyTicks(p, 5)
	p.AppendEvent(history.Event{Time: t0.Add(12 * time.Second), Kind: history.AdviceStarted, AdviceID: "thrash", Title: "Thrashing", Category: "hardware_limit", Severity: "warning"})
	historyTicks(p, 10)
	p.AppendEvent(history.Event{Time: t0.Add(32 * time.Second), Kind: history.AdviceSeverityChanged, AdviceID: "thrash", Title: "Thrashing", Category: "hardware_limit", Severity: "critical"})
	historyTicks(p, 10)
	p.AppendEvent(history.Event{Time: t0.Add(52 * time.Second), Kind: history.AdviceEnded, AdviceID: "thrash", Title: "Thrashing", Category: "hardware_limit", Severity: "critical"})
	historyTicks(p, 10) // 80 s

	a := at(t, p, t0.Add(31*time.Second)) // nearest sample is 30 s
	if !a.Found || !a.Sample.Time.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("at 31s = %+v", a)
	}
	if len(a.Sample.Applications) == 0 || a.Sample.Applications[0].Name == "" {
		t.Fatalf("no Applications in the sample: %+v", a.Sample)
	}
	if len(a.Advice) != 1 || a.Advice[0].ID != "thrash" || a.Advice[0].Severity != "warning" || !a.Advice[0].Since.Equal(t0.Add(12*time.Second)) {
		t.Fatalf("advice at 31s = %+v", a.Advice)
	}
	if got := at(t, p, t0.Add(40*time.Second)).Advice; len(got) != 1 || got[0].Severity != "critical" {
		t.Fatalf("advice at 40s = %+v", got)
	}
	if got := at(t, p, t0.Add(8*time.Second)).Advice; len(got) != 0 {
		t.Fatalf("advice before it started = %+v", got)
	}
	if got := at(t, p, t0.Add(70*time.Second)).Advice; len(got) != 0 {
		t.Fatalf("advice after it ended = %+v", got)
	}
}

func TestHistoryAtSaysWhatIsTruncated(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	historyTicks(p, 5)
	a := at(t, p, t0.Add(10*time.Second))
	if !a.Found || a.ShownApplications != len(a.Sample.Applications) || a.TotalApplications < a.ShownApplications || a.ShownApplications > 10 {
		t.Fatalf("truncation fields: shown %d total %d apps %d", a.ShownApplications, a.TotalApplications, len(a.Sample.Applications))
	}
	for _, app := range a.Sample.Applications {
		if len(app.Processes) > 8 || len(app.Processes) > app.ProcessCount {
			t.Fatalf("%s: %d recorded of %d", app.Name, len(app.Processes), app.ProcessCount)
		}
	}
}

func TestHistoryAtADayAgoOrDuringAGapHasNoSample(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	historyTicks(p, 10)
	p.Stop()
	m.Clock.Advance(time.Hour) // the program was not running for an hour
	p2 := m.Start()
	historyTicks(p2, 10)
	gap := at(t, p2, t0.Add(30*time.Minute))
	if gap.Found || gap.Sample != nil {
		t.Fatalf("a sample inside the gap: %+v", gap)
	}
	if gap.Previous.IsZero() || gap.Next.IsZero() || !gap.Previous.Before(gap.Next) {
		t.Fatalf("gap neighbours %v .. %v", gap.Previous, gap.Next)
	}
	if far := at(t, p2, t0.Add(-24*time.Hour)); far.Found {
		t.Fatalf("a sample before History began: %+v", far)
	}
}

func TestHistoryAtRejectsABadTime(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	for _, q := range []string{"/api/history/at", "/api/history/at?time=yesterday", "/api/history/series?minutes=-3", "/api/history/series?around=x"} {
		if code, _ := p.Get(q); code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", q, code)
		}
	}
}

func TestHistorySeriesGivesGraphPointsAndAdviceMarkers(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	historyTicks(p, 5)
	p.AppendEvent(history.Event{Time: t0.Add(15 * time.Second), Kind: history.AdviceStarted, AdviceID: "thrash", Title: "Thrashing", Severity: "warning"})
	historyTicks(p, 25)
	p.AppendEvent(history.Event{Time: t0.Add(45 * time.Second), Kind: history.AdviceEnded, AdviceID: "thrash", Title: "Thrashing", Severity: "warning"})
	historyTicks(p, 5)
	var s struct {
		From, To time.Time
		Points   []struct {
			Time    time.Time `json:"time"`
			RAMUsed *uint64   `json:"ram_used_bytes"`
		} `json:"points"`
		Events []history.Event `json:"events"`
	}
	p.GetJSON("/api/history/series?around="+url.QueryEscape(t0.Add(30*time.Second).Format(time.RFC3339))+"&minutes=1", &s)
	if len(s.Points) < 5 || s.Points[0].RAMUsed == nil {
		t.Fatalf("points = %+v", s.Points)
	}
	for _, pt := range s.Points {
		if pt.Time.Before(t0) || pt.Time.After(t0.Add(60*time.Second)) {
			t.Fatalf("point outside the window: %v", pt.Time)
		}
	}
	if len(s.Events) != 2 || s.Events[0].Kind != history.AdviceStarted || s.Events[1].Kind != history.AdviceEnded {
		t.Fatalf("events = %+v", s.Events)
	}
}

func TestHistoryAdviceEventsSurviveARestartIntoTheSliderViews(t *testing.T) {
	m := harness.New(t, histFx)
	p := m.Start()
	historyTicks(p, 5)
	p.AppendEvent(history.Event{Time: t0.Add(5 * time.Second), Kind: history.AdviceStarted, AdviceID: "thrash", Title: "Thrashing", Severity: "warning"})
	p.Stop()
	p2 := m.Start()
	historyTicks(p2, 5)
	if a := at(t, p2, t0.Add(8*time.Second)); len(a.Advice) != 1 {
		t.Fatalf("Advice lost over restart: %+v", a.Advice)
	}
}
