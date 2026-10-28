package request

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/vault"
)

// provider is an OAuth 2.0 server that checks what clients send.
type provider struct {
	*httptest.Server
	mu        sync.Mutex
	challenge string
	grants    []string // grant_type of each token request, in order
	clients   []string // how each token request authenticated the client
	refusing  bool     // refresh tokens are refused
}

func newProvider(t *testing.T) *provider {
	p := &provider{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch r.URL.Path {
		case "/authorize":
			q := r.URL.Query()
			p.challenge = q.Get("code_challenge")
			if q.Get("response_type") != "code" || q.Get("client_id") != "app" || (p.challenge != "" && q.Get("code_challenge_method") != "S256") {
				http.Error(w, "bad authorization request", 400)
				return
			}
			back, _ := url.Parse(q.Get("redirect_uri"))
			back.RawQuery = url.Values{"code": {"the-code"}, "state": {q.Get("state")}}.Encode()
			http.Redirect(w, r, back.String(), http.StatusFound)
		case "/token":
			r.ParseForm()
			grant := r.PostForm.Get("grant_type")
			p.grants = append(p.grants, grant)
			if id, secret, ok := r.BasicAuth(); ok {
				id, _ = url.QueryUnescape(id)
				secret, _ = url.QueryUnescape(secret)
				p.clients = append(p.clients, "basic "+id+":"+secret)
			} else {
				p.clients = append(p.clients, "body "+r.PostForm.Get("client_id")+":"+r.PostForm.Get("client_secret"))
			}
			w.Header().Set("Content-Type", "application/json")
			switch grant {
			case "client_credentials":
				fmt.Fprintf(w, `{"access_token":"cc-%d","token_type":"Bearer","expires_in":"3600","scope":"%s"}`, len(p.grants), r.PostForm.Get("scope"))
			case "password":
				if r.PostForm.Get("username") != "ann" || r.PostForm.Get("password") != "pw" {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant","error_description":"wrong password"}`)
					return
				}
				fmt.Fprint(w, `{"access_token":"pw-token","expires_in":3600}`)
			case "authorization_code":
				sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
				if r.PostForm.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge || !strings.HasPrefix(r.PostForm.Get("redirect_uri"), "http://127.0.0.1:") {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant"}`)
					return
				}
				// Expires at once, so the next send must refresh it.
				fmt.Fprint(w, `{"access_token":"ac-1","refresh_token":"r-1","expires_in":1}`)
			case "refresh_token":
				if p.refusing || r.PostForm.Get("refresh_token") != "r-1" {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant"}`)
					return
				}
				fmt.Fprint(w, `{"access_token":"ac-2","expires_in":3600}`)
			}
		default:
			fmt.Fprint(w, r.Header.Get("Authorization"))
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *provider) log() (grants, clients []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.grants...), append([]string(nil), p.clients...)
}

func TestOAuth2TokensWithoutABrowser(t *testing.T) {
	p := newProvider(t)
	srv := testProxy(t)
	tokens := &Tokens{}
	secrets := vault.Secrets{{Name: "secret", Value: "s/1 &", Domains: []string{"127.0.0.1"}}}
	in := Context{Tokens: tokens, Vault: secrets.Lookup, Folders: []Folder{{Path: "Shop", Auth: Auth{Type: AuthOAuth2, OAuth2: OAuth2{
		Grant: GrantClientCredentials, TokenURL: "{{idp}}/token", ClientID: "my app", ClientSecret: "{{vault:secret}}", Scope: "read write"}}}},
		Globals: map[string]string{"idp": p.URL}}
	r := Request{Method: "GET", URL: p.URL + "/api", Collection: "Shop", Auth: Auth{Type: AuthInherit}}
	for range 2 {
		out := r.Execute(t.Context(), srv, in, nil, nil)
		if out.Err != nil || out.Response.ResponseBody != "Bearer cc-1" {
			t.Fatalf("%s: %v; sent %q", out.Stage, out.Err, out.Response.ResponseBody)
		}
	}
	grants, clients := p.log()
	if strings.Join(grants, ",") != "client_credentials" || clients[0] != "basic my app:s/1 &" {
		t.Errorf("token requests %q by %q: the second send must reuse the token", grants, clients)
	}
	if got, _ := tokens.Peek(in.Folders[0].Auth.OAuth2.Expand(in.Globals)); got.Scope != "read write" || got.Expiry.IsZero() {
		t.Errorf("kept token %+v", got)
	}

	// The vault secret goes only to the hosts it allows: here, not the
	// token endpoint, so nothing is requested.
	in.Vault = vault.Secrets{{Name: "secret", Value: "s", Domains: []string{"login.example.com"}}}.Lookup
	in.Tokens = &Tokens{}
	if out := r.Execute(t.Context(), srv, in, nil, nil); out.Err == nil || out.Stage != "Access token failed" || !strings.Contains(out.Err.Error(), "not allowed") {
		t.Errorf("refused secret: %s: %v", out.Stage, out.Err)
	}
	if grants, _ := p.log(); len(grants) != 1 {
		t.Errorf("the token endpoint was asked again: %q", grants)
	}

	pw := Request{Method: "GET", URL: p.URL + "/api", Auth: Auth{Type: AuthOAuth2, OAuth2: OAuth2{
		Grant: GrantPassword, TokenURL: p.URL + "/token", ClientID: "app", ClientSecret: "s", ClientAuth: "Body",
		Username: "ann", Password: "{{pw}}", HeaderPrefix: "Token"}}}
	out := pw.Execute(t.Context(), srv, Context{}, map[string]string{"pw": "pw"}, nil)
	if out.Err != nil || out.Response.ResponseBody != "Token pw-token" {
		t.Fatalf("password grant: %s: %v", out.Stage, out.Err)
	}
	if _, clients := p.log(); clients[len(clients)-1] != "body app:s" {
		t.Errorf("client in body = %q", clients[len(clients)-1])
	}
	out = pw.Execute(t.Context(), srv, Context{}, map[string]string{"pw": "wrong"}, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "invalid_grant): wrong password") {
		t.Errorf("a refused password = %v", out.Err)
	}
	out = pw.Execute(t.Context(), srv, Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "{{pw}}") {
		t.Errorf("an undefined variable = %v", out.Err)
	}
}

func TestOAuth2AuthorizationCodeSignsInThroughTheBrowser(t *testing.T) {
	p := newProvider(t)
	srv := testProxy(t)
	tokens := &Tokens{}
	auth := Auth{Type: AuthOAuth2, OAuth2: OAuth2{AuthURL: p.URL + "/authorize", TokenURL: p.URL + "/token", ClientID: "app", Scope: "openid"}}
	r := Request{Method: "GET", URL: p.URL + "/api", Auth: auth}

	// Without a token, a send asks the user to sign in rather than open a browser.
	if out := r.Execute(t.Context(), srv, Context{Tokens: tokens}, nil, nil); !errors.Is(out.Err, ErrSignInNeeded) {
		t.Fatalf("a send without a token = %v", out.Err)
	}
	// The "browser" follows the authorization server's redirect back to the
	// loopback listener.
	var opened string
	browser := func(u string) error {
		opened = u
		res, err := http.Get(u)
		if err == nil {
			res.Body.Close()
		}
		return err
	}
	if _, err := tokens.New(t.Context(), auth.OAuth2, ProxySender(t.Context(), srv), browser); err != nil {
		t.Fatal(err)
	}
	if q, _ := url.Parse(opened); q.Query().Get("code_challenge") == "" || q.Query().Get("scope") != "openid" {
		t.Errorf("authorization URL %q", opened)
	}
	// The token expired at once; the send refreshes it and keeps the
	// refresh token the server did not replace.
	out := r.Execute(t.Context(), srv, Context{Tokens: tokens}, nil, nil)
	if out.Err != nil || out.Response.ResponseBody != "Bearer ac-2" {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if got, _ := tokens.Peek(auth.OAuth2); got.Refresh != "r-1" {
		t.Errorf("kept refresh token %q", got.Refresh)
	}
	grants, clients := p.log()
	if strings.Join(grants, ",") != "authorization_code,refresh_token" || clients[0] != "body app:" {
		t.Errorf("token requests %q by %q", grants, clients)
	}

	// A browser that comes back with another state is refused.
	forged := func(u string) error {
		q, _ := url.Parse(u)
		back, _ := url.Parse(q.Query().Get("redirect_uri"))
		back.RawQuery = "code=the-code&state=forged"
		res, err := http.Get(back.String())
		if err == nil {
			res.Body.Close()
		}
		return err
	}
	if _, err := tokens.New(t.Context(), auth.OAuth2, ProxySender(t.Context(), srv), forged); err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("forged state = %v", err)
	}
	auth.OAuth2.RedirectURL = "https://oauth.pstmn.io/v1/callback"
	if _, err := tokens.New(t.Context(), auth.OAuth2, ProxySender(t.Context(), srv), browser); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("a non-loopback callback = %v", err)
	}
}

func TestIterationDataFiles(t *testing.T) {
	csv := "\uFEFFid, qty\r\n1,\"2,5\"\r\n7,\n"
	rows, err := ReadIterationData("orders.csv", []byte(csv))
	if err != nil || len(rows) != 2 || rows[0]["id"] != "1" || rows[0]["qty"] != "2,5" || rows[1]["qty"] != "" {
		t.Fatalf("CSV rows %v, %v", rows, err)
	}
	rows, err = ReadIterationData("users.JSON", []byte(`[{"name":"ann","age":31,"tags":["a"],"ok":true,"none":null}]`))
	if err != nil || rows[0]["name"] != "ann" || rows[0]["age"] != "31" || rows[0]["tags"] != `["a"]` || rows[0]["ok"] != "true" || rows[0]["none"] != "null" {
		t.Fatalf("JSON rows %v, %v", rows, err)
	}
	for name, data := range map[string]string{
		"a.csv": "id,id\n1,2", "b.csv": "id,\n1,2", "c.csv": "id,qty\n1", "d.csv": "", "e.json": `{"id":1}`, "f.json": `[1]`, "g.csv": "id\n",
	} {
		if _, err := ReadIterationData(name, []byte(data)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A data row sits over the environment and under pm.variables, and is
// never written back to the environment.
func TestIterationDataResolvesBetweenEnvironmentAndLocals(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.URL.RawQuery }))
	defer upstream.Close()
	r := Request{Method: "GET", URL: upstream.URL + "/?a={{a}}&b={{b}}&c={{c}}",
		Pre: `pm.variables.set("c", "local"); pm.test("data", () => pm.expect(pm.iterationData.get("a")).to.equal("data"))`}
	in := Context{Data: map[string]string{"a": "data", "b": "data", "c": "data"}, Iteration: 1, Iterations: 3}
	out := r.Execute(t.Context(), testProxy(t), in, map[string]string{"a": "env"}, nil)
	if out.Err != nil || got != "a=data&b=data&c=local" || len(out.Tests) != 1 || !out.Tests[0].Pass {
		t.Fatalf("%s: %v; query %q; tests %+v", out.Stage, out.Err, got, out.Tests)
	}
	if out.Vars["a"] != "env" || len(out.Vars) != 1 {
		t.Errorf("the environment after the send = %v", out.Vars)
	}
	sources := map[string]string{}
	for _, v := range out.Variables {
		sources[v.Name] = v.Source
	}
	if sources["a"] != "Data file" || sources["c"] != "Pre-request script" {
		t.Errorf("sources %v", sources)
	}
}
