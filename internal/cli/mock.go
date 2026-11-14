package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
)

// mockCommand serves a collection's saved examples at their method and URL,
// as Postman's mock servers do, until the context is cancelled (exit 130).
func mockCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: vscroxy-cli mock [options] <collections-root | postman.json>\nServes each saved example at its method and URL until interrupted (Ctrl-C).")
		flags.PrintDefaults()
	}
	folder := flags.String("folder", "", "Collection/folder whose examples to serve (includes children)")
	envPath := flags.String("env", "", "Explicit native env.json or Postman environment/globals file")
	envName := flags.String("environment", "", "Environment name in --env; defaults to its active environment")
	addr := flags.String("addr", "127.0.0.1:0", "Address to listen on; port 0 picks a free port, printed on start")
	port := flags.Int("port", -1, "Port to listen on, replacing the port in --addr (0 picks a free port)")
	vars := map[string]string{}
	flags.Func("var", "Environment override KEY=VALUE (repeatable; use --env for secrets)", func(s string) error {
		key, value, ok := strings.Cut(s, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return errors.New("expected KEY=VALUE")
		}
		vars[key] = value
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 || *port > 65535 {
		fmt.Fprintln(stderr, "Provide one collection input and a port from 0 to 65535.")
		return 2
	}
	listen := *addr
	if *port >= 0 {
		host, _, err := net.SplitHostPort(listen)
		if err != nil {
			fmt.Fprintln(stderr, "--addr must be host:port.")
			return 2
		}
		listen = net.JoinHostPort(host, strconv.Itoa(*port))
	}
	if !loopbackListen(listen) {
		fmt.Fprintf(stderr, "Warning: %s is not a loopback address; other computers that can reach it get these examples without authentication.\n", listen)
	}

	requests, _, warnings, err := loadCollection(flags.Arg(0), *folder)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load collection; check the input and folder selection.")
		return 2
	}
	if len(warnings) > 0 {
		fmt.Fprintf(stderr, "Import warning: %d unsupported feature categories were omitted or adapted.\n", len(warnings))
	}
	_, values, globals, err := readEnvironment(*envPath, *envName)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load environment; check --env and --environment.")
		return 2
	}
	// Variables for {{name}} in example URLs and bodies: globals, then the
	// environment, then --var, the same precedence run uses.
	env := maps.Clone(globals)
	maps.Copy(env, values)
	maps.Copy(env, vars)

	var examples []request.Example
	for _, r := range requests {
		examples = append(examples, r.Examples...)
	}
	if len(examples) == 0 {
		fmt.Fprintln(stderr, "No saved examples in the selected collection; save a response as an example first.")
		return 2
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintln(stderr, "Could not listen on that address; choose another with --addr or --port.")
		return 2
	}
	srv := &http.Server{Handler: mockserver.New(examples, env), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		case <-done:
		}
	}()
	fmt.Fprintf(stdout, "Serving %d examples at http://%s until interrupted (Ctrl-C)\n", len(examples), ln.Addr())
	err = srv.Serve(ln)
	if ctx.Err() != nil {
		return 130
	}
	if errors.Is(err, http.ErrServerClosed) {
		return 0
	}
	fmt.Fprintln(stderr, "Mock server stopped:", err)
	return 2
}

// loopbackListen reports whether a host:port listen address stays on this
// computer. An empty host listens on every interface.
func loopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
