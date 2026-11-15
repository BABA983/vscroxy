package cli

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/ab"
	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/repro"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/store"
)

// abCommand replays one collection or saved session against two environments
// and reports structural differences per endpoint.
func abCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ab", flag.ContinueOnError)
	flags.SetOutput(stderr)
	aPath := flags.String("a", "", "Environment A (native env.json or Postman environment)")
	bPath := flags.String("b", "", "Environment B (native env.json or Postman environment)")
	aName := flags.String("environment-a", "", "Environment name in --a")
	bName := flags.String("environment-b", "", "Environment name in --b")
	baseA := flags.String("base-a", "", "Session replay only: replace each flow's origin with this base for A")
	baseB := flags.String("base-b", "", "Session replay only: replace each flow's origin with this base for B")
	includeUnsafe := flags.Bool("include-unsafe", false, "Session replay: also replay POST, PUT, PATCH and DELETE")
	keepCredentials := flags.Bool("keep-credentials", false, "Session replay: keep captured credential headers")
	var headerFlagsA, headerFlagsB abHeaders
	flags.Var(&headerFlagsA, "header-a", "Header Name: Value added to A (repeatable)")
	flags.Var(&headerFlagsB, "header-b", "Header Name: Value added to B (repeatable)")
	folder := flags.String("folder", "", "Collection/folder path to run (includes children)")
	flags.StringVar(folder, "i", "", "Alias of --folder")
	jsonPath := flags.String("report-json", "", "Write the A/B report as JSON")
	xmlPath := flags.String("report-junit", "", "Write the A/B report as JUnit XML")
	timeout := flags.Duration("timeout", 5*time.Minute, "Whole replay timeout per side")
	stepTimeout := flags.Duration("request-timeout", 30*time.Second, "Per-request timeout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 || *timeout <= 0 || *stepTimeout <= 0 || *jsonPath == "" && *xmlPath == "" {
		fmt.Fprintln(stderr, "Provide one input, positive timeouts, and --report-json or --report-junit.")
		return 2
	}
	headersA, err := parseABHeaders(headerFlagsA)
	if err != nil {
		fmt.Fprintln(stderr, "Could not parse --header-a:", err)
		return 2
	}
	headersB, err := parseABHeaders(headerFlagsB)
	if err != nil {
		fmt.Fprintln(stderr, "Could not parse --header-b:", err)
		return 2
	}
	input := flags.Arg(0)
	session, err := readABSession(input)
	if err != nil {
		fmt.Fprintln(stderr, "Could not read session:", err)
		return 2
	}
	if session != nil {
		if *baseA == "" || *baseB == "" {
			fmt.Fprintln(stderr, "A session replay needs --base-a and --base-b.")
			return 2
		}
		if *includeUnsafe {
			fmt.Fprintln(stderr, "Warning: --include-unsafe replays captured POST, PUT, PATCH and DELETE requests against both bases.")
		}
	} else if *baseA != "" || *baseB != "" || *includeUnsafe || *keepCredentials {
		fmt.Fprintln(stderr, "--base-a and --base-b apply only to session input.")
		return 2
	}
	var sideA, sideB abEnvironment
	if session == nil {
		nameA, valuesA, globalsA, err := readEnvironment(*aPath, *aName)
		if err != nil {
			fmt.Fprintln(stderr, "Could not load environment A:", err)
			return 2
		}
		nameB, valuesB, globalsB, err := readEnvironment(*bPath, *bName)
		if err != nil {
			fmt.Fprintln(stderr, "Could not load environment B:", err)
			return 2
		}
		sideA = abEnvironment{name: nameA, vars: valuesA, globals: globalsA}
		sideB = abEnvironment{name: nameB, vars: valuesB, globals: globalsB}
		if sideA.name == "" {
			sideA.name = "A"
		}
		if sideB.name == "" {
			sideB.name = "B"
		}
	} else {
		sideA.name, sideB.name = "A", "B"
	}
	var requests []request.Request
	var initialSkipped []ab.Skipped
	var folders []request.Folder
	var resolve request.Resolver
	if session == nil {
		var warnings []string
		requests, folders, warnings, err = loadCollection(input, *folder)
		if err != nil {
			fmt.Fprintln(stderr, "Could not load collection:", err)
			return 2
		}
		all, _, _, err := loadCollection(input, "")
		if err != nil {
			fmt.Fprintln(stderr, "Could not load collection:", err)
			return 2
		}
		resolve = library.Resolver(all, folders)
		for _, warning := range warnings {
			fmt.Fprintln(stderr, "Import warning:", warning)
		}
	} else {
		requests, initialSkipped = session.requests(*baseA, *includeUnsafe, *keepCredentials, headersA)
	}
	if len(requests) == 0 {
		if session == nil {
			fmt.Fprintln(stderr, "No HTTP(S) requests to replay.")
			return 2
		}
		report := ab.Report{Input: filepath.Base(input), A: sideA.name, B: sideB.name}
		for _, flow := range initialSkipped {
			report.Skip(flow.Method, flow.URL, flow.Reason)
		}
		return finishABReport(report, *jsonPath, *xmlPath, stdout, stderr)
	}
	root, err := os.MkdirTemp("", "vscroxy-ab-")
	if err != nil {
		fmt.Fprintln(stderr, "Could not create temporary runtime.")
		return 2
	}
	defer os.RemoveAll(root)
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
	drainCtx, stopDrain := context.WithCancel(context.Background())
	var drain sync.WaitGroup
	drain.Go(func() {
		for {
			select {
			case _, ok := <-srv.Flows():
				if !ok {
					return
				}
			case _, ok := <-srv.Frames():
				if !ok {
					return
				}
			case <-drainCtx.Done():
				return
			}
		}
	})
	defer func() { stopDrain(); drain.Wait() }()

	runSide := func(side abEnvironment, base string, extra http.Header) ([]abResult, []ab.Skipped, error) {
		sideRequests := requests
		var skipped []ab.Skipped
		if session != nil {
			sideRequests, skipped = session.requests(base, *includeUnsafe, *keepCredentials, extra)
		}
		if len(extra) > 0 && session == nil {
			sideRequests = slices.Clone(requests)
			for i := range sideRequests {
				sideRequests[i].Headers = mergeABHeaders(sideRequests[i].Headers, extra)
			}
		}
		tokens := &request.Tokens{}
		jar := newRecordingJar()
		steps := make([]runner.Step, len(sideRequests))
		for i, r := range sideRequests {
			steps[i] = runner.Step{Request: r, Context: request.Context{
				Environment: side.name, Folders: request.Chain(folders, r.Collection), Globals: side.globals,
				Tokens: tokens, Certificates: certificates, Resolve: resolve,
			}}
		}
		runCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		var results []abResult
		_, runErr := runner.Run(runCtx, srv, steps, runner.Options{
			Iterations: 1, Timeout: *stepTimeout, Vars: side.vars, Cookies: jar,
		}, func(result runner.Result) error {
			results = append(results, abResultOf(result))
			return nil
		})
		if runErr != nil {
			return results, skipped, runErr
		}
		return results, skipped, nil
	}
	resultsA, skipped, err := runSide(sideA, *baseA, headersA)
	if err != nil {
		fmt.Fprintln(stderr, "Replay A incomplete:", err)
		return 2
	}
	resultsB, _, err := runSide(sideB, *baseB, headersB)
	if err != nil {
		fmt.Fprintln(stderr, "Replay B incomplete:", err)
		return 2
	}
	byIndex := func(results []abResult) map[int]abResult {
		out := make(map[int]abResult, len(results))
		for _, result := range results {
			out[result.index] = result
		}
		return out
	}
	indexA, indexB := byIndex(resultsA), byIndex(resultsB)
	report := ab.Report{Input: filepath.Base(input), A: sideA.name, B: sideB.name}
	for i, r := range requests {
		left, okA := indexA[i]
		right, okB := indexB[i]
		if !okA {
			left = abResult{response: ab.Response{Error: "not run"}}
		}
		if !okB {
			right = abResult{response: ab.Response{Error: "not run"}}
		}
		endpoint := ab.Compare(r.Name, r.Method, sessionEndpoint(session, r.ID, r.URL), left.response, right.response)
		report.Add(endpoint)
	}
	for _, flow := range skipped {
		report.Skip(flow.Method, flow.URL, flow.Reason)
	}
	return finishABReport(report, *jsonPath, *xmlPath, stdout, stderr)
}

func finishABReport(report ab.Report, jsonPath, xmlPath string, stdout, stderr io.Writer) int {
	for _, output := range []struct {
		path string
		data func() ([]byte, error)
	}{{jsonPath, report.JSON}, {xmlPath, report.JUnit}} {
		if output.path == "" {
			continue
		}
		data, err := output.data()
		if err == nil {
			err = store.WriteFile(output.path, data)
		}
		if err != nil {
			fmt.Fprintln(stderr, "Could not write report.")
			return 2
		}
	}
	for _, endpoint := range report.Endpoints {
		if len(endpoint.Differences) > 0 {
			fmt.Fprintf(stdout, "DIFF %s %s · %s\n", endpoint.Method, endpoint.URL, strings.Join(endpoint.Differences, " · "))
		}
	}
	for _, flow := range report.Skipped {
		fmt.Fprintf(stdout, "SKIP %s %s · %s\n", flow.Method, flow.URL, flow.Reason)
	}
	fmt.Fprintf(stdout, "A/B replay · %d endpoints · %d same · %d different · %d skipped\n", len(report.Endpoints), report.Same, report.Different, len(report.Skipped))
	if report.Different > 0 {
		return 1
	}
	return 0
}

type abEnvironment struct {
	name    string
	vars    map[string]string
	globals map[string]string
}

type abResult struct {
	index    int
	response ab.Response
}

func abResultOf(result runner.Result) abResult {
	out := abResult{index: result.Index}
	if result.Execution.Err != nil {
		out.response.Error = result.Execution.Err.Error()
	}
	if response := result.Execution.Response; response != nil {
		out.response.Status = response.Status
		out.response.ContentType = response.ResponseHeader.Get("Content-Type")
		out.response.Body = contractBody(response.ResponseBody, response.ResponseBodyFile)
	}
	return out
}

type abSession struct {
	Title string `json:"title"`
	Flows []struct {
		ID         int    `json:"id"`
		Method     string `json:"method"`
		URL        string `json:"url"`
		ReqHeaders string `json:"reqHeaders"`
		ReqBody    string `json:"reqBody"`
		ReqBytes   []byte `json:"reqBytes"`
		Kind       string `json:"kind"`
	} `json:"flows"`
}

func readABSession(path string) (*abSession, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if _, ok := repro.Collection(data); ok {
		return nil, nil
	}
	var probe struct {
		Flows jsontext.Value `json:"flows"`
	}
	if json.Unmarshal(data, &probe) != nil || len(probe.Flows) == 0 || bytes.Equal(probe.Flows, []byte("null")) {
		return nil, nil
	}
	var session abSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	if len(session.Flows) == 0 {
		return nil, errors.New("the session has no flows")
	}
	return &session, nil
}

func (s *abSession) requests(base string, includeUnsafe, keepCredentials bool, extra http.Header) ([]request.Request, []ab.Skipped) {
	var out []request.Request
	var skipped []ab.Skipped
	for _, flow := range s.Flows {
		if flow.Method == "" || flow.Kind != "" && flow.Kind != "http" {
			continue
		}
		if !includeUnsafe && !safeABMethod(flow.Method) {
			skipped = append(skipped, ab.Skipped{Method: flow.Method, URL: abPath(flow.URL), Reason: "unsafe method; use --include-unsafe"})
			continue
		}
		target, err := rewriteOrigin(flow.URL, base)
		if err != nil {
			continue
		}
		headers := flow.ReqHeaders
		if !keepCredentials {
			headers = stripABCredentials(headers)
		}
		headers = mergeABHeaders(headers, extra)
		body := flow.ReqBody
		if len(flow.ReqBytes) > 0 {
			body = string(flow.ReqBytes)
		}
		out = append(out, request.Request{
			ID: fmt.Sprintf("session-%d", flow.ID), Name: flow.Method + " " + flow.URL,
			Method: flow.Method, URL: target, Headers: headers, Body: body,
		})
	}
	return out, skipped
}

func safeABMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS":
		return true
	}
	return false
}

func stripABCredentials(block string) string {
	header := request.ParseHeaderBlock(block)
	for name := range header {
		if request.IsSensitiveHeader(name) {
			header.Del(name)
		}
	}
	return request.HeaderBlock(header)
}

func mergeABHeaders(block string, extra http.Header) string {
	if len(extra) == 0 {
		return block
	}
	header := request.ParseHeaderBlock(block)
	for name, values := range extra {
		header.Del(name)
		for _, value := range values {
			header.Add(name, value)
		}
	}
	return request.HeaderBlock(header)
}

func abPath(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return raw
}

type abHeaders []string

func (h *abHeaders) String() string { return strings.Join(*h, ", ") }

func (h *abHeaders) Set(value string) error {
	*h = append(*h, value)
	return nil
}

func parseABHeaders(values abHeaders) (http.Header, error) {
	if len(values) == 0 {
		return nil, nil
	}
	_, headers, _, err := request.Checks{Headers: strings.Join(values, "\n")}.Parse()
	return headers, err
}

func rewriteOrigin(raw, base string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("flow URL is not absolute")
	}
	b, err := url.Parse(base)
	if err != nil || b.Scheme == "" || b.Host == "" {
		return "", errors.New("base must be an absolute URL")
	}
	u.Scheme, u.Host = b.Scheme, b.Host
	if b.Path != "" && b.Path != "/" {
		u.Path = strings.TrimSuffix(b.Path, "/") + u.Path
	}
	return u.String(), nil
}

func sessionEndpoint(session *abSession, id, fallback string) string {
	if session == nil {
		return fallback
	}
	for _, flow := range session.Flows {
		if fmt.Sprintf("session-%d", flow.ID) == id {
			return flow.URL
		}
	}
	return fallback
}
