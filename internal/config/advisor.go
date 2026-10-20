package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Advisor holds the thresholds of the Advisor. Config file keys are
// advisor_window, advisor_psi_percent, advisor_working_set_percent and
// advisor_thrash_pages_per_sec.
type Advisor struct {
	// Window is how long a condition must hold to be Sustained.
	Window time.Duration
	// PSIPercent: memory pressure (PSI some or full, avg10) above this is high.
	PSIPercent float64
	// WorkingSetPercent: Working Set above this share of physical RAM is too big.
	WorkingSetPercent float64
	// ThrashPagesPerSec: swap-in and swap-out both above this is Thrashing.
	ThrashPagesPerSec float64
	// SnoozePresets are the periods offered for Snoozed Advice (config key
	// snooze_presets, e.g. "1d, 7d"; units d, h, m or any Go duration).
	SnoozePresets []SnoozePreset
}

// SnoozePreset is one offered Snoozed period; Label is how it is named in the
// API and dashboard.
type SnoozePreset struct {
	Label    string
	Duration time.Duration
}

// DefaultAdvisor returns the built-in thresholds.
func DefaultAdvisor() Advisor {
	return Advisor{Window: 5 * time.Minute, PSIPercent: 15, WorkingSetPercent: 90, ThrashPagesPerSec: 100,
		SnoozePresets: []SnoozePreset{{"1d", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour}}}
}

// set applies one config line; ok is false when the key is not an advisor key.
func (a *Advisor) set(key, v string, line int) (ok bool, err error) {
	num := func(max float64) (float64, error) {
		f, e := strconv.ParseFloat(v, 64)
		if e != nil || f <= 0 || f > max {
			return 0, fmt.Errorf("line %d: invalid %s %q", line, key, v)
		}
		return f, nil
	}
	switch key {
	case "advisor_window":
		d, e := time.ParseDuration(strings.Trim(v, `"`))
		if e != nil || d <= 0 {
			return true, fmt.Errorf("line %d: invalid advisor_window %q", line, v)
		}
		a.Window = d
	case "advisor_psi_percent":
		a.PSIPercent, err = num(100)
	case "advisor_working_set_percent":
		a.WorkingSetPercent, err = num(1000)
	case "advisor_thrash_pages_per_sec":
		a.ThrashPagesPerSec, err = num(1e9)
	case "snooze_presets":
		var ps []SnoozePreset
		seen := map[string]bool{}
		for _, f := range strings.Split(strings.Trim(v, `"`), ",") {
			f = strings.TrimSpace(f)
			d, e := parsePeriod(f)
			if e != nil || seen[f] {
				return true, fmt.Errorf("line %d: invalid snooze_presets entry %q", line, f)
			}
			seen[f] = true
			ps = append(ps, SnoozePreset{f, d})
		}
		a.SnoozePresets = ps
	default:
		return false, nil
	}
	return true, err
}

// parsePeriod reads "7d", "36h" or any Go duration; it must be positive.
func parsePeriod(s string) (time.Duration, error) {
	var d time.Duration
	var err error
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		if days, err = strconv.Atoi(n); err == nil {
			d = time.Duration(days) * 24 * time.Hour
		}
	} else {
		d, err = time.ParseDuration(s)
	}
	if err == nil && d <= 0 {
		err = fmt.Errorf("not positive")
	}
	return d, err
}
