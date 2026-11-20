// Package cli implements headless commands without desktop or MyGo imports.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/datadir"
	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/openapi"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/version"
)

// Run returns an exit code: 0 success, 1 failed attempt, 2 setup/output error,
// 124 whole-run deadline, 130 cancellation. Flags precede the input path.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// Like the desktop, a command can be prefixed with a data root; the CLI
	// otherwise uses the same VSCROXY_HOME / ~/.vscroxy resolver. Both
	// --user-data-dir <path> and --user-data-dir=<path> are accepted.
	for len(args) > 0 {
		if args[0] == "--user-data-dir" || args[0] == "-user-data-dir" {
			if len(args) < 2 {
				break
			}
			datadir.SetFlag(args[1])
			args = args[2:]
			continue
		}
		if path, ok := datadir.FlagValue(args[0]); ok && path != "" {
			datadir.SetFlag(path)
			args = args[1:]
			continue
		}
		break
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		fmt.Fprintln(stdout, "vscroxy-cli "+version.String())
		return 0
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: vscroxy-cli run [options] <collections-root | postman.json>\n       vscroxy-cli load [options] <collections-root | postman.json>\n       vscroxy-cli ab [options] <collections-root | postman.json | session.json>\n       vscroxy-cli mock [options] <collections-root | postman.json>\n       vscroxy-cli openapi lint [options] <spec>\n       vscroxy-cli control --file <discovery> [options] <action>\nPrefix a command with --user-data-dir <path> (or --user-data-dir=<path>) to use another data folder.\nUse 'run --help', 'load --help', 'ab --help', 'mock --help' or 'control --help' for options.")
		return 0
	}
	if args[0] == "control" {
		return controlCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "load" {
		return loadCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "ab" {
		return abCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "mock" {
		return mockCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "openapi" {
		return openapiCommand(ctx, args[1:], stdout, stderr)
	}
	if args[0] != "run" {
		fmt.Fprintln(stderr, "Unknown command; use 'vscroxy-cli --help'.")
		return 2
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	folder := flags.String("folder", "", "Collection/folder path to run (includes children)")
	flags.StringVar(folder, "i", "", "Alias of --folder, for Newman-style item selection")
	envPath := flags.String("env", "", "Explicit native env.json or Postman environment/globals file")
	envName := flags.String("environment", "", "Environment name in --env; defaults to its active environment")
	dataPath := flags.String("data", "", "CSV or JSON iteration data")
	addonsDir := flags.String("addons", "", "Folder of addons scripts required by name, as <data folder>/tools/addons")
	iterations := flags.Int("iterations", 0, "Iterations, 1–1000; default is data row count or 1")
	bail := flags.Bool("bail", false, "Stop after the first failed attempt")
	timeout := flags.Duration("timeout", 5*time.Minute, "Whole-run timeout (must be positive)")
	stepTimeout := flags.Duration("request-timeout", 30*time.Second, "Per-request timeout (must be positive)")
	delayRequest := flags.Duration("delay-request", 0, "Wait before each request after the first, including between iterations (0 for none)")
	maxSteps := flags.Int("max-steps", 10000, "Maximum attempts, including script jumps (1–100000)")
	jsonPath := flags.String("report-json", "", "Write a private JSON report")
	xmlPath := flags.String("report-junit", "", "Write a private JUnit XML report")
	htmlPath := flags.String("report-html", "", "Write a self-contained HTML report")
	contractPath := flags.String("contract", "", "Fail when sent exchanges drift from this OpenAPI contract")
	mockPath := flags.String("mock", "", "Serve this collection's saved examples on loopback for the run and point {{base}} and {{base_url}} at it")
	exports := runExports{}
	flags.StringVar(&exports.environment, "export-environment", "", "Write the environment's final values as a Postman environment file")
	flags.StringVar(&exports.globals, "export-globals", "", "Write the global variables' final values as a Postman globals file")
	flags.StringVar(&exports.cookieJar, "export-cookie-jar", "", "Write the run's cookies as a JSON cookie jar")
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
	outputs := append([]string{*jsonPath, *xmlPath, *htmlPath}, exports.paths()...)
	for i, output := range outputs {
		if output == "" {
			continue
		}
		for _, other := range outputs[i+1:] {
			if other != "" && (overlaps(output, other) || overlaps(other, output)) {
				fmt.Fprintln(stderr, "Report and export destinations must be different files.")
				return 2
			}
		}
	}
	var contract *openapi.Checker
	if *contractPath != "" {
		loaded, loadErr := openapi.LoadChecker(*contractPath)
		if loadErr != nil {
			fmt.Fprintln(stderr, "Could not load contract:", loadErr)
			return 2
		}
		contract = loaded
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
	// Refuse destinations that could replace an input. Nothing writes
	// requests, environments, or data, including script-generated writes.
	for _, output := range outputs {
		if output == "" {
			continue
		}
		for _, input := range []string{flags.Arg(0), *envPath, *dataPath, *contractPath} {
			if input != "" && overlaps(output, input) {
				fmt.Fprintln(stderr, "Report or export destination overlaps an input; choose a separate output directory.")
				return 2
			}
		}
	}
	// A --mock collection is a short-lived server for this run: its examples
	// answer on loopback, and the base variables point there instead of at a
	// real service. Its listener, file watch and goroutines are joined on
	// every exit path, including cancel and the whole-run deadline.
	var runMock *mockRuntime
	var restoreMockVars map[string]*string
	if *mockPath != "" {
		mockVars := maps.Clone(globals)
		maps.Copy(mockVars, values)
		source := mockSource{input: *mockPath, vars: mockVars}
		baseline := collectionFingerprint(source.input)
		set, _, err := source.load()
		if err != nil {
			fmt.Fprintln(stderr, "Could not load the --mock collection; check its input and folder selection.")
			return 2
		}
		runMock, err = startMockRuntime(source, baseline, set, mockserver.Options{}, "127.0.0.1:0", "127.0.0.1:0", stdout, stderr, true)
		if err != nil {
			fmt.Fprintln(stderr, "Could not start the --mock server; save an example in that collection first.")
			return 2
		}
		defer runMock.Close()
		restoreMockVars = map[string]*string{}
		for key, value := range map[string]string{"base": runMock.httpURL, "base_url": runMock.httpURL, "grpc_base": runMock.grpcURL} {
			if value == "" && key == "grpc_base" {
				continue
			}
			old, present := values[key]
			if present {
				restoreMockVars[key] = &old
			} else {
				restoreMockVars[key] = nil
			}
			values[key] = value
		}
		fmt.Fprintf(stdout, "Mock for this run at %s\n", runMock.httpURL)
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
				if contract == nil && f.DebugRun == "" {
					// Keep a local send's spill file until the run ends:
					// {{response "..."}} may still need to read that body
					// from the session store. Foreign traffic is discarded.
					srv.Discard([]string{f.RequestBodyFile, f.ResponseBodyFile})
				}
			case f := <-srv.Frames():
				srv.Discard([]string{f.Frame.File})
			case <-drainCtx.Done():
				return
			}
		}
	})
	defer func() { stopDrain(); drain.Wait() }()
	jar := newRecordingJar()
	tokens := &request.Tokens{}
	// One session store per run: a login step can hand its latest response to
	// any later step through {{response "Login" "/token"}}.
	responses := &request.Responses{}
	steps := make([]runner.Step, len(requests))
	for i, r := range requests {
		steps[i] = runner.Step{Request: r, Context: request.Context{Environment: name, Folders: request.Chain(folders, r.Collection), Globals: globals, Tokens: tokens, Addons: addons, Certificates: certificates, Resolve: resolve, Responses: responses}}
	}
	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	start := time.Now()
	report := runner.Report{Version: 1, StartedAt: start.UTC()}
	if contract != nil {
		report.Contract = &runner.ContractReport{Spec: filepath.Base(*contractPath)}
	}
	var contractProblems []string
	// Exports hold the state the run ended with: the environment a script
	// last set, and the globals' initial values with the scripts' changes.
	finalVars, finalGlobals := maps.Clone(values), maps.Clone(globals)
	summary, runErr := runner.Run(runCtx, srv, steps, runner.Options{Iterations: *iterations, Timeout: *stepTimeout, Delay: *delayRequest, StopOnFailure: *bail, Vars: values, Data: rows, Cookies: jar, MaxSteps: *maxSteps}, func(result runner.Result) error {
		r := requests[result.Index]
		report.Append(r, result, *details)
		if contract != nil && !result.Execution.Skipped {
			endpoint, problems, matched := checkContract(contract, result)
			report.Contract.Checked++
			if matched && len(problems) == 0 {
				report.Contract.Matched++
			} else {
				if !matched {
					endpoint = contractEndpoint(result)
					problems = []string{"no operation in the contract serves this request"}
				}
				report.Contract.Failed++
				if endpoint != "" && !slices.Contains(report.Contract.Endpoints, endpoint) {
					report.Contract.Endpoints = append(report.Contract.Endpoints, endpoint)
				}
				line := r.Name + " · " + endpoint + ": " + strings.Join(problems, " · ")
				if len(line) > 700 {
					line = line[:700] + "…"
				}
				contractProblems = append(contractProblems, line)
			}
		}
		if result.Execution.Vars != nil {
			finalVars = maps.Clone(result.Execution.Vars)
		}
		for _, write := range result.Execution.Writes {
			if write.Scope != "global" {
				continue
			}
			if !write.Unset {
				finalGlobals[write.Name] = write.Value
			} else {
				delete(finalGlobals, write.Name)
			}
		}
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
	if report.Contract != nil && report.Contract.Failed > 0 && code == 0 {
		code = 1
	}
	for _, output := range []struct {
		path string
		data func() ([]byte, error)
	}{{*jsonPath, report.JSON}, {*xmlPath, report.JUnit}, {*htmlPath, report.HTML}} {
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
	// The mock's address exists only for this run; an exported environment
	// gets back whatever the caller had in these variables beforehand.
	for key, old := range restoreMockVars {
		if old == nil {
			delete(finalVars, key)
		} else {
			finalVars[key] = *old
		}
	}
	if err := exports.write(name, finalVars, finalGlobals, jar); err != nil {
		fmt.Fprintln(stderr, "Could not write exported state.")
		code = 2
	}
	if runErr != nil {
		fmt.Fprintln(stderr, "Run incomplete:", report.Problem)
	}
	if report.Contract != nil {
		if _, err := fmt.Fprintf(stdout, "Contract %s · %d checked · %d matched · %d drifted\n", report.Contract.Spec, report.Contract.Checked, report.Contract.Matched, report.Contract.Failed); err != nil {
			return 2
		}
		for _, problem := range contractProblems {
			if _, err := fmt.Fprintln(stdout, "DRIFT "+problem); err != nil {
				return 2
			}
		}
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

// checkContract validates one attempt against the contract. An empty endpoint
// means the contract has no operation for the request.
func checkContract(checker *openapi.Checker, result runner.Result) (string, []string, bool) {
	var ex openapi.Exchange
	if prepared := result.Execution.Request; prepared != nil {
		ex.Method, ex.URL, ex.Header = prepared.Method, prepared.URL, prepared.Header
		ex.Body = contractBody(prepared.Body, prepared.BodyFile)
	}
	if response := result.Execution.Response; response != nil {
		ex.Status = response.Status
		ex.ResponseHeader = response.ResponseHeader
		ex.ResponseBody = contractBody(response.ResponseBody, response.ResponseBodyFile)
	}
	drift, matched := checker.Check(ex)
	return drift.Endpoint, drift.Problems, matched
}

func contractEndpoint(result runner.Result) string {
	prepared := result.Execution.Request
	if prepared == nil {
		return ""
	}
	path := ""
	if u, err := url.Parse(prepared.URL); err == nil {
		path = u.EscapedPath()
	}
	return strings.TrimSpace(prepared.Method + " " + path)
}

// contractBody returns a body for validation, reading a spilled body within a
// bound so one huge response cannot exhaust the run.
func contractBody(text, file string) []byte {
	if file != "" {
		if f, err := os.Open(file); err == nil {
			data, readErr := io.ReadAll(io.LimitReader(f, 16<<20))
			_ = f.Close()
			if readErr == nil {
				return data
			}
		}
	}
	return []byte(text)
}
