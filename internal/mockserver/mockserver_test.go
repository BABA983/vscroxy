package mockserver

import (
	"net/http"
	"net/http/httptest"
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
