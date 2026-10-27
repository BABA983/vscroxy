package app

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

func requestProxy(t *testing.T) *capture.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv, err := capture.New(addr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestPreRequestVariablesAndOverridesReachCurrentRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s|%s|%s|%s", r.Header.Get("Authorization"), r.Header.Get("X-Direct"), r.URL.RawQuery, body)
	}))
	defer upstream.Close()
	srv := requestProxy(t)
	for _, mode := range []string{"Raw", "URL encoded", "Multipart"} {
		for _, auth := range []requestAuth{{}, {Type: "Bearer", Token: "{{token}}"}, {Type: "Basic", Username: "{{user}}", Password: "{{token}}"}} {
			t.Run(mode+auth.Type, func(t *testing.T) {
				r := savedRequest{Method: "POST", URL: upstream.URL + "?q={{generated}}", Headers: "Authorization: Bearer {{token}}", Auth: auth,
					Body: "{{token}}", BodyMode: mode, Form: []requestField{{Name: "value", Value: "{{token}}"}},
					Pre: `vars.set("token", "new-token"); vars.set("user", "alice"); vars.set("generated", "fresh"); req.set("X-Direct", "script");`, Checks: responseChecks{Status: "200"}}
				got := executeRequest(t.Context(), srv, r, map[string]string{"token": "old-token"}, nil)
				wantAuth := "Bearer new-token"
				if auth.Type == "Basic" {
					wantAuth = "Basic YWxpY2U6bmV3LXRva2Vu"
				}
				if got.err != nil || got.response == nil || !strings.HasPrefix(got.response.ResponseBody, wantAuth+"|script|q=fresh|") || !strings.Contains(got.response.ResponseBody, "new-token") || strings.Contains(got.response.ResponseBody, "old-token") {
					t.Fatalf("execution = %+v, response = %+v", got, got.response)
				}
				r.Checks = responseChecks{}
				got = executeRequest(t.Context(), srv, r, map[string]string{"token": "old-token"}, nil)
				if got.err != nil || got.response == nil || !strings.HasPrefix(got.response.ResponseBody, wantAuth+"|script|q=fresh|") {
					t.Fatalf("without Checks: %+v, response = %+v", got, got.response)
				}
				// Direct request edits win over generated auth and form encoding.
				r.Pre += `req.set("Authorization", "direct"); req.body = "direct-body"; req.url = req.url.replace("?q=", "/edited?q="); req.method = "PATCH";`
				got = executeRequest(t.Context(), srv, r, nil, nil)
				if got.err != nil || got.request.Method != "PATCH" || got.response.ResponseBody != "direct|script|q=fresh|direct-body" || !strings.Contains(got.request.URL, "/edited?") {
					t.Fatalf("direct edits lost: %+v, response = %+v", got, got.response)
				}
				r.Body = ""
				r.Pre += `req.body = ""; req.remove("Authorization");`
				got = executeRequest(t.Context(), srv, r, nil, nil)
				if got.err != nil || got.response == nil || got.response.ResponseBody != "|script|q=fresh|" {
					t.Fatalf("explicit empty body/header removal lost: %+v, response = %+v", got, got.response)
				}
			})
		}
	}
}

func TestStructuredRequestSurvivesSavingAndSendsBinaryMultipart(t *testing.T) {
	payload := []byte{0, 255, 1, 10, 'a', '&'}
	file := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer token-19" || !reflect.DeepEqual(r.URL.Query()["tag"], []string{"a/b", "two words"}) {
			http.Error(w, "wrong method/auth/query", 400)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("upload")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer f.Close()
		got, _ := io.ReadAll(f)
		if !bytes.Equal(got, payload) || !reflect.DeepEqual(r.MultipartForm.Value["label"], []string{"中文 & =", "second"}) || len(r.MultipartForm.Value["disabled"]) != 0 {
			http.Error(w, "wrong multipart content", 400)
			return
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"upload":"complete"}`)
	}))
	defer upstream.Close()
	r := savedRequest{ID: "multipart-asset", Name: "Upload", Collection: "Demo", Method: "POST", URL: upstream.URL + "/upload?tag=a%2Fb&tag=two+words",
		Headers: "Content-Type: stale/type\nContent-Length: 1\nAuthorization: stale",
		Auth:    requestAuth{Type: "Bearer", Token: "{{token}}"}, BodyMode: "Multipart",
		Form: []requestField{{Name: "label", Value: "中文 & ="}, {Name: "label", Value: "second"}, {Name: "upload", Value: file, File: true}, {Name: "disabled", Value: "not sent", Disabled: true}},
		Post: `test("created",res.status===201); test("body",res.json().upload==="complete")`,
	}
	path := filepath.Join(t.TempDir(), "requests.json")
	if err := saveRequests(path, []savedRequest{r}); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRequests(path)
	if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], r) {
		t.Fatalf("round trip: %+v %v", loaded, err)
	}
	a := workspaceApp()
	a.addDocument(loaded[0], "saved:Demo\x00Upload")
	if a.dirty() {
		t.Fatal("reopened structured request is unexpectedly dirty")
	}
	a.formFields[0].Value = "edited"
	if !a.dirty() || a.baseline.Form[0].Value != "中文 & =" || loaded[0].Form[0].Value != "中文 & =" {
		t.Fatal("draft shares saved form fields")
	}
	got := executeRequest(t.Context(), requestProxy(t), r, map[string]string{"token": "token-19"}, &cookieStore{})
	if got.err != nil || got.response == nil || got.response.Status != 201 || got.response.ResponseBody != `{"upload":"complete"}` || len(got.tests) != 2 || got.failed() {
		t.Fatalf("send: %+v", got)
	}
}

func TestURLEncodedAuthAndCookiesFollowHTTPRules(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "cookie-7", Path: "/api", HttpOnly: true})
			http.Redirect(w, r, "/api/redirect", 302)
		case "/api/redirect":
			c, err := r.Cookie("session")
			if err != nil || c.Value != "cookie-7" {
				http.Error(w, "no redirect cookie", 400)
				return
			}
			fmt.Fprint(w, "redirect ok")
		case "/api/form":
			u, p, ok := r.BasicAuth()
			if !ok || u != "user-19" || p != "p:a &" {
				http.Error(w, "bad basic auth", 401)
				return
			}
			if err := r.ParseForm(); err != nil || !reflect.DeepEqual(r.PostForm["tag"], []string{"a&b", "=中文"}) || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				http.Error(w, "bad form", 400)
				return
			}
			c, err := r.Cookie("session")
			if err != nil || c.Value != "cookie-7" {
				http.Error(w, "bad session", 401)
				return
			}
			fmt.Fprint(w, "form ok")
		case "/outside":
			if r.Header.Get("Cookie") != "" {
				http.Error(w, "cookie escaped its path", 400)
				return
			}
			fmt.Fprint(w, "scoped")
		default:
			fmt.Fprint(w, r.URL.RawQuery+"|"+r.Header.Get("X-Key"))
		}
	}))
	defer upstream.Close()
	srv, jar := requestProxy(t), &cookieStore{}
	for _, r := range []savedRequest{
		{Method: "GET", URL: upstream.URL + "/login"},
		{Method: "POST", URL: upstream.URL + "/api/form", Headers: "Content-Type: stale", Auth: requestAuth{Type: "Basic", Username: "{{user}}", Password: "p:a &"}, BodyMode: "URL encoded", Form: []requestField{{Name: "tag", Value: "a&b"}, {Name: "tag", Value: "=中文"}}},
		{Method: "GET", URL: upstream.URL + "/outside"},
	} {
		got := executeRequest(t.Context(), srv, r, map[string]string{"user": "user-19"}, jar)
		if got.err != nil || got.response.Status != 200 {
			t.Fatalf("%s: %+v", r.URL, got)
		}
	}
	items := jar.snapshot()
	if len(items) != 1 || !items[0].cookie.HttpOnly || items[0].cookie.Path != "/api" {
		t.Fatalf("cookie metadata: %+v", items)
	}
	jar.remove(items[0].key)
	u, _ := url.Parse(upstream.URL + "/api/form")
	if len(jar.Cookies(u)) != 0 {
		t.Fatal("deleting a path cookie left it active")
	}
	for _, auth := range []requestAuth{{Type: "API key", Key: "key", Value: "a & b", Location: "Query"}, {Type: "API key", Key: "X-Key", Value: "header-7", Location: "Header"}} {
		got := executeRequest(t.Context(), srv, savedRequest{Method: "GET", URL: upstream.URL + "/key?tag=one&tag=two", Auth: auth}, nil, jar)
		if got.err != nil || got.response.Status != 200 {
			t.Fatalf("API key: %+v", got)
		}
		if auth.Location == "Query" && got.response.ResponseBody != "key=a+%26+b&tag=one&tag=two|" || auth.Location == "Header" && got.response.ResponseBody != "tag=one&tag=two|header-7" {
			t.Fatalf("API key placement: %s", got.response.ResponseBody)
		}
	}
}

func TestParamsEditorUpdatesURLWithoutLosingDuplicateKeysOrEscapedPath(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{URL: "https://example.com/a%2Fb?tag=first&tag=two+words#anchor"}, "")
	tt := ui.NewTester(a.view, 1100, 700)
	for _, label := range []string{"Params", "parameter value 2"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("changed & 中文")
	u, err := url.Parse(a.composeURL)
	if err != nil || u.EscapedPath() != "/a%2Fb" || u.Fragment != "anchor" || !reflect.DeepEqual(u.Query()["tag"], []string{"first", "changed & 中文"}) {
		t.Fatalf("URL: %q %v", a.composeURL, err)
	}
	a.composeURL = "https://example.com/new?fresh=7"
	tt.Frame()
	if len(a.queryRows) != 1 || a.queryRows[0].Name != "fresh" {
		t.Fatal("URL edits did not refresh Params")
	}
	if err := tt.Click("Auth"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Authorization type"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Bearer"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Use {{variables}} for secrets. Auth uses variables after the pre-script; explicit req.set/remove overrides auth. Saving stores these fields locally.") {
		t.Fatal("missing auth storage warning")
	}
}

func TestCookieManagerRespectsDomainsSecureAndDeletion(t *testing.T) {
	u, _ := url.Parse("https://api.example.com/api/login")
	jar := &cookieStore{}
	jar.SetCookies(u, []*http.Cookie{
		{Name: "host", Value: "one", Secure: true, Path: "/api"},
		{Name: "domain", Value: "two", Domain: "example.com", Path: "/"},
		{Name: "invalid", Value: "three", Domain: "com", Path: "/"},
	})
	if len(jar.snapshot()) != 2 || len(jar.Cookies(u)) != 2 {
		t.Fatal("valid cookies lost or public-suffix cookie accepted")
	}
	plain := u.Clone()
	plain.Scheme = "http"
	other, _ := url.Parse("https://other.example.com/api/login")
	for _, target := range []*url.URL{plain, other} {
		got := jar.Cookies(target)
		if len(got) != 1 || got[0].Name != "domain" {
			t.Fatalf("scope leaked for %s: %v", target, got)
		}
	}
	foreign, _ := url.Parse("https://unrelated.test/")
	jar.SetCookies(foreign, []*http.Cookie{{Name: "domain", Domain: "example.com", Path: "/", MaxAge: -1}})
	if len(jar.snapshot()) != 2 || len(jar.Cookies(u)) != 2 {
		t.Fatal("unrelated domain removed a cookie")
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "domain", Domain: "example.com", Path: "/", MaxAge: -1}})
	if len(jar.snapshot()) != 1 || len(jar.Cookies(other)) != 0 {
		t.Fatal("valid Set-Cookie deletion was ignored")
	}
	jar.remove(jar.snapshot()[0].key)
	if len(jar.Cookies(u)) != 0 || len(jar.snapshot()) != 0 {
		t.Fatal("manager deletion did not revoke the remaining cookie")
	}
}

func TestStructuredOpenAPIDraftWarnsWithoutCopyingSecretsOrStaleRawBody(t *testing.T) {
	spec, err := generateOpenAPI([]savedRequest{{Method: "POST", URL: "https://example.com/upload", Auth: requestAuth{Type: "Bearer", Token: "private-token-19"}, BodyMode: "Multipart", Body: "hidden stale body", Form: []requestField{{Name: "file", Value: "/private/local/path", File: true}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(spec.Notes, "\n")
	if !strings.Contains(notes, "structured authentication is omitted") || !strings.Contains(notes, "structured form body is omitted") {
		t.Fatalf("missing limitations: %s", notes)
	}
	for _, secret := range []string{"private-token-19", "/private/local/path", "hidden stale body", "requestBody"} {
		if strings.Contains(spec.Source, secret) {
			t.Fatalf("draft copied inactive or sensitive data: %s", secret)
		}
	}
}

func TestFormErrorsDoNotSilentlyFallBackToRawBody(t *testing.T) {
	for _, r := range []savedRequest{
		{URL: "http://example.com", BodyMode: "Multipart", Form: []requestField{{Name: "file", Value: filepath.Join(t.TempDir(), "missing"), File: true}}},
		{URL: "http://example.com", BodyMode: "URL encoded", Form: []requestField{{Name: "file", Value: "unused", File: true}}},
		{URL: "http://example.com", BodyMode: "URL encoded", Form: []requestField{{Name: "big", Value: strings.Repeat("&", 6<<20)}}},
	} {
		if _, err := prepareRequest(r, nil); err == nil {
			t.Fatal("invalid form was accepted")
		}
	}
}

func waitRunner(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.runner.running && time.Now().Before(deadline) {
		a.drainRunner()
		time.Sleep(time.Millisecond)
	}
	if a.runner.running {
		a.runner.cancel()
		t.Fatal("runner did not finish")
	}
}

func TestRequestDiagnosticsKeepPhasesErrorsAndSentVariables(t *testing.T) {
	headers := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("Authorization")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	srv := requestProxy(t)
	r := savedRequest{Method: "GET", URL: upstream.URL, Auth: requestAuth{Type: "Bearer", Token: "{{token}}"},
		Pre:  `log("preparing"); vars.set("token","sent-token");`,
		Post: "log('received', res.status); vars.set('token','next-token');\nthrow new Error('post failed');"}
	got := executeRequest(t.Context(), srv, r, map[string]string{"token": "old-token"}, nil)
	if <-headers != "Bearer sent-token" || got.response == nil || got.response.Status != 200 || got.outcome() != "Post-response script failed" {
		t.Fatalf("wrong observable request/result: %s", got.summary())
	}
	if len(got.console) != 5 || got.console[0].Phase != "pre-request" || got.console[2].Phase != "post-response" || got.console[4].Kind != "error" || !strings.Contains(got.console[4].Text, "post-response.js:2:") {
		t.Fatalf("console lost phase or error position: %+v", got.console)
	}
	if got.vars["token"] != "next-token" || len(got.variables) != 1 || got.variables[0].Value != "sent-token" || got.variables[0].Source != "Pre-request script" {
		t.Fatalf("post-script rewrote send-time variable evidence: %+v", got.variables)
	}
	var log experimentLog
	if err := log.retain(snapshotExperiment(0, r, "staging", got)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "experiments.json")
	if err := saveExperiments(path, log); err != nil {
		t.Fatal(err)
	}
	restored, err := loadExperiments(path)
	if err != nil || !reflect.DeepEqual(restored.Records[0].Console, got.console) || !reflect.DeepEqual(restored.Records[0].Variables, got.variables) {
		t.Fatal("diagnostics did not survive restart", err)
	}
	for _, src := range []string{"\n\nthrow new Error('before send');", "\n\nlet = ;"} {
		r.Pre = src
		failed := executeRequest(t.Context(), nil, r, nil, nil)
		if failed.response != nil || failed.outcome() != "Pre-request script failed" || len(failed.console) == 0 || !strings.Contains(failed.summary(), "pre-request.js") || !strings.Contains(failed.summary(), "3") {
			t.Fatalf("pre-script error lacks stage/location or sent traffic: %+v", failed)
		}
	}
}

func TestCollectionRunnerChainsVariablesCookiesAssertionsAndStops(t *testing.T) {
	paths := make(chan string, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "runner", Path: "/api"})
			fmt.Fprint(w, `{"id":27}`)
			return
		}
		if r.URL.Path == "/api/27" {
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "runner" {
				http.Error(w, "no runner session", 401)
				return
			}
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"base": upstream.URL}}}
	a.activeEnv = "Local"
	a.requests = []savedRequest{
		{Name: "01 Login", Collection: "Demo", Method: "GET", URL: "{{base}}/login", Post: `log("login received");vars.set("id",String(res.json().id));test("login",res.status===200)`},
		{Name: "02 Detail", Collection: "Demo/nested", Method: "GET", URL: "{{base}}/api/{{id}}", Post: `test("cookie and variable",res.status===200 && res.json().ok===true)`},
		{Name: "03 Failure", Collection: "Demo/nested", Method: "GET", URL: "{{base}}/fail", Post: `test("deliberate failure",false)`},
		{Name: "04 Never", Collection: "Demo/nested", Method: "GET", URL: "{{base}}/never"},
	}
	a.addDocument(a.requests[0], "saved:Demo\x0001 Login")
	a.openRunner()
	a.startRunner()
	waitRunner(t, a)
	var visited []string
	for len(paths) > 0 {
		visited = append(visited, <-paths)
	}
	if a.runner.completed != 3 || a.runner.failures != 1 || !reflect.DeepEqual(visited, []string{"/login", "/api/27", "/fail"}) {
		t.Fatalf("runner results %+v; requests %v", a.runner.rows, visited)
	}
	if a.activeVars()["id"] != "27" || a.composeURL != "{{base}}/login" || a.dirty() {
		t.Fatal("runner did not merge variables or mutated the request tab")
	}
	if len(a.experiments.log.Records) != 3 || len(a.experiments.log.Records[2].Console) != 2 || a.experiments.log.Records[2].Console[0].Text != "login received" || a.runner.rows[0].recordID == 0 {
		t.Fatal("Runner diagnostics were not retained alongside Send records")
	}
	// Rerun without stop-on-failure and verify every iteration executes.
	a.runner.stopOnFailure = false
	a.runner.iterations = "2"
	a.startRunner()
	waitRunner(t, a)
	if a.runner.completed != 8 || a.runner.failures != 2 {
		t.Fatalf("repeat results: %+v", a.runner)
	}
}

func TestCollectionRunnerCancellationRevokesInFlightRequest(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, "released")
	}))
	defer upstream.Close()
	defer close(release)
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.requests = []savedRequest{{Name: "Slow", Method: "GET", URL: upstream.URL}}
	a.openRunner()
	a.startRunner()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not send")
	}
	a.runner.cancelled = true
	a.runner.cancel()
	waitRunner(t, a)
	if a.runner.status != "Cancelled" {
		t.Fatalf("status %q", a.runner.status)
	}
	if len(a.experiments.log.Records) != 1 || a.experiments.log.Records[0].Outcome != "Cancelled" || a.runner.completed != 1 {
		t.Fatal("the cancelled attempt disappeared from run records")
	}
}
