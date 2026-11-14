package mockserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

// The examples are the ones a collection keeps: their URLs are as authored, so
// {{base}} and the :id path parameter are not expanded when they are saved.
func TestMockAnswersFromItsExamples(t *testing.T) {
	examples := []request.Example{
		{Name: "201 Created", Status: 201, Body: `{"id":1}`,
			Request: request.ExampleRequest{Method: "POST", URL: "{{base}}/projects"}},
		{Name: "404 Not Found", Status: 404, Body: `{"error":"missing"}`,
			Request: request.ExampleRequest{Method: "POST", URL: "{{base}}/projects"}},
		{Name: "One project", Status: 200, Headers: "Content-Type: application/json", Body: `{"token":"{{token}}"}`,
			Request: request.ExampleRequest{Method: "GET", URL: "{{base}}/projects/:id?expand=1"}},
	}
	s := New(examples, map[string]string{"base": "https://api.test/v1", "token": "t-9"})

	cases := []struct {
		name, method, target string
		header               map[string]string
		status               int
		body, contentType    string
	}{
		{name: "first example of the route", method: "POST", target: "/v1/projects", status: 201, body: `{"id":1}`},
		{name: "named example", method: "POST", target: "/v1/projects", header: map[string]string{SelectHeader: "404 Not Found"},
			status: 404, body: `{"error":"missing"}`},
		{name: "unknown name falls back to the first", method: "POST", target: "/v1/projects",
			header: map[string]string{SelectHeader: "nope"}, status: 201, body: `{"id":1}`},
		{name: "path parameter, query ignored, body and header filled",
			method: "GET", target: "/v1/projects/42?x=1", status: 200, body: `{"token":"t-9"}`, contentType: "application/json"},
		{name: "a path parameter is one segment", method: "GET", target: "/v1/projects/42/extra", status: 404},
		{name: "the variable is part of the path", method: "GET", target: "/projects/42", status: 404},
		{name: "the method must match", method: "DELETE", target: "/v1/projects", status: 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.target, nil)
			for k, v := range c.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
			if c.body != "" && rec.Body.String() != c.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), c.body)
			}
			if c.contentType != "" && rec.Header().Get("Content-Type") != c.contentType {
				t.Fatalf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), c.contentType)
			}
		})
	}
}

// An example with no status answers 200, and an example without a method is a GET.
func TestMockDefaultsStatusAndMethod(t *testing.T) {
	s := New([]request.Example{{Name: "Ping", Body: "pong", Request: request.ExampleRequest{URL: "http://h/ping"}}}, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "pong" {
		t.Fatalf("GET /ping = %d %q, want 200 pong", rec.Code, rec.Body.String())
	}
}

// Within a route, X-Mock-Response-Name wins over X-Mock-Response-Code, the code
// picks the first example with that status, and x-mock-match-request-body
// answers only from examples whose request body equals the request's body.
func TestMockChoosesByCodeAndRequestBody(t *testing.T) {
	examples := []request.Example{
		{Name: "Apple", Status: 201, Body: "created-apple",
			Request: request.ExampleRequest{Method: "POST", URL: "{{base}}/orders", Body: `{"item":"apple"}`}},
		{Name: "Missing", Status: 404, Body: "missing-pear",
			Request: request.ExampleRequest{Method: "POST", URL: "{{base}}/orders", Body: `{"item":"{{fruit}}"}`}},
		{Name: "Kiwi", Status: 201, Body: "created-kiwi",
			Request: request.ExampleRequest{Method: "POST", URL: "{{base}}/orders", Body: `{"item":"kiwi"}`}},
	}
	s := New(examples, map[string]string{"base": "https://api.test", "fruit": "pear"})

	cases := []struct {
		name   string
		header map[string]string
		body   string
		status int
		answer string
	}{
		{name: "code 404 picks the example with that status", header: map[string]string{CodeHeader: "404"}, status: 404, answer: "missing-pear"},
		{name: "code 201 picks the first with that status", header: map[string]string{CodeHeader: "201"}, status: 201, answer: "created-apple"},
		{name: "name beats code", header: map[string]string{SelectHeader: "Missing", CodeHeader: "201"}, status: 404, answer: "missing-pear"},
		{name: "a code no example has falls back to the first", header: map[string]string{CodeHeader: "500"}, status: 201, answer: "created-apple"},
		{name: "a match flag other than true is ignored", header: map[string]string{MatchBodyHeader: "false"}, body: `{"item":"kiwi"}`, status: 201, answer: "created-apple"},
		{name: "body match picks the example with that body", header: map[string]string{MatchBodyHeader: "true"}, body: `{"item":"kiwi"}`, status: 201, answer: "created-kiwi"},
		{name: "body match expands variables in the example body", header: map[string]string{MatchBodyHeader: "true"}, body: `{"item":"pear"}`, status: 404, answer: "missing-pear"},
		{name: "body match and code together", header: map[string]string{MatchBodyHeader: "true", CodeHeader: "201"}, body: `{"item":"kiwi"}`, status: 201, answer: "created-kiwi"},
		{name: "no body matches is a 404", header: map[string]string{MatchBodyHeader: "true"}, body: `{"item":"fig"}`, status: 404, answer: "no example has this request body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(c.body))
			for k, v := range c.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.answer) {
				t.Fatalf("got %d %q, want %d containing %q", rec.Code, rec.Body.String(), c.status, c.answer)
			}
		})
	}
}
