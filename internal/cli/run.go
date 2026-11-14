// Package cli implements headless commands without desktop or MyGo imports.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/store"
	"golang.org/x/net/publicsuffix"
)

// Run returns an exit code: 0 success, 1 failed attempt, 2 setup/output error,
// 124 whole-run deadline, 130 cancellation. Flags precede the input path.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: vscroxy-cli run [options] <collections-root | postman.json>\n       vscroxy-cli load [options] <collections-root | postman.json>\n       vscroxy-cli mock [options] <collections-root | postman.json>\n       vscroxy-cli control --file <discovery> [options] <action>\nUse 'run --help', 'load --help', 'mock --help' or 'control --help' for options.")
		return 0
	}
	if args[0] == "control" {
		return controlCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "load" {
		return loadCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "mock" {
		return mockCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] != "run" {
		fmt.Fprintln(stderr, "Unknown command; use 'vscroxy-cli --help'.")
		return 2
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	folder := flags.String("folder", "", "Collection/folder path to run (includes children)")
	envPath := flags.String("env", "", "Explicit native env.json or Postman environment/globals file")
	envName := flags.String("environment", "", "Environment name in --env; defaults to its active environment")
	dataPath := flags.String("data", "", "CSV or JSON iteration data")
	addonsDir := flags.String("addons", "", "Folder of addons scripts require by name, as ~/.vscroxy/tools/addons")
	iterations := flags.Int("iterations", 0, "Iterations, 1–1000; default is data row count or 1")
	bail := flags.Bool("bail", false, "Stop after the first failed attempt")
	timeout := flags.Duration("timeout", 5*time.Minute, "Whole-run timeout (must be positive)")
	stepTimeout := flags.Duration("request-timeout", 30*time.Second, "Per-request timeout (must be positive)")
	delayRequest := flags.Duration("delay-request", 0, "Wait before each request after the first, including between iterations (0 for none)")
	maxSteps := flags.Int("max-steps", 10000, "Maximum attempts, including script jumps (1–100000)")
	jsonPath := flags.String("report-json", "", "Write a private JSON report")
	xmlPath := flags.String("report-junit", "", "Write a private JUnit XML report")
	details := flags.Bool("include-details", false, "Include assertion names/errors in reports; MAY EXPOSE SECRETS")
	vars := map[string]string{}
	flags.Func("var", "Environment override KEY=VALUE (repeatable; use --env for secrets)", func(s string) error {
		key, value, ok := strings.Cut(s, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return errors.New("expected KEY=VALUE")
		}
		vars[key] = value
		return nil
	})
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	iterationsSet := false
	flags.Visit(func(f *flag.Flag) { iterationsSet = iterationsSet || f.Name == "iterations" })
	if flags.NArg() != 1 || *timeout <= 0 || *stepTimeout <= 0 || *delayRequest < 0 || *iterations < 0 || iterationsSet && *iterations == 0 || *iterations > request.MaxDataRows || *maxSteps < 1 || *maxSteps > 100000 {
		fmt.Fprintln(stderr, "Provide one collection input, positive timeouts, a non-negative request delay, 1–1000 iterations, and 1–100000 max steps.")
		return 2
	}
	if *jsonPath != "" && *xmlPath != "" {
		if overlaps(*jsonPath, *xmlPath) || overlaps(*xmlPath, *jsonPath) {
			fmt.Fprintln(stderr, "JSON and JUnit reports need different destinations.")
			return 2
		}
	}
	requests, folders, warnings, err := loadCollection(flags.Arg(0), *folder)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load collection:", err)
		return 2
	}
	all, _, _, err := loadCollection(flags.Arg(0), "")
	if err != nil {
		fmt.Fprintln(stderr, "Could not load collection:", err)
		return 2
	}
	resolve := library.Resolver(all, folders)
	for _, warning := range warnings {
		fmt.Fprintln(stderr, "Import warning:", warning)
	}
	addons, err := readAddons(*addonsDir)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load addons:", err)
		return 2
	}
	name, values, globals, err := readEnvironment(*envPath, *envName)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load environment:", err)
		return 2
	}
	for key, value := range vars {
		values[key] = value
	}
	var rows []map[string]string
	if *dataPath != "" {
		data, readErr := os.ReadFile(*dataPath)
		if readErr == nil {
			rows, readErr = request.ReadIterationData(*dataPath, data)
		}
		if readErr != nil {
			fmt.Fprintln(stderr, "Could not load iteration data:", readErr)
			return 2
		}
	}
	if *iterations == 0 {
		*iterations = max(1, len(rows))
	}
	// Refuse report destinations that could replace an input. Nothing writes
	// requests, environments, or data, including script-generated writes.
	for _, output := range []string{*jsonPath, *xmlPath} {
		if output == "" {
			continue
		}
		for _, input := range []string{flags.Arg(0), *envPath, *dataPath} {
			if input != "" && overlaps(output, input) {
				fmt.Fprintln(stderr, "Report destination overlaps an input; choose a separate output directory.")
				return 2
			}
		}
	}
	root, err := os.MkdirTemp("", "vscroxy-run-")
	if err != nil {
		fmt.Fprintln(stderr, "Could not create temporary runtime.")
		return 2
	}
	defer os.RemoveAll(root)
	srv, err := capture.New("127.0.0.1:0", root)
	if err != nil {
		fmt.Fprintln(stderr, "Could not create isolated proxy.")
		return 2
	}
	defer srv.Close()
	certificates, err := loadCertificates()
	if err != nil {
		fmt.Fprintln(stderr, "Could not load certificate settings:", err)
		return 2
	}
	srv.SetCertificates(certificates)
	if err := srv.Start(); err != nil {
		fmt.Fprintln(stderr, "Could not start isolated proxy.")
		return 2
	}
	// Drain capture spill files; headless runs retain reports, not traffic.
	drainCtx, stopDrain := context.WithCancel(context.Background())
	var drain sync.WaitGroup
	drain.Go(func() {
		for {
			select {
			case f, ok := <-srv.Flows():
				if !ok {
					return
				}
				srv.Discard([]string{f.RequestBodyFile, f.ResponseBodyFile})
			case f := <-srv.Frames():
				srv.Discard([]string{f.Frame.File})
			case <-drainCtx.Done():
				return
			}
		}
	})
	defer func() { stopDrain(); drain.Wait() }()
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	tokens := &request.Tokens{}
	steps := make([]runner.Step, len(requests))
	for i, r := range requests {
		steps[i] = runner.Step{Request: r, Context: request.Context{Environment: name, Folders: request.Chain(folders, r.Collection), Globals: globals, Tokens: tokens, Addons: addons, Certificates: certificates, Resolve: resolve}}
	}
	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	start := time.Now()
	report := runner.Report{Version: 1, StartedAt: start.UTC()}
	summary, runErr := runner.Run(runCtx, srv, steps, runner.Options{Iterations: *iterations, Timeout: *stepTimeout, Delay: *delayRequest, StopOnFailure: *bail, Vars: values, Data: rows, Cookies: jar, MaxSteps: *maxSteps}, func(result runner.Result) error {
		r := requests[result.Index]
		report.Append(r, result, *details)
		state := "PASS"
		if result.Execution.Skipped {
			state = "SKIP"
		} else if result.Execution.Failed() {
			state = "FAIL"
		}
		if _, err := fmt.Fprintf(stdout, "%s iteration=%d request=%q · %s\n", state, result.Iteration, r.Name, result.Execution.Outcome()); err != nil {
			return err
		}
		if next := result.Execution.Next; next != nil && *next != "" && !slices.ContainsFunc(requests, func(r request.Request) bool { return r.ID == *next || r.Name == *next }) {
			return errors.New("unknown next request")
		}
		return nil
	})
	report.Finish(summary, runErr, time.Since(start))
	code := 0
	switch {
	case errors.Is(runErr, context.Canceled):
		code = 130
	case errors.Is(runErr, context.DeadlineExceeded):
		code = 124
	case runErr != nil:
		code = 2
	case summary.Failed > 0:
		code = 1
	}
	for _, output := range []struct {
		path string
		data func() ([]byte, error)
	}{{*jsonPath, report.JSON}, {*xmlPath, report.JUnit}} {
		if output.path == "" {
			continue
		}
		data, err := output.data()
		if err == nil {
			err = store.WriteFile(output.path, data)
		}
		if err != nil {
			fmt.Fprintln(stderr, "Could not write report.")
			code = 2
		}
	}
	if runErr != nil {
		fmt.Fprintln(stderr, "Run incomplete:", report.Problem)
	}
	if _, err := fmt.Fprintf(stdout, "%d completed · %d failed · %d skipped\n", summary.Completed, summary.Failed, summary.Skipped); err != nil {
		return 2
	}
	return code
}

func overlaps(output, input string) bool {
	// Resolve existing ancestors too, so a report through a directory symlink
	// cannot overwrite an input. The final destination need not exist yet.
	canonical := func(name string) string {
		name, _ = filepath.Abs(name)
		var missing []string
		for {
			if real, err := filepath.EvalSymlinks(name); err == nil {
				return filepath.Join(append([]string{real}, missing...)...)
			}
			parent := filepath.Dir(name)
			if parent == name {
				return name
			}
			missing = append([]string{filepath.Base(name)}, missing...)
			name = parent
		}
	}
	out, in := canonical(output), canonical(input)
	if out == in {
		return true
	}
	if info, err := os.Stat(input); err == nil && info.IsDir() {
		rel, err := filepath.Rel(in, out)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	return false
}
