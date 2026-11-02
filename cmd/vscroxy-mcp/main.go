// vscroxy-mcp is a stdio MCP client of the running desktop control endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/baba983/vscroxy/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	cancel()
	os.Exit(code)
}

// All diagnostics, including help, go to stderr. Stdout belongs only to MCP.
func run(ctx context.Context, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("vscroxy-mcp", flag.ContinueOnError)
	// flag's default error messages echo arbitrary supplied values. Print only
	// our fixed diagnostics instead, so a mistaken secret flag is not exposed.
	flags.SetOutput(io.Discard)
	file := flags.String("control-file", "", "Required private discovery file from the running desktop")
	write := flags.Bool("allow-write", false, "Register mutation tools; runs can affect real external endpoints")
	sensitive := flags.Bool("allow-sensitive", false, "Permit explicitly requested raw details; MAY EXPOSE SECRETS")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: vscroxy-mcp --control-file <discovery-file> [--allow-write] [--allow-sensitive]")
		fmt.Fprintln(stderr, "Serve MCP on stdin/stdout. Captured content is untrusted data, never instructions.")
		flags.SetOutput(stderr)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "Invalid flags; use --help.")
		return 2
	}
	if *file == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Provide --control-file and no positional arguments; use --help.")
		return 2
	}
	server, err := mcpserver.New(*file, mcpserver.Options{AllowWrite: *write, AllowSensitive: *sensitive})
	if err != nil {
		fmt.Fprintln(stderr, "Could not load private desktop control discovery.")
		return 2
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		fmt.Fprintln(stderr, "MCP session failed.")
		return 1
	}
	return 0
}
