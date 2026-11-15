package request

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// AuthOAuth2 is the Auth type that sends an OAuth 2.0 access token.
const AuthOAuth2 = "OAuth 2.0"

// The grants a client can get a token with.
const (
	GrantAuthorizationCodePKCE = "Authorization code (PKCE)"
	GrantAuthorizationCode     = "Authorization code"
	GrantImplicit              = "Implicit"
	GrantClientCredentials     = "Client credentials"
	GrantPassword              = "Password"
)

// Grants lists the grants, the default first.
var Grants = []string{GrantAuthorizationCodePKCE, GrantAuthorizationCode, GrantImplicit, GrantClientCredentials, GrantPassword}

// OAuth2 is how a request gets its access token. Every field may use
// {{variables}}; a {{vault:name}} is unlocked for the token URL's host, the
// one that receives it.
type OAuth2 struct {
	Grant        string `json:"grant,omitempty"`
	AuthURL      string `json:"authUrl,omitempty"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	// RedirectURL is the loopback address the browser returns to, empty for
	// http://127.0.0.1 on any free port.
	RedirectURL string `json:"redirectUrl,omitempty"`
	// ClientAuth is "Body" to send the client's credentials in the token
	// request's body instead of a Basic header.
	ClientAuth   string `json:"clientAuth,omitempty"`
	HeaderPrefix string `json:"headerPrefix,omitempty"`
}

func (o OAuth2) grant() string { return cmp.Or(o.Grant, GrantAuthorizationCodePKCE) }

// interactive says the grant needs the user to sign in in a browser.
func (o OAuth2) interactive() bool {
	switch o.grant() {
	case GrantAuthorizationCodePKCE, GrantAuthorizationCode, GrantImplicit:
		return true
	}
	return false
}

// Label names the configuration in the token list, without secrets.
func (o OAuth2) Label() string {
	raw := o.TokenURL
	if o.grant() == GrantImplicit {
		raw = o.AuthURL
	}
	host := ""
	if u, err := url.Parse(raw); err == nil {
		host = u.Host
	}
	var parts []string
	for _, part := range []string{o.grant(), o.ClientID, host} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// fields are the configuration's texts, for expansion and variable lists.
func (o *OAuth2) fields() []*string {
	return []*string{&o.AuthURL, &o.TokenURL, &o.ClientID, &o.ClientSecret, &o.Scope, &o.Username, &o.Password, &o.RedirectURL, &o.HeaderPrefix}
}

// Variables are the {{names}} the configuration uses.
func (o OAuth2) Variables() []string {
	var names []string
	for _, f := range o.fields() {
		names = append(names, Request{URL: *f}.Variables()...)
	}
	return names
}

// Expand fills the {{variables}} of every field from vars.
func (o OAuth2) Expand(vars map[string]string) OAuth2 {
	for _, f := range o.fields() {
		*f = ExpandVars(*f, vars)
	}
	return o
}

// Key identifies the token an expanded configuration gets: who asks for
// what, where. Secrets are not part of it.
func (o OAuth2) Key() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{o.grant(), o.TokenURL, o.AuthURL, o.ClientID, o.Scope, o.Username}, "\x00")))
	return fmt.Sprintf("%x", sum[:12])
}

// Token is an access token and what came with it. Key, Label and Config let
// the token list show and refresh it; they are not part of a token response.
type Token struct {
	Access  string    `json:"access"`
	Type    string    `json:"type,omitempty"`
	Refresh string    `json:"refresh,omitempty"`
	Scope   string    `json:"scope,omitempty"`
	Expiry  time.Time `json:"expiry,omitzero"`
	Key     string    `json:"key,omitempty"`
	Label   string    `json:"label,omitempty"`
	Config  OAuth2    `json:"config,omitzero"`
}

// Fresh says the token can be sent: it does not expire within 30 seconds.
func (t Token) Fresh(now time.Time) bool {
	return t.Access != "" && (t.Expiry.IsZero() || now.Add(30*time.Second).Before(t.Expiry))
}

// Sender sends a request through the proxy, so it is captured too.
type Sender func(*Prepared) (capture.Flow, error)

// ProxySender sends through srv; its sends belong to the run ctx is tagged with.
func ProxySender(ctx context.Context, srv *capture.Server) Sender {
	runKey, _ := ctx.Value(debugRunKey{}).(string)
	return func(req *Prepared) (capture.Flow, error) {
		return srv.ReplayWithCookies(ctx, capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body}, nil, true)
	}
}

// ErrSignInNeeded is what a send fails with when its grant needs a browser
// sign-in and there is no token to use or refresh.
var ErrSignInNeeded = errors.New("no OAuth 2.0 access token: choose “Get new access token” in the Auth settings first")

// tokenRequest posts form to the token URL and reads the token it returns.
func (o OAuth2) tokenRequest(send Sender, form url.Values) (Token, error) {
	u, err := url.Parse(o.TokenURL)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return Token{}, fmt.Errorf("the access token URL %q must be an absolute HTTP(S) URL", o.TokenURL)
	}
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}}
	if o.ClientAuth == "Body" || o.ClientSecret == "" {
		form.Set("client_id", o.ClientID)
		if o.ClientSecret != "" {
			form.Set("client_secret", o.ClientSecret)
		}
	} else {
		// RFC 6749 §2.3.1: both parts are form-encoded before Basic encoding.
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(o.ClientID)+":"+url.QueryEscape(o.ClientSecret))))
	}
	got, err := send(&Prepared{Method: "POST", URL: o.TokenURL, Header: header, Body: form.Encode()})
	if err != nil {
		return Token{}, fmt.Errorf("token request: %w", err)
	}
	var body struct {
		Access      string `json:"access_token"`
		Type        string `json:"token_type"`
		Refresh     string `json:"refresh_token"`
		Scope       string `json:"scope"`
		ExpiresIn   any    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal([]byte(got.ResponseBody), &body) != nil {
		// Some servers answer form-encoded whatever Accept says.
		if values, err := url.ParseQuery(got.ResponseBody); err == nil {
			body.Access, body.Type, body.Refresh, body.Scope = values.Get("access_token"), values.Get("token_type"), values.Get("refresh_token"), values.Get("scope")
			body.ExpiresIn, body.Error, body.Description = values.Get("expires_in"), values.Get("error"), values.Get("error_description")
		}
	}
	if body.Error != "" {
		return Token{}, fmt.Errorf("the token endpoint refused (%d %s): %s", got.Status, body.Error, cmp.Or(body.Description, "no description"))
	}
	if got.Status >= 400 || body.Access == "" {
		return Token{}, fmt.Errorf("the token endpoint answered %d without an access token", got.Status)
	}
	t := Token{Access: body.Access, Type: body.Type, Refresh: body.Refresh, Scope: cmp.Or(body.Scope, form.Get("scope"))}
	var seconds float64
	switch v := body.ExpiresIn.(type) {
	case float64:
		seconds = v
	case string:
		seconds, _ = strconv.ParseFloat(v, 64)
	}
	if seconds > 0 {
		t.Expiry = time.Now().Add(time.Duration(seconds * float64(time.Second)))
	}
	return t, nil
}

// fetch gets a token without a browser: client credentials or password.
func (o OAuth2) fetch(send Sender) (Token, error) {
	form := url.Values{}
	switch o.grant() {
	case GrantClientCredentials:
		form.Set("grant_type", "client_credentials")
	case GrantPassword:
		form.Set("grant_type", "password")
		form.Set("username", o.Username)
		form.Set("password", o.Password)
	default:
		return Token{}, ErrSignInNeeded
	}
	if o.Scope != "" {
		form.Set("scope", o.Scope)
	}
	return o.tokenRequest(send, form)
}

// refresh trades a refresh token for a new access token. A server that
// returns no new refresh token keeps the old one valid.
func (o OAuth2) refresh(send Sender, old Token) (Token, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.Refresh}}
	if o.Scope != "" {
		form.Set("scope", o.Scope)
	}
	t, err := o.tokenRequest(send, form)
	if err == nil && t.Refresh == "" {
		t.Refresh = old.Refresh
	}
	return t, err
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// authorize signs in through the browser (RFC 8252): it listens on a
// loopback address, opens the authorization URL with open, and trades the
// code the browser brings back for a token.
func (o OAuth2) authorize(ctx context.Context, send Sender, open func(string) error) (Token, error) {
	if o.grant() == GrantImplicit {
		return o.authorizeImplicit(ctx, open)
	}
	auth, err := url.Parse(o.AuthURL)
	if err != nil || auth.Host == "" || auth.Scheme != "http" && auth.Scheme != "https" {
		return Token{}, fmt.Errorf("the authorization URL %q must be an absolute HTTP(S) URL", o.AuthURL)
	}
	redirect, err := url.Parse(cmp.Or(o.RedirectURL, "http://127.0.0.1/callback"))
	if err != nil || redirect.Scheme != "http" || (redirect.Hostname() != "127.0.0.1" && redirect.Hostname() != "localhost" && redirect.Hostname() != "[::1]" && redirect.Hostname() != "::1") {
		return Token{}, fmt.Errorf("the callback URL %q must be http://127.0.0.1, localhost or [::1], with an optional port and path", o.RedirectURL)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(redirect.Hostname(), cmp.Or(redirect.Port(), "0")))
	if err != nil {
		return Token{}, fmt.Errorf("listen for the browser's return: %w", err)
	}
	defer listener.Close()
	redirect.Host = net.JoinHostPort(redirect.Hostname(), strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	redirect.Path = cmp.Or(redirect.Path, "/")

	state, verifier := randomToken(), randomToken()
	q := auth.Query()
	q.Set("response_type", "code")
	q.Set("client_id", o.ClientID)
	q.Set("redirect_uri", redirect.String())
	q.Set("state", state)
	if o.Scope != "" {
		q.Set("scope", o.Scope)
	}
	if o.grant() == GrantAuthorizationCodePKCE {
		sum := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	auth.RawQuery = q.Encode()

	type answer struct {
		code string
		err  error
	}
	answers := make(chan answer, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != redirect.Path {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var got answer
		switch {
		case q.Get("state") != state:
			got.err = errors.New("the browser returned a different state; sign in again")
		case q.Get("error") != "":
			got.err = fmt.Errorf("the authorization server refused: %s %s", q.Get("error"), q.Get("error_description"))
		case q.Get("code") == "":
			got.err = errors.New("the browser returned without an authorization code")
		default:
			got.code = q.Get("code")
		}
		message := "Signed in. You can close this tab and return to vscroxy."
		if got.err != nil {
			message = "Sign-in failed: " + got.err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>vscroxy</title><p style=\"font:16px system-ui;margin:3em\">%s</p>", html.EscapeString(message))
		select {
		case answers <- got:
		default:
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	if err := open(auth.String()); err != nil {
		return Token{}, fmt.Errorf("open the browser: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var got answer
	select {
	case got = <-answers:
	case <-ctx.Done():
		return Token{}, fmt.Errorf("waited for the browser sign-in: %w", ctx.Err())
	}
	if got.err != nil {
		return Token{}, got.err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {got.code}, "redirect_uri": {redirect.String()}}
	if o.grant() == GrantAuthorizationCodePKCE {
		form.Set("code_verifier", verifier)
	}
	return o.tokenRequest(send, form)
}

// authorizeImplicit signs in through the browser with the implicit grant.
// The provider returns the token in the redirect fragment, which the browser
// never sends to a server, so the callback page posts it back to the loopback
// listener.
func (o OAuth2) authorizeImplicit(ctx context.Context, open func(string) error) (Token, error) {
	auth, err := url.Parse(o.AuthURL)
	if err != nil || auth.Host == "" || auth.Scheme != "http" && auth.Scheme != "https" {
		return Token{}, fmt.Errorf("the authorization URL %q must be an absolute HTTP(S) URL", o.AuthURL)
	}
	redirect, err := url.Parse(cmp.Or(o.RedirectURL, "http://127.0.0.1/callback"))
	if err != nil || redirect.Scheme != "http" || (redirect.Hostname() != "127.0.0.1" && redirect.Hostname() != "localhost" && redirect.Hostname() != "[::1]" && redirect.Hostname() != "::1") {
		return Token{}, fmt.Errorf("the callback URL %q must be http://127.0.0.1, localhost or [::1], with an optional port and path", o.RedirectURL)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(redirect.Hostname(), cmp.Or(redirect.Port(), "0")))
	if err != nil {
		return Token{}, fmt.Errorf("listen for the browser's return: %w", err)
	}
	defer listener.Close()
	redirect.Host = net.JoinHostPort(redirect.Hostname(), strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	redirect.Path = cmp.Or(redirect.Path, "/")

	state := randomToken()
	q := auth.Query()
	q.Set("response_type", "token")
	q.Set("client_id", o.ClientID)
	q.Set("redirect_uri", redirect.String())
	q.Set("state", state)
	if o.Scope != "" {
		q.Set("scope", o.Scope)
	}
	auth.RawQuery = q.Encode()

	type answer struct {
		token Token
		err   error
	}
	answers := make(chan answer, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != redirect.Path {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, implicitCallbackPage)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		form := r.PostForm
		var got answer
		switch {
		case form.Get("state") != state:
			got.err = errors.New("the browser returned a different state; sign in again")
		case form.Get("error") != "":
			got.err = fmt.Errorf("the authorization server refused: %s %s", form.Get("error"), form.Get("error_description"))
		case form.Get("access_token") == "":
			got.err = errors.New("the browser returned without an access token")
		default:
			got.token = Token{Access: form.Get("access_token"), Type: form.Get("token_type"), Scope: form.Get("scope")}
			if seconds, err := strconv.ParseFloat(form.Get("expires_in"), 64); err == nil && seconds > 0 {
				got.token.Expiry = time.Now().Add(time.Duration(seconds * float64(time.Second)))
			}
		}
		message := "Signed in. You can close this tab and return to vscroxy."
		if got.err != nil {
			message = "Sign-in failed: " + got.err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>vscroxy</title><p style=\"font:16px system-ui;margin:3em\">%s</p>", html.EscapeString(message))
		select {
		case answers <- got:
		default:
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	if err := open(auth.String()); err != nil {
		return Token{}, fmt.Errorf("open the browser: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case got := <-answers:
		if got.err != nil {
			return Token{}, got.err
		}
		return got.token, nil
	case <-ctx.Done():
		return Token{}, fmt.Errorf("waited for the browser sign-in: %w", ctx.Err())
	}
}

// implicitCallbackPage posts the redirect fragment back to the listener. The
// fragment never reaches a server on its own.
const implicitCallbackPage = `<!doctype html><title>vscroxy</title>
<p style="font:16px system-ui;margin:3em">Signing in…</p>
<script>
(async () => {
  const params = new URLSearchParams(location.hash.slice(1) || location.search.slice(1));
  if (![...params.keys()].length) return;
  try {
    const res = await fetch(location.pathname, { method: "POST", body: params });
    document.open();
    document.write(await res.text());
    document.close();
  } catch (err) {
    document.body.textContent = "Sign-in failed: " + err;
  }
})();
</script>`

// Tokens keeps OAuth 2.0 access tokens by configuration. It is safe for
// concurrent sends; save, when set, receives a snapshot after every change so
// the app can persist the tokens sealed.
type Tokens struct {
	mu     sync.Mutex
	tokens map[string]Token
	save   func([]Token)
}

// SetSave installs the snapshot callback. It is called without the lock.
func (s *Tokens) SetSave(save func([]Token)) {
	s.mu.Lock()
	s.save = save
	s.mu.Unlock()
}

// Peek is the token kept for an expanded configuration.
func (s *Tokens) Peek(o OAuth2) (Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[o.Key()]
	return t, ok
}

// All is every kept token, sorted by label, for the token list.
func (s *Tokens) All() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Load replaces the kept tokens, as when the sealed file is opened.
func (s *Tokens) Load(list []Token) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = map[string]Token{}
	for _, t := range list {
		if t.Key == "" {
			t.Key = t.Config.Key()
		}
		if t.Label == "" {
			t.Label = t.Config.Label()
		}
		s.tokens[t.Key] = t
	}
}

// Delete forgets one token and returns whether it existed.
func (s *Tokens) Delete(key string) bool {
	s.mu.Lock()
	_, ok := s.tokens[key]
	delete(s.tokens, key)
	list, save := s.snapshotLocked(), s.save
	s.mu.Unlock()
	if ok && save != nil {
		save(list)
	}
	return ok
}

// Clear forgets the configuration's token.
func (s *Tokens) Clear(o OAuth2) { s.Delete(o.Key()) }

func (s *Tokens) snapshotLocked() []Token {
	list := make([]Token, 0, len(s.tokens))
	for key, t := range s.tokens {
		if t.Key == "" {
			t.Key = key
		}
		if t.Label == "" {
			t.Label = t.Config.Label()
		}
		list = append(list, t)
	}
	slices.SortFunc(list, func(a, b Token) int {
		return cmp.Or(cmp.Compare(a.Label, b.Label), cmp.Compare(a.Key, b.Key))
	})
	return list
}

func (s *Tokens) keep(o OAuth2, t Token) {
	t.Key, t.Label, t.Config = o.Key(), o.Label(), o
	s.mu.Lock()
	if s.tokens == nil {
		s.tokens = map[string]Token{}
	}
	s.tokens[t.Key] = t
	list, save := s.snapshotLocked(), s.save
	s.mu.Unlock()
	if save != nil {
		save(list)
	}
}

// get is the token a send uses: the kept one while it is fresh, else a
// refreshed one, else, for grants without a browser, a new one. Concurrent
// sends of one configuration wait for a single refresh or fetch.
func (s *Tokens) get(o OAuth2, send Sender) (Token, error) {
	s.mu.Lock()
	key := o.Key()
	t, ok := s.tokens[key]
	if ok && t.Fresh(time.Now()) {
		s.mu.Unlock()
		return t, nil
	}
	if ok && t.Refresh != "" {
		if fresh, err := o.refresh(send, t); err == nil {
			fresh.Key, fresh.Label, fresh.Config = key, o.Label(), o
			s.tokens[key] = fresh
			list, save := s.snapshotLocked(), s.save
			s.mu.Unlock()
			if save != nil {
				save(list)
			}
			return fresh, nil
		} else if o.interactive() {
			s.mu.Unlock()
			return Token{}, fmt.Errorf("refreshing the access token failed (%w); get a new access token", err)
		}
	}
	t, err := o.fetch(send)
	if err != nil {
		s.mu.Unlock()
		return Token{}, err
	}
	t.Key, t.Label, t.Config = key, o.Label(), o
	if s.tokens == nil {
		s.tokens = map[string]Token{}
	}
	s.tokens[key] = t
	list, save := s.snapshotLocked(), s.save
	s.mu.Unlock()
	if save != nil {
		save(list)
	}
	return t, nil
}

// New gets a new token, signing in through the browser when the grant
// needs it, and keeps it. The lock is not held while the user signs in.
func (s *Tokens) New(ctx context.Context, o OAuth2, send Sender, open func(string) error) (Token, error) {
	var t Token
	var err error
	if o.interactive() {
		t, err = o.authorize(ctx, send, open)
	} else {
		t, err = o.fetch(send)
	}
	if err == nil {
		s.keep(o, t)
	}
	return t, err
}

// Refresh renews one kept token. A refresh token is used first; otherwise an
// interactive grant signs in again and a machine grant fetches a new token.
func (s *Tokens) Refresh(ctx context.Context, key string, send Sender, open func(string) error) (Token, error) {
	s.mu.Lock()
	t, ok := s.tokens[key]
	s.mu.Unlock()
	if !ok {
		return Token{}, errors.New("no saved access token")
	}
	if t.Refresh != "" {
		if fresh, err := t.Config.refresh(send, t); err == nil {
			fresh.Key, fresh.Label, fresh.Config = key, t.Config.Label(), t.Config
			s.keep(t.Config, fresh)
			return fresh, nil
		} else if !t.Config.interactive() {
			return Token{}, err
		}
	}
	return s.New(ctx, t.Config, send, open)
}

// OAuth2Config is auth's OAuth 2.0 configuration as a request filed in c
// uses it: variables expanded, and vault secrets unlocked for the token
// URL's host, the only one they are sent to.
// values are the request's effective variables.
func (c Context) OAuth2Config(auth Auth, values map[string]string) (OAuth2, error) {
	o := auth.OAuth2.Expand(values)
	host := hostOf(o.TokenURL)
	if o.grant() == GrantImplicit {
		host = hostOf(o.AuthURL)
	}
	secrets := map[string]string{}
	for _, name := range auth.OAuth2.Variables() {
		if secret, ok := vaultName(name); ok {
			if c.Vault == nil {
				return OAuth2{}, fmt.Errorf("{{%s}}: the vault is not available", name)
			}
			value, err := c.Vault(secret, host)
			if err != nil {
				return OAuth2{}, fmt.Errorf("{{%s}}: %w", name, err)
			}
			secrets[name] = value
		}
	}
	o = o.Expand(secrets)
	for _, f := range o.fields() {
		if names := (Request{URL: *f}).Variables(); len(names) > 0 {
			return OAuth2{}, fmt.Errorf("OAuth 2.0 settings use {{%s}}, which is not defined", names[0])
		}
	}
	return o, nil
}
