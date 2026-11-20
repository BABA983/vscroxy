package request

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

const responseRefToken = "eyJhbGciOi.token-signature-123456"

func responseRefSource(url string) Request {
	return Request{ID: "login", Name: "Login", Collection: "Shop", Method: "POST", URL: url + "/login", Body: `{"grant":"password"}`}
}

func responseRefResolver(source Request) Resolver {
	return func(ref string) (Callee, error) {
		for _, name := range append([]string{source.ID, source.Name}, source.ReferenceNames...) {
			if ref == name {
				return Callee{Request: source}, nil
			}
		}
		return Callee{}, errors.New("no request " + ref)
	}
}

func TestResponseRefUsesCachedResponseEveryWhere(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI()+"|"+r.Header.Get("Authorization")+"|"+r.Header.Get("X-Token")+"|"+r.Header.Get("X-Header"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	source := responseRefSource(upstream.URL)
	ref := `{{response "Login" "/data/access_token"}}`
	r := Request{ID: "me", Name: "Me", Collection: "Shop", Method: "GET",
		URL:     upstream.URL + `/me?token=` + ref,
		Headers: "Authorization: Bearer " + ref + "\nX-Token: " + ref + "\nX-Header: {{response \"Login\" \"header:X-Token\"}}",
		Body:    `{"token":"` + ref + `"}`,
	}
	responses := &Responses{}
	responses.Add("login", &capture.Flow{Status: 200,
		ResponseHeader: http.Header{"X-Token": {"header-from-login"}},
		ResponseBody:   `{"data":{"access_token":"` + responseRefToken + `"}}`})
	out := r.Execute(t.Context(), testProxy(t), Context{Resolve: responseRefResolver(source), Responses: responses}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := "/me?token=" + responseRefToken + "|Bearer " + responseRefToken + "|" + responseRefToken + "|header-from-login"
	if len(seen) != 1 || seen[0] != want || strings.Contains(seen[0], "response") {
		t.Fatalf("sent = %q; want one request with the cached token in query, headers and body", seen)
	}
	if strings.Contains(out.Request.Body, "response") || !strings.Contains(out.Request.Body, responseRefToken) {
		t.Errorf("body = %q; want the expanded token", out.Request.Body)
	}
}

func TestResponseRefSendsMissingSource(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+"|"+r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/login":
			w.Header().Set("X-Token", "header-from-login")
			_, _ = w.Write([]byte(`{"access_token":"` + responseRefToken + `"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer upstream.Close()

	source := responseRefSource(upstream.URL)
	r := Request{ID: "me", Name: "Me", Collection: "Shop", Method: "GET", URL: upstream.URL + "/me",
		Auth: Auth{Type: "Bearer", Token: `{{response "Login" "/access_token"}}`}}
	responses := &Responses{}
	out := r.Execute(t.Context(), testProxy(t), Context{Resolve: responseRefResolver(source), Responses: responses}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/login|", "/me|Bearer " + responseRefToken}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("sends = %q; want %q", seen, want)
	}
	if snapshot, ok, _ := responses.Lookup("login"); !ok || snapshot.Body == "" {
		t.Fatal("the source response was not kept in the session store")
	}
}

func TestEditedAuthorizationHeaderDropsItsVariableFromTheReport(t *testing.T) {
	var mu sync.Mutex
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	r := Request{ID: "edited", Name: "Edited", Method: "GET", URL: upstream.URL + "/",
		Auth: Auth{Type: "Bearer", Token: "{{auth_token}}"},
		Pre:  `pm.request.headers.upsert("Authorization", "Bearer script-token")`}
	out := r.Execute(t.Context(), testProxy(t), Context{}, map[string]string{"auth_token": "private-token"}, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for _, variable := range out.Variables {
		if variable.Name == "auth_token" {
			t.Fatalf("variables = %+v; want no auth_token after the header was hand-edited", out.Variables)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != "Bearer script-token" {
		t.Fatalf("Authorization = %q; want the script's header", seen)
	}
}

func TestResponseRefCycleFailsClearly(t *testing.T) {
	loop := Request{ID: "login", Name: "Login", Collection: "Shop", Method: "GET", URL: `http://127.0.0.1:1/{{response "Login" "/token"}}`}
	consumer := Request{ID: "me", Name: "Me", Collection: "Shop", Method: "GET", URL: `http://127.0.0.1:1/?token={{response "Login" "/token"}}`}
	out := consumer.Execute(t.Context(), nil, Context{Resolve: responseRefResolver(loop), Responses: &Responses{}}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "cycle") || !strings.Contains(out.Err.Error(), "Login") {
		t.Fatalf("err = %v; want a clear response reference cycle error", out.Err)
	}
	if strings.Contains(out.Err.Error(), responseRefToken) {
		t.Fatal("the error must not include a secret value")
	}
}

func TestResponseRefMissingPointerNamesTheField(t *testing.T) {
	responses := &Responses{}
	responses.Add("login", &capture.Flow{Status: 200, ResponseBody: `{"access_token":"` + responseRefToken + `"}`})
	source := responseRefSource("http://127.0.0.1:1")
	r := Request{ID: "me", Name: "Me", Collection: "Shop", Method: "GET",
		URL: `http://127.0.0.1:1/?token={{response "Login" "/data/missing"}}`}
	out := r.Execute(t.Context(), nil, Context{Resolve: responseRefResolver(source), Responses: responses}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), `/data/missing`) || !strings.Contains(out.Err.Error(), "pointer") {
		t.Fatalf("err = %v; want a missing pointer error naming the field", out.Err)
	}
}

func TestResponseRefValueKeepsRawJSONLeaves(t *testing.T) {
	body := `{"id":1234567890123,"big":9007199254740993,"obj":{"a":1},"flag":true,"text":"x"}`
	snapshot := ResponseSnapshot{Body: body}
	for pointer, want := range map[string]string{
		"/id":   "1234567890123",
		"/big":  "9007199254740993",
		"/obj":  `{"a":1}`,
		"/flag": "true",
		"/text": "x",
	} {
		got, err := Value(snapshot, ResponseRef{Pointer: pointer})
		if err != nil || got != want {
			t.Errorf("Value(%s) = %q, %v; want %q", pointer, got, err, want)
		}
	}
}

func TestParseResponseRefRejectsEmbeddedHeaderPrefix(t *testing.T) {
	for _, expr := range []string{
		`{{response "Login" "xheader:X-Session"}}`,
		`{{response "Login" "notheader:X-Session"}}`,
	} {
		if _, ok := ParseResponseRef(expr); ok {
			t.Errorf("%s parsed; only a pointer or a header: prefix is valid", expr)
		}
	}
}

func TestResponseRefParseAndPointerEscapes(t *testing.T) {
	ref, ok := ParseResponseRef(`{{ response "Old name" "/a~1b/~0c" }}`)
	if !ok || ref.Name != "Old name" || ref.Pointer != "/a~1b/~0c" {
		t.Fatalf("parsed = %+v, %v", ref, ok)
	}
	snapshot := ResponseSnapshot{Body: `{"a/b":{"~c":"secret"}}`}
	got, err := Value(snapshot, ref)
	if err != nil || got != "secret" {
		t.Fatalf("Value = %q, %v", got, err)
	}
	if _, err := Value(snapshot, ResponseRef{Pointer: "/a/b~2"}); err == nil {
		t.Fatal("an invalid RFC 6901 escape was accepted")
	}
	if refs := VariableRefs(`{{ response "Login" "/token" }} {{other}}`); len(refs) != 1 || refs[0].Name != "other" {
		t.Fatalf("VariableRefs = %+v; want the response reference ignored", refs)
	}
}

func TestResponseRefsInExpandedFieldsOnly(t *testing.T) {
	source := responseRefSource("http://127.0.0.1:1")
	responses := &Responses{}
	responses.Add("login", &capture.Flow{Status: 200, ResponseBody: `{"token":"` + responseRefToken + `"}`})
	r := Request{ID: "me", Name: "Me", Collection: "Other", Method: "GET", URL: `http://127.0.0.1:1/?t={{response "Login" "/token"}}`}
	out := r.Execute(t.Context(), nil, Context{Resolve: responseRefResolver(source), Responses: responses}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "same collection") {
		t.Fatalf("err = %v; want a cross-collection refusal", out.Err)
	}
	r.Collection = "Shop"
	r.URL = `http://127.0.0.1:1/?t={{response "Login" "/token"}}`
	out = r.Execute(t.Context(), nil, Context{Resolve: nil, Responses: responses}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "resolver") {
		t.Fatalf("err = %v; want a missing resolver error", out.Err)
	}
}
