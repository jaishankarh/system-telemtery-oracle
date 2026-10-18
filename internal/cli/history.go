package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/grouping"
	"github.com/jaishankarh/system-telemtery-oracle/internal/history"
)

// ErrKilled, used as a context cancel cause, makes run stop the way a killed
// process would: no final flush, no clean-shutdown mark. Tests use it to
// simulate a crash.
var ErrKilled = fmt.Errorf("killed")

// withHistory serves GET /api/history?around=&last_boot=&minutes= from the
// running instance's History.
func withHistory(rec *history.Recorder, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		q, err := queryFromValues(r.URL.Query().Get("around"), r.URL.Query().Get("last_boot") == "true", r.URL.Query().Get("minutes"), time.Now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if rec == nil {
			http.Error(w, "History is unavailable", http.StatusServiceUnavailable)
			return
		}
		res, err := rec.Query(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(res)
	})
	registerHistoryViews(mux, rec)
	mux.Handle("/", next)
	return mux
}

func queryFromValues(around string, lastBoot bool, minutes string, now func() time.Time) (history.Query, error) {
	q := history.Query{LastBoot: lastBoot}
	if minutes != "" {
		n, err := strconv.Atoi(minutes)
		if err != nil || n <= 0 {
			return q, fmt.Errorf("minutes must be a positive integer, got %q", minutes)
		}
		q.Minutes = n
	}
	if around != "" {
		t, err := parseTime(around, now())
		if err != nil {
			return q, err
		}
		q.Around = t
	}
	if lastBoot && !q.Around.IsZero() {
		return q, fmt.Errorf("--around and --last-boot are mutually exclusive")
	}
	return q, nil
}

// parseTime accepts RFC 3339, "2006-01-02 15:04[:05]" or "15:04[:05]" (today),
// the latter two in the machine's local zone.
func parseTime(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location()), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q (use RFC 3339, \"YYYY-MM-DD HH:MM\" or \"HH:MM\")", s)
}

// replay implements `replay [--around TIME | --last-boot] [--minutes N] [--processes]`.
// It asks the running instance if there is one, otherwise reads the History file.
func replay(env Env, args []string) int {
	fl := flag.NewFlagSet("replay", flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	around := fl.String("around", "", "show the window centred on this time")
	last := fl.Bool("last-boot", false, "show the lead-up to the end of the last session")
	minutes := fl.Int("minutes", 0, "window length in minutes (default 10)")
	procs := fl.Bool("processes", false, "list Processes with command lines for every sample, not only the last")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	ms := ""
	if *minutes != 0 {
		ms = strconv.Itoa(*minutes)
	}
	q, err := queryFromValues(*around, *last, ms, env.Machine.Clock.Now)
	if err != nil {
		fmt.Fprintln(env.Stderr, "error:", err)
		return 2
	}
	res, ok := askInstance(env, q)
	if !ok {
		res, err = history.ReadFile(env.Machine.StateDir, q)
		if err != nil {
			fmt.Fprintln(env.Stderr, "error:", err)
			return 1
		}
	}
	history.Render(env.Stdout, res, *procs)
	return 0
}

// askInstance queries the running instance recorded in the state dir, if any.
func askInstance(env Env, q history.Query) (history.Result, bool) {
	b, err := os.ReadFile(env.Machine.StateDir + "/" + history.InstanceFile)
	if err != nil {
		return history.Result{}, false
	}
	v := "?last_boot=" + strconv.FormatBool(q.LastBoot)
	if q.Minutes > 0 {
		v += "&minutes=" + strconv.Itoa(q.Minutes)
	}
	if !q.Around.IsZero() {
		v += "&around=" + q.Around.Format(time.RFC3339)
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + strings.TrimSpace(string(b)) + "/api/history" + v)
	if err != nil {
		return history.Result{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return history.Result{}, false
	}
	var res history.Result
	if json.NewDecoder(resp.Body).Decode(&res) != nil {
		return history.Result{}, false
	}
	return res, true
}

// startHistory opens History and surfaces an unclean previous shutdown. A
// state dir that cannot be written disables History but not the program.
// When ctx is cancelled with ErrKilled the returned Recorder is abandoned,
// not closed, so the file looks like the process was killed.
func startHistory(env Env, ctx context.Context, g *grouping.Grouper, first collector.Sample) *killableRecorder {
	rec, err := history.Open(env.Machine, g)
	if err != nil {
		fmt.Fprintln(env.Stderr, "warning: History disabled:", err)
		return &killableRecorder{ctx: ctx}
	}
	if unclean, at := rec.PreviousSession(); unclean {
		when := "an unknown time"
		if !at.IsZero() {
			when = at.Format(time.RFC3339)
		}
		fmt.Fprintf(env.Stderr, "notice: last session ended without clean shutdown at %s\n", when)
	}
	if env.OnRecorder != nil {
		env.OnRecorder(rec)
	}
	rec.Observe(first)
	return &killableRecorder{Recorder: rec, ctx: ctx}
}

type killableRecorder struct {
	*history.Recorder
	ctx context.Context
}

// Close ends the session cleanly, unless the process was "killed".
func (k *killableRecorder) Close() {
	if k.Recorder == nil {
		return
	}
	if context.Cause(k.ctx) == ErrKilled {
		k.Recorder.Abandon()
		return
	}
	k.Recorder.Close()
}

// announceInstance records the server address in the state dir so `replay`
// can ask this instance; the returned func removes it on clean exit.
func announceInstance(env Env, ctx context.Context, addr string) func() {
	path := env.Machine.StateDir + "/" + history.InstanceFile
	if os.MkdirAll(env.Machine.StateDir, 0o700) != nil || os.WriteFile(path, []byte(addr), 0o600) != nil {
		return func() {}
	}
	return func() {
		if context.Cause(ctx) != ErrKilled {
			os.Remove(path)
		}
	}
}
