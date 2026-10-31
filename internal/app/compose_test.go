package app

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

func TestCapturedDraftUsesCompleteDecodedPayload(t *testing.T) {
	body := strings.Repeat("中文\x00x", (maxBodyView/8)+1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer zr.Close()
			reader = zr
		}
		got, _ := io.ReadAll(reader)
		if string(got) != body || len(r.Header.Values("X-Repeat")) != 2 || r.URL.RawQuery != "q=one&q=two" {
			http.Error(w, "payload or headers changed", 400)
			return
		}
		w.WriteHeader(201)
	}))
	defer upstream.Close()
	srv := requestProxy(t)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	zw.Write([]byte(body))
	zw.Close()
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+"?q=one&q=two", &compressed)
	req.Header = http.Header{"Content-Encoding": {"gzip"}, "X-Repeat": {"a", "b"}}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a := workspaceApp()
	a.flows = nil
	select {
	case captured := <-srv.Flows():
		a.pending = []capture.Flow{captured}
	case <-time.After(5 * time.Second):
		t.Fatal("no captured request")
	}
	a.drain()
	f := a.flows[0]
	if f.reqFile == "" || f.reqSize != len(body) || f.reqHeader.Get("Content-Encoding") != "gzip" {
		t.Fatal("test must exercise a real decoded, spilled gzip capture")
	}
	if d := a.editCapture(f); d == nil || d.composeBody != body {
		t.Fatal("draft did not retain the full spilled body")
	}
	file := filepath.Join(t.TempDir(), "requests.json")
	if err := saveRequests(file, []savedRequest{a.requestDocument.value()}); err != nil {
		t.Fatal(err)
	}
	list, err := loadRequests(file)
	if err != nil {
		t.Fatal(err)
	}
	got := list[0].Execute(t.Context(), srv, request.Context{}, nil, nil)
	if got.Err != nil || got.Response == nil || got.Response.Status != 201 {
		t.Fatalf("replay failed: %+v, response %+v", got, got.Response)
	}
	a.requests = nil
	a.saveCapture(f)
	if len(a.requests) != 1 || a.requests[0].Body != body || request.ParseHeaderBlock(a.requests[0].Headers).Get("Content-Encoding") != "" {
		t.Fatal("save captured request bypassed conversion contract")
	}
	a.regressionFrom(f)
	if a.composeBody != body || a.origin == nil || a.checks.Status != "201" || request.ParseHeaderBlock(a.composeHeaders).Get("Content-Encoding") != "" {
		t.Fatal("regression draft bypassed the complete decoded conversion contract")
	}
}

func TestCapturedDraftRejectsUnavailablePayload(t *testing.T) {
	for _, f := range []flow{
		{reqBody: "abc", reqSize: 20},
		{reqBody: "abc", reqError: "interrupted"},
		{reqFile: filepath.Join(t.TempDir(), "missing")},
		{reqBody: string([]byte{0xff, 0x00})},
		{reqTrailer: http.Header{"X-Trailer": {"value"}}},
		{kind: "connect"},
		{method: "CONNECT"}, // Imported sessions may not carry a Kind.
	} {
		a := workspaceApp()
		a.requests = nil
		a.editCapture(f)
		a.regressionFrom(f)
		a.saveCapture(f)
		if len(a.documents) != 0 || len(a.requests) != 0 || a.message == "" {
			t.Fatalf("unreplayable capture silently converted: %+v", f)
		}
	}
}

// A draft is a new request on its own connection: the client's connection
// headers, proxy credentials and headers Connection names never carry over.
func TestCapturedDraftDropsConnectionHeaders(t *testing.T) {
	f := flow{method: "GET", url: "http://api.example.test/", reqHeader: http.Header{
		"Proxy-Connection":    {"Keep-Alive"},
		"Proxy-Authorization": {"Basic cHJveHk6c2VjcmV0"},
		"Connection":          {"keep-alive, X-Hop"},
		"X-Hop":               {"1"},
		"Keep-Alive":          {"timeout=5"},
		"X-Kept":              {"a", "b"},
		"Accept":              {"*/*"},
	}}
	r, err := f.requestDraft()
	if err != nil {
		t.Fatal(err)
	}
	h := request.ParseHeaderBlock(r.Headers)
	for _, name := range []string{"Proxy-Connection", "Proxy-Authorization", "Connection", "X-Hop", "Keep-Alive"} {
		if h.Get(name) != "" {
			t.Errorf("draft kept %s", name)
		}
	}
	if len(h.Values("X-Kept")) != 2 || h.Get("Accept") != "*/*" {
		t.Errorf("draft lost end-to-end headers: %q", r.Headers)
	}
}

// A captured Authorization header moves to the Auth tab only when the tab
// sends the same bytes again; anything else stays a plain header.
func TestCapturedAuthorizationBecomesAuthOnlyWhenExact(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte("ann:pa:ss"))
	for _, tc := range []struct {
		values []string
		auth   string
	}{
		{[]string{"Bearer abc.def"}, "Bearer"},
		{[]string{"Basic " + basic}, "Basic"},
		{[]string{"bearer abc"}, ""},                                   // scheme spelling would change
		{[]string{"Bearer abc", "Bearer xyz"}, ""},                     // only one header survives an Auth tab
		{[]string{"Basic " + strings.TrimRight(basic, "=") + "x"}, ""}, // not canonical base64
		{[]string{"Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon"))}, ""},
		{[]string{"Digest username=ann"}, ""},
	} {
		f := flow{method: "GET", url: "https://api.example.test/me", reqHeader: http.Header{"Authorization": tc.values, "Accept": {"*/*"}}}
		r, err := f.requestDraft()
		if err != nil {
			t.Fatal(err)
		}
		if r.Auth.Type != tc.auth {
			t.Errorf("%q: auth = %q, want %q", tc.values, r.Auth.Type, tc.auth)
		}
		req, err := r.Prepare(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Values("Authorization"); !slices.Equal(got, tc.values) {
			t.Errorf("%q: draft sends Authorization %q", tc.values, got)
		}
	}
	r, _ := (flow{method: "GET", url: "https://x.test", reqHeader: http.Header{"Authorization": {"Basic " + basic}}}).requestDraft()
	if r.Auth.Username != "ann" || r.Auth.Password != "pa:ss" {
		t.Errorf("Basic split at the first colon: %+v", r.Auth)
	}
}

func TestEnvironmentTablePreservesValuesAndRejectsDuplicateKeys(t *testing.T) {
	a := workspaceApp()
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.openVars()
	a.varsRows = []request.Field{{Name: "base", Value: "http://a.test"}, {Name: "empty"}, {Name: "spaced", Value: "  = x  "}}
	a.saveVars()
	f, err := loadEnvs(a.envsPath)
	envs := f.Envs
	if err != nil || len(envs) != 1 || envs[0].Vars["spaced"] != "  = x  " || len(envs[0].Vars) != 3 {
		t.Fatalf("environment table lost literal values: %+v %v", envs, err)
	}
	a.openVars()
	a.varsRows = append(a.varsRows, request.Field{Name: " base ", Value: "replacement"})
	a.saveVars()
	if !a.varsOpen || a.varsProblem == "" || a.activeVars()["base"] != "http://a.test" {
		t.Fatal("duplicate key silently replaced a value")
	}
}

func TestEnvsRoundTrip(t *testing.T) {
	path := t.TempDir() + "/env.json"
	envs := []environment{
		{Name: "dev", Vars: map[string]string{"base": "http://127.0.0.1:8099"}},
		{Name: "live", Vars: map[string]string{"base": "https://example.com"}},
	}
	if err := saveEnvs(path, envFile{Active: "live", Envs: envs}); err != nil {
		t.Fatalf("save: %v", err)
	}
	f, err := loadEnvs(path)
	got, active := f.Envs, f.Active
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if active != "live" {
		t.Errorf("active = %q, want live", active)
	}
	if len(got) != 2 || got[0].Name != "dev" || got[0].Vars["base"] != "http://127.0.0.1:8099" {
		t.Fatalf("envs = %+v", got)
	}
	if missing, err := loadEnvs(t.TempDir() + "/none.json"); err != nil || missing.Envs != nil || missing.Active != "" {
		t.Errorf("missing file = %+v, %v; want nothing", missing, err)
	}
}
