package runner

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
)

// Report is a portable CI report. Set StartedAt when the run starts; its zero
// value is otherwise ready to use. Only authored labels and explicitly selected
// execution fields are retained, never the request or response themselves.
type Report struct {
	Version        int       `json:"version"`
	StartedAt      time.Time `json:"startedAt"`
	DurationMillis int64     `json:"durationMillis"`
	Completed      int       `json:"completed"`
	Failed         int       `json:"failed"`
	Skipped        int       `json:"skipped"`
	Stopped        bool      `json:"stopped"`
	Incomplete     bool      `json:"incomplete"`
	Problem        string    `json:"problem"`
	Results        []Attempt `json:"results"`
}

// Attempt is one request attempt, not one assertion. Error and assertion names
// and errors may contain runtime secrets only if Append's includeDetails is true.
type Attempt struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Collection     string      `json:"collection"`
	Iteration      int         `json:"iteration"`
	Status         int         `json:"status,omitzero"`
	DurationMillis int64       `json:"durationMillis"`
	Failed         bool        `json:"failed"`
	Skipped        bool        `json:"skipped"`
	Outcome        string      `json:"outcome"`
	Error          string      `json:"error,omitempty"`
	Tests          []Assertion `json:"tests"`
}

// Assertion preserves a script/check result, including pm.test.skip. Names are
// ordinal labels by default because even an assertion's name can embed secrets.
type Assertion struct {
	Name    string `json:"name"`
	Pass    bool   `json:"pass"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// Append snapshots an attempt. includeDetails opts in to potentially sensitive
// assertion names/errors and execution errors, but never wire data or variables.
func (r *Report) Append(req request.Request, result Result, includeDetails bool) {
	r.Version = 1
	got := result.Execution
	attempt := Attempt{
		ID: req.ID, Name: req.Name, Collection: req.Collection,
		Iteration: result.Iteration, DurationMillis: result.Duration.Milliseconds(),
		Failed: got.Failed(), Skipped: got.Skipped, Outcome: reportOutcome(got),
		Tests: make([]Assertion, 0, len(got.Tests)),
	}
	if got.Response != nil {
		attempt.Status = got.Response.Status
	}
	if includeDetails && got.Err != nil {
		attempt.Error = got.Err.Error()
	}
	for i, test := range got.Tests {
		assertion := Assertion{Name: fmt.Sprintf("Test %d", i+1), Pass: test.Pass, Skipped: test.Skipped}
		if includeDetails {
			assertion.Name, assertion.Error = test.Name, test.Error
		}
		attempt.Tests = append(attempt.Tests, assertion)
	}
	r.Results = append(r.Results, attempt)
}

// reportOutcome keeps Execution's categories without accepting an arbitrary
// Stage string as report text. Stages set by Execute are fixed, safe labels.
func reportOutcome(got request.Execution) string {
	if got.Skipped || got.Err == nil || errors.Is(got.Err, context.Canceled) || errors.Is(got.Err, context.DeadlineExceeded) {
		return got.Outcome()
	}
	switch got.Stage {
	case "Request setup failed", "Pre-request script failed", "Access token failed",
		"Network error", "Post-response script failed", "Digest authentication failed":
		return got.Stage
	default:
		return "Execution failed"
	}
}

// Finish records the engine summary and total wall time. A deliberate
// setNextRequest stop is not an interruption; an error (including a caller's
// execution budget) is. Raw run errors are never included, even with details.
func (r *Report) Finish(summary Summary, err error, duration time.Duration) {
	r.Version = 1
	r.DurationMillis = duration.Milliseconds()
	r.Completed, r.Failed, r.Skipped = summary.Completed, summary.Failed, summary.Skipped
	r.Stopped, r.Incomplete, r.Problem = summary.Stopped, err != nil, ""
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			r.Problem = "cancelled"
		case errors.Is(err, context.DeadlineExceeded):
			r.Problem = "timed out"
		default:
			r.Problem = "run interrupted"
		}
	}
}

// JSON encodes the versioned report, including an empty results array for an
// empty run. Calling it does not mutate the report.
func (r Report) JSON() ([]byte, error) {
	r.Version = 1
	return json.Marshal(r)
}

type junitSuite struct {
	XMLName   xml.Name    `xml:"testsuite"`
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr,omitempty"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr,omitempty"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	Skipped   *junitProblem `xml:"skipped,omitempty"`
	Tests     string        `xml:"system-out,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Detail  string `xml:",chardata"`
}

// JUnit emits one testcase per attempt. Assertions are safe text in system-out,
// not separate testcases; an assertion skip does not skip the whole request.
// An interrupted run adds an error testcase, even if no request ever started.
func (r Report) JUnit() ([]byte, error) {
	suite := junitSuite{Name: "collection runner", Time: reportSeconds(r.DurationMillis)}
	if !r.StartedAt.IsZero() {
		suite.Timestamp = r.StartedAt.Format(time.RFC3339Nano)
	}
	for _, attempt := range r.Results {
		testcase := junitCase{
			Name:      fmt.Sprintf("%s (iteration %d)", cmp.Or(attempt.Name, attempt.ID, "Request"), attempt.Iteration),
			Classname: attempt.Collection, Time: reportSeconds(attempt.DurationMillis),
		}
		if attempt.Failed {
			suite.Failures++
			testcase.Failure = &junitProblem{Message: attempt.Outcome, Detail: attempt.Error}
		} else if attempt.Skipped {
			suite.Skipped++
			testcase.Skipped = &junitProblem{Message: attempt.Outcome}
		}
		var tests []string
		for _, test := range attempt.Tests {
			state := "passed"
			if test.Skipped {
				state = "skipped"
			} else if !test.Pass {
				state = "failed"
			}
			line := test.Name + ": " + state
			if test.Error != "" {
				line += ": " + test.Error
			}
			tests = append(tests, line)
		}
		testcase.Tests = strings.Join(tests, "\n")
		suite.Cases = append(suite.Cases, testcase)
	}
	if r.Incomplete {
		suite.Errors++
		suite.Cases = append(suite.Cases, junitCase{
			Name: "Run interrupted", Time: "0.000",
			Error: &junitProblem{Message: cmp.Or(r.Problem, "run interrupted")},
		})
	}
	suite.Tests = len(suite.Cases)
	return xml.Marshal(suite)
}

func reportSeconds(millis int64) string {
	return strconv.FormatFloat(float64(millis)/1000, 'f', 3, 64)
}
