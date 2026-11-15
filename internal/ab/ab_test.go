package ab

import (
	"strings"
	"testing"
)

func TestCompareReportsStructureNotValues(t *testing.T) {
	a := Response{Status: 200, ContentType: "application/json; charset=utf-8", Body: []byte(`{"id":1,"name":"A","tags":["x"]}`)}
	b := Response{Status: 200, ContentType: "application/json", Body: []byte(`{"name":"B","id":2,"tags":["y"]}`)}
	if endpoint := Compare("List", "GET", "/items", a, b); len(endpoint.Differences) != 0 {
		t.Fatalf("value-only change: %q", endpoint.Differences)
	}

	b.Body = []byte(`{"id":"2","name":"B","tags":["y"],"extra":true}`)
	differences := strings.Join(Compare("List", "GET", "/items", a, b).Differences, "\n")
	for _, want := range []string{"/id: type number vs string", "/extra: only on B"} {
		if !strings.Contains(differences, want) {
			t.Errorf("differences lack %q: %s", want, differences)
		}
	}

	b = Response{Status: 500, ContentType: "text/plain", Body: []byte("broken")}
	differences = strings.Join(Compare("List", "GET", "/items", a, b).Differences, "\n")
	for _, want := range []string{"status 200 vs 500", `content type "application/json" vs "text/plain"`, "body is JSON on A, not on B"} {
		if !strings.Contains(differences, want) {
			t.Errorf("differences lack %q: %s", want, differences)
		}
	}
}

func TestCompareReportsRequestFailure(t *testing.T) {
	endpoint := Compare("List", "GET", "/items", Response{Error: "dial failed"}, Response{Status: 200})
	if len(endpoint.Differences) != 1 || !strings.Contains(endpoint.Differences[0], "only on A") {
		t.Fatalf("differences = %q", endpoint.Differences)
	}
}

func TestReportJSONAndJUnit(t *testing.T) {
	report := Report{Input: "collections", A: "staging", B: "production"}
	report.Add(Compare("List", "GET", "/items", Response{Status: 200, ContentType: "application/json", Body: []byte(`{"id":1}`)}, Response{Status: 200, ContentType: "application/json", Body: []byte(`{"id":2}`)}))
	report.Add(Compare("Create", "POST", "/items", Response{Status: 201, ContentType: "application/json", Body: []byte(`{"id":1}`)}, Response{Status: 500, ContentType: "application/json", Body: []byte(`{"error":"no"}`)}))
	report.Skip("POST", "/orders", "unsafe method; use --include-unsafe")
	data, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"same":1`, `"different":1`, `"status 201 vs 500"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON lacks %q: %s", want, data)
		}
	}
	junit, err := report.JUnit()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`tests="3"`, `failures="1"`, `<failure message="status 201 vs 500">`, `<skipped message="unsafe method; use --include-unsafe">`} {
		if !strings.Contains(string(junit), want) {
			t.Errorf("JUnit lacks %q: %s", want, junit)
		}
	}
}
