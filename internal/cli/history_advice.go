package cli

import (
	"github.com/jaishankarh/system-telemtery-oracle/internal/advisor"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// adviceEventsToHistory records Advice start, end and Severity changes in History
// so that Replay shows when each piece of Advice began and ended.
func adviceEventsToHistory(rec history.EventAppender) func(advisor.Event) {
	return func(e advisor.Event) {
		kind := history.AdviceStarted
		switch e.Type {
		case "advice_ended":
			kind = history.AdviceEnded
		case "advice_severity_changed":
			kind = history.AdviceSeverityChanged
		}
		apps := make([]string, 0, len(e.Applications))
		for _, a := range e.Applications {
			apps = append(apps, a.Name)
		}
		detail := e.Rule
		if e.PreviousSeverity != "" {
			detail += ": " + string(e.PreviousSeverity) + " to " + string(e.Severity)
		}
		rec.AppendEvent(history.Event{
			Time:         e.Time,
			Kind:         kind,
			AdviceID:     e.AdviceID,
			Title:        e.Title,
			Category:     string(e.Category),
			Severity:     string(e.Severity),
			Applications: apps,
			Detail:       detail,
		})
	}
}
