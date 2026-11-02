// vscroxy-cli is the headless automation client. It never imports MyGo.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/sirupsen/logrus"
)

func main() {
	// The proxy dependency logs complete request URLs even on errors. The CLI
	// emits only its controlled summaries, never dependency traffic logs.
	logrus.SetOutput(io.Discard)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
