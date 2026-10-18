// Package cli implements the sys-telemetry-app commands: run (default),
// report, and the install/uninstall placeholders.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/advisor"
	"github.com/jaishankarh/system-telemtery-oracle/internal/collector"
	"github.com/jaishankarh/system-telemtery-oracle/internal/config"
	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
	"github.com/jaishankarh/system-telemtery-oracle/internal/selfguard"
	"github.com/jaishankarh/system-telemtery-oracle/internal/server"
)

// Env is everything Run needs from outside.
type Env struct {
	Machine machine.Machine
	Stdout  io.Writer
	Stderr  io.Writer
	// OnListen, if set, is called with the bound address once the server listens.
	OnListen func(addr string)

	grouping config.Grouping // user grouping rules from the config file
}

// RealEnv is the production environment.
func RealEnv() Env { return Env{Machine: machine.Real(), Stdout: os.Stdout, Stderr: os.Stderr} }

// Run executes one invocation and returns the process exit code.
func Run(ctx context.Context, env Env, args []string) int {
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run", "report":
		cfg, err := parseFlags(env, cmd, args)
		if err != nil {
			fmt.Fprintln(env.Stderr, "error:", err)
			return 2
		}
		env.grouping = cfg.Grouping
		if cmd == "report" {
			return report(env, cfg, collector.NewLinux(env.Machine))
		}
		return run(ctx, env, cfg)
	case "install", "uninstall":
		return installCmd(env, cmd)
	default:
		fmt.Fprintf(env.Stderr, "unknown command %q\nusage: sys-telemetry-app [run|report|install|uninstall] [--port N] [--interval D] [--config PATH]\n", cmd)
		return 2
	}
}

// parseFlags resolves settings: flags beat the config file beat defaults.
func parseFlags(env Env, name string, args []string) (config.Config, error) {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	port := fl.Int("port", 0, "dashboard port on 127.0.0.1")
	interval := fl.Duration("interval", 0, "sampling interval (minimum 1s)")
	path := fl.String("config", "", "config file (default "+config.DefaultPath+")")
	if err := fl.Parse(args); err != nil {
		return config.Config{}, err
	}
	set := map[string]bool{}
	fl.Visit(func(f *flag.Flag) { set[f.Name] = true })

	cfg := config.Default()
	file := *path
	if file == "" {
		file = config.DefaultPath
	}
	if data, err := env.Machine.ReadFile(file); err == nil {
		if cfg, err = config.Parse(data); err != nil {
			return cfg, fmt.Errorf("%s: %w", file, err)
		}
	} else if set["config"] || !errors.Is(err, fs.ErrNotExist) {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if set["port"] {
		cfg.Port = *port
	}
	if set["interval"] {
		cfg.Interval = *interval
	}
	return cfg, cfg.Validate()
}

func run(ctx context.Context, env Env, cfg config.Config) int {
	guard := selfguard.Start(env.Machine)
	if st := guard.Status(); !st.OOMProtected {
		fmt.Fprintln(env.Stderr, "warning: could not exempt own process from the OOM killer:", st.OOMError)
	}
	col := collector.NewLinux(env.Machine)
	srv := server.New(cfg.Interval, newGrouper(env))
	srv.SetAdvisor(advisor.New(env.Machine, cfg.Advisor, advisor.DefaultRules()))
	first, _ := col.Collect()
	srv.Update(first)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port)) // loopback only (ADR 0003)
	if err != nil {
		fmt.Fprintln(env.Stderr, "error:", err)
		return 1
	}
	hs := &http.Server{Handler: withSelf(guard, srv.Handler()), ReadHeaderTimeout: 5 * time.Second}
	go hs.Serve(ln)
	fmt.Fprintf(env.Stderr, "sys-telemetry-app listening on http://%s\n", ln.Addr())
	if env.OnListen != nil {
		env.OnListen(ln.Addr().String())
	}

	tk := env.Machine.Clock.NewTicker(cfg.Interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			sc, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			hs.Shutdown(sc)
			cancel()
			return 0
		case <-tk.C():
			guard.Check() // before publishing, so a published sample implies the check ran
			if s, err := col.Collect(); err == nil {
				srv.Update(s)
			}
		}
	}
}
