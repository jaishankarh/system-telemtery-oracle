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
}

// DefaultAdvisor returns the built-in thresholds.
func DefaultAdvisor() Advisor {
	return Advisor{Window: 5 * time.Minute, PSIPercent: 15, WorkingSetPercent: 90, ThrashPagesPerSec: 100}
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
	default:
		return false, nil
	}
	return true, err
}
