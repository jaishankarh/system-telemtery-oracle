package collector

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Process is one process as listed in the Processes view. Fields that the
// kernel did not provide (no permission, old kernel, kernel thread, first
// sample for rates) are nil. JSON field names are the API.
type Process struct {
	PID     int    `json:"pid"`
	User    string `json:"user"`
	Name    string `json:"name"`
	Command string `json:"command"`
	// RSS is from /proc/<pid>/status; PSS and Swap are the kernel's
	// per-process rollup (smaps_rollup), refreshed every MemoryDetailEvery.
	RSS     *uint64  `json:"rss_bytes"`
	PSS     *uint64  `json:"pss_bytes"`
	Swap    *uint64  `json:"swap_bytes"`
	CPU     *float64 `json:"cpu_percent"` // percent of one core
	Read    *float64 `json:"disk_read_bytes_per_sec"`
	Write   *float64 `json:"disk_write_bytes_per_sec"`
	Threads int      `json:"threads"`
	OpenFDs *int     `json:"open_fds"`
	// Location, for grouping into Applications (SPEC 5.2): parent PID, cgroup
	// path (systemd hierarchy) and working directory. Empty when unreadable.
	PPID   int    `json:"ppid"`
	Cgroup string `json:"cgroup,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

// MemoryDetailEvery is how often PSS and swap are re-read per process; reading
// smaps_rollup for hundreds of processes is the expensive part (SPEC 4.4).
const MemoryDetailEvery = 10 * time.Second

const clockTicksPerSec = 100 // USER_HZ is 100 on every Linux ABI we target

type procPrev struct {
	start   uint64 // starttime from stat; with the PID it identifies the process
	ticks   uint64
	readB   uint64
	writeB  uint64
	haveIO  bool
	pss     *uint64
	swap    *uint64
	memAt   time.Time
	seenAt  time.Time
	haveMem bool
	// noRollup remembers that smaps_rollup does not exist, so the source stays
	// listed as missing on samples that do not re-read memory detail.
	noRollup bool
	// name is kept so a process that exits between samples can still be named
	// as a possible OOM-kill victim (oomVictims, below).
	name string
}

type procState struct {
	prev map[int]*procPrev
}

func (l *Linux) collectProcesses(now time.Time, s *Sample) {
	names, err := l.m.ReadDir("/proc")
	if err != nil {
		return // /proc/meminfo etc. already report a missing /proc
	}
	users := l.loadUsers()
	if l.proc.prev == nil {
		l.proc.prev = map[int]*procPrev{}
	}
	prev := l.proc.prev
	next := make(map[int]*procPrev, len(names))
	miss := map[string]bool{}
	for _, n := range names {
		pid, err := strconv.Atoi(n)
		if err != nil || pid <= 0 {
			continue
		}
		p, st, ok := l.readProcess(pid, now, users, prev[pid], miss)
		if !ok {
			continue // exited while we were looking
		}
		next[pid] = st
		s.Processes = append(s.Processes, p)
	}
	if s.OOMKills != nil && *s.OOMKills > 0 {
		s.OOMVictims = oomVictims(prev, next)
	}
	l.proc.prev = next
	for k := range miss {
		s.ProcessMissing = append(s.ProcessMissing, k)
	}
	sort.Strings(s.ProcessMissing)
}

// maxOOMVictims bounds how many exited processes are named as possible OOM
// kills, so one noisy sample cannot make the finding unreadable.
const maxOOMVictims = 5

// oomVictims names the processes most likely killed by the OOM killer: present
// in prev, gone from next, ranked by last-known PSS + swap (largest first). A
// process whose memory was never read (no PSS) is skipped: there is nothing to
// rank it by or show, and most are short-lived helpers, not OOM targets.
func oomVictims(prev, next map[int]*procPrev) []OOMVictim {
	var out []OOMVictim
	for pid, p := range prev {
		if _, alive := next[pid]; alive || p.pss == nil {
			continue
		}
		out = append(out, OOMVictim{PID: pid, Name: p.name, PSSBytes: p.pss, SwapBytes: p.swap})
	}
	sort.Slice(out, func(i, j int) bool { return oomVictimWeight(out[i]) > oomVictimWeight(out[j]) })
	if len(out) > maxOOMVictims {
		out = out[:maxOOMVictims]
	}
	return out
}

func oomVictimWeight(v OOMVictim) uint64 {
	var w uint64
	if v.PSSBytes != nil {
		w += *v.PSSBytes
	}
	if v.SwapBytes != nil {
		w += *v.SwapBytes
	}
	return w
}

func (l *Linux) readProcess(pid int, now time.Time, users map[string]string, old *procPrev, miss map[string]bool) (Process, *procPrev, bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	statB, err := l.m.ReadFile(dir + "/stat")
	if err != nil {
		return Process{}, nil, false
	}
	comm, ticks, start, threads, ok := parseProcStat(statB)
	if !ok {
		return Process{}, nil, false
	}
	p := Process{PID: pid, Name: comm, Threads: threads}
	st := &procPrev{start: start, ticks: ticks, name: comm}
	if old != nil && old.start != start {
		old = nil // PID reused by a different process
	}
	if old != nil {
		st.pss, st.swap, st.memAt, st.haveMem, st.noRollup = old.pss, old.swap, old.memAt, old.haveMem, old.noRollup
	}

	var vmRSS, vmSwap *uint64
	uid := ""
	if b, err := l.m.ReadFile(dir + "/status"); err == nil {
		uid, vmRSS, vmSwap = parseProcStatus(b)
	}
	p.RSS = vmRSS
	p.User = users[uid]
	if p.User == "" {
		p.User = uid
	}

	cmd := bytes.TrimRight(mustRead(l, dir+"/cmdline"), "\x00")
	if len(cmd) == 0 {
		p.Command = "[" + comm + "]" // kernel thread
	} else {
		p.Command = strings.ReplaceAll(string(cmd), "\x00", " ")
	}
	userProcess := vmRSS != nil // kernel threads have no address space
	l.readLocation(&p, dir, statB, userProcess, miss)

	if names, err := l.m.ReadDir(dir + "/fd"); err == nil {
		n := len(names)
		p.OpenFDs = &n
	}

	if b, err := l.m.ReadFile(dir + "/io"); err == nil {
		r, w, ok := parseProcIO(b)
		if ok {
			st.readB, st.writeB, st.haveIO = r, w, true
			if old != nil && old.haveIO {
				if dt := now.Sub(old.seenAt).Seconds(); dt > 0 && r >= old.readB && w >= old.writeB {
					a, c := float64(r-old.readB)/dt, float64(w-old.writeB)/dt
					p.Read, p.Write = &a, &c
				}
			}
		}
	} else if userProcess && errors.Is(err, fs.ErrNotExist) {
		miss["/proc/PID/io"] = true
	}
	st.seenAt = now
	if old != nil {
		if dt := now.Sub(old.seenAt).Seconds(); dt > 0 && ticks >= old.ticks {
			c := float64(ticks-old.ticks) / clockTicksPerSec / dt * 100
			p.CPU = &c
		}
	}

	// PSS and swap: smaps_rollup, but only every MemoryDetailEvery.
	if !st.haveMem || now.Sub(st.memAt) >= MemoryDetailEvery {
		st.haveMem, st.memAt = true, now
		st.pss, st.swap = nil, nil
		if b, err := l.m.ReadFile(dir + "/smaps_rollup"); err == nil {
			st.pss, st.swap = parseRollup(b)
		} else {
			st.noRollup = userProcess && errors.Is(err, fs.ErrNotExist)
			st.swap = vmSwap // kernels before 4.14: swap only
		}
	}
	if st.noRollup {
		miss["/proc/PID/smaps_rollup"] = true
	}
	p.PSS, p.Swap = st.pss, st.swap
	return p, st, true
}

func mustRead(l *Linux, p string) []byte {
	b, _ := l.m.ReadFile(p)
	return b
}

// parseProcStat handles comm containing spaces and parentheses by splitting at
// the last ')'.
func parseProcStat(b []byte) (comm string, ticks, start uint64, threads int, ok bool) {
	s := string(b)
	o, c := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if o < 0 || c < o {
		return
	}
	comm = s[o+1 : c]
	f := strings.Fields(s[c+1:]) // f[0] is field 3 (state)
	if len(f) < 20 {
		return
	}
	u, e1 := strconv.ParseUint(f[11], 10, 64)  // utime, field 14
	k, e2 := strconv.ParseUint(f[12], 10, 64)  // stime, field 15
	t, e3 := strconv.Atoi(f[17])               // num_threads, field 20
	st, e4 := strconv.ParseUint(f[19], 10, 64) // starttime, field 22
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return
	}
	return comm, u + k, st, t, true
}

func parseProcStatus(b []byte) (uid string, rss, swap *uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		switch k {
		case "Uid":
			uid = f[0]
		case "VmRSS", "VmSwap":
			if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
				n *= 1024
				if k == "VmRSS" {
					rss = &n
				} else {
					swap = &n
				}
			}
		}
	}
	return
}

func parseProcIO(b []byte) (r, w uint64, ok bool) {
	var gr, gw bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "read_bytes":
			r, gr = n, true
		case "write_bytes":
			w, gw = n, true
		}
	}
	return r, w, gr && gw
}

func parseRollup(b []byte) (pss, swap *uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || (k != "Pss" && k != "Swap") {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
			n *= 1024
			if k == "Pss" {
				pss = &n
			} else {
				swap = &n
			}
		}
	}
	return
}

// loadUsers maps uid to user name from /etc/passwd (missing file: numeric uids).
func (l *Linux) loadUsers() map[string]string {
	users := map[string]string{}
	b, err := l.m.ReadFile("/etc/passwd")
	if err != nil {
		return users
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 3 {
			if _, dup := users[f[2]]; !dup {
				users[f[2]] = f[0]
			}
		}
	}
	return users
}
