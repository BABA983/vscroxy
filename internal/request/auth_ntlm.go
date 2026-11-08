package request

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/go-ntlmssp"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
)

// AuthNTLM is the Auth type that answers an NTLM challenge.
const AuthNTLM = "NTLM"

// NTLMAuth is Postman's NTLM configuration. Every text field may use
// {{variables}}. A send starts without credentials, then answers the server's
// Type 1/2/3 challenge on the same connection; only NTLMv2 is supported.
type NTLMAuth struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Domain and Workstation are optional. An empty domain comes from the
	// username's DOMAIN\user or user@domain form.
	Domain      string `json:"domain,omitempty"`
	Workstation string `json:"workstation,omitempty"`
	// DisableRetry sends once and returns the server's 401 without answering
	// the challenge, as Postman's "disable retrying the request" does.
	DisableRetry bool `json:"disableRetryRequest,omitzero"`
}

// fields are the configuration's texts, for expansion and variable lists.
func (n *NTLMAuth) fields() []*string {
	return []*string{&n.Username, &n.Password, &n.Domain, &n.Workstation}
}

func (n NTLMAuth) expand(vars map[string]string) NTLMAuth {
	for _, f := range n.fields() {
		*f = ExpandVars(*f, vars)
	}
	return n
}

// errNTLM marks an NTLM exchange the server or the configuration made
// impossible, as opposed to a transport failure.
var errNTLM = errors.New("NTLM authentication failed")

// ntlmSend runs the NTLM exchange for req through srv, or directly when srv is
// nil. The request is sent once without credentials; a 401 or 403 offering
// NTLM is answered with Type 1 and then Type 3 on one connection. Redirects
// follow the request's policy, and credentials stay on the original origin.
func ntlmSend(ctx context.Context, srv *capture.Server, flow capture.Flow, req *Prepared, auth NTLMAuth, jar http.CookieJar, policy capture.TransferPolicy, in Context) (capture.Flow, error) {
	req.Remove("Authorization")
	first, _, err := capture.NewReplayRequest(ctx, flow)
	if err != nil {
		return capture.Flow{}, err
	}
	session, err := newExchangeSession(srv, flow, policy, in.Certificates)
	if err != nil {
		return capture.Flow{}, err
	}
	defer session.Close()
	transport := &ntlmTransport{origin: first.URL, auth: auth, session: session, jar: jar}
	client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: policy.Redirects.Checker(first)}
	started := time.Now()
	res, err := client.Do(first)
	if err != nil {
		return capture.Flow{}, err
	}
	defer res.Body.Close()
	preview, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return capture.Flow{}, err
	}
	rest, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		return capture.Flow{}, err
	}
	// The final request carries the answer that was accepted; a response from
	// another origin followed a redirect and never saw it.
	if transport.lastAuth != "" && res.Request != nil && sameOrigin(res.Request.URL, first.URL) {
		req.Set("Authorization", transport.lastAuth)
	}
	requestHeader := req.Header.Clone()
	if transport.lastRequest != nil {
		requestHeader = transport.lastRequest.Header.Clone()
	}
	return capture.Flow{
		Method: req.Method, URL: req.URL, Status: res.StatusCode,
		RequestHeader: requestHeader, RequestBody: strings.Clone(req.Body[:min(len(req.Body), 8<<20)]),
		RequestBodySize: len(req.Body),
		ResponseHeader:  res.Header.Clone(), ResponseBody: string(preview),
		ResponseBodySize: len(preview) + int(rest), Bytes: len(preview) + int(rest),
		ResponseTrailer: res.Trailer.Clone(), ResponseProto: res.Proto,
		Millis: int(time.Since(started).Milliseconds()), At: time.Now(),
	}, nil
}

// ntlmTransport answers an NTLM challenge for the original origin and sends
// every other origin's requests (redirect targets) without credentials.
type ntlmTransport struct {
	origin  *url.URL
	auth    NTLMAuth
	session exchangeSession
	jar     http.CookieJar
	// lastAuth and lastRequest describe the last attempt, whose response is
	// the exchange's result.
	lastAuth    string
	lastRequest *http.Request
}

func (t *ntlmTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sameOrigin(req.URL, t.origin) {
		redirected := req.Clone(req.Context())
		redirected.Header = req.Header.Clone()
		redirected.Header.Del("Authorization")
		return t.session.RoundTrip(redirected)
	}
	return t.exchange(req)
}

// exchange sends req, answers an NTLM challenge on the same connection, and
// returns the last response. It stops after Type 3: a 401 then is the result,
// not another challenge to answer.
func (t *ntlmTransport) exchange(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
	}
	send := func(authorization string, freshCookies bool) (*http.Response, error) {
		attempt := req.Clone(req.Context())
		attempt.Header = req.Header.Clone()
		attempt.Header.Del("Authorization")
		if authorization != "" {
			attempt.Header.Set("Authorization", authorization)
		}
		if freshCookies {
			// Intermediate responses bypass the caller's client, so its cookie
			// jar is refreshed here.
			attempt.Header.Del("Cookie")
			if t.jar != nil {
				for _, cookie := range t.jar.Cookies(req.URL) {
					attempt.AddCookie(cookie)
				}
			}
		}
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			attempt.Body = body
		}
		res, err := t.session.RoundTrip(attempt)
		if err == nil {
			t.lastAuth, t.lastRequest = authorization, attempt
			if t.jar != nil {
				t.jar.SetCookies(req.URL, res.Cookies())
			}
		}
		return res, err
	}

	first, err := send("", false)
	if err != nil {
		return nil, err
	}
	if t.auth.DisableRetry {
		return first, nil
	}
	if !ntlmChallengeOffered(first) {
		return first, nil
	}
	if err := drainNTLM(first); err != nil {
		return nil, err
	}

	user, domain := ntlmUserDomain(t.auth.Username, t.auth.Domain)
	if user == "" {
		return nil, fmt.Errorf("%w: enter a username", errNTLM)
	}
	negotiate, err := ntlmssp.NewNegotiateMessage(domain, t.auth.Workstation)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNTLM, err)
	}
	second, err := send("NTLM "+base64.StdEncoding.EncodeToString(negotiate), true)
	if err != nil {
		return nil, err
	}
	type2, offered := ntlmChallenge(second.Header.Values("WWW-Authenticate"))
	if !offered || second.StatusCode != http.StatusUnauthorized && second.StatusCode != http.StatusForbidden {
		return second, nil
	}
	if len(type2) == 0 {
		_ = drainNTLM(second)
		return nil, fmt.Errorf("%w: the server did not send an NTLM challenge", errNTLM)
	}
	if err := drainNTLM(second); err != nil {
		return nil, err
	}

	account := user
	if domain != "" {
		account = domain + `\` + user
	}
	authenticate, err := ntlmssp.NewAuthenticateMessage(type2, account, t.auth.Password,
		&ntlmssp.AuthenticateMessageOptions{WorkstationName: t.auth.Workstation})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNTLM, err)
	}
	return send("NTLM "+base64.StdEncoding.EncodeToString(authenticate), true)
}

// ntlmChallengeOffered reports whether a response is an NTLM challenge.
func ntlmChallengeOffered(res *http.Response) bool {
	if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
		return false
	}
	_, offered := ntlmChallenge(res.Header.Values("WWW-Authenticate"))
	return offered
}

// drainNTLM reads an intermediate response's body so its connection can carry
// the next step.
func drainNTLM(res *http.Response) error {
	if res.Body == nil {
		return nil
	}
	_, err := io.Copy(io.Discard, io.LimitReader(res.Body, 8<<20))
	return errors.Join(err, res.Body.Close())
}

// ntlmUserDomain splits Postman's username forms: a non-empty Domain field
// wins; otherwise DOMAIN\user or user@domain supply the domain.
func ntlmUserDomain(username, domain string) (user, userDomain string) {
	username, domain = strings.TrimSpace(username), strings.TrimSpace(domain)
	if domain != "" {
		return username, domain
	}
	if strings.Contains(username, `\`) && strings.Contains(username, "@") {
		return username, ""
	}
	if before, after, ok := strings.Cut(username, `\`); ok && before != "" && after != "" && !strings.Contains(after, `\`) {
		return after, before
	}
	if before, after, ok := strings.Cut(username, "@"); ok && before != "" && after != "" && !strings.Contains(after, "@") {
		return before, after
	}
	return username, ""
}

// ntlmChallenge reports whether WWW-Authenticate values offer NTLM and returns
// the first Type 2 message they carry. A bare "NTLM" challenge has none yet.
func ntlmChallenge(values []string) (type2 []byte, offered bool) {
	for _, value := range values {
		for i := 0; i < len(value); {
			switch value[i] {
			case ' ', '\t', ',':
				i++
				continue
			case '"':
				i++
				for i < len(value) && value[i] != '"' {
					if value[i] == '\\' {
						i++
					}
					i++
				}
				i++
				continue
			}
			start := i
			for i < len(value) && !strings.ContainsRune(" \t,=\"", rune(value[i])) {
				i++
			}
			if start == i {
				i++
				continue
			}
			token := value[start:i]
			j := i
			for j < len(value) && (value[j] == ' ' || value[j] == '\t') {
				j++
			}
			if j < len(value) && value[j] == '=' {
				// A parameter name and value: skip to the next challenge.
				i = j + 1
				for i < len(value) && (value[i] == ' ' || value[i] == '\t') {
					i++
				}
				if i < len(value) && value[i] == '"' {
					i++
					for i < len(value) && value[i] != '"' {
						if value[i] == '\\' {
							i++
						}
						i++
					}
					i++
					continue
				}
				for i < len(value) && value[i] != ',' {
					i++
				}
				continue
			}
			if strings.EqualFold(token, "NTLM") {
				offered = true
				tokenStart := j
				for j < len(value) && !strings.ContainsRune(" \t,", rune(value[j])) {
					j++
				}
				if raw := value[tokenStart:j]; raw != "" {
					if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
						return decoded, true
					}
				}
				i = j
				continue
			}
			i = j
		}
	}
	return nil, offered
}

// exchangeSession is the set of connections one send reuses across an auth
// handshake: the capture proxy's replay session, or direct connections when no
// proxy is running.
type exchangeSession interface {
	http.RoundTripper
	Close()
}

// newExchangeSession sends through srv when it runs, and directly otherwise.
func newExchangeSession(srv *capture.Server, flow capture.Flow, policy capture.TransferPolicy, certificates certs.Settings) (exchangeSession, error) {
	if srv != nil {
		session, err := srv.ReplaySession(flow, policy)
		if err != nil {
			return nil, err
		}
		return session, nil
	}
	return &directSession{certificates: certificates, insecure: policy.TLS == capture.TLSInsecure, transports: make(map[string]*http.Transport)}, nil
}

// directSession keeps one connection per origin for a send that runs without
// the capture proxy.
type directSession struct {
	certificates certs.Settings
	insecure     bool

	mu         sync.Mutex
	closed     bool
	transports map[string]*http.Transport
}

func (d *directSession) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Host == "" {
		return nil, errors.New("a direct send needs an absolute HTTP(S) URL")
	}
	scheme := strings.ToLower(req.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("a direct send supports HTTP(S) only")
	}
	address := certs.Address(req.URL)
	key := scheme + "://" + address
	d.mu.Lock()
	transport, closed := d.transports[key], d.closed
	d.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	if transport == nil {
		transport = &http.Transport{MaxConnsPerHost: 1, ForceAttemptHTTP2: false}
		if scheme == "https" {
			config, err := d.certificates.TLSConfig(address, nil, d.insecure)
			if err != nil {
				return nil, err
			}
			transport.TLSClientConfig = config
		}
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			transport.CloseIdleConnections()
			return nil, net.ErrClosed
		}
		if existing := d.transports[key]; existing != nil {
			d.mu.Unlock()
			transport.CloseIdleConnections()
			transport = existing
		} else {
			d.transports[key] = transport
			d.mu.Unlock()
		}
	}
	return transport.RoundTrip(req)
}

func (d *directSession) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	transports := d.transports
	d.transports = nil
	d.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

// sameOrigin reports whether two URLs name the same scheme, host and port.
func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && certs.Address(a) == certs.Address(b)
}
