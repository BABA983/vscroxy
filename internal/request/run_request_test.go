package request

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// callTable resolves runRequest arguments by ID for these tests.
func callTable(calls map[string]Callee) Resolver {
	return func(ref string) (Callee, error) {
		c, ok := calls[ref]
		if !ok {
			return Callee{}, errors.New("no request " + ref)
		}
		return c, nil
	}
}

func TestRunRequestSendsTheNamedRequestInItsOwnFolders(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("X-Over"))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	shop := Folder{Path: "Shop", Variables: []Variable{{Name: "tier", Initial: "shop"}}, Auth: Auth{Type: "Bearer", Token: "shop-token"}}
	other := Folder{Path: "Other", Variables: []Variable{{Name: "tier", Initial: "other"}}}
	child := Request{ID: "child", Name: "Child", Collection: "Shop", Method: "GET", URL: upstream.URL + "/{{tier}}",
		Auth: Auth{Type: AuthInherit}, Pre: `pm.request.headers.upsert("X-Over", pm.variables.get("x"))`}
	parent := Request{Name: "Parent", Collection: "Other", Method: "GET", URL: upstream.URL + "/parent",
		Pre: `const res = await pm.execution.runRequest("child", { variables: { x: "over" } }); pm.environment.set("code", String(res.code));`}
	in := Context{Folders: []Folder{other}, Resolve: callTable(map[string]Callee{"child": {Request: child, Folders: []Folder{shop}}})}

	out := parent.Execute(t.Context(), testProxy(t), in, map[string]string{}, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if got := out.Vars["code"]; got != "201" {
		t.Errorf("runRequest returned code %q; want the child's 201", got)
	}
	mu.Lock()
	defer mu.Unlock()
	// The child's send happens before the parent's own: its folder supplies
	// the path variable and the bearer token, and the call's override reaches it.
	if len(seen) != 2 || seen[0] != "/shop Bearer shop-token over" {
		t.Errorf("sends = %q; want the child first at /shop with its folder auth and the override", seen)
	}
}

func TestRunRequestRefusesACycle(t *testing.T) {
	loop := Request{ID: "loop", Name: "Loop", Method: "GET", URL: "http://127.0.0.1:1/", Pre: `await pm.execution.runRequest("loop")`}
	in := Context{Resolve: callTable(map[string]Callee{"loop": {Request: loop}})}
	out := loop.Execute(t.Context(), testProxy(t), in, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "nest more than 5 levels") {
		t.Errorf("cycle: err = %v; want the depth limit", out.Err)
	}
}

func TestRunRequestLimitsCallsPerScript(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer upstream.Close()
	child := Request{ID: "child", Name: "Child", Method: "GET", URL: upstream.URL}
	parent := Request{Method: "GET", URL: upstream.URL,
		Pre: `for (let i = 0; i < 11; i++) await pm.execution.runRequest("child");`}
	in := Context{Resolve: callTable(map[string]Callee{"child": {Request: child}})}
	out := parent.Execute(t.Context(), testProxy(t), in, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "at most 10 times") {
		t.Fatalf("err = %v; want the eleventh call refused", out.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 10 {
		t.Errorf("child sends = %d; want the first ten calls only", hits)
	}
}

func TestRunRequestSkippedChildGivesNull(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer upstream.Close()
	skip := Request{ID: "skip", Name: "Skip", Method: "GET", URL: upstream.URL, Pre: `pm.execution.skipRequest()`}
	parent := Request{Method: "GET", URL: upstream.URL,
		Pre: `const res = await pm.execution.runRequest("skip"); pm.environment.set("was", res === null ? "null" : "value");`}
	in := Context{Resolve: callTable(map[string]Callee{"skip": {Request: skip}})}
	out := parent.Execute(t.Context(), testProxy(t), in, nil, nil)
	if out.Err != nil || out.Vars["was"] != "null" {
		t.Errorf("err = %v, was = %q; want the skipped child to return null", out.Err, out.Vars["was"])
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("sends = %d; want only the parent, since the child was skipped", hits)
	}
}

func TestRunRequestNeedsAResolver(t *testing.T) {
	r := Request{Method: "GET", URL: "http://127.0.0.1:1/", Pre: `await pm.execution.runRequest("child")`}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "not available in this run") {
		t.Errorf("err = %v; want the run to refuse runRequest", out.Err)
	}
}
