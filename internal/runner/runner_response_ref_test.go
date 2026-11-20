package runner

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestRunnerSharesSessionResponsesWithResponseReferences(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+"|"+r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/login" {
			_, _ = w.Write([]byte(`{"token":"runner-private-token-123456"}`))
		}
	}))
	defer upstream.Close()

	source := request.Request{ID: "login", Name: "Login", Collection: "Shop", Method: "GET", URL: upstream.URL + "/login"}
	consumer := request.Request{ID: "me", Name: "Me", Collection: "Shop", Method: "GET", URL: upstream.URL + "/me",
		Auth: request.Auth{Type: "Bearer", Token: `{{response "Login" "/token"}}`}}
	responses := &request.Responses{}
	steps := []Step{{Request: source, Context: request.Context{Responses: responses}},
		{Request: consumer, Context: request.Context{Responses: responses,
			Resolve: func(ref string) (request.Callee, error) { return request.Callee{Request: source}, nil }}}}
	var failure error
	summary, err := Run(t.Context(), runnerProxy(t), steps, Options{Iterations: 1}, func(result Result) error {
		if result.Execution.Err != nil {
			failure = result.Execution.Err
		}
		return nil
	})
	if err != nil || failure != nil || summary.Completed != 2 || summary.Failed != 0 {
		t.Fatalf("run = %+v, %v, %v", summary, err, failure)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/login|", "/me|Bearer runner-private-token-123456"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("sends = %q; want %q", seen, want)
	}
}
