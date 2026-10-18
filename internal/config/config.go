// Package config loads the optional TOML config file. Every value has a
// working default; the file never needs to exist. Only the TOML subset needed
// is parsed (`key = value`, [[group]] and [[rename]] tables, comments, strings, integers), with no
// third-party dependency; later tickets extend it for thresholds and rules.
package config

import (
	"fmt"
	"regexp"
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
	// Grouping holds the user's grouping rules and renames.
	Grouping Grouping
}

// Grouping is the user's say in how Processes roll up into Applications.
type Grouping struct {
	Rules   []GroupRule // [[group]] tables, in file order; first match wins
	Renames []Rename    // [[rename]] tables
}

// GroupRule puts the Processes it matches into the Application called Name.
// Every field that is set must match; at least one is set.
type GroupRule struct {
	Name       string
	Executable string // basename of the executable, as in argv[0] or the process name
	Command    string // regular expression over the full command line
	Cgroup     string // regular expression over the cgroup path
	Cwd        string // working directory, or any directory below it
}

// Rename gives the Application named From the display name To.
type Rename struct{ From, To string }

// Default returns the built-in settings.
func Default() Config { return Config{Port: 9191, Interval: 2 * time.Second} }

// Parse overlays the file contents onto the defaults.
func Parse(data []byte) (Config, error) {
	c := Default()
	// section is "" at top level, else the open table: "group" or "rename".
	section := ""
	var group map[string]string
	var groupLine int
	flush := func() error {
		if section == "group" {
			return c.addGroup(group, groupLine)
		}
		if section == "rename" {
			return c.addRename(group, groupLine)
		}
		return nil
	}
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if err := flush(); err != nil {
				return c, err
			}
			head, err := stripComment(line)
			if err != nil {
				return c, fmt.Errorf("line %d: %v", n, err)
			}
			switch head {
			case "[[group]]":
				section = "group"
			case "[[rename]]":
				section = "rename"
			default:
				return c, fmt.Errorf("line %d: unknown table %s", n, head)
			}
			group, groupLine = map[string]string{}, n
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("line %d: expected key = value", n)
		}
		k = strings.TrimSpace(k)
		v, err := parseValue(v)
		if err != nil {
			return c, fmt.Errorf("line %d: %v", n, err)
		}
		if section != "" {
			if _, dup := group[k]; dup {
				return c, fmt.Errorf("line %d: duplicate key %q", n, k)
			}
			if !tableKeys[section][k] {
				return c, fmt.Errorf("line %d: unknown key %q in [[%s]]", n, k, section)
			}
			group[k] = v
			continue
		}
		switch k {
		case "port":
			p, err := strconv.Atoi(v)
			if err != nil || p < 0 || p > 65535 {
				return c, fmt.Errorf("line %d: invalid port %q", n, v)
			}
			c.Port = p
		case "interval":
			d, err := time.ParseDuration(v)
			if err != nil {
				return c, fmt.Errorf("line %d: invalid interval %q", n, v)
			}
			c.Interval = d
		default:
			return c, fmt.Errorf("line %d: unknown key %q", n, k)
		}
	}
	if err := flush(); err != nil {
		return c, err
	}
	return c, c.Validate()
}

var tableKeys = map[string]map[string]bool{
	"group":  {"name": true, "executable": true, "command": true, "cgroup": true, "cwd": true},
	"rename": {"from": true, "to": true},
}

func (c *Config) addGroup(f map[string]string, line int) error {
	r := GroupRule{Name: f["name"], Executable: f["executable"], Command: f["command"], Cgroup: f["cgroup"], Cwd: f["cwd"]}
	if r.Name == "" {
		return fmt.Errorf("line %d: [[group]] needs a name", line)
	}
	if r.Executable == "" && r.Command == "" && r.Cgroup == "" && r.Cwd == "" {
		return fmt.Errorf("line %d: [[group]] %q needs at least one of executable, command, cgroup, cwd", line, r.Name)
	}
	for _, re := range []string{r.Command, r.Cgroup} {
		if _, err := regexp.Compile(re); err != nil {
			return fmt.Errorf("line %d: [[group]] %q: invalid regular expression: %v", line, r.Name, err)
		}
	}
	c.Grouping.Rules = append(c.Grouping.Rules, r)
	return nil
}

func (c *Config) addRename(f map[string]string, line int) error {
	if f["from"] == "" || f["to"] == "" {
		return fmt.Errorf("line %d: [[rename]] needs from and to", line)
	}
	c.Grouping.Renames = append(c.Grouping.Renames, Rename{From: f["from"], To: f["to"]})
	return nil
}

// parseValue reads a basic "string" (with \\ escapes), a literal 'string', or a
// bare word, followed by an optional comment.
func parseValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("missing value")
	}
	switch raw[0] {
	case '\'':
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated string")
		}
		return raw[1 : 1+end], endOfValue(raw[2+end:])
	case '"':
		var b strings.Builder
		for i := 1; i < len(raw); i++ {
			switch ch := raw[i]; ch {
			case '"':
				return b.String(), endOfValue(raw[i+1:])
			case '\\':
				i++
				if i >= len(raw) {
					return "", fmt.Errorf("unterminated string")
				}
				switch raw[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case '"', '\\':
					b.WriteByte(raw[i])
				default:
					return "", fmt.Errorf("unsupported escape \\%c", raw[i])
				}
			default:
				b.WriteByte(ch)
			}
		}
		return "", fmt.Errorf("unterminated string")
	}
	v, _, _ := strings.Cut(raw, "#")
	return strings.TrimSpace(v), nil
}

func endOfValue(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest != "" && rest[0] != '#' {
		return fmt.Errorf("unexpected text after value: %q", rest)
	}
	return nil
}

func stripComment(line string) (string, error) {
	head, _, _ := strings.Cut(line, "#")
	return strings.TrimSpace(head), nil
}

// Validate checks limits.
func (c Config) Validate() error {
	if c.Interval < MinInterval {
		return fmt.Errorf("interval %v is below the minimum %v", c.Interval, MinInterval)
	}
	return nil
}
