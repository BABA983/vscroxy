package httpfile

import (
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestParseReadsRequestsVariablesAndAttributes(t *testing.T) {
	source := "\ufeff@baseUrl = https://api.example.com\r\n@token = abc\r\n\r\n# A comment\r\n### List items\r\nGET {{baseUrl}}/items?limit=2\r\nAccept: application/json\r\nX-Trace: one\r\nX-Trace: two\r\n\r\n### Create\r\n# @name create\r\n# @timeout 5000\r\n# @no-redirect\r\nPOST {{baseUrl}}/items\r\nContent-Type: application/json\r\n\r\n{\r\n  \"name\": \"Demo\",\r\n  \"tags\": []\r\n}\r\n"
	doc, err := Parse("Shop", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "Shop" || len(doc.Requests) != 2 {
		t.Fatalf("document = %+v", doc)
	}
	if !slices.EqualFunc(doc.Variables, []request.Variable{{Name: "baseUrl", Initial: "https://api.example.com"}, {Name: "token", Initial: "abc"}},
		func(a, b request.Variable) bool { return a.Name == b.Name && a.Initial == b.Initial }) {
		t.Fatalf("variables = %+v", doc.Variables)
	}
	list := doc.Requests[0]
	if list.Name != "List items" || list.Method != "GET" || list.URL != "{{baseUrl}}/items?limit=2" {
		t.Fatalf("list = %+v", list)
	}
	if !strings.Contains(list.Headers, "Accept: application/json") || !strings.Contains(list.Headers, "X-Trace: one\nX-Trace: two") {
		t.Fatalf("headers = %q", list.Headers)
	}
	create := doc.Requests[1]
	if create.Name != "create" || create.Method != "POST" || create.Body != "{\n  \"name\": \"Demo\",\n  \"tags\": []\n}" {
		t.Fatalf("create = %+v", create)
	}
	if create.Settings.TimeoutSeconds == nil || *create.Settings.TimeoutSeconds != 5 || create.Settings.FollowRedirects == nil || *create.Settings.FollowRedirects {
		t.Fatalf("settings = %+v", create.Settings)
	}
}

func TestParseReportsUnsupportedParts(t *testing.T) {
	source := `### Handler
GET https://api.example.com/one

> {% client.test("ok", () => {}); %}

### Socket
WEBSOCKET wss://api.example.com/socket
Content-Type: application/json

{"subscribe":"prices"}

### File
POST https://api.example.com/upload
Content-Type: application/json

< ./body.json
`
	doc, err := Parse("File", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Requests) != 2 {
		t.Fatalf("requests = %+v", doc.Requests)
	}
	if doc.Requests[0].Body != "" || doc.Requests[1].Body != "" {
		t.Fatalf("bodies = %q, %q", doc.Requests[0].Body, doc.Requests[1].Body)
	}
	want := []string{"response handlers are not imported", "WEBSOCKET requests are not imported", "a file body is not imported"}
	for _, part := range want {
		if !slices.ContainsFunc(doc.Warnings, func(line string) bool { return strings.Contains(line, part) }) {
			t.Fatalf("warnings = %q, want %q", doc.Warnings, part)
		}
	}
}

func TestParseBareURLAndSeparatorNames(t *testing.T) {
	doc, err := Parse("", []byte("### One\nhttps://api.example.com/ping\n\n###\nGET https://api.example.com/two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "HTTP file" || len(doc.Requests) != 2 {
		t.Fatalf("document = %+v", doc)
	}
	if doc.Requests[0].Method != "GET" || doc.Requests[0].URL != "https://api.example.com/ping" {
		t.Fatalf("bare URL = %+v", doc.Requests[0])
	}
	if doc.Requests[1].Name != "Request 2" {
		t.Fatalf("unnamed request = %+v", doc.Requests[1])
	}
}

func TestWriteAppliesAuthAndRoundTrips(t *testing.T) {
	requests := []request.Request{{
		Name: "Create", Method: "POST", URL: "{{baseUrl}}/items",
		Headers: "Content-Type: application/json\nX-Trace: one\nX-Trace: two",
		Body:    "{\"name\":\"Demo\"}",
		Auth:    request.Auth{Type: "Bearer", Token: "{{token}}"},
	}}
	vars := []request.Variable{{Name: "baseUrl", Initial: "https://api.example.com"}, {Name: "token", Initial: "abc"}}
	data, warnings, err := Write("Shop", requests, vars)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("write: %v, %q", err, warnings)
	}
	text := string(data)
	if !strings.Contains(text, "@baseUrl = https://api.example.com") || !strings.Contains(text, "### Create") ||
		!strings.Contains(text, "Authorization: Bearer {{token}}") {
		t.Fatalf("output = %q", text)
	}
	doc, err := Parse("Shop", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Requests) != 1 {
		t.Fatalf("round trip = %+v", doc)
	}
	r := doc.Requests[0]
	if r.Name != "Create" || r.Method != "POST" || r.URL != "{{baseUrl}}/items" || r.Body != "{\"name\":\"Demo\"}" {
		t.Fatalf("round trip = %+v", r)
	}
	if !strings.Contains(r.Headers, "Authorization: Bearer {{token}}") || !strings.Contains(r.Headers, "X-Trace: two") {
		t.Fatalf("round trip headers = %q", r.Headers)
	}
}

func TestWriteReportsUnsupportedBodiesAndProtocols(t *testing.T) {
	requests := []request.Request{
		{Name: "Binary", Method: "POST", URL: "https://api.example.com/upload", BodyMode: "Binary", BodyFile: "/tmp/file.bin"},
		{Name: "gRPC", Protocol: "gRPC", URL: "grpc://api.example.com"},
		{Name: "Split", Method: "POST", URL: "https://api.example.com/split", Body: "one\n### not a separator\ntwo"},
	}
	data, warnings, err := Write("Mixed", requests, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "### Binary") || !strings.Contains(string(data), "### Split") || strings.Contains(string(data), "gRPC") {
		t.Fatalf("output = %q", data)
	}
	for _, part := range []string{"binary bodies are not exported", "gRPC requests are not exported", "starting with ###"} {
		if !slices.ContainsFunc(warnings, func(line string) bool { return strings.Contains(line, part) }) {
			t.Fatalf("warnings = %q, want %q", warnings, part)
		}
	}
}

func TestWriteLeavesOutLineBrokenVariables(t *testing.T) {
	data, warnings, err := Write("Vars", nil, []request.Variable{{Name: "good", Initial: "one"}, {Name: "bad", Initial: "two\nthree"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "bad") || !strings.Contains(string(data), "@good = one") {
		t.Fatalf("output = %q", data)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "line break") {
		t.Fatalf("warnings = %q", warnings)
	}
}
