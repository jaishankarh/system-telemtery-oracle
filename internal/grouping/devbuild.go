package grouping

import (
	"path"
	"strings"
	"sync"

	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
)

// DevBuild groups dev servers, compilers and build jobs per Project, wherever
// they were launched from, including an editor's built-in terminal.
//
// Project = nearest ancestor of the Process's working directory that is a VCS
// root, otherwise the working directory itself.
//
// Position in the chain: SPEC section 6 lists App Profiles before Dev & Build,
// but an editor profile claims its descendants, so a dev server started in the
// editor's terminal would stay in the editor's row. DevBuild therefore sits
// before App Profiles. It stays out of the editor's way by claiming only
// Processes whose own command is a dev or build tool (or a shell or Node
// helper beneath one): an editor's language servers, extension hosts and
// helper daemons (tsserver, gopls, the Gradle daemon) are not dev or build
// commands, so they are left to the profiles.
type DevBuild struct {
	mu    sync.Mutex
	roots map[string]string // working directory -> Project root, valid for one sample
}

func (*DevBuild) Name() string { return "Dev & Build per Project" }

// Prepare starts a sample with an empty Project cache.
func (d *DevBuild) Prepare(*Context) {
	d.mu.Lock()
	d.roots = map[string]string{}
	d.mu.Unlock()
}

func (d *DevBuild) Match(p *collector.Process, c *Context) (Match, bool) {
	if !isDevCommand(p) && !d.underDevCommand(p, c) {
		return Match{}, false
	}
	cwd := p.Cwd
	for a := p; cwd == "" && a != nil; a = c.Parent(a) { // no readable cwd: use the nearest known one up the tree
		cwd = a.Cwd
	}
	if cwd == "" {
		return Match{}, false
	}
	root := d.projectRoot(cwd, c)
	name := path.Base(root)
	return Match{Kind: KindDevBuild, Key: root, Name: "Dev & Build: " + name}, true
}

// underDevCommand: a shell or Node helper whose ancestors, through nothing but
// such helpers, include a dev or build command (npm run dev -> sh -c vite -> node).
func (d *DevBuild) underDevCommand(p *collector.Process, c *Context) bool {
	if !passthrough[executableName(p)] {
		return false
	}
	for _, a := range c.Ancestors(p) {
		if isDevCommand(a) {
			return true
		}
		if !passthrough[executableName(a)] {
			return false
		}
	}
	return false
}

var vcsMarkers = []string{".git", ".hg", ".svn", ".jj", ".bzr"}

func (d *DevBuild) projectRoot(cwd string, c *Context) string {
	cwd = strings.TrimSuffix(cwd, " (deleted)")
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.roots == nil {
		d.roots = map[string]string{}
	}
	if r, ok := d.roots[cwd]; ok {
		return r
	}
	root := ProjectRoot(c.Machine, cwd)
	d.roots[cwd] = root
	return root
}

// passthrough executables relay work for the command that started them: they are claimed only beneath a dev or build command.
var passthrough = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true, "env": true,
	"node": true, "nodejs": true, "esbuild": true, // esbuild --service is a helper of a dev server
	"timeout": true, "time": true, "nice": true,
}

// tools are dev servers, compilers and build tools, recognised by executable name.
var tools = setOf(
	"cargo", "rustc", "make", "gmake", "ninja", "cmake", "tsc", "vite", "webpack", "webpack-dev-server",
	"rollup", "parcel", "turbo", "nx", "nuxt", "nuxi", "next", "ng", "astro", "rspack", "swc",
	"tsx", "ts-node", "nodemon", "babel", "jest", "vitest", "tsup", "sass", "tailwindcss",
	"gcc", "g++", "cc", "c++", "clang", "clang++", "cc1", "cc1plus", "as", "ld", "ld.bfd", "ld.gold", "ld.lld", "lld", "collect2",
	"javac", "kotlinc", "kotlin-compiler", "sbt", "bazel", "zig", "gradle", "gradlew", "mvn", "mvnw",
)

// packageManagers run scripts; a script-running subcommand makes them a dev or build job.
var packageManagers = setOf("npm", "pnpm", "yarn", "bun")
var runners = setOf("npx", "pnpx", "bunx")
var scriptCommands = setOf("run", "run-script", "dev", "start", "build", "serve", "watch", "test", "preview", "exec", "dlx")
var goCommands = setOf("build", "run", "test", "install", "vet", "generate")
var goToolchain = setOf("compile", "link", "asm", "cgo", "vet")

// javaLaunchers are the entry points of Gradle and Maven clients. The Gradle
// daemon (GradleDaemon) is deliberately absent: App Profiles own it.
var javaLaunchers = []string{"GradleWrapperMain", "org.codehaus.plexus.classworlds.launcher.Launcher", "MavenWrapperMain"}

func setOf(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// isDevCommand reports whether the Process's own command line is a dev server,
// compiler or build job.
func isDevCommand(p *collector.Process) bool {
	if strings.HasPrefix(p.Command, "[") {
		return false
	}
	f := strings.Fields(p.Command)
	if len(f) == 0 {
		return false
	}
	exe, args := path.Base(strings.TrimLeft(f[0], "-")), f[1:]
	switch {
	case exe == "node" || exe == "nodejs":
		script, rest := firstScript(args)
		if script == "" {
			return false
		}
		exe, args = scriptTool(script), rest
	case exe == "java":
		for _, a := range args {
			for _, l := range javaLaunchers {
				if strings.Contains(a, l) {
					return true
				}
			}
		}
		return false
	case goToolchain[exe] && strings.Contains(f[0], "/pkg/tool/"):
		return true
	}
	return classify(exe, args)
}

func classify(exe string, args []string) bool {
	switch {
	case tools[exe] || strings.HasPrefix(exe, "cargo-"):
		return true
	case exe == "go":
		return len(args) > 0 && goCommands[args[0]]
	case packageManagers[exe]:
		sub, _ := firstScript(args)
		return scriptCommands[sub]
	case runners[exe]:
		sub, rest := firstScript(args)
		return sub != "" && classify(path.Base(sub), rest)
	}
	return false
}

// firstScript returns the first argument that is not a flag, and what follows it.
func firstScript(args []string) (string, []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
	}
	return "", nil
}

// noise are path components that do not name a tool: node_modules/webpack-cli/bin/cli.js is webpack.
var noise = setOf("bin", "dist", "lib", "cli", "index", "main", "build", ".bin")

// scriptTool names the tool a Node script file belongs to.
func scriptTool(script string) string {
	parts := strings.Split(script, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		n := parts[i]
		for _, ext := range []string{".js", ".mjs", ".cjs", ".ts"} {
			n = strings.TrimSuffix(n, ext)
		}
		if n == "" || noise[n] {
			continue
		}
		switch n {
		case "npm-cli":
			return "npm"
		case "npx-cli":
			return "npx"
		}
		return strings.TrimSuffix(n, "-cli")
	}
	return ""
}
