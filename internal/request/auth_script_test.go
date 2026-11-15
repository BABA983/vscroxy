package request

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// authAccess is the Go side of pm.request.auth; these are the Postman field
// names the import and export use.
func TestRequestAuthFieldsUsePostmanNames(t *testing.T) {
	check := func(t *testing.T, a *Auth, fields map[string]any) {
		t.Helper()
		acc := authAccess(a).(map[string]any)
		set := acc["set"].(func(string, string) error)
		get := acc["get"].(func(string) (any, error))
		for name, want := range fields {
			if err := set(name, "x"); err != nil {
				t.Errorf("%s: set %q: %v", a.Type, name, err)
			}
			got, err := get(name)
			if err != nil || got != want && want != "" {
				t.Errorf("%s: get %q = %v, %v; want %v", a.Type, name, got, err, want)
			}
		}
	}
	check(t, &Auth{Type: AuthOAuth2}, map[string]any{
		"authUrl": "x", "accessTokenUrl": "x", "clientId": "x", "clientSecret": "x",
		"scope": "x", "username": "x", "password": "x", "redirect_uri": "x", "headerPrefix": "x",
	})
	check(t, &Auth{Type: AuthOAuth1}, map[string]any{
		"consumerKey": "x", "consumerSecret": "x", "token": "x", "tokenSecret": "x", "signatureMethod": "x",
		"privateKey": "x", "timestamp": "x", "nonce": "x", "version": "x", "realm": "x", "callback": "x", "verifier": "x",
	})
	check(t, &Auth{Type: AuthHawk}, map[string]any{
		"authId": "x", "authKey": "x", "algorithm": "x", "user": "x", "nonce": "x", "timestamp": "x",
		"extraData": "x", "app": "x", "delegation": "x",
	})
	check(t, &Auth{Type: AuthAWS}, map[string]any{
		"accessKey": "x", "secretKey": "x", "region": "x", "service": "x", "sessionToken": "x",
	})
	check(t, &Auth{Type: AuthNTLM}, map[string]any{
		"username": "x", "password": "x", "domain": "x", "workstation": "x",
	})
	check(t, &Auth{Type: AuthJWT}, map[string]any{
		"algorithm": "x", "secret": "x", "privateKey": "x", "payload": "x", "header": "x",
		"headerPrefix": "x", "queryParamKey": "x",
	})
	check(t, &Auth{Type: AuthEdgeGrid}, map[string]any{
		"accessToken": "x", "clientToken": "x", "clientSecret": "x", "baseURL": "x",
		"nonce": "x", "timestamp": "x", "headersToSign": "x",
	})
}

func TestRequestAuthSwitchesAndBooleanFields(t *testing.T) {
	a := Auth{Type: "Bearer"}
	acc := authAccess(&a).(map[string]any)
	setType := acc["setType"].(func(string) error)
	for _, name := range []string{"oauth2", "oauth1", "hawk", "awsv4", "ntlm", "jwt", "edgegrid"} {
		if err := setType(name); err != nil {
			t.Fatalf("setType(%q): %v", name, err)
		}
	}
	if a.Type != AuthEdgeGrid {
		t.Fatalf("type = %q", a.Type)
	}
	acc = authAccess(&a).(map[string]any)
	set := acc["set"].(func(string, string) error)
	get := acc["get"].(func(string) (any, error))
	if err := set("headersToSign", "x-akamai"); err != nil {
		t.Fatal(err)
	}
	if got, _ := get("headersToSign"); got != "x-akamai" {
		t.Fatalf("headersToSign = %v", got)
	}

	ntlm := &Auth{Type: AuthNTLM}
	acc = authAccess(ntlm).(map[string]any)
	set = acc["set"].(func(string, string) error)
	if err := set("disableRetryRequest", "true"); err != nil || !ntlm.NTLM.DisableRetry {
		t.Fatalf("disableRetryRequest: %v, %+v", err, ntlm.NTLM)
	}
	if err := set("disableRetryRequest", "maybe"); err == nil {
		t.Fatal("a non-boolean was accepted")
	}

	jwt := &Auth{Type: AuthJWT}
	acc = authAccess(jwt).(map[string]any)
	set = acc["set"].(func(string, string) error)
	if err := set("addTokenTo", "queryParam"); err != nil || jwt.JWT.AddTo != "Query" {
		t.Fatalf("addTokenTo: %v, %+v", err, jwt.JWT)
	}
	if err := set("isSecretBase64Encoded", "true"); err != nil || !jwt.JWT.SecretBase64 {
		t.Fatalf("isSecretBase64Encoded: %v, %+v", err, jwt.JWT)
	}

	oauth2 := &Auth{Type: AuthOAuth2}
	acc = authAccess(oauth2).(map[string]any)
	set = acc["set"].(func(string, string) error)
	if err := set("grant_type", "implicit"); err != nil || oauth2.OAuth2.Grant != GrantImplicit {
		t.Fatalf("grant_type: %v, %+v", err, oauth2.OAuth2)
	}
	if err := set("grant_type", "not-a-grant"); err == nil {
		t.Fatal("an unknown grant was accepted")
	}
	if err := set("addTokenTo", "queryParams"); err == nil {
		t.Fatal("an unsupported token location was accepted")
	}
}

// A pre-request script can build an OAuth 2.0 configuration and the send uses
// it, without touching the saved document.
func TestRequestAuthScriptBuildsOAuth2(t *testing.T) {
	p := newProvider(t)
	var sent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("Authorization")
	}))
	defer upstream.Close()
	srv := testProxy(t)
	pre := `pm.request.auth.type = "oauth2";
		pm.expect(pm.request.auth.get("grant_type")).to.equal("authorization_code_with_pkce");
		pm.request.auth.set("grant_type", "client_credentials");
		pm.request.auth.set("accessTokenUrl", "` + p.URL + `/token");
		pm.request.auth.set("clientId", "app");
		pm.request.auth.set("scope", "read");`
	r := Request{Method: "GET", URL: upstream.URL, Auth: Auth{Type: "Bearer"}, Pre: pre}
	out := r.Execute(t.Context(), srv, Context{Tokens: &Tokens{}}, nil, nil)
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	if sent != "Bearer cc-1" {
		t.Fatalf("sent %q", sent)
	}
}
