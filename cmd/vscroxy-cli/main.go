// vscroxy-cli is the headless automation client. It never imports MyGo.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/baba983/vscroxy/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
