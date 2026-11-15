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
	"google.golang.org/grpc"
)

// mockCommand serves a collection's saved examples at their method and URL,
// as Postman's mock servers do, until the context is cancelled (exit 130).
// Unary gRPC examples are served on their own listener.
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
	grpcAddr := flags.String("grpc-addr", "127.0.0.1:0", "Address for unary gRPC examples; port 0 picks a free port")
	rateLimit := flags.Int("rate-limit", 0, "Answer at most this many requests per second (0 for no limit)")
	errorRate := flags.Float64("error-rate", 0, "Inject an error for this share of requests, 0–1")
	errorStatus := flags.Int("error-status", 0, "Injected error status, 400–599 (default 503)")
	errorBody := flags.String("error-body", "", "Injected error body (default a JSON error)")
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
	if flags.NArg() != 1 || *port > 65535 || *rateLimit < 0 || *rateLimit > 100000 || *errorRate < 0 || *errorRate > 1 || *errorStatus != 0 && (*errorStatus < 400 || *errorStatus > 599) {
		fmt.Fprintln(stderr, "Provide one collection input, a port from 0 to 65535, a rate limit 0–100000, an error rate 0–1, and an error status 400–599 or 0.")
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
	if !loopbackListen(*grpcAddr) {
		fmt.Fprintf(stderr, "Warning: %s is not a loopback address; other computers that can reach the gRPC mock without authentication.\n", *grpcAddr)
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
	var grpcExamples []mockserver.GRPCExample
	for _, r := range requests {
		if r.Protocol == "gRPC" {
			for _, e := range r.Examples {
				grpcExamples = append(grpcExamples, mockserver.GRPCExample{
					Method: r.GRPC.Method, Schema: r.GRPC.Schema, Body: e.Body,
					Headers: e.Headers, Status: e.Status,
					Delay: time.Duration(e.DelayMS) * time.Millisecond,
				})
			}
			continue
		}
		if r.Protocol == "" {
			examples = append(examples, r.Examples...)
		}
	}
	if len(examples) == 0 && len(grpcExamples) == 0 {
		fmt.Fprintln(stderr, "No saved examples in the selected collection; save a response as an example first.")
		return 2
	}

	opts := mockserver.Options{RatePerSecond: *rateLimit, ErrorRate: *errorRate, ErrorStatus: *errorStatus, ErrorBody: *errorBody}
	var httpServer *http.Server
	var grpcServer *grpc.Server
	var httpURL, grpcURL string
	if len(examples) > 0 {
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			fmt.Fprintln(stderr, "Could not listen on that address; choose another with --addr or --port.")
			return 2
		}
		httpServer = &http.Server{Handler: mockserver.NewWithOptions(examples, env, opts), ReadHeaderTimeout: 10 * time.Second}
		go httpServer.Serve(ln)
		httpURL = "http://" + ln.Addr().String()
	}
	if len(grpcExamples) > 0 {
		grpcServer, err = mockserver.NewGRPC(grpcExamples)
		if err != nil {
			if httpServer != nil {
				httpServer.Close()
			}
			fmt.Fprintln(stderr, "Could not build the gRPC mock; check the examples' schemas and methods.")
			return 2
		}
		ln, err := net.Listen("tcp", *grpcAddr)
		if err != nil {
			if httpServer != nil {
				httpServer.Close()
			}
			fmt.Fprintln(stderr, "Could not listen for gRPC; choose another --grpc-addr.")
			return 2
		}
		go grpcServer.Serve(ln)
		grpcURL = ln.Addr().String()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if httpServer != nil {
				_ = httpServer.Shutdown(shutdown)
			}
			if grpcServer != nil {
				grpcServer.Stop()
			}
		case <-done:
		}
	}()
	if httpURL != "" {
		fmt.Fprintf(stdout, "Serving %d examples at %s until interrupted (Ctrl-C)\n", len(examples), httpURL)
	}
	if grpcURL != "" {
		fmt.Fprintf(stdout, "Serving %d unary gRPC examples at %s until interrupted (Ctrl-C)\n", len(grpcExamples), grpcURL)
	}
	// Both servers run in their own goroutines until the caller cancels.
	<-ctx.Done()
	return 130
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
