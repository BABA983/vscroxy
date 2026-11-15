package runner

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
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
	// Contract is set when the run gated sent exchanges against an OpenAPI
	// contract. It holds counts and drifted endpoints, never raw traffic.
	Contract *ContractReport `json:"contract,omitempty"`
}

// ContractReport is an OpenAPI drift gate's outcome for one run.
type ContractReport struct {
	Spec      string   `json:"spec"`
	Checked   int      `json:"checked"`
	Matched   int      `json:"matched"`
	Failed    int      `json:"failed"`
	Endpoints []string `json:"endpoints,omitempty"`
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

// htmlAttempt is one report row with its assertion tallies.
type htmlAttempt struct {
	Attempt
	TestsPassed  int
	TestsFailed  int
	TestsSkipped int
}

// htmlReport is the page's data; the template escapes every value, so a
// request name or assertion error can never inject markup.
type htmlReport struct {
	StartedAt      time.Time
	DurationMillis int64
	Completed      int
	Failed         int
	Skipped        int
	Stopped        bool
	Incomplete     bool
	Problem        string
	Attempts       []htmlAttempt
}

// HTML renders a self-contained report: no scripts, fonts or stylesheets are
// fetched, so it opens from an artifact directory or a CI job's archive. Like
// JSON and JUnit it contains only what Append retained; pass includeDetails to
// Append when the report should carry assertion names and errors.
func (r Report) HTML() ([]byte, error) {
	view := htmlReport{
		StartedAt: r.StartedAt, DurationMillis: r.DurationMillis,
		Completed: r.Completed, Failed: r.Failed, Skipped: r.Skipped,
		Stopped: r.Stopped, Incomplete: r.Incomplete, Problem: r.Problem,
		Attempts: make([]htmlAttempt, 0, len(r.Results)),
	}
	for _, attempt := range r.Results {
		row := htmlAttempt{Attempt: attempt}
		for _, test := range attempt.Tests {
			switch {
			case test.Skipped:
				row.TestsSkipped++
			case test.Pass:
				row.TestsPassed++
			default:
				row.TestsFailed++
			}
		}
		view.Attempts = append(view.Attempts, row)
	}
	var out bytes.Buffer
	if err := reportHTML.Execute(&out, view); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

var reportHTML = template.Must(template.New("report").Funcs(template.FuncMap{
	"seconds": func(millis int64) string { return reportSeconds(millis) },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Collection run report</title>
<style>
:root{color-scheme:light dark;--bg:#fbfbfc;--panel:#fff;--text:#18181b;--muted:#666;--line:#e4e4e7;--pass:#12805c;--fail:#c02525;--skip:#8a6d1a}
@media (prefers-color-scheme:dark){:root{--bg:#111113;--panel:#18181b;--text:#ececee;--muted:#a1a1aa;--line:#2e2e33;--pass:#4ade80;--fail:#f87171;--skip:#facc15}}
*{box-sizing:border-box}
body{margin:0;padding:32px;background:var(--bg);color:var(--text);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:1040px;margin:0 auto}
h1{font-size:22px;margin:0 0 4px}
p.meta{color:var(--muted);margin:0 0 24px}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:12px;margin-bottom:24px}
.card{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:14px 16px}
.card b{display:block;font-size:22px;font-variant-numeric:tabular-nums}
.card span{color:var(--muted);font-size:12px}
.banner{border:1px solid var(--line);border-left:4px solid var(--fail);background:var(--panel);border-radius:8px;padding:10px 14px;margin-bottom:20px}
table{width:100%;border-collapse:collapse;background:var(--panel);border:1px solid var(--line);border-radius:10px;overflow:hidden}
th,td{text-align:left;padding:9px 12px;border-bottom:1px solid var(--line);vertical-align:top}
th{font-size:12px;color:var(--muted);font-weight:600}
tr:last-child td{border-bottom:0}
td.num{font-variant-numeric:tabular-nums;white-space:nowrap}
.pass{color:var(--pass)}.fail{color:var(--fail)}.skip{color:var(--skip)}
code{font:12px ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;word-break:break-word}
footer{color:var(--muted);font-size:12px;margin-top:20px}
</style>
</head>
<body>
<main>
<h1>Collection run report</h1>
<p class="meta">{{if not .StartedAt.IsZero}}Started {{.StartedAt.UTC.Format "2006-01-02 15:04:05 UTC"}} · {{end}}{{seconds .DurationMillis}}s</p>
<div class="cards">
<div class="card"><b>{{.Completed}}</b><span>completed</span></div>
<div class="card"><b class="{{if .Failed}}fail{{else}}pass{{end}}">{{.Failed}}</b><span>failed</span></div>
<div class="card"><b>{{.Skipped}}</b><span>skipped</span></div>
</div>
{{if .Incomplete}}<div class="banner"><strong>Run incomplete.</strong> {{.Problem}}</div>{{end}}
{{if .Stopped}}<div class="banner" style="border-left-color:var(--skip)">A script stopped the run with <code>pm.execution.setNextRequest(null)</code>.</div>{{end}}
<table>
<thead><tr><th>Iteration</th><th>Request</th><th>Collection</th><th>Status</th><th>Time</th><th>Outcome</th><th>Assertions</th></tr></thead>
<tbody>
{{range .Attempts}}<tr>
<td class="num">{{.Iteration}}</td>
<td>{{.Name}}{{if .Error}}<br><code>{{.Error}}</code>{{end}}</td>
<td>{{.Collection}}</td>
<td class="num">{{if .Status}}{{.Status}}{{else}}—{{end}}</td>
<td class="num">{{seconds .DurationMillis}}s</td>
<td class="{{if .Failed}}fail{{else if .Skipped}}skip{{else}}pass{{end}}">{{.Outcome}}</td>
<td>{{if .Tests}}<span class="pass">{{.TestsPassed}} passed</span>{{if .TestsFailed}} · <span class="fail">{{.TestsFailed}} failed</span>{{end}}{{if .TestsSkipped}} · <span class="skip">{{.TestsSkipped}} skipped</span>{{end}}{{range .Tests}}<br><code>{{.Name}}{{if .Error}}: {{.Error}}{{end}}</code>{{end}}{{else}}—{{end}}</td>
</tr>{{end}}
</tbody>
</table>
<footer>Generated by vscroxy. Variables, headers and bodies are never included. Assertion names are ordinal unless the run opted in with --include-details.</footer>
</main>
</body>
</html>
`))
