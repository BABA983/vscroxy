package request

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
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

// ErrNTLM marks an NTLM exchange the server or the configuration made
// impossible, as opposed to a transport failure.
var ErrNTLM = errors.New("NTLM authentication failed")

// The reasons an NTLM exchange fails, stable for the interface's translation.
const (
	NTLMClosedConnection = "closed-connection"
	NTLMNoChallenge      = "no-challenge"
	NTLMUsernameRequired = "username-required"
	NTLMUnsupported      = "unsupported"
	NTLMProxyRequired    = "proxy-required"
)

// NTLMError is an NTLM exchange failure. Kind names the reason for the
// interface; Detail is the English text for logs and headless runs.
type NTLMError struct {
	Kind   string
	Detail string
}

func (e *NTLMError) Error() string {
	if e.Detail == "" {
		return ErrNTLM.Error()
	}
	return ErrNTLM.Error() + ": " + e.Detail
}

func (e *NTLMError) Unwrap() error { return ErrNTLM }

// ntlmError builds an NTLM failure with a reason the interface translates.
func ntlmError(kind, detail string) error { return &NTLMError{Kind: kind, Detail: detail} }

// ntlmSend runs the NTLM exchange for req through srv. The request is sent
// once without credentials; a 401 or 403 offering NTLM is answered with Type 1
// and then Type 3 on one connection. Redirects follow the request's policy,
// and credentials stay on the original origin.
func ntlmSend(ctx context.Context, srv *capture.Server, flow capture.Flow, req *Prepared, auth NTLMAuth, jar http.CookieJar, policy capture.TransferPolicy) (capture.Flow, error) {
	if srv == nil {
		return capture.Flow{}, ntlmError(NTLMProxyRequired, "start the local proxy first")
	}
	req.Remove("Authorization")
	first, _, err := capture.NewReplayRequest(ctx, flow)
	if err != nil {
		return capture.Flow{}, err
	}
	session, err := srv.ReplaySession(ctx, flow, policy)
	if err != nil {
		return capture.Flow{}, err
	}
	defer session.Close()
	transport := &ntlmTransport{origin: first.URL, auth: auth, session: session, jar: jar, userCookies: flow.RequestHeader.Values("Cookie")}
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
	session *capture.ReplaySession
	jar     http.CookieJar
	// userCookies are the request's own Cookie header values, before the
	// caller's jar added any. Each leg rebuilds the header from them and the
	// jar, so a value the server refreshed is not hidden by the client's copy.
	userCookies []string
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
	// send makes one attempt. body says whether it carries the authored body;
	// mustReuse, when set, is the connection an earlier step used and the
	// attempt must share it. gotConn receives the connection the attempt got.
	send := func(authorization string, body bool, mustReuse net.Conn, gotConn func(net.Conn)) (*http.Response, error) {
		attempt := req.Clone(req.Context())
		attempt.Header = req.Header.Clone()
		attempt.Header.Del("Authorization")
		if authorization != "" {
			attempt.Header.Set("Authorization", authorization)
		}
		// Rebuild the Cookie header from the request's own values and the
		// jar's cookies whose names they do not name. The caller's client
		// already wrote the jar's values into req.Header, so copying that
		// header would keep a stale value the server's 401 replaced.
		attempt.Header.Del("Cookie")
		names := make(map[string]bool)
		for _, value := range t.userCookies {
			attempt.Header.Add("Cookie", value)
			for part := range strings.SplitSeq(value, ";") {
				name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
				names[name] = true
			}
		}
		if t.jar != nil {
			for _, cookie := range t.jar.Cookies(req.URL) {
				if !names[cookie.Name] {
					attempt.AddCookie(cookie)
				}
			}
		}
		if body {
			if req.GetBody != nil {
				fresh, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				attempt.Body = fresh
			}
		} else {
			// Type 1 carries no body. A server may answer the challenge before
			// reading a large POST, and resending that body would close the
			// connection the answer needs.
			attempt.Body = http.NoBody
			attempt.GetBody = nil
			attempt.ContentLength = 0
			attempt.Header.Del("Content-Length")
		}
		roundTrip := func() (*http.Response, error) {
			res, err := t.session.RoundTrip(attempt)
			if err == nil {
				t.lastAuth, t.lastRequest = authorization, attempt
				if t.jar != nil {
					t.jar.SetCookies(req.URL, res.Cookies())
				}
			}
			return res, err
		}
		if mustReuse == nil && gotConn == nil {
			return roundTrip()
		}
		check := &connCheck{want: mustReuse}
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			if gotConn != nil {
				gotConn(info.Conn)
			}
			check.record(info)
		}}
		attempt = attempt.WithContext(httptrace.WithClientTrace(attempt.Context(), trace))
		res, err := roundTrip()
		if mustReuse != nil && !check.reused() {
			if res != nil {
				_ = drainNTLM(res)
			}
			return nil, ntlmError(NTLMClosedConnection, "the server closed the connection after its NTLM challenge")
		}
		return res, err
	}

	first, err := send("", true, nil, nil)
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
		return nil, ntlmError(NTLMUsernameRequired, "enter a username")
	}
	negotiate, err := ntlmssp.NewNegotiateMessage(domain, t.auth.Workstation)
	if err != nil {
		return nil, ntlmError(NTLMUnsupported, err.Error())
	}
	// Type 1 runs without the body, and its connection is the one the answer
	// must reuse: the proxy pins its upstream to that downstream connection.
	// If the connection the first 401 came on is gone, the challenge starts
	// again on a fresh one.
	var type1 connHolder
	sendType1 := func() (*http.Response, error) {
		return send("NTLM "+base64.StdEncoding.EncodeToString(negotiate), false, nil, type1.set)
	}
	second, err := sendType1()
	if err != nil && req.Context().Err() == nil {
		second, err = sendType1()
	}
	if err != nil {
		return nil, err
	}
	type1Conn := type1.get()
	type2, offered := ntlmChallenge(second.Header.Values("WWW-Authenticate"))
	if !offered || second.StatusCode != http.StatusUnauthorized && second.StatusCode != http.StatusForbidden {
		return second, nil
	}
	if len(type2) == 0 {
		_ = drainNTLM(second)
		return nil, ntlmError(NTLMNoChallenge, "the server did not send an NTLM challenge")
	}
	closed := second.Close
	drainErr := drainNTLM(second)
	if closed {
		return nil, ntlmError(NTLMClosedConnection, "the server closed the connection after its NTLM challenge")
	}
	if drainErr != nil {
		return nil, drainErr
	}

	account := user
	if domain != "" {
		account = domain + `\` + user
	}
	authenticate, err := ntlmssp.NewAuthenticateMessage(type2, account, t.auth.Password,
		&ntlmssp.AuthenticateMessageOptions{WorkstationName: t.auth.Workstation})
	if err != nil {
		return nil, ntlmError(NTLMUnsupported, err.Error())
	}
	third, err := send("NTLM "+base64.StdEncoding.EncodeToString(authenticate), true, type1Conn, nil)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, err
		}
		return nil, ntlmError(NTLMClosedConnection, "the server closed the connection after its NTLM challenge")
	}
	return third, nil
}

// connHolder keeps the last connection a request's trace saw.
type connHolder struct {
	mu   sync.Mutex
	conn net.Conn
}

func (h *connHolder) set(conn net.Conn) {
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
}

func (h *connHolder) get() net.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn
}

// connCheck records whether an attempt reused the connection NTLM needs.
type connCheck struct {
	mu   sync.Mutex
	want net.Conn
	seen bool
	ok   bool
}

func (c *connCheck) record(info httptrace.GotConnInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = true
	c.ok = info.Reused && info.Conn == c.want
}

// reused reports whether the attempt got the wanted connection. An attempt
// whose trace never fired cannot be checked and is not refused.
func (c *connCheck) reused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.seen || c.ok
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
// wins and the username is sent as written; otherwise DOMAIN\user or
// user@domain supply the domain.
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

// sameOrigin reports whether two URLs name the same scheme, host and port.
func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && certs.Address(a) == certs.Address(b)
}
