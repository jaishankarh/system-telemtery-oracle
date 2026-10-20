package collector

import (
	"errors"
	"io/fs"
	"strings"
)

// readLocation fills the fields the Grouper needs: parent PID, cgroup path
// and working directory. Each is optional (ADR 0006): a process we may not
// inspect keeps them empty, and a kernel without the file is listed in miss.
func (l *Linux) readLocation(p *Process, dir string, statB []byte, userProcess bool, miss map[string]bool) {
	p.PPID = parseParentPID(statB)
	if b, err := l.m.ReadFile(dir + "/cgroup"); err == nil {
		p.Cgroup = parseCgroupPath(string(b))
	} else if userProcess && errors.Is(err, fs.ErrNotExist) {
		miss["/proc/PID/cgroup"] = true
	}
	if target, err := l.m.Readlink(dir + "/cwd"); err == nil {
		p.Cwd = strings.TrimSuffix(target, " (deleted)")
	}
}

// parseParentPID reads ppid, the field after the state in /proc/<pid>/stat.
func parseParentPID(b []byte) int {
	s := string(b)
	c := strings.LastIndexByte(s, ')')
	if c < 0 {
		return 0
	}
	f := strings.Fields(s[c+1:])
	if len(f) < 2 {
		return 0
	}
	n := 0
	for _, r := range f[1] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// parseCgroupPath picks the path that names the process's systemd cgroup:
// the unified (v2) line "0::/path", or on cgroup v1 the "name=systemd"
// hierarchy, else the first line.
func parseCgroupPath(content string) string {
	first, v2, systemd := "", "", ""
	for _, line := range strings.Split(content, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if first == "" {
			first = parts[2]
		}
		switch {
		case parts[1] == "name=systemd":
			systemd = parts[2]
		case parts[1] == "":
			v2 = parts[2]
		}
	}
	for _, c := range []string{systemd, v2, first} {
		if c != "" {
			return c
		}
	}
	return ""
}
