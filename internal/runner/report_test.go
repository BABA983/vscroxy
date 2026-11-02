package runner_test

import (
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

// Decode independently of the writer's XML types to check the CI-facing shape.
type reportXML struct {
	XMLName   xml.Name `xml:"testsuite"`
	Tests     int      `xml:"tests,attr"`
	Failures  int      `xml:"failures,attr"`
	Errors    int      `xml:"errors,attr"`
	Skipped   int      `xml:"skipped,attr"`
	Time      string   `xml:"time,attr"`
	Timestamp string   `xml:"timestamp,attr"`
	Cases     []struct {
		Name      string         `xml:"name,attr"`
		Classname string         `xml:"classname,attr"`
		Time      string         `xml:"time,attr"`
		Failure   *reportProblem `xml:"failure"`
		Error     *reportProblem `xml:"error"`
		Skipped   *reportProblem `xml:"skipped"`
		Output    string         `xml:"system-out"`
	} `xml:"testcase"`
}

type reportProblem struct {
	Message string `xml:"message,attr"`
	Detail  string `xml:",chardata"`
}

func decodeReportXML(t *testing.T, report runner.Report) reportXML {
	t.Helper()
	data, err := report.JUnit()
	if err != nil {
		t.Fatal(err)
	}
	var got reportXML
	if err := xml.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode JUnit: %v\n%s", err, data)
	}
	if got.Tests != len(got.Cases) {
		t.Fatalf("test count %d does not match %d testcases", got.Tests, len(got.Cases))
	}
	return got
}

func TestReportJSONAndJUnit(t *testing.T) {
	start := time.Date(2026, time.November, 2, 10, 20, 30, 0, time.UTC)
	report := runner.Report{StartedAt: start}
	req := request.Request{ID: "request-id", Name: "Regression", Collection: "API/Errors"}
	// An explicitly expected 404 is a regression success, not an HTTP failure.
	report.Append(req, runner.Result{Iteration: 2, Duration: 1250 * time.Millisecond, Execution: request.Execution{
		Response: &capture.Flow{Status: 404}, ExpectedStatus: 404,
		Tests: []request.TestResult{{Name: "expected status", Pass: true}, {Name: "later", Pass: true, Skipped: true}},
	}}, false)
	report.Append(req, runner.Result{Iteration: 3, Duration: 250 * time.Millisecond, Execution: request.Execution{
		Response: &capture.Flow{Status: 200},
		Tests:    []request.TestResult{{Name: "first", Pass: true}, {Name: "bad", Pass: false}, {Name: "also bad", Pass: false}},
	}}, false)
	report.Append(req, runner.Result{Iteration: 4, Execution: request.Execution{Skipped: true}}, false)
	report.Finish(runner.Summary{Completed: 3, Failed: 1, Skipped: 1, Stopped: true}, nil, 1500*time.Millisecond)

	data, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"version": float64(1), "startedAt": start.Format(time.RFC3339), "durationMillis": float64(1500),
		"completed": float64(3), "failed": float64(1), "skipped": float64(1),
		"stopped": true, "incomplete": false, "problem": "",
	} {
		if !reflect.DeepEqual(wire[key], want) {
			t.Errorf("JSON %s = %#v, want %#v", key, wire[key], want)
		}
	}
	if len(wire) != 10 {
		t.Errorf("unexpected JSON fields: %s", data)
	}
	rows := wire["results"].([]any)
	if len(rows) != 3 {
		t.Fatalf("results = %d, want 3", len(rows))
	}
	first := rows[0].(map[string]any)
	for key, want := range map[string]any{
		"id": req.ID, "name": req.Name, "collection": req.Collection,
		"iteration": float64(2), "status": float64(404), "durationMillis": float64(1250),
		"failed": false, "skipped": false, "outcome": "HTTP error response",
	} {
		if !reflect.DeepEqual(first[key], want) {
			t.Errorf("attempt JSON %s = %#v, want %#v", key, first[key], want)
		}
	}
	wantTests := []any{
		map[string]any{"name": "Test 1", "pass": true, "skipped": false},
		map[string]any{"name": "Test 2", "pass": true, "skipped": true},
	}
	if !reflect.DeepEqual(first["tests"], wantTests) {
		t.Errorf("tests = %#v, want %#v", first["tests"], wantTests)
	}
	if rows[1].(map[string]any)["failed"] != true || rows[2].(map[string]any)["skipped"] != true {
		t.Errorf("failure/skip flags: %s", data)
	}
	if _, ok := rows[2].(map[string]any)["status"]; ok {
		t.Error("pre-request skip has an HTTP status")
	}

	suite := decodeReportXML(t, report)
	if suite.Tests != 3 || suite.Failures != 1 || suite.Errors != 0 || suite.Skipped != 1 || suite.Time != "1.500" || suite.Timestamp != start.Format(time.RFC3339) {
		t.Fatalf("JUnit summary: %+v", suite)
	}
	if suite.Cases[0].Failure != nil || suite.Cases[0].Skipped != nil || suite.Cases[0].Error != nil {
		t.Errorf("expected 404 or skipped assertion failed/skipped the request: %+v", suite.Cases[0])
	}
	if suite.Cases[0].Time != "1.250" || suite.Cases[0].Name != "Regression (iteration 2)" || suite.Cases[0].Classname != req.Collection {
		t.Errorf("JUnit request identity/time: %+v", suite.Cases[0])
	}
	if suite.Cases[0].Output != "Test 1: passed\nTest 2: skipped" {
		t.Errorf("assertion skip not preserved: %q", suite.Cases[0].Output)
	}
	if suite.Cases[1].Failure == nil || suite.Cases[1].Failure.Message != "Checks failed" || suite.Cases[1].Time != "0.250" || suite.Cases[1].Error != nil {
		t.Errorf("failed attempt: %+v", suite.Cases[1])
	}
	if suite.Cases[2].Skipped == nil || suite.Cases[2].Failure != nil {
		t.Errorf("skipped attempt: %+v", suite.Cases[2])
	}
}

func TestReportFailuresFollowExecution(t *testing.T) {
	for _, test := range []struct {
		name    string
		got     request.Execution
		failed  bool
		outcome string
	}{
		{name: "unexpected HTTP error", got: request.Execution{Response: &capture.Flow{Status: 500}}, failed: true, outcome: "HTTP error response"},
		{name: "transport error", got: request.Execution{Err: errors.New("private network detail"), Stage: "Network error"}, failed: true, outcome: "Network error"},
		{name: "cancelled attempt", got: request.Execution{Err: fmt.Errorf("private: %w", context.Canceled)}, failed: true, outcome: "Cancelled"},
		{name: "timed out attempt", got: request.Execution{Err: fmt.Errorf("private: %w", context.DeadlineExceeded)}, failed: true, outcome: "Timed out"},
		{name: "WebSocket success", got: request.Execution{}, outcome: "WebSocket completed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var report runner.Report
			report.Append(request.Request{Name: test.name}, runner.Result{Iteration: 1, Execution: test.got}, false)
			if report.Results[0].Failed != test.failed || report.Results[0].Outcome != test.outcome {
				t.Fatalf("attempt: %+v", report.Results[0])
			}
			suite := decodeReportXML(t, report)
			if (suite.Cases[0].Failure != nil) != test.failed || suite.Errors != 0 {
				t.Errorf("JUnit: %+v", suite)
			}
		})
	}
}

func TestReportInterruptedRuns(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		problem string
	}{
		{"cancelled", fmt.Errorf("secret: %w", context.Canceled), "cancelled"},
		{"deadline", fmt.Errorf("secret: %w", context.DeadlineExceeded), "timed out"},
		{"budget", errors.New("secret attempt budget exhausted"), "run interrupted"},
	} {
		for _, withAttempt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/attempt=%v", test.name, withAttempt), func(t *testing.T) {
				var report runner.Report
				var summary runner.Summary
				if withAttempt {
					report.Append(request.Request{Name: "done"}, runner.Result{Iteration: 1}, false)
					summary.Completed = 1
				}
				report.Finish(summary, test.err, 2*time.Second)
				data, err := report.JSON()
				if err != nil {
					t.Fatal(err)
				}
				var got runner.Report
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				if !got.Incomplete || got.Problem != test.problem || got.Completed != summary.Completed || got.DurationMillis != 2000 || strings.Contains(string(data), "secret") {
					t.Fatalf("interrupted JSON: %s", data)
				}
				if !withAttempt && !strings.Contains(string(data), `"results":[]`) {
					t.Errorf("empty results must be an array: %s", data)
				}
				suite := decodeReportXML(t, report)
				if suite.Tests != summary.Completed+1 || suite.Errors != 1 || suite.Failures != 0 || suite.Skipped != 0 {
					t.Fatalf("interrupted JUnit: %+v", suite)
				}
				last := suite.Cases[len(suite.Cases)-1]
				if last.Error == nil || last.Error.Message != test.problem || last.Error.Detail != "" {
					t.Errorf("missing controlled error testcase: %+v", last)
				}
				// A report reused for another Finish must not retain an old error.
				report.Finish(summary, nil, time.Second)
				if report.Incomplete || report.Problem != "" || decodeReportXML(t, report).Errors != 0 {
					t.Error("Finish retained a previous interruption")
				}
			})
		}
	}
}

func TestReportSecretsAndDetailOptIn(t *testing.T) {
	req := request.Request{
		ID: "safe-id", Name: "Safe label", Collection: "Safe collection",
		URL: "https://secret-authored-url.invalid", Headers: "Authorization: secret-authored-header", Body: "secret-authored-body",
	}
	result := runner.Result{
		Iteration: 1, RunKey: "secret-run-key", Before: map[string]string{"token": "secret-before"},
		Console: []request.ConsoleMessage{{Text: "secret-runner-console"}},
		Execution: request.Execution{
			Request:  &request.Prepared{URL: "https://secret-runtime-url.invalid", Header: http.Header{"Authorization": {"secret-runtime-header"}}, Body: "secret-runtime-body"},
			Response: &capture.Flow{Status: 200, URL: "https://secret-response-url.invalid", RequestHeaders: "secret-request-header", ResponseHeaders: "secret-response-header", RequestBody: "secret-request-body", ResponseBody: "secret-response-body"},
			Tests:    []request.TestResult{{Name: "detail-test-name", Error: "detail-test-error"}},
			Err:      errors.New("detail-execution-error"), Stage: "secret-arbitrary-stage",
			Vars: map[string]string{"token": "secret-variable"}, Locals: map[string]string{"token": "secret-local"},
			Variables: []request.ResolvedVariable{{Name: "token", Value: "secret-resolved"}},
			Console:   []request.ConsoleMessage{{Text: "secret-execution-console"}},
			Writes:    []request.Write{{Name: "token", Value: "secret-write"}}, Replies: []string{"secret-WebSocket-reply"},
		},
	}
	for _, details := range []bool{false, true} {
		t.Run(fmt.Sprintf("details=%v", details), func(t *testing.T) {
			var report runner.Report
			report.Append(req, result, details)
			report.Finish(runner.Summary{Completed: 1, Failed: 1}, errors.New("secret-run-error"), time.Second)
			jsonData, err := report.JSON()
			if err != nil {
				t.Fatal(err)
			}
			xmlData, err := report.JUnit()
			if err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string][]byte{"JSON": jsonData, "JUnit": xmlData} {
				if strings.Contains(string(data), "secret-") {
					t.Errorf("%s leaked runtime data: %s", name, data)
				}
				for _, detail := range []string{"detail-test-name", "detail-test-error", "detail-execution-error"} {
					if strings.Contains(string(data), detail) != details {
						t.Errorf("%s inclusion of %q does not match details=%v: %s", name, detail, details, data)
					}
				}
				if !strings.Contains(string(data), req.Name) || !strings.Contains(string(data), req.Collection) {
					t.Errorf("%s lost authored labels: %s", name, data)
				}
			}
		})
	}
}

func TestReportXMLRoundTripEscaping(t *testing.T) {
	label := "A < B & C > D \"quoted\" 'apostrophe' ☃"
	report := runner.Report{}
	report.Append(request.Request{ID: "id", Name: label, Collection: label}, runner.Result{Iteration: 1, Execution: request.Execution{
		Tests: []request.TestResult{{Name: label, Error: label}}, Err: errors.New(label), Stage: "Network error",
	}}, true)
	suite := decodeReportXML(t, report)
	got := suite.Cases[0]
	if got.Name != label+" (iteration 1)" || got.Classname != label || got.Failure == nil || got.Failure.Detail != label || got.Output != label+": failed: "+label {
		t.Fatalf("XML did not round-trip authored labels/details: %+v", got)
	}
}

func TestReportAppendSnapshotsAndZeroValue(t *testing.T) {
	var empty runner.Report
	data, err := empty.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version":1`) || !strings.Contains(string(data), `"results":[]`) || empty.Version != 0 {
		t.Fatalf("zero report JSON or mutation: %s, %+v", data, empty)
	}
	if got := decodeReportXML(t, empty); got.Tests != 0 || got.Errors != 0 {
		t.Errorf("zero report JUnit: %+v", got)
	}
	result := runner.Result{Iteration: 1, Execution: request.Execution{
		Response: &capture.Flow{Status: 200}, Tests: []request.TestResult{{Name: "original", Pass: true}},
	}}
	var report runner.Report
	report.Append(request.Request{ID: "id"}, result, true)
	result.Execution.Response.Status = 500
	result.Execution.Tests[0].Name = "mutated"
	result.Execution.Tests[0].Pass = false
	if got := report.Results[0]; got.Status != 200 || got.Tests[0].Name != "original" || !got.Tests[0].Pass || got.Failed {
		t.Errorf("report aliases mutable execution: %+v", got)
	}
}
