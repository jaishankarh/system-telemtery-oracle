package grouping

import (
	"regexp"
	"strings"
	"sync"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// App Profiles: built-in recognisers that fold a well-known program and its
// helper Processes into one Application (ADR 0005, SPEC section 6).
//
// The recognisers are data, not code. To teach the tool a new program, add one
// Profile to builtinProfiles in profiles_table.go: a Key, a display Name, the
// Roots that identify the program's own executable, and (optionally) Roles
// that label what each of its Processes is for. No other file changes.
//
// How a Process is assigned:
//
//   - It joins the Profile whose Roots match its own command line (helpers of
//     a browser or Electron editor run the same executable, so they match
//     directly), or else the Profile of its nearest ancestor that belongs to
//     one (language servers, ripgrep and file watchers an editor starts).
//   - A Process that starts as a Profile's own root and is Absorbable, such as
//     a Gradle daemon, joins a Host (editor or IDE) above it instead.
//   - Processes below a terminal shell that a Host started are not the Host's:
//     the shell is, its dev servers and builds are left to later layers (Dev &
//     Build per Project, systemd unit, top ancestor).
//
// Each Process of a Profile also gets a Role (the Role constants), which the
// API serves as "role" on the Application's Processes so that Advice can name
// an editor's file watchers or a browser's renderers.

// Roles a Process can have inside an App Profile Application.
const (
	RoleMain           = "main"            // the program's own process
	RoleHelper         = "helper"          // anything else the program started
	RoleRenderer       = "renderer"        // browser tab or editor window renderer
	RoleGPU            = "gpu"             // GPU process
	RoleUtility        = "utility"         // network, audio, decoder and other utility processes
	RoleZygote         = "zygote"          // Chromium zygote
	RoleExtension      = "extension"       // browser extension process
	RoleCrashReporter  = "crash-reporter"  // crashpad handler and similar
	RoleExtensionHost  = "extension-host"  // editor extension host
	RoleLanguageServer = "language-server" // language server started by an editor or IDE
	RoleFileWatcher    = "file-watcher"    // editor or IDE file watcher
	RoleRipgrep        = "ripgrep"         // editor search
	RolePtyHost        = "pty-host"        // editor terminal host
	RoleTerminalShell  = "terminal-shell"  // shell in an editor or IDE terminal
	RoleSharedProcess  = "shared-process"  // editor shared process
	RoleGradleDaemon   = "gradle-daemon"
	RoleKotlinDaemon   = "kotlin-daemon"
	RoleMavenServer    = "maven-server" // IDE Maven server or Maven daemon
	RoleEmulatorVM     = "emulator-vm"  // the emulated device
)

// Recogniser matches a Process by its name or its full command line.
type Recogniser struct {
	Comm []string // exact process names (the kernel's comm)
	Cmd  string   // regular expression over the full command line
}

// RoleRule gives a Process the Role when the Recogniser matches. The first
// rule of a Profile that matches wins.
type RoleRule struct {
	Role  string
	Match Recogniser
}

// Profile is one App Profile.
type Profile struct {
	Key   string // identity of the Application
	Name  string // display name
	Roots []Recogniser
	Roles []RoleRule
	// Host marks an editor or IDE: Absorbable Profiles that it started are
	// folded into it.
	Host bool
	// Absorbable marks a program that is its own Application when nothing
	// hosts it but belongs to the Host that started it otherwise.
	Absorbable bool
	// TerminalParentRoles are the Roles whose child shell is a terminal the
	// user opened; everything started from that shell is not this Profile's.
	TerminalParentRoles []string
}

type compiledRecogniser struct {
	comm map[string]bool
	cmd  *regexp.Regexp
}

func compileRecogniser(r Recogniser) compiledRecogniser {
	c := compiledRecogniser{}
	if len(r.Comm) > 0 {
		c.comm = map[string]bool{}
		for _, n := range r.Comm {
			c.comm[n] = true
		}
	}
	if r.Cmd != "" {
		c.cmd = regexp.MustCompile(r.Cmd)
	}
	return c
}

func (c compiledRecogniser) matches(p *collector.Process) bool {
	if c.comm[p.Name] {
		return true
	}
	return c.cmd != nil && !strings.HasPrefix(p.Command, "[") && c.cmd.MatchString(p.Command)
}

type compiledRule struct {
	role  string
	match compiledRecogniser
}

type compiledProfile struct {
	Profile
	roots []compiledRecogniser
	rules []compiledRule
}

// helperFlag marks a command line as a helper of a Chromium, Electron or Firefox program.
var helperFlag = regexp.MustCompile(`(^|\s)(--type=|-contentproc(\s|$))`)

var shells = map[string]bool{
	"bash": true, "zsh": true, "fish": true, "sh": true, "dash": true,
	"ksh": true, "tcsh": true, "csh": true, "nu": true, "pwsh": true,
}

// AppProfiles is the App Profiles Layer.
type AppProfiles struct {
	profiles []compiledProfile

	mu     sync.Mutex
	ctx    *Context // the sample the memo below belongs to
	owners map[int]ownerRes
	cache  map[int]*procInfo // survives samples: recognising a command line is the costly part
}

// NewAppProfiles builds the Layer from the built-in table.
func NewAppProfiles() *AppProfiles { return NewAppProfilesFrom(builtinProfiles()) }

// NewAppProfilesFrom builds the Layer from the given Profiles, tried in order.
// It panics on an invalid regular expression: the table is code.
func NewAppProfilesFrom(defs []Profile) *AppProfiles {
	a := &AppProfiles{cache: map[int]*procInfo{}}
	for _, d := range defs {
		cp := compiledProfile{Profile: d}
		for _, r := range d.Roots {
			cp.roots = append(cp.roots, compileRecogniser(r))
		}
		for _, r := range d.Roles {
			cp.rules = append(cp.rules, compiledRule{r.Role, compileRecogniser(r.Match)})
		}
		a.profiles = append(a.profiles, cp)
	}
	return a
}

func (*AppProfiles) Name() string { return "App Profiles" }

// procInfo is what recognising one Process costs, kept while the Process lives.
type procInfo struct {
	cmd, name string
	self      int // index of the Profile whose root this Process is, or -1
	roleProf  int
	role      string
}

type ownerRes struct {
	prof int // index into profiles, -1 for none
	role string
}

var noOwner = ownerRes{prof: -1}

func (a *AppProfiles) Match(p *collector.Process, c *Context) (Match, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx != c {
		a.ctx = c
		a.owners = make(map[int]ownerRes, len(c.ByPID))
		for pid := range a.cache {
			if _, ok := c.ByPID[pid]; !ok {
				delete(a.cache, pid)
			}
		}
	}
	r := a.owner(p, c, 0)
	if r.prof < 0 {
		return Match{}, false
	}
	pr := a.profiles[r.prof]
	return Match{Kind: KindProfile, Key: pr.Key, Name: pr.Name, Role: r.role}, true
}

func (a *AppProfiles) info(p *collector.Process) *procInfo {
	if in := a.cache[p.PID]; in != nil && in.cmd == p.Command && in.name == p.Name {
		return in
	}
	in := &procInfo{cmd: p.Command, name: p.Name, self: -1, roleProf: -1}
	for i, pr := range a.profiles {
		for _, r := range pr.roots {
			if r.matches(p) {
				in.self = i
				break
			}
		}
		if in.self >= 0 {
			break
		}
	}
	a.cache[p.PID] = in
	return in
}

const maxOwnerDepth = 256

func (a *AppProfiles) owner(p *collector.Process, c *Context, depth int) ownerRes {
	if r, ok := a.owners[p.PID]; ok {
		return r
	}
	a.owners[p.PID] = noOwner // guards against a parent cycle
	in := a.info(p)
	parent := noOwner
	if par := c.Parent(p); par != nil && depth < maxOwnerDepth {
		parent = a.owner(par, c, depth+1)
	}
	inherited := parent.prof
	if parent.role == RoleTerminalShell {
		inherited = -1 // started from the user's terminal: not the editor's
	}
	prof := inherited
	if in.self >= 0 {
		prof = in.self
		if a.profiles[in.self].Absorbable && inherited >= 0 && a.profiles[inherited].Host {
			prof = inherited
		}
	}
	res := noOwner
	if prof >= 0 {
		res = ownerRes{prof: prof, role: a.role(p, in, prof, parent)}
	}
	a.owners[p.PID] = res
	return res
}

func (a *AppProfiles) role(p *collector.Process, in *procInfo, prof int, parent ownerRes) string {
	pr := a.profiles[prof]
	if shells[p.Name] && parent.prof == prof {
		for _, r := range pr.TerminalParentRoles {
			if r == parent.role {
				return RoleTerminalShell
			}
		}
	}
	if in.roleProf == prof {
		return in.role
	}
	role := RoleHelper
	found := false
	for _, r := range pr.rules {
		if r.match.matches(p) {
			role, found = r.role, true
			break
		}
	}
	if !found && in.self == prof && !helperFlag.MatchString(p.Command) {
		role = RoleMain
	}
	in.roleProf, in.role = prof, role
	return role
}
