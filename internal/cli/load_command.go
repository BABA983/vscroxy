package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/store"
)

func loadCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("load", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: vscroxy-cli load [options] <collections-root | postman.json>\nClosed workload: each user repeats the selected HTTP(S) steps with private state.")
		flags.PrintDefaults()
	}
	folder := flags.String("folder", "", "Collection/folder path to load (includes children)")
	envPath := flags.String("env", "", "Explicit native env.json or Postman environment/globals file")
	envName := flags.String("environment", "", "Environment name in --env; defaults to its active environment")
	jsonPath := flags.String("report-json", "", "Write a private aggregate-only JSON report (no raw details)")
	opts := runner.LoadOptions{}
	flags.IntVar(&opts.Users, "users", 1, "Concurrent users (1–64)")
	flags.DurationVar(&opts.Duration, "duration", 30*time.Second, "Load duration (1s–10m)")
	flags.DurationVar(&opts.Timeout, "request-timeout", 30*time.Second, "Per-request timeout (positive, up to 1m)")
	flags.IntVar(&opts.MaxAttempts, "max-attempts", 100000, "Total attempt budget across users (1–1000000)")
	flags.BoolVar(&opts.StopOnFailure, "bail", false, "Stop all users after a failed attempt")
	opts.Vars = map[string]string{}
	flags.Func("var", "Environment override KEY=VALUE (repeatable; use --env for secrets)", func(s string) error {
		key, value, ok := strings.Cut(s, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return errors.New("expected KEY=VALUE")
		}
		opts.Vars[key] = value
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	// The engine accepts zero defaults for programmatic callers, but explicit
	// zero on the command line is almost always a mistaken bound.
	if flags.NArg() != 1 || opts.Timeout <= 0 || opts.MaxAttempts <= 0 || opts.Validate() != nil {
		fmt.Fprintln(stderr, "Provide one collection input, 1–64 users, 1s–10m duration, a positive request timeout up to 1m, and 1–1000000 max attempts.")
		return 2
	}
	requests, folders, warnings, err := loadCollection(flags.Arg(0), *folder)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load collection; check the input and folder selection.")
		return 2
	}
	if len(warnings) > 0 {
		// Import warnings can quote untrusted format names; never echo input.
		if _, err := fmt.Fprintf(stderr, "Import warning: %d unsupported feature categories were omitted or adapted.\n", len(warnings)); err != nil {
			return 2
		}
	}
	name, values, globals, err := readEnvironment(*envPath, *envName)
	if err != nil {
		fmt.Fprintln(stderr, "Could not load environment; check --env and --environment.")
		return 2
	}
	for key, value := range opts.Vars {
		values[key] = value
	}
	opts.Vars = values
	if *jsonPath != "" {
		alias, err := loadReportAliasesInput(*jsonPath, flags.Arg(0), *envPath)
		if err != nil {
			fmt.Fprintln(stderr, "Could not verify report destination.")
			return 2
		}
		if alias {
			fmt.Fprintln(stderr, "Report destination overlaps an input; choose a separate output directory.")
			return 2
		}
	}
	root, err := os.MkdirTemp("", "vscroxy-load-")
	if err != nil {
		fmt.Fprintln(stderr, "Could not create temporary runtime.")
		return 2
	}
	defer os.RemoveAll(root)
	// capture.New installs a process-local DNS resolver. Restore it after
	// closing the temporary server, including on setup errors.
	resolver := net.DefaultResolver
	defer func() { net.DefaultResolver = resolver }()
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
	steps := make([]runner.Step, len(requests))
	for i, r := range requests {
		steps[i] = runner.Step{Request: r, Context: request.Context{Environment: name, Folders: request.Chain(folders, r.Collection), Globals: globals, Certificates: certificates}}
	}
	// RunLoad suppresses recording (including auxiliary sends), owns per-user
	// cookies/tokens/state, and returns aggregates, not individual executions.
	report, runErr := runner.RunLoad(ctx, srv, steps, opts, nil)
	if !report.Done {
		fmt.Fprintln(stderr, "Could not start load; select 1–200 HTTP(S) requests.")
		return 2
	}
	code := 0
	switch {
	case errors.Is(runErr, context.Canceled):
		code = 130
	case errors.Is(runErr, context.DeadlineExceeded):
		code = 124
	case runErr != nil:
		code = 2
	case report.Total.Failed > 0:
		code = 1
	}
	if *jsonPath != "" {
		data, err := json.Marshal(report)
		if err == nil {
			err = store.WriteFile(*jsonPath, data)
		}
		if err != nil {
			fmt.Fprintln(stderr, "Could not write report.")
			code = 2
		}
	}
	if runErr != nil {
		if _, err := fmt.Fprintln(stderr, "Load incomplete:", report.Reason); err != nil {
			code = 2
		}
	}
	m := report.Total
	if _, err := fmt.Fprintf(stdout, "Load %s · %d users · %d completed · %d failed · %d skipped · %d interrupted\nThroughput: %.2f completed/s · execution latency: P50 %.3f ms · P95 %.3f ms · P99 %.3f ms\nScope: closed workload; latency includes scripts/auth/network/checks for completed attempts (including failures), excludes skips and load interruptions; throughput is completed attempts / elapsed time. Primary response counts/bytes exclude redirects and auxiliary traffic.\n", report.Reason, report.Users, m.Completed, m.Failed, m.Skipped, m.Interrupted, m.RequestsPerSec, m.P50Millis, m.P95Millis, m.P99Millis); err != nil {
		return 2
	}
	return code
}

// In addition to canonical path/ancestor checks, compare identities: a report
// outside a collection can still be a hard link to a request or folder file.
func loadReportAliasesInput(output string, inputs ...string) (bool, error) {
	outInfo, outErr := os.Stat(output)
	if outErr != nil && !errors.Is(outErr, os.ErrNotExist) {
		return false, outErr
	}
	for _, input := range inputs {
		if input == "" {
			continue
		}
		if overlaps(output, input) {
			return true, nil
		}
		if outErr != nil {
			continue // a nonexistent destination cannot be a hard link
		}
		info, err := os.Stat(input)
		if err != nil {
			return false, err
		}
		if os.SameFile(outInfo, info) {
			return true, nil
		}
		if !info.IsDir() {
			continue
		}
		root, err := filepath.EvalSymlinks(input)
		if err != nil {
			return false, err
		}
		alias := false
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil // ignored dangling symlink, not a hard link
			}
			if err != nil {
				return err
			}
			if os.SameFile(outInfo, info) {
				alias = true
				return fs.SkipAll
			}
			return nil
		})
		if err != nil || alias {
			return alias, err
		}
	}
	return false, nil
}
