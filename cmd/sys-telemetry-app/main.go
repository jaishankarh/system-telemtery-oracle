// Command sys-telemetry-app is a lightweight Linux telemetry and bottleneck analyzer.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jaishankarh/system-telemtery-oracle/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Run(ctx, cli.RealEnv(), os.Args[1:]))
}
