package helper

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ErrProxyAuth means the upstream proxy rejected the configured credentials.
// Callers must not fall back to another proxy or DIRECT after it.
var ErrProxyAuth = errors.New("upstream proxy authentication failed")

// ErrProxyResponse means the proxy answered with an HTTP response instead of
// establishing the tunnel. Like an authentication failure, it is final: a
// proxy's 403 or 502 is a policy decision, not a transport failure.
var ErrProxyResponse = errors.New("upstream proxy returned an HTTP response")

// ProxyAuth negotiates authentication on an upstream HTTP proxy connection.
// The proxy package performs the HTTP exchange; these callbacks supply the
// Proxy-Authorization value for each attempt. A nil *ProxyAuth uses the Basic
// credentials in the proxy URL.
type ProxyAuth struct {
	// Initial is the Proxy-Authorization value for the first request. Empty
	// sends the request without credentials.
	Initial string
	// Answer returns the value answering a 407's Proxy-Authenticate values.
	// final reports that this value is the origin request's answer (Type 3),
	// whose response is the final response and is not bound by a handshake
	// timeout. An error makes that 407 the final response.
	Answer func(challenges []string) (authorization string, final bool, err error)
}

// GetProxyConn connect proxy
// ref: http/transport.go dialConn func
func GetProxyConn(ctx context.Context, proxyUrl *url.URL, address string, proxyTLS *tls.Config, auth *ProxyAuth) (net.Conn, error) {
	var conn net.Conn
	if proxyUrl.Scheme == "socks5" {
		//检测socks5认证信息
		proxyAuth := &proxy.Auth{}
		if proxyUrl.User != nil {
			user := proxyUrl.User.Username()
			pass, _ := proxyUrl.User.Password()
			proxyAuth.User = user
			proxyAuth.Password = pass
		}
		dialer, err := proxy.SOCKS5("tcp", proxyUrl.Host, proxyAuth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		dc := dialer.(interface {
			DialContext(ctx context.Context, network, addr string) (net.Conn, error)
		})
		conn, err = dc.DialContext(ctx, "tcp", address)
		if err != nil {
			if isSOCKSAuthError(err) {
				return nil, fmt.Errorf("%w: %v", ErrProxyAuth, err)
			}
			return nil, err
		}
		return conn, err
	}
	conn, err := DialProxyConn(ctx, proxyUrl, proxyTLS)
	if err != nil {
		return nil, err
	}
	connectReq := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: address},
		Host:   address,
		Header: http.Header{},
	}
	if auth != nil {
		if err := authenticateProxy(ctx, conn, connectReq, auth); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
	if proxyUrl.User != nil {
		// User.String() percent-encodes reserved characters in the
		// password; Basic authentication needs the raw pair.
		username := proxyUrl.User.Username()
		password, _ := proxyUrl.User.Password()
		connectReq.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	}
	connectCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	didReadResponse := make(chan struct{}) // closed after CONNECT write+read is done or fails
	var resp *http.Response
	// Write the CONNECT request & read the response.
	go func() {
		defer close(didReadResponse)
		err = connectReq.Write(conn)
		if err != nil {
			return
		}
		// Okay to use and discard buffered reader here, because
		// TLS server will not speak until spoken to.
		br := bufio.NewReader(conn)
		resp, err = http.ReadResponse(br, connectReq)
	}()
	select {
	case <-connectCtx.Done():
		conn.Close()
		<-didReadResponse
		return nil, connectCtx.Err()
	case <-didReadResponse:
		// resp or err now set
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != 200 {
		conn.Close()
		if resp.Status == "" {
			return nil, errors.New("unknown status code")
		}
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return nil, fmt.Errorf("%w: %w: %s", ErrProxyResponse, ErrProxyAuth, resp.Status)
		}
		return nil, fmt.Errorf("%w: %s", ErrProxyResponse, resp.Status)
	}
	return conn, nil
}

// DialProxyConn opens a TCP connection to an HTTP(S) proxy, completing the TLS
// handshake for an https proxy. It sends no request.
func DialProxyConn(ctx context.Context, proxyUrl *url.URL, proxyTLS *tls.Config) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyUrl.Host)
	if err != nil {
		return nil, err
	}
	if proxyUrl.Scheme != "https" {
		return conn, nil
	}
	// An https upstream is always verified: its credentials are sent only
	// after its certificate proves the endpoint. RootCAs comes from the app's
	// certificate settings and keeps the system pool.
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if proxyTLS != nil {
		tlsConfig = proxyTLS.Clone()
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = proxyUrl.Hostname()
	}
	tlsConn := tls.Client(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// authenticateProxy runs the configured challenge exchange on one proxy
// connection and leaves a 200 CONNECT as the tunnel. Every attempt reuses the
// same connection, which NTLM requires.
func authenticateProxy(ctx context.Context, conn net.Conn, req *http.Request, auth *ProxyAuth) error {
	connectCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	br := bufio.NewReader(conn)
	resp, err := writeConnect(connectCtx, conn, br, req, auth.Initial)
	for i := 0; err == nil && resp.StatusCode == http.StatusProxyAuthRequired && i < 4; i++ {
		challenges := resp.Header.Values("Proxy-Authenticate")
		drainProxyResponse(resp)
		next, final, answerErr := auth.Answer(challenges)
		if answerErr != nil {
			break
		}
		// The Type 3 CONNECT is the origin request; its 200 is the tunnel, so
		// it keeps the caller's context rather than the handshake bound.
		attemptCtx := connectCtx
		if final {
			attemptCtx = ctx
		}
		resp, err = writeConnect(attemptCtx, conn, br, req, next)
	}
	if err != nil {
		return err
	}
	if resp.StatusCode == 200 {
		return nil
	}
	if resp.Status == "" {
		return errors.New("unknown status code")
	}
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return fmt.Errorf("%w: %w: %s", ErrProxyResponse, ErrProxyAuth, resp.Status)
	}
	return fmt.Errorf("%w: %s", ErrProxyResponse, resp.Status)
}

// writeConnect writes one CONNECT attempt and reads its response. The context
// ends a blocked write or read by closing the connection.
func writeConnect(ctx context.Context, conn net.Conn, br *bufio.Reader, req *http.Request, authorization string) (*http.Response, error) {
	attempt := req.Clone(ctx)
	attempt.Header = req.Header.Clone()
	attempt.Header.Del("Proxy-Authorization")
	if authorization != "" {
		attempt.Header.Set("Proxy-Authorization", authorization)
	}
	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		if err := attempt.Write(conn); err != nil {
			done <- result{err: err}
			return
		}
		resp, err := http.ReadResponse(br, attempt)
		done <- result{resp: resp, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	case got := <-done:
		return got.resp, got.err
	}
}

// drainProxyResponse reads an intermediate 407 body so the connection can
// carry the next attempt.
func drainProxyResponse(resp *http.Response) {
	if resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}

// isSOCKSAuthError recognizes x/net/proxy's authentication failures without
// exposing the username or password in the error.
func isSOCKSAuthError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "authentication") ||
		strings.Contains(text, "no acceptable authentication")
}
