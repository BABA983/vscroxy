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

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/mcpserver"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

// resolveControlFile keeps an explicit path, and otherwise uses the same
// discovery file the desktop writes for Settings → AI agents.
func resolveControlFile(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return control.DefaultFile()
}

// All diagnostics, including help, go to stderr. Stdout belongs only to MCP.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("vscroxy-mcp", flag.ContinueOnError)
	// flag's default error messages echo arbitrary supplied values. Print only
	// our fixed diagnostics instead, so a mistaken secret flag is not exposed.
	flags.SetOutput(io.Discard)
	file := flags.String("control-file", "", "Private discovery file; defaults to VSCROXY_CONTROL_FILE or control.json in the data root")
	write := flags.Bool("allow-write", false, "Register mutation tools; runs can affect real external endpoints")
	sensitive := flags.Bool("allow-sensitive", false, "Permit explicitly requested raw details; MAY EXPOSE SECRETS")
	showVersion := flags.Bool("version", false, "Print the version and exit")
	var disabled []string
	flags.Func("disable-tool", "Do not offer this tool, by name such as request.run (repeatable)", func(name string) error {
		disabled = append(disabled, name)
		return nil
	})
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: vscroxy-mcp [--control-file <discovery-file>] [--allow-write] [--allow-sensitive] [--disable-tool <name>]...")
		fmt.Fprintln(stderr, "       vscroxy-mcp --version")
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
	if *showVersion {
		fmt.Fprintln(stdout, "vscroxy-mcp "+version.String())
		return 0
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "No positional arguments; use --help.")
		return 2
	}
	controlFile, err := resolveControlFile(*file)
	if err != nil {
		fmt.Fprintln(stderr, "Could not find the desktop control discovery file; pass --control-file or set VSCROXY_HOME.")
		return 2
	}
	server, err := mcpserver.New(controlFile, mcpserver.Options{AllowWrite: *write, AllowSensitive: *sensitive, Disable: disabled})
	if err != nil {
		fmt.Fprintln(stderr, "A --disable-tool name is unknown; use --help.")
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
