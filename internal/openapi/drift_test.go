package openapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const driftSpec = `openapi: 3.0.3
info: {title: Pets, version: 1.0.0}
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [name]
                properties:
                  name: {type: string}
`

func TestCheckerValidatesRequestsAndResponses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.yaml")
	if err := os.WriteFile(path, []byte(driftSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	checker, err := LoadChecker(path)
	if err != nil {
		t.Fatal(err)
	}
	good := Exchange{Method: "GET", URL: "https://api.example.test/pets/7", Status: 200,
		ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: []byte(`{"name":"Rex"}`)}
	drift, matched := checker.Check(good)
	if !matched || drift.Endpoint != "GET /pets/{id}" || len(drift.Problems) != 0 {
		t.Fatalf("good exchange: matched=%v drift=%+v", matched, drift)
	}

	bad := good
	bad.URL, bad.Status = "https://api.example.test/pets/seven", 500
	drift, matched = checker.Check(bad)
	if !matched {
		t.Fatal("bad exchange did not match its operation")
	}
	joined := strings.Join(drift.Problems, "\n")
	for _, want := range []string{"id", "status is not supported"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q: %s", want, joined)
		}
	}

	bad = good
	bad.ResponseBody = []byte(`{}`)
	drift, _ = checker.Check(bad)
	if joined := strings.Join(drift.Problems, "\n"); !strings.Contains(joined, "name") {
		t.Errorf("response schema drift lacks %q: %s", "name", joined)
	}

	if _, matched := checker.Check(Exchange{Method: "GET", URL: "https://api.example.test/unknown"}); matched {
		t.Error("an unrecorded endpoint matched")
	}
}

func TestValidationProblemsFlattensAndDedupes(t *testing.T) {
	problems := validationProblems(openapi3.MultiError{errors.New("a\nb"), errors.New("a")})
	if !slices.Equal(problems, []string{"a", "b"}) {
		t.Fatalf("problems = %q", problems)
	}
}
