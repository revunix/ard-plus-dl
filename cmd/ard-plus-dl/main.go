// Command ard-plus-dl downloads movies, series and Tatort episodes from an
// ARD Plus subscription.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/revunix/ard-plus-dl/internal/cli"
)

// version is set with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	cli.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
