package request

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Execute signs twice (before and after a script's header edits); the
// second signature must replace the first, not duplicate it.
func TestOAuth1ExecutePlacements(t *testing.T) {
	var mu sync.Mutex
	var queries, bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		queries, bodies = append(queries, r.URL.RawQuery), append(bodies, string(body))
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	srv := testProxy(t)
	for _, addTo := range []string{"Query", "Body"} {
		t.Run(addTo, func(t *testing.T) {
			r := Request{Method: "POST", URL: upstream.URL + "/me", BodyMode: "URL encoded",
				Form: []Field{{Name: "a", Value: "b"}},
				Auth: Auth{Type: AuthOAuth1, OAuth1: OAuth1Auth{
					ConsumerKey: "ck", ConsumerSecret: "cs", Timestamp: "137131201", Nonce: "7d8f3e4a", AddTo: addTo,
				}}}
			out := r.Execute(t.Context(), srv, Context{}, nil, nil)
			if out.Err != nil || out.Response == nil || out.Response.Status != 200 {
				t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
			}
			mu.Lock()
			query, body := queries[len(queries)-1], bodies[len(bodies)-1]
			mu.Unlock()
			if addTo == "Query" {
				if strings.Count(query, "oauth_signature=") != 1 || !strings.Contains(query, "oauth_consumer_key=ck") {
					t.Errorf("query = %q", query)
				}
				if body != "a=b" {
					t.Errorf("body = %q", body)
				}
			} else {
				if strings.Count(body, "oauth_signature=") != 1 || !strings.HasPrefix(body, "a=b&oauth_consumer_key=ck") {
					t.Errorf("body = %q", body)
				}
				if query != "" {
					t.Errorf("query = %q", query)
				}
			}
		})
	}
}
