package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

func TestStateDirAndFilesAreRootOnly(t *testing.T) {
	m, p, id := activeThrashing(t, "")
	mustPost(t, p, "/api/advice/hide", `{"id":"`+id+`"}`)
	dir := filepath.Join(m.Root, "state")
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %v (%v), want 0700", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(filepath.Join(dir, "state.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("state.json mode = %v (%v), want 0600", st.Mode().Perm(), err)
	}
}

func TestAnExistingWorldReadableStateDirIsTightened(t *testing.T) {
	m, p, _ := activeThrashing(t, "")
	p.Stop()
	dir := filepath.Join(m.Root, "state")
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m.Start()
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %v after start, want 0700", st.Mode().Perm())
	}
}

func TestReplayIgnoresAnInstanceAddressThatIsNotThisProgram(t *testing.T) {
	m, _, _ := activeThrashing(t, "")
	// What is on the recorded address is some other service (a stale port reused).
	imposter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"live":true,"samples":[],"events":[{"time":"2026-10-19T12:00:00Z","kind":"advice_start","advice_id":"x","title":"IMPOSTER"}]}`)
	}))
	defer imposter.Close()
	m.WriteFile("state/"+history.InstanceFile, strings.TrimPrefix(imposter.URL, "http://"))
	if out := replayOut(t, m, "--minutes", "5"); strings.Contains(out, "IMPOSTER") {
		t.Fatalf("replay trusted an address that is not this program:\n%s", out)
	}
}
