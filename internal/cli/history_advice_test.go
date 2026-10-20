package cli_test

import (
	"strings"
	"testing"
)

// Advice start and end are recorded in History, so Replay shows when each began and ended.
func TestReplayShowsWhenAdviceStartedAndEnded(t *testing.T) {
	m, p, id := activeThrashing(t, "")
	out := replayOut(t, m, "--minutes", "30")
	if !strings.Contains(out, id) {
		t.Fatalf("replay lacks the started Advice %q:\n%s", id, out)
	}
	for i := 0; i < 8; i++ { // swapping stops; the condition no longer holds
		p.Tick()
	}
	out = replayOut(t, m, "--minutes", "30")
	if !strings.Contains(out, "advice_end") && !strings.Contains(strings.ToLower(out), "ended") {
		t.Fatalf("replay lacks the end of the Advice:\n%s", out)
	}
}
