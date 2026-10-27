package app

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"golang.org/x/net/http/httpguts"
)

// Checks stay declarative: sharing a case never imports executable scripts.
// JSON checks address scalar fields with RFC 6901 pointers, not body snapshots.
type responseChecks struct {
	Status  string `json:"status,omitempty"`
	Headers string `json:"headers,omitempty"`
	JSON    string `json:"json,omitempty"`
}

type jsonCheck struct {
	pointer jsontext.Pointer
	want    jsontext.Value
}

func (r responseChecks) parse() (int, http.Header, []jsonCheck, error) {
	status := 0
	if r.Status != "" {
		var err error
		status, err = strconv.Atoi(r.Status)
		if err != nil || status < 100 || status > 599 {
			return 0, nil, nil, errors.New("Expected status must be an HTTP code from 100 to 599, or empty.")
		}
	}
	headers := http.Header{}
	for i, line := range strings.Split(r.Headers, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return 0, nil, nil, fmt.Errorf("Header check line %d must be a valid Name: Value pair.", i+1)
		}
		headers.Add(name, value)
	}
	var checks []jsonCheck
	for i, line := range strings.Split(r.JSON, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		path, value, ok := strings.Cut(line, " = ")
		pointer, want := jsontext.Pointer(strings.TrimSpace(path)), jsontext.Value(strings.TrimSpace(value))
		if !ok || !pointer.IsValid() || !want.IsValid() || want.Kind() == '{' || want.Kind() == '[' {
			return 0, nil, nil, fmt.Errorf("JSON check line %d: use /field/path = a JSON string, number, boolean or null.", i+1)
		}
		checks = append(checks, jsonCheck{pointer, want})
	}
	return status, headers, checks, nil
}

func (r responseChecks) evaluate(f capture.Flow) []testResult {
	status, headers, checks, _ := r.parse() // Validated before sending.
	var results []testResult
	if status != 0 {
		results = append(results, testResult{Name: fmt.Sprintf("Status equals %d (received %d)", status, f.Status), Pass: status == f.Status})
	}
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		for _, want := range headers[name] {
			// Any repeated header may match; missing is not equal to empty.
			results = append(results, testResult{Name: "Header " + name + " equals expected value", Pass: slices.Contains(f.ResponseHeader.Values(name), want)})
		}
	}
	for _, check := range checks {
		got, ok := jsonField(jsontext.Value(f.ResponseBody), check.pointer)
		complete := f.ResponseBodySize <= len(f.ResponseBody) && f.ResponseBodyError == ""
		results = append(results, testResult{Name: "JSON " + string(check.pointer) + " equals expected value", Pass: complete && ok && equalJSONScalar(got, check.want)})
	}
	return results
}

func jsonField(raw jsontext.Value, pointer jsontext.Pointer) (jsontext.Value, bool) {
	if !raw.IsValid() {
		return nil, false
	}
	for token := range pointer.Tokens() {
		switch raw.Kind() {
		case '{':
			var object map[string]jsontext.Value
			if json.Unmarshal(raw, &object) != nil {
				return nil, false
			}
			var ok bool
			raw, ok = object[token]
			if !ok {
				return nil, false
			}
		case '[':
			var array []jsontext.Value
			i, err := strconv.Atoi(token)
			if err != nil || strconv.Itoa(i) != token || json.Unmarshal(raw, &array) != nil || i < 0 || i >= len(array) {
				return nil, false
			}
			raw = array[i]
		default:
			return nil, false
		}
	}
	return raw, true
}

func equalJSONScalar(a, b jsontext.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	if a.Kind() == '0' {
		// Do not round large IDs to float64, nor confuse 42 with "42".
		for _, raw := range []jsontext.Value{a, b} {
			if len(raw) > 1024 {
				return false
			}
			if _, exp, ok := strings.Cut(strings.ToLower(string(raw)), "e"); ok {
				n, err := strconv.Atoi(exp)
				if err != nil || n < -10000 || n > 10000 {
					return false
				}
			}
		}
		x, ok := new(big.Rat).SetString(string(a))
		y, valid := new(big.Rat).SetString(string(b))
		return ok && valid && x.Cmp(y) == 0
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && x == y
}

// Only names travel with a case, never the active environment's values.
func requestVariables(r savedRequest) []string {
	var names []string
	texts := []string{r.Method, r.URL, r.Headers}
	if r.BodyMode == "" || r.BodyMode == "Raw" {
		texts = append(texts, r.Body)
	} else {
		for _, field := range r.Form {
			if !field.Disabled {
				texts = append(texts, field.Name, field.Value)
			}
		}
	}
	switch r.Auth.Type {
	case "Bearer":
		texts = append(texts, r.Auth.Token)
	case "Basic":
		texts = append(texts, r.Auth.Username, r.Auth.Password)
	case "API key":
		texts = append(texts, r.Auth.Key, r.Auth.Value)
	}
	for _, text := range texts {
		for {
			_, rest, ok := strings.Cut(text, "{{")
			if !ok {
				break
			}
			name, tail, ok := strings.Cut(rest, "}}")
			if !ok {
				break
			}
			names = append(names, strings.TrimSpace(name))
			text = tail
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

type regressionFile struct {
	Format  string       `json:"format"`
	Version int          `json:"version"`
	Request savedRequest `json:"request"`
}

func decodeRegression(data []byte) (savedRequest, error) {
	var file regressionFile
	if len(data) > 1<<20 {
		return savedRequest{}, errors.New("Regression files are limited to 1 MiB.")
	}
	if err := json.Unmarshal(data, &file, json.RejectUnknownMembers(true)); err != nil {
		return savedRequest{}, err
	}
	r := file.Request
	if file.Format != "vscroxy-regression" || file.Version != 1 {
		return r, errors.New("Unsupported regression file format or version.")
	}
	if r.Pre != "" || r.Post != "" || len(r.Form) != 0 || r.BodyMode != "" && r.BodyMode != "Raw" {
		return r, errors.New("Portable regressions cannot contain scripts, forms or local files.")
	}
	if r.Checks == (responseChecks{}) || r.URL == "" || r.Name == "" {
		return r, errors.New("A regression needs a name, URL and at least one response check.")
	}
	status, headers, checks, err := r.Checks.parse()
	if err == nil && status == 0 && len(headers) == 0 && len(checks) == 0 {
		err = errors.New("Add at least one response check.")
	}
	return r, err
}

// Conservative by default: preserve only content negotiation header values.
// Replace all other header/query values and the whole body, including unknown
// credentials. Paths and assertion literals still require human review.
func portableRegression(r savedRequest) (string, []string, error) {
	if r.Checks == (responseChecks{}) {
		return "", nil, errors.New("Add at least one response check before sharing.")
	}
	if r.BodyMode != "" && r.BodyMode != "Raw" || len(r.Form) != 0 {
		return "", nil, errors.New("Portable regressions currently support raw HTTP bodies only; local files are never exported.")
	}
	req, err := prepareRequest(r, nil)
	if err != nil {
		return "", nil, err
	}
	original := req.URL
	if strings.HasPrefix(original, "{{") {
		_, tail, ok := strings.Cut(original, "}}")
		if !ok || tail != "" && !strings.HasPrefix(tail, "/") && !strings.HasPrefix(tail, "?") {
			return "", nil, errors.New("Use an HTTP(S) URL or {{base_url}}/path before sharing.")
		}
		original = "http://variable" + tail
	}
	u, err := url.Parse(original)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		return "", nil, errors.New("Use an HTTP(S) URL or {{base_url}}/path before sharing.")
	}
	changes := []string{"Target origin → {{base_url}}; URL credentials/fragment removed", "Name and collection replaced; environment, cookies, scripts and responses excluded"}
	// Keep the raw path, including %2F and {{path_variables}}. EscapedPath
	// would turn template braces into %7B and make the variables inert.
	_, authority, _ := strings.Cut(original, "://")
	path := ""
	if at := strings.IndexAny(authority, "/?#"); at >= 0 {
		path, _, _ = strings.Cut(authority[at:], "?")
		path, _, _ = strings.Cut(path, "#")
	}
	var query []string
	for i, part := range strings.Split(u.RawQuery, "&") {
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		query = append(query, key+"={{query_"+strconv.Itoa(i+1)+"}}")
	}
	target := "{{base_url}}" + path
	if len(query) > 0 {
		target += "?" + strings.Join(query, "&")
		changes = append(changes, "Every query value → {{query_N}} (supply URL-encoded values)")
	}
	headers := req.header.Clone()
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		if curlSkip[strings.ToLower(name)] || strings.EqualFold(name, "Host") {
			headers.Del(name)
			continue
		}
		if strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Accept") {
			continue
		}
		for i := range headers[name] {
			headers[name][i] = "{{header_" + strconv.Itoa(len(changes)) + "_" + strconv.Itoa(i+1) + "}}"
		}
		changes = append(changes, "Header "+name+" → variable(s)")
	}
	body := ""
	if req.Body != "" {
		body = "{{request_body}}"
		changes = append(changes, "Entire request body → {{request_body}}")
	}
	out := savedRequest{Name: "Shared regression", Collection: "Regressions", Method: req.Method, URL: target, Headers: headerBlock(headers), Body: body, Checks: r.Checks}
	data, err := json.Marshal(regressionFile{Format: "vscroxy-regression", Version: 1, Request: out}, jsontext.WithIndent("  "))
	if err != nil {
		return "", nil, err
	}
	_, err = decodeRegression(data)
	return string(data), changes, err
}

func (a *app) regressionFrom(f flow) {
	r, err := f.requestDraft()
	if err != nil {
		a.setMessage("Save regression: " + err.Error())
		return
	}
	r.Name = uniqueRequestName(a.requests, f.method+" "+hostOnly(f.host)+" regression")
	r.Collection, r.Checks.Status = "Regressions", strconv.Itoa(f.status)
	a.addDocument(r, "")
	a.setDebugSource(newDebugSource(f))
	a.sourceFlow, a.composeTab = f.id, 5
	a.setMessage("Review checks and variables, then Save request; nothing has been sent")
}
