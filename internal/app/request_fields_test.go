package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
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
		for _, auth := range []request.Auth{{}, {Type: "Bearer", Token: "{{token}}"}, {Type: "Basic", Username: "{{user}}", Password: "{{token}}"}} {
			t.Run(mode+auth.Type, func(t *testing.T) {
				r := savedRequest{Method: "POST", URL: upstream.URL + "?q={{generated}}", Headers: "Authorization: Bearer {{token}}", Auth: auth,
					Body: "{{token}}", BodyMode: mode, Form: []request.Field{{Name: "value", Value: "{{token}}"}},
					Pre: `vars.set("token", "new-token"); vars.set("user", "alice"); vars.set("generated", "fresh"); req.set("X-Direct", "script");`, Checks: request.Checks{Status: "200"}}
				got := r.Execute(t.Context(), srv, request.Context{}, map[string]string{"token": "old-token"}, nil)
				wantAuth := "Bearer new-token"
				if auth.Type == "Basic" {
					wantAuth = "Basic YWxpY2U6bmV3LXRva2Vu"
				}
				if got.Err != nil || got.Response == nil || !strings.HasPrefix(got.Response.ResponseBody, wantAuth+"|script|q=fresh|") || !strings.Contains(got.Response.ResponseBody, "new-token") || strings.Contains(got.Response.ResponseBody, "old-token") {
					t.Fatalf("execution = %+v, response = %+v", got, got.Response)
				}
				r.Checks = request.Checks{}
				got = r.Execute(t.Context(), srv, request.Context{}, map[string]string{"token": "old-token"}, nil)
				if got.Err != nil || got.Response == nil || !strings.HasPrefix(got.Response.ResponseBody, wantAuth+"|script|q=fresh|") {
					t.Fatalf("without Checks: %+v, response = %+v", got, got.Response)
				}
				// Direct request edits win over generated auth and form encoding.
				r.Pre += `req.set("Authorization", "direct"); req.body = "direct-body"; req.url = req.url.replace("?q=", "/edited?q="); req.method = "PATCH";`
				got = r.Execute(t.Context(), srv, request.Context{}, nil, nil)
				if got.Err != nil || got.Request.Method != "PATCH" || got.Response.ResponseBody != "direct|script|q=fresh|direct-body" || !strings.Contains(got.Request.URL, "/edited?") {
					t.Fatalf("direct edits lost: %+v, response = %+v", got, got.Response)
				}
				r.Body = ""
				r.Pre += `req.body = ""; req.remove("Authorization");`
				got = r.Execute(t.Context(), srv, request.Context{}, nil, nil)
				if got.Err != nil || got.Response == nil || got.Response.ResponseBody != "|script|q=fresh|" {
					t.Fatalf("explicit empty body/header removal lost: %+v, response = %+v", got, got.Response)
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
		Auth:    request.Auth{Type: "Bearer", Token: "{{token}}"}, BodyMode: "Multipart",
		Form: []request.Field{{Name: "label", Value: "中文 & ="}, {Name: "label", Value: "second"}, {Name: "upload", Value: file, File: true}, {Name: "disabled", Value: "not sent", Disabled: true}},
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
	got := r.Execute(t.Context(), requestProxy(t), request.Context{}, map[string]string{"token": "token-19"}, &cookieStore{})
	if got.Err != nil || got.Response == nil || got.Response.Status != 201 || got.Response.ResponseBody != `{"upload":"complete"}` || len(got.Tests) != 2 || got.Failed() {
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
		{Method: "POST", URL: upstream.URL + "/api/form", Headers: "Content-Type: stale", Auth: request.Auth{Type: "Basic", Username: "{{user}}", Password: "p:a &"}, BodyMode: "URL encoded", Form: []request.Field{{Name: "tag", Value: "a&b"}, {Name: "tag", Value: "=中文"}}},
		{Method: "GET", URL: upstream.URL + "/outside"},
	} {
		got := r.Execute(t.Context(), srv, request.Context{}, map[string]string{"user": "user-19"}, jar)
		if got.Err != nil || got.Response.Status != 200 {
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
	for _, auth := range []request.Auth{{Type: "API key", Key: "key", Value: "a & b", Location: "Query"}, {Type: "API key", Key: "X-Key", Value: "header-7", Location: "Header"}} {
		got := savedRequest{Method: "GET", URL: upstream.URL + "/key?tag=one&tag=two", Auth: auth}.Execute(t.Context(), srv, request.Context{}, nil, jar)
		if got.Err != nil || got.Response.Status != 200 {
			t.Fatalf("API key: %+v", got)
		}
		if auth.Location == "Query" && got.Response.ResponseBody != "key=a+%26+b&tag=one&tag=two|" || auth.Location == "Header" && got.Response.ResponseBody != "tag=one&tag=two|header-7" {
			t.Fatalf("API key placement: %s", got.Response.ResponseBody)
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
	if !tt.HasText("Prefer {{variables}} or {{vault:name}} for secrets; these fields are saved in plaintext.") {
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
	spec, err := generateOpenAPI([]savedRequest{{Method: "POST", URL: "https://example.com/upload", Auth: request.Auth{Type: "Bearer", Token: "private-token-19"}, BodyMode: "Multipart", Body: "hidden stale body", Form: []request.Field{{Name: "file", Value: "/private/local/path", File: true}}}}, "")
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
		{URL: "http://example.com", BodyMode: "Multipart", Form: []request.Field{{Name: "file", Value: filepath.Join(t.TempDir(), "missing"), File: true}}},
		{URL: "http://example.com", BodyMode: "URL encoded", Form: []request.Field{{Name: "file", Value: "unused", File: true}}},
		{URL: "http://example.com", BodyMode: "URL encoded", Form: []request.Field{{Name: "big", Value: strings.Repeat("&", 6<<20)}}},
	} {
		if _, err := r.Prepare(nil); err == nil {
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
	r := savedRequest{Method: "GET", URL: upstream.URL, Auth: request.Auth{Type: "Bearer", Token: "{{token}}"},
		Pre:  `log("preparing"); vars.set("token","sent-token");`,
		Post: "log('received', res.status); vars.set('token','next-token');\nthrow new Error('post failed');"}
	got := r.Execute(t.Context(), srv, request.Context{}, map[string]string{"token": "old-token"}, nil)
	if <-headers != "Bearer sent-token" || got.Response == nil || got.Response.Status != 200 || got.Outcome() != "Post-response script failed" {
		t.Fatalf("wrong observable request/result: %s", runSummary(got))
	}
	if len(got.Console) != 5 || got.Console[0].Phase != "pre-request" || got.Console[2].Phase != "post-response" || got.Console[4].Kind != "error" || !strings.Contains(got.Console[4].Text, "post-response.js:2:") {
		t.Fatalf("console lost phase or error position: %+v", got.Console)
	}
	if got.Vars["token"] != "next-token" || len(got.Variables) != 1 || got.Variables[0].Value != "sent-token" || got.Variables[0].Source != "Pre-request script" {
		t.Fatalf("post-script rewrote send-time variable evidence: %+v", got.Variables)
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
	if err != nil || !reflect.DeepEqual(restored.Records[0].Console, got.Console) || !reflect.DeepEqual(restored.Records[0].Variables, got.Variables) {
		t.Fatal("diagnostics did not survive restart", err)
	}
	for _, src := range []string{"\n\nthrow new Error('before send');", "\n\nlet = ;"} {
		r.Pre = src
		failed := r.Execute(t.Context(), nil, request.Context{}, nil, nil)
		if failed.Response != nil || failed.Outcome() != "Pre-request script failed" || len(failed.Console) == 0 || !strings.Contains(runSummary(failed), "pre-request.js") || !strings.Contains(runSummary(failed), "3") {
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

// A collection's settings reach the requests filed under it: chosen from the
// sidebar, saved to the folder's folder.json, and shown where a request inherits.
func TestCollectionSettingsGiveRequestsTheirAuth(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.workspace = 1
	tt := ui.NewTester(a.view, 1280, 760)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("List projects")
	if err := tt.RightClick("List projects"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Folder “API” settings…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Folder · Workspace/API") {
		t.Fatal("the folder's settings did not open")
	}
	click("Authorization type")
	click("Bearer")
	click("Bearer token")
	tt.Type("{{vault:api}}")
	click("Save settings")
	data, err := os.ReadFile(filepath.Join(a.library.root, "Workspace", "API", "folder.json"))
	if err != nil || !strings.Contains(string(data), `{{vault:api}}`) {
		t.Fatalf("Workspace/API/folder.json = %s, %v", data, err)
	}
	click("Auth")
	click("Authorization type")
	click("Inherit")
	if !tt.HasText("Uses Bearer auth from Workspace/API.") {
		t.Fatal("an inheriting request does not say whose auth it sends")
	}
	in := a.inheritance(a.requestDocument.value().Collection)
	if auth, _ := in.InheritAuth(a.requestDocument.value().Auth); auth.Token != "{{vault:api}}" {
		t.Fatalf("inherited %+v", auth)
	}
}

// Postman's request workflows: setNextRequest loops and jumps, pm.variables
// carry through the run, and global and collection writes are saved.
func TestCollectionRunnerFollowsSetNextRequest(t *testing.T) {
	paths := make(chan string, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.RequestURI()
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"base": upstream.URL}}}
	a.activeEnv = "Local"
	a.requests = []savedRequest{
		{Request: request.Request{Name: "01 Start", Collection: "Loop", Method: "GET", URL: "{{base}}/start",
			Post: `pm.variables.set("count", "0"); pm.globals.set("started", "yes")`}},
		{Request: request.Request{Name: "02 Poll", Collection: "Loop", Method: "GET", URL: "{{base}}/poll?n={{count}}",
			Post: `const n = Number(pm.variables.get("count")) + 1
pm.variables.set("count", n)
pm.collectionVariables.set("polls", n)
pm.execution.setNextRequest(n < 3 ? "02 Poll" : "04 Done")`}},
		{Request: request.Request{Name: "03 Jumped over", Collection: "Loop", Method: "GET", URL: "{{base}}/never"}},
		{Request: request.Request{Name: "04 Done", Collection: "Loop", Method: "GET", URL: "{{base}}/done",
			Pre:  `pm.test("globals from earlier requests", () => pm.expect(pm.globals.get("started")).to.equal("yes"))`,
			Post: `pm.test("collection variables from earlier requests", () => pm.expect(pm.collectionVariables.get("polls")).to.equal("3")); pm.execution.setNextRequest(null)`}},
		{Request: request.Request{Name: "05 After the stop", Collection: "Loop", Method: "GET", URL: "{{base}}/never"}},
	}
	a.runner.collection = "Loop"
	a.openRunner()
	a.runner.iterations = "2"
	a.startRunner()
	waitRunner(t, a)
	var visited []string
	for len(paths) > 0 {
		visited = append(visited, <-paths)
	}
	if want := []string{"/start", "/poll?n=0", "/poll?n=1", "/poll?n=2", "/done"}; !reflect.DeepEqual(visited, want) {
		t.Fatalf("visited %v, want %v", visited, want)
	}
	if a.runner.failures != 0 || len(a.runner.rows[4].tests) != 2 {
		t.Fatalf("rows %+v", a.runner.rows)
	}
	if a.globals["started"] != "yes" || len(a.folders) != 1 || a.folders[0].Path != "Loop" ||
		!reflect.DeepEqual(a.folders[0].Variables, []request.Field{{Name: "polls", Value: "3"}}) {
		t.Errorf("saved globals %v, collections %+v", a.globals, a.folders)
	}
	if _, ok := a.activeVars()["count"]; ok {
		t.Error("pm.variables must not be saved to the environment")
	}
}

func TestImportPostmanCollectionAndEnvironment(t *testing.T) {
	a := workspaceApp()
	a.requests = nil
	a.importData([]byte(`{"info":{"name":"Shop","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"auth":{"type":"bearer","bearer":[{"key":"token","value":"{{vault:token}}"}]},
"variable":[{"key":"base","value":"https://api.example.com"}],
"item":[{"name":"Orders","item":[{"id":"r1","name":"List","request":{"method":"GET","url":"{{base}}/orders"},
"event":[{"listen":"test","script":{"exec":["pm.test(\"ok\", () => pm.response.to.be.ok)"]}}],
"response":[{"name":"example"}]}]}]}`), "shop.json")
	if len(a.requests) != 1 || a.requests[0].ID != "r1" || a.requests[0].Collection != "Shop/Orders" || a.requests[0].Auth.Type != request.AuthInherit ||
		a.requests[0].Post != `pm.test("ok", () => pm.response.to.be.ok)` {
		t.Fatalf("requests %+v", a.requests)
	}
	if in := a.inheritance("Shop/Orders"); in.Values(nil)["base"] != "https://api.example.com" {
		t.Errorf("collection variables did not arrive: %+v", a.folders)
	}
	if auth, from := a.inheritance("Shop/Orders").InheritAuth(a.requests[0].Auth); auth.Token != "{{vault:token}}" || from != "Shop" {
		t.Errorf("inherited auth %+v from %q", auth, from)
	}
	if len(a.requests[0].Examples) != 1 || a.requests[0].Examples[0].Name != "example" {
		t.Errorf("the saved example was not imported: %+v", a.requests[0].Examples)
	}
	// Importing it again updates the same request instead of adding a copy.
	a.importData([]byte(`{"info":{"name":"Shop","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"item":[{"name":"Orders","item":[{"id":"r1","name":"List","request":{"method":"POST","url":"{{base}}/orders"}}]}]}`), "shop.json")
	if len(a.requests) != 1 || a.requests[0].Method != "POST" {
		t.Errorf("re-import %+v", a.requests)
	}
	a.importData([]byte(`{"name":"Staging","values":[{"key":"base","value":"https://staging","enabled":true}],"_postman_variable_scope":"environment"}`), "staging.json")
	a.importData([]byte(`{"name":"g","values":[{"key":"team","value":"core"}],"_postman_variable_scope":"globals"}`), "globals.json")
	if !slices.ContainsFunc(a.envs, func(e environment) bool { return e.Name == "Staging" && e.Vars["base"] == "https://staging" }) || a.globals["team"] != "core" {
		t.Errorf("envs %+v, globals %v", a.envs, a.globals)
	}
}

func TestOpeningARequestWithoutHeadersLeavesItUnchanged(t *testing.T) {
	a := workspaceApp()
	r := savedRequest{Request: request.Request{ID: "no-headers", Name: "No headers", Method: "GET", URL: "https://example.com"}}
	a.addDocument(r, r.key())
	d := a.requestDocument
	d.composeTab = composeTabHeaders
	ui.NewTester(a.view, 1100, 700).Frame()
	if len(d.headerRows) != 1 || d.dirty() {
		t.Fatalf("header rows %+v, dirty %v: the table's blank row must not count as an edit", d.headerRows, d.dirty())
	}
}

// Renaming a folder from its settings moves its requests and settings; the
// request's tab follows and still inherits.
func TestRenamingAFolderFromItsSettings(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	if err := a.writeLibrary(a.requests, []request.Folder{{Path: "Workspace/API", Auth: request.Auth{Type: "Bearer", Token: "t"}}}); err != nil {
		t.Fatal(err)
	}
	a.workspace = 1
	tt := ui.NewTester(a.view, 1280, 760)
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	if err := tt.RightClick("List projects"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Folder “API” settings…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Folder name"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Public API")
	if err := tt.Click("Save settings"); err != nil {
		t.Fatal(err)
	}
	if a.folderUI.open || a.composeCollection != "Workspace/Public API" || a.dirty() {
		t.Fatalf("dialog open %v (%s), tab collection %q, dirty %v", a.folderUI.open, a.folderUI.problem, a.composeCollection, a.dirty())
	}
	if _, err := os.Stat(filepath.Join(a.library.root, "Workspace", "Public API", "folder.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.library.root, "Workspace", "API")); err == nil {
		t.Fatal("the old folder was left behind")
	}
	if auth, from := a.inheritance(a.composeCollection).InheritAuth(request.Auth{Type: request.AuthInherit}); auth.Token != "t" || from != "Workspace/Public API" {
		t.Fatalf("inherited %+v from %q", auth, from)
	}
}

// Each Runner iteration reads one data file row; more iterations than rows
// repeat the last one, and data values never reach the environment.
func TestCollectionRunnerIteratesOverDataRows(t *testing.T) {
	queries := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { queries <- r.URL.RawQuery }))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"base": upstream.URL, "sku": "env"}}}
	a.activeEnv = "Local"
	a.requests = []savedRequest{{Request: request.Request{Name: "Order", Collection: "Data", Method: "GET",
		URL: "{{base}}/?sku={{sku}}&qty={{qty}}&i={{i}}", Pre: `pm.variables.set("i", pm.info.iteration + "/" + pm.info.iterationCount)`}}}
	a.runner.collection = "Data"
	a.openRunner()
	rows, err := request.ReadIterationData("orders.csv", []byte("sku,qty\nA-1,2\nB-2,5\n"))
	a.runner.useData("orders.csv", rows, err)
	if a.runner.iterations != "2" {
		t.Fatalf("iterations = %q, want the row count", a.runner.iterations)
	}
	a.runner.iterations = "3"
	a.startRunner()
	waitRunner(t, a)
	var got []string
	for len(queries) > 0 {
		got = append(got, <-queries)
	}
	if want := []string{"sku=A-1&qty=2&i=0/3", "sku=B-2&qty=5&i=1/3", "sku=B-2&qty=5&i=2/3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queries %q, want %q", got, want)
	}
	if vars := a.activeVars(); vars["sku"] != "env" || vars["qty"] != "" || vars["i"] != "" {
		t.Errorf("the environment after the run = %v", vars)
	}
	a.runner.useData("broken.csv", nil, errors.New("CSV: wrong number of fields"))
	if a.runner.dataName != "orders.csv" || !strings.Contains(a.runner.dataProblem, "broken.csv") {
		t.Errorf("a bad file must keep the loaded one and say why: %+v", a.runner.dataProblem)
	}
}
