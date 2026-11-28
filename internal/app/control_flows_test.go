package app

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/control"
)

func decodeFlow(t *testing.T, value jsontext.Value, target any) {
	t.Helper()
	if err := json.Unmarshal(value, target); err != nil {
		t.Fatalf("decode %s: %v", value, err)
	}
}

func controlFailure(t *testing.T, value jsontext.Value) (code, field, hint string) {
	t.Helper()
	var failure struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Field string `json:"field"`
		Hint  string `json:"hint"`
	}
	decodeFlow(t, value, &failure)
	if failure.Error == "" {
		t.Fatalf("expected an error, got %s", value)
	}
	return failure.Code, failure.Field, failure.Hint
}

func TestFlowsGetTextBodyMode(t *testing.T) {
	jsonRequest := `{"hello":"world"}`
	jsonResponse := `{"error":"missing","retryable":true}`
	spill := `{"spilled":"text","n":42}`
	spillPath := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(spillPath, []byte(spill), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := "\xff\x00\x01"
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	a := &app{flows: []flow{
		{
			id: 1, live: true, method: "GET", host: "api.example.com", path: "/items", at: at,
			url:     "https://user:password@api.example.com/items?token=query-secret#fragment",
			process: "Browser",
			reqHeader: http.Header{
				"Content-Type":  {"application/json"},
				"Accept":        {"application/json"},
				"Authorization": {"Bearer header-secret"},
				"Cookie":        {"session=cookie-secret"},
			},
			reqBody: jsonRequest, reqSize: len(jsonRequest),
			respHeader: http.Header{
				"Content-Type":     {"application/json; charset=utf-8"},
				"Content-Encoding": {"gzip"}, // capture stores the decoded body, not the wire bytes
				"Set-Cookie":       {"session=cookie-secret"},
			},
			resBody: jsonResponse, resSize: len(jsonResponse),
		},
		{
			id: 2, live: true, method: "GET", path: "/blob", at: at.Add(time.Minute),
			url: "https://api.example.com/blob", respHeader: http.Header{"Content-Type": {"application/octet-stream"}},
			resBody: binary, resSize: len(binary),
		},
		{
			id: 3, live: true, method: "GET", path: "/spill", at: at.Add(2 * time.Minute),
			url: "https://api.example.com/spill", respHeader: http.Header{"Content-Type": {"text/plain"}},
			resBody: "preview", resFile: spillPath, resSize: len(spill),
		},
	}}
	client := desktopClient(t, a)

	var text struct {
		URL                 string              `json:"url"`
		RequestContentType  string              `json:"requestContentType"`
		ResponseContentType string              `json:"responseContentType"`
		RequestHeaders      map[string][]string `json:"requestHeaders"`
		ResponseHeaders     map[string][]string `json:"responseHeaders"`
		RequestBody         string              `json:"requestBody"`
		RequestBodyEncoding string              `json:"requestBodyEncoding"`
		RequestBodyBytes    int                 `json:"requestBodyBytes"`
		RequestBodyTrunc    bool                `json:"requestBodyTruncated"`
		ResponseBody        string              `json:"responseBody"`
		ResponseEncoding    string              `json:"responseBodyEncoding"`
		ResponseBytes       int                 `json:"responseBodyBytes"`
		ResponseTrunc       bool                `json:"responseBodyTruncated"`
		ResponseIncomplete  bool                `json:"responseBodyIncomplete"`
	}
	decodeFlow(t, desktopCall(t, client, "flows.get", map[string]any{"id": 1, "body": "text"}), &text)
	if text.RequestContentType != "application/json" || text.ResponseContentType != "application/json; charset=utf-8" {
		t.Fatalf("content types: request %q response %q", text.RequestContentType, text.ResponseContentType)
	}
	if text.RequestBody != jsonRequest || text.RequestBodyEncoding != "text" || text.RequestBodyBytes != len(jsonRequest) || text.RequestBodyTrunc {
		t.Fatalf("request text body: %+v", text)
	}
	if text.ResponseBody != jsonResponse || text.ResponseEncoding != "text" || text.ResponseBytes != len(jsonResponse) || text.ResponseTrunc || text.ResponseIncomplete {
		t.Fatalf("response text body: %+v", text)
	}
	if got := text.RequestHeaders["Authorization"]; !slices.Equal(got, []string{exportReplacement}) {
		t.Fatalf("Authorization = %v", got)
	}
	if got := text.RequestHeaders["Cookie"]; !slices.Equal(got, []string{exportReplacement}) {
		t.Fatalf("Cookie = %v", got)
	}
	if got := text.ResponseHeaders["Set-Cookie"]; !slices.Equal(got, []string{exportReplacement}) {
		t.Fatalf("Set-Cookie = %v", got)
	}
	if !slices.Equal(text.RequestHeaders["Content-Type"], []string{"application/json"}) ||
		!slices.Equal(text.RequestHeaders["Accept"], []string{"application/json"}) {
		t.Fatalf("public headers were redacted: %v", text.RequestHeaders)
	}
	if strings.Contains(string(flowJSON(t, text)), "header-secret") || strings.Contains(string(flowJSON(t, text)), "cookie-secret") ||
		strings.Contains(string(flowJSON(t, text)), "query-secret") || strings.Contains(string(flowJSON(t, text)), "password") {
		t.Fatalf("text mode leaked a header, cookie, query or userinfo secret: %s", flowJSON(t, text))
	}
	if !strings.Contains(text.URL, "token=REDACTED") || strings.Contains(text.URL, "#") {
		t.Fatalf("redacted URL = %q", text.URL)
	}

	var truncated struct {
		RequestBody      string `json:"requestBody"`
		RequestBodyBytes int    `json:"requestBodyBytes"`
		RequestTrunc     bool   `json:"requestBodyTruncated"`
		ResponseBody     string `json:"responseBody"`
		ResponseBytes    int    `json:"responseBodyBytes"`
		ResponseTrunc    bool   `json:"responseBodyTruncated"`
	}
	decodeFlow(t, desktopCall(t, client, "flows.get", map[string]any{"id": 1, "body": "text", "maxBytes": 8}), &truncated)
	if truncated.RequestBody != jsonRequest[:8]+"\n…Truncated" || !truncated.RequestTrunc || truncated.RequestBodyBytes != len(jsonRequest) {
		t.Fatalf("request truncation: %+v", truncated)
	}
	if truncated.ResponseBody != jsonResponse[:8]+"\n…Truncated" || !truncated.ResponseTrunc || truncated.ResponseBytes != len(jsonResponse) {
		t.Fatalf("response truncation: %+v", truncated)
	}

	var binaryBody struct {
		ResponseBody     string `json:"responseBody"`
		ResponseEncoding string `json:"responseBodyEncoding"`
		ResponseBytes    int    `json:"responseBodyBytes"`
		ResponseTrunc    bool   `json:"responseBodyTruncated"`
	}
	decodeFlow(t, desktopCall(t, client, "flows.get", map[string]any{"id": 2, "body": "text"}), &binaryBody)
	decoded, err := base64.StdEncoding.DecodeString(binaryBody.ResponseBody)
	if err != nil || string(decoded) != binary || binaryBody.ResponseEncoding != "base64" || binaryBody.ResponseBytes != len(binary) || binaryBody.ResponseTrunc {
		t.Fatalf("binary body: %+v, decoded %q, %v", binaryBody, decoded, err)
	}
	decodeFlow(t, desktopCall(t, client, "flows.get", map[string]any{"id": 2, "body": "text", "maxBytes": 1}), &binaryBody)
	decoded, _ = base64.StdEncoding.DecodeString(binaryBody.ResponseBody)
	if string(decoded) != binary[:1] || !binaryBody.ResponseTrunc {
		t.Fatalf("truncated binary body: %+v, decoded %q", binaryBody, decoded)
	}

	var spilled struct {
		ResponseBody      string `json:"responseBody"`
		ResponseEncoding  string `json:"responseBodyEncoding"`
		ResponseBytes     int    `json:"responseBodyBytes"`
		ResponseTrunc     bool   `json:"responseBodyTruncated"`
		ResponseIncomplet bool   `json:"responseBodyIncomplete"`
	}
	decodeFlow(t, desktopCall(t, client, "flows.get", map[string]any{"id": 3, "body": "text", "maxBytes": 5}), &spilled)
	if spilled.ResponseBody != spill[:5]+"\n…Truncated" || spilled.ResponseEncoding != "text" ||
		spilled.ResponseBytes != len(spill) || !spilled.ResponseTrunc || spilled.ResponseIncomplet {
		t.Fatalf("spill body: %+v", spilled)
	}

	// Raw details stay available, but combining them with text mode is
	// ambiguous and is rejected before any body is read.
	var raw struct {
		RequestBodyBase64 string `json:"requestBodyBase64"`
	}
	decodeFlow(t, desktopCall(t, client, "flows.get", map[string]any{"id": 1, "details": true}), &raw)
	if raw.RequestBodyBase64 != base64.StdEncoding.EncodeToString([]byte(jsonRequest)) {
		t.Fatalf("raw request body = %q", raw.RequestBodyBase64)
	}
	for _, tc := range []struct {
		params map[string]any
		field  string
	}{
		{map[string]any{"id": 1, "details": true, "body": "text"}, "details, body"},
		{map[string]any{"id": 1, "body": "text", "maxBytes": 65537}, "maxBytes"},
		{map[string]any{"id": 1, "body": "base64"}, "body"},
	} {
		code, field, _ := controlFailure(t, desktopCall(t, client, "flows.get", tc.params))
		if code != "invalid-flow-parameter" || field != tc.field {
			t.Fatalf("%v: code=%q field=%q", tc.params, code, field)
		}
	}
	code, _, hint := controlFailure(t, desktopCall(t, client, "flows.get", map[string]any{"id": 99, "body": "text"}))
	if code != "flow-not-found" || !strings.Contains(hint, "flows.list") {
		t.Fatalf("missing flow: code=%q hint=%q", code, hint)
	}
}

func flowJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFlowsListAfterCursorExactLimitAndFilters(t *testing.T) {
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	a := &app{flows: []flow{
		{id: 1, live: true, method: "GET", host: "api.example.com", path: "/v1/items", process: "Browser", at: at},
		{id: 3, live: true, method: "POST", host: "api.example.com", path: "/v1/items/3", process: "Browser", at: at.Add(time.Minute)},
		{id: 5, live: true, method: "GET", host: "other.test", path: "/v1/other", process: "curl", at: at.Add(2 * time.Minute)},
		{id: 7, live: true, method: "GET", host: "api.example.com", path: "/v1/items/7", process: "Browser", at: at.Add(3 * time.Minute)},
		{id: 9, live: false, method: "GET", host: "api.example.com", path: "/ignored", at: at.Add(4 * time.Minute)},
	}}
	client := desktopClient(t, a)
	type page struct {
		Items []struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		} `json:"items"`
		NextBefore *int `json:"nextBefore"`
		NextAfter  *int `json:"nextAfter"`
	}
	ids := func(value jsontext.Value) (page, []int) {
		var p page
		decodeFlow(t, value, &p)
		var got []int
		for _, item := range p.Items {
			got = append(got, item.ID)
		}
		return p, got
	}

	// Newest first by default.
	if _, got := ids(desktopCall(t, client, "flows.list", nil)); !slices.Equal(got, []int{7, 5, 3, 1}) {
		t.Fatalf("default order = %v", got)
	}
	// after is exclusive and ascending.
	p, got := ids(desktopCall(t, client, "flows.list", map[string]any{"after": 3}))
	if !slices.Equal(got, []int{5, 7}) || p.NextAfter == nil || *p.NextAfter != 0 {
		t.Fatalf("after 3 = %v, nextAfter %v", got, p.NextAfter)
	}
	// One page of one row gets a nextAfter; the next page is not empty.
	p, got = ids(desktopCall(t, client, "flows.list", map[string]any{"after": 3, "limit": 1}))
	if !slices.Equal(got, []int{5}) || p.NextAfter == nil || *p.NextAfter != 5 {
		t.Fatalf("after 3 limit 1 = %v, nextAfter %v", got, p.NextAfter)
	}
	p, got = ids(desktopCall(t, client, "flows.list", map[string]any{"after": 5, "limit": 1}))
	if !slices.Equal(got, []int{7}) || p.NextAfter == nil || *p.NextAfter != 0 {
		t.Fatalf("after 5 = %v, nextAfter %v", got, p.NextAfter)
	}
	// Exactly limit matching rows is the last page: the old cursor returned
	// a non-zero nextBefore and the next call was empty.
	p, got = ids(desktopCall(t, client, "flows.list", map[string]any{"before": 7, "limit": 3}))
	if !slices.Equal(got, []int{5, 3, 1}) || p.NextBefore == nil || *p.NextBefore != 0 {
		t.Fatalf("exact-limit page = %v, nextBefore %v", got, p.NextBefore)
	}
	p, got = ids(desktopCall(t, client, "flows.list", map[string]any{"before": 7, "limit": 2}))
	if !slices.Equal(got, []int{5, 3}) || p.NextBefore == nil || *p.NextBefore != 3 {
		t.Fatalf("before page = %v, nextBefore %v", got, p.NextBefore)
	}
	_, got = ids(desktopCall(t, client, "flows.list", map[string]any{"before": 3}))
	if !slices.Equal(got, []int{1}) {
		t.Fatalf("second before page = %v", got)
	}
	// Filters: app is an exact label, path is a substring, since is inclusive.
	if _, got = ids(desktopCall(t, client, "flows.list", map[string]any{"app": "browser"})); !slices.Equal(got, []int{7, 3, 1}) {
		t.Fatalf("app filter = %v", got)
	}
	if _, got = ids(desktopCall(t, client, "flows.list", map[string]any{"path": "items/7"})); !slices.Equal(got, []int{7}) {
		t.Fatalf("path filter = %v", got)
	}
	if _, got = ids(desktopCall(t, client, "flows.list", map[string]any{"since": at.Add(2 * time.Minute).Format(time.RFC3339Nano)})); !slices.Equal(got, []int{7, 5}) {
		t.Fatalf("since filter = %v", got)
	}
	// Cursors are mutually exclusive, and each validation names its field.
	for _, tc := range []struct {
		params map[string]any
		field  string
	}{
		{map[string]any{"before": 1, "after": 7}, "before, after"},
		{map[string]any{"limit": 201}, "limit"},
		{map[string]any{"status": 42}, "status"},
		{map[string]any{"since": "yesterday"}, "since"},
		{map[string]any{"until": "later"}, "until"},
		{map[string]any{"maxBytes": 10}, "body, maxBytes"},
	} {
		code, field, hint := controlFailure(t, desktopCall(t, client, "flows.list", tc.params))
		if code != "invalid-flow-parameter" || field != tc.field || hint == "" {
			t.Fatalf("%v: code=%q field=%q hint=%q", tc.params, code, field, hint)
		}
	}
}

func TestStatusReportsHelperPaths(t *testing.T) {
	dir := t.TempDir()
	cliName, mcpName := helperExecutableName("vscroxy-cli", runtime.GOOS), helperExecutableName("vscroxy-mcp", runtime.GOOS)
	for _, name := range []string{cliName, mcpName} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := &app{}
	client := desktopClient(t, a)
	var status struct {
		CLIPath string `json:"cliPath"`
		MCPPath string `json:"mcpPath"`
	}
	decodeFlow(t, desktopCall(t, client, "status", nil), &status)
	if status.CLIPath != filepath.Join(dir, cliName) || status.MCPPath != filepath.Join(dir, mcpName) {
		t.Fatalf("helper paths: cli %q mcp %q", status.CLIPath, status.MCPPath)
	}
}

func TestControlActionCatalogCoversDispatch(t *testing.T) {
	a := &app{}
	for _, action := range control.Actions() {
		if action.Name == "system-proxy.get" {
			// A real read-only OS check; not part of the dispatch coverage test.
			continue
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("%s panicked with empty parameters: %v", action.Name, recovered)
				}
			}()
			_, err := a.controlValue(control.Command{Action: action.Name, Params: jsontext.Value(`{}`)})
			if err != nil && (err.Error() == "unknown action" || strings.HasPrefix(err.Error(), "unknown ")) {
				t.Errorf("%s is in the help catalog but not dispatched: %v", action.Name, err)
			}
		}()
	}
	if _, err := a.controlValue(control.Command{Action: "not.an.action", Params: jsontext.Value(`{}`)}); err == nil || err.Error() != "unknown action" {
		t.Fatalf("unknown action = %v", err)
	}
}
