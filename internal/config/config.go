// Package config loads the optional TOML config file. Every value has a
// working default; the file never needs to exist. Only the TOML subset needed
// is parsed (top-level `key = value`, comments, strings, integers), with no
// third-party dependency; later tickets extend it for thresholds and rules.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MinInterval is the shortest allowed sampling interval.
const MinInterval = time.Second

// DefaultPath is read when --config is not given (missing is fine).
const DefaultPath = "/etc/sys-telemetry-app/config.toml"

// Config holds every setting with its default.
type Config struct {
	Port     int
	Interval time.Duration
}

// Default returns the built-in settings.
func Default() Config { return Config{Port: 9191, Interval: 2 * time.Second} }

// Parse overlays the file contents onto the defaults.
func Parse(data []byte) (Config, error) {
	c := Default()
	for i, line := range strings.Split(string(data), "\n") {
		if j := strings.Index(line, "#"); j >= 0 && !strings.Contains(line[:j], `"`) {
			line = line[:j]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("line %d: expected key = value", i+1)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "port":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 65535 {
				return c, fmt.Errorf("line %d: invalid port %q", i+1, v)
			}
			c.Port = n
		case "interval":
			d, err := time.ParseDuration(strings.Trim(v, `"`))
			if err != nil {
				return c, fmt.Errorf("line %d: invalid interval %q", i+1, v)
			}
			c.Interval = d
		default:
			return c, fmt.Errorf("line %d: unknown key %q", i+1, k)
		}
	}
	return c, c.Validate()
}

// Validate checks limits.
func (c Config) Validate() error {
	if c.Interval < MinInterval {
		return fmt.Errorf("interval %v is below the minimum %v", c.Interval, MinInterval)
	}
	return nil
}
