package app

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

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
	if r.Checks == (request.Checks{}) || r.URL == "" || r.Name == "" {
		return r, errors.New("A regression needs a name, URL and at least one response check.")
	}
	status, headers, checks, err := r.Checks.Parse()
	if err == nil && status == 0 && len(headers) == 0 && len(checks) == 0 {
		err = errors.New("Add at least one response check.")
	}
	return r, err
}

// Conservative by default: preserve only content negotiation header values.
// Replace all other header/query values and the whole body, including unknown
// credentials. Paths and assertion literals still require human review.
func portableRegression(r savedRequest) (string, []string, error) {
	if r.Checks == (request.Checks{}) {
		return "", nil, errors.New("Add at least one response check before sharing.")
	}
	if r.BodyMode != "" && r.BodyMode != "Raw" || len(r.Form) != 0 {
		return "", nil, errors.New("Portable regressions currently support raw HTTP bodies only; local files are never exported.")
	}
	req, err := r.Prepare(nil)
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
	headers := req.Header.Clone()
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
	out := savedRequest{Name: "Shared regression", Collection: "Regressions", Method: req.Method, URL: target, Headers: request.HeaderBlock(headers), Body: body, Checks: r.Checks}
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
	r.Source = newDebugSource(f)
	r.Name = uniqueRequestName(a.requests, f.method+" "+hostOnly(f.host)+" regression")
	r.Collection, r.Checks.Status = "Regressions", strconv.Itoa(f.status)
	a.addDocument(r, "")
	a.sourceFlow, a.composeTab = f.id, 5
	a.setMessage("Review checks and variables, then Save request; nothing has been sent")
}
