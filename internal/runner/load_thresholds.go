package runner

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LoadThreshold is one pass condition, such as p95<500ms or errors<1%.
type LoadThreshold struct {
	Metric string  `json:"metric"`
	Op     string  `json:"op"`
	Value  float64 `json:"value"`
	// Percent says an errors value is a share of completed attempts rather
	// than a count.
	Percent bool   `json:"percent,omitempty"`
	Text    string `json:"text"`
}

// ThresholdResult is one condition's outcome in the report.
type ThresholdResult struct {
	Metric string  `json:"metric"`
	Op     string  `json:"op"`
	Want   float64 `json:"want"`
	Got    float64 `json:"got"`
	Pass   bool    `json:"pass"`
	Text   string  `json:"text"`
}

var loadLatencyMetrics = map[string]bool{"mean": true, "p50": true, "p90": true, "p95": true, "p99": true, "max": true}
var loadCountMetrics = map[string]bool{"completed": true, "failed": true, "skipped": true, "errors": true}

// ParseThresholds reads a comma-separated list such as
// "p95<500ms,errors<1%,failureRate<2". Latency values take a duration or
// milliseconds; errors and failureRate take a percentage; rps and counts are
// plain numbers.
func ParseThresholds(spec string) ([]LoadThreshold, error) {
	var thresholds []LoadThreshold
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.IndexAny(part, "<>")
		if i <= 0 || i == len(part)-1 {
			return nil, fmt.Errorf("%q must be metric<value or metric>value", part)
		}
		metric := strings.ToLower(strings.TrimSpace(part[:i]))
		if metric == "failurerate" {
			metric = "failureRate"
		}
		op := part[i : i+1]
		value := strings.TrimSpace(part[i+1:])
		if strings.HasPrefix(value, "=") {
			op += "="
			value = strings.TrimSpace(value[1:])
		}
		t := LoadThreshold{Metric: metric, Op: op, Text: part}
		switch {
		case loadLatencyMetrics[metric]:
			if d, err := time.ParseDuration(value); err == nil {
				t.Value = float64(d) / float64(time.Millisecond)
			} else if ms, err := strconv.ParseFloat(value, 64); err == nil {
				t.Value = ms
			} else {
				return nil, fmt.Errorf("%q: latency needs a duration or milliseconds", part)
			}
		case metric == "failureRate":
			t.Percent = true
			t.Value = parsePercent(value)
		case metric == "errors":
			if strings.HasSuffix(value, "%") {
				t.Percent = true
				t.Value = parsePercent(value)
			} else if n, err := strconv.ParseFloat(value, 64); err == nil {
				t.Value = n
			} else {
				return nil, fmt.Errorf("%q: errors needs a count or a percentage", part)
			}
		case metric == "rps":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, fmt.Errorf("%q: rps needs a number", part)
			}
			t.Value = n
		case loadCountMetrics[metric]:
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, fmt.Errorf("%q: %s needs a number", part, metric)
			}
			t.Value = n
		default:
			return nil, fmt.Errorf("%q: unknown metric %q", part, metric)
		}
		if t.Value < 0 {
			return nil, fmt.Errorf("%q: the value must not be negative", part)
		}
		thresholds = append(thresholds, t)
	}
	return thresholds, nil
}

func parsePercent(value string) float64 {
	n, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), "%"), 64)
	return n
}

// EvaluateThresholds reports every condition against the totals.
func EvaluateThresholds(thresholds []LoadThreshold, m LoadMetrics) []ThresholdResult {
	results := make([]ThresholdResult, 0, len(thresholds))
	for _, t := range thresholds {
		got := thresholdValue(t, m)
		pass := false
		switch t.Op {
		case "<":
			pass = got < t.Value
		case "<=":
			pass = got <= t.Value
		case ">":
			pass = got > t.Value
		case ">=":
			pass = got >= t.Value
		}
		results = append(results, ThresholdResult{Metric: t.Metric, Op: t.Op, Want: t.Value, Got: got, Pass: pass, Text: t.Text})
	}
	return results
}

func thresholdValue(t LoadThreshold, m LoadMetrics) float64 {
	switch t.Metric {
	case "mean":
		return m.MeanMillis
	case "p50":
		return m.P50Millis
	case "p90":
		return m.P90Millis
	case "p95":
		return m.P95Millis
	case "p99":
		return m.P99Millis
	case "max":
		return m.MaxMillis
	case "failureRate":
		return m.FailureRate * 100
	case "errors":
		if t.Percent {
			if m.Completed == 0 {
				return 0
			}
			return float64(m.Errors) / float64(m.Completed) * 100
		}
		return float64(m.Errors)
	case "rps":
		return m.RequestsPerSec
	case "completed":
		return float64(m.Completed)
	case "failed":
		return float64(m.Failed)
	case "skipped":
		return float64(m.Skipped)
	}
	return 0
}

// LoadDelta is one metric before and after, with the percentage change. A
// missing baseline is reported as zero change rather than an infinite one.
type LoadDelta struct {
	Before        float64 `json:"before"`
	After         float64 `json:"after"`
	ChangePercent float64 `json:"changePercent"`
}

// LoadComparison is a new report's totals and per-request p95/failure rate
// against a previous JSON report.
type LoadComparison struct {
	PreviousStartedAt time.Time            `json:"previousStartedAt"`
	Total             LoadDelta            `json:"total"`
	Requests          []LoadRequestCompare `json:"requests"`
}

type LoadRequestCompare struct {
	ID             string    `json:"id"`
	P95Millis      LoadDelta `json:"p95Millis"`
	FailureRate    LoadDelta `json:"failureRate"`
	RequestsPerSec LoadDelta `json:"requestsPerSecond"`
}

func loadDelta(before, after float64) LoadDelta {
	d := LoadDelta{Before: before, After: after}
	if before != 0 {
		d.ChangePercent = (after - before) / before * 100
	}
	return d
}

// CompareLoad compares after against before, matching requests by saved ID
// and then by index. It is informational; thresholds decide pass/fail.
func CompareLoad(before, after LoadReport) LoadComparison {
	comparison := LoadComparison{PreviousStartedAt: before.StartedAt}
	comparison.Total = loadDelta(before.Total.P95Millis, after.Total.P95Millis)
	for i, request := range after.Requests {
		var old *LoadRequest
		for j := range before.Requests {
			if before.Requests[j].ID != "" && before.Requests[j].ID == request.ID {
				old = &before.Requests[j]
				break
			}
		}
		if old == nil && i < len(before.Requests) {
			old = &before.Requests[i]
		}
		if old == nil {
			continue
		}
		comparison.Requests = append(comparison.Requests, LoadRequestCompare{
			ID:             request.ID,
			P95Millis:      loadDelta(old.Stats.P95Millis, request.Stats.P95Millis),
			FailureRate:    loadDelta(old.Stats.FailureRate, request.Stats.FailureRate),
			RequestsPerSec: loadDelta(old.Stats.RequestsPerSec, request.Stats.RequestsPerSec),
		})
	}
	return comparison
}
