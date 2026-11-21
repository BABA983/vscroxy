package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
)

// MaxProxyAuthBody is the largest plain-HTTP request body an upstream proxy
// authentication exchange buffers so the Type 3 attempt can resend it. A
// larger body fails with a clear error; HTTPS uses the CONNECT tunnel and
// needs no buffering.
const MaxProxyAuthBody = 8 << 20

// proxyAuthTransport sends a plain HTTP request through an upstream proxy that
// needs an authentication exchange. The first connection is the one the caller
// already dialled; every attempt reuses it, because a challenge and its answer
// must stay on one connection.
type proxyAuthTransport struct {
	proxyURL *url.URL
	proxyTLS *tls.Config
	auth     *ProxyAuth
	// handshakeContext bounds the Type 1 challenge rounds. Tests replace it
	// with a shorter bound. The Type 3 answer is the origin request, so its
	// response uses the request's own context.
	handshakeContext func(context.Context) (context.Context, context.CancelFunc)

	mu    sync.Mutex
	first net.Conn
	used  bool
}

// RoundTrip sends the request, answers a 407 challenge, and returns the final
// response. The response body owns the proxy connection and closes it.
func (t *proxyAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	conn, err := t.connection(req.Context())
	if err != nil {
		return nil, err
	}
	var body []byte
	if req.Body != nil {
		body, err = io.ReadAll(io.LimitReader(req.Body, MaxProxyAuthBody+1))
		_ = req.Body.Close()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if len(body) > MaxProxyAuthBody {
			_ = conn.Close()
			return nil, fmt.Errorf("the plain HTTP request body is larger than %d MiB; an NTLM upstream proxy buffers it to answer the challenge, so use HTTPS, whose CONNECT tunnel carries the body without buffering", MaxProxyAuthBody>>20)
		}
		if len(body) == 0 {
			body = nil
		}
	}
	res, err := t.exchange(req, conn, body)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	res.Body = &closingBody{ReadCloser: res.Body, conn: conn}
	return res, nil
}

// connection returns the pre-dialled proxy connection once, then dials a new
// one for any later attempt. The separate transport is built per request and
// does not keep idle connections.
func (t *proxyAuthTransport) connection(ctx context.Context) (net.Conn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.used && t.first != nil {
		t.used = true
		return t.first, nil
	}
	return helper.DialProxyConn(ctx, t.proxyURL, t.proxyTLS)
}

// exchange runs the challenge loop on one connection. A 407 after the answer
// is final, as it is for Basic: the configured proxy refused the request. The
// first attempt and the Type 3 origin request use the request's own context;
// only the Type 1 challenge rounds get the handshake bound.
func (t *proxyAuthTransport) exchange(req *http.Request, conn net.Conn, body []byte) (*http.Response, error) {
	br := bufio.NewReader(conn)
	res, err := writeProxyRequest(req.Context(), conn, br, req, body, t.auth.Initial)
	for i := 0; err == nil && res.StatusCode == http.StatusProxyAuthRequired && i < 4; i++ {
		challenges := res.Header.Values("Proxy-Authenticate")
		drainProxyResponse(res)
		next, final, answerErr := t.auth.Answer(challenges)
		if answerErr != nil {
			break
		}
		if final {
			// Type 3 is the origin request; its response may be a long poll.
			res, err = writeProxyRequest(req.Context(), conn, br, req, body, next)
			break
		}
		handshakeCtx, cancel := t.handshakeCtx(req.Context())
		res, err = writeProxyRequest(handshakeCtx, conn, br, req, body, next)
		cancel()
	}
	return res, err
}

// handshakeCtx bounds one Type 1 challenge round. The default matches the
// CONNECT helper's one-minute bound; a test can inject a shorter one.
func (t *proxyAuthTransport) handshakeCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if t.handshakeContext != nil {
		return t.handshakeContext(ctx)
	}
	return context.WithTimeout(ctx, time.Minute)
}

// writeProxyRequest writes one absolute-form attempt and reads its response.
// The body is already buffered so the answer can resend it. The context ends a
// blocked write or read by closing the connection.
func writeProxyRequest(ctx context.Context, conn net.Conn, br *bufio.Reader, req *http.Request, body []byte, authorization string) (*http.Response, error) {
	attempt := req.Clone(ctx)
	attempt.Header = req.Header.Clone()
	attempt.Header.Del("Proxy-Authorization")
	if authorization != "" {
		attempt.Header.Set("Proxy-Authorization", authorization)
	}
	if body != nil || req.ContentLength != 0 {
		attempt.Body = io.NopCloser(bytes.NewReader(body))
		attempt.ContentLength = int64(len(body))
		attempt.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	} else {
		attempt.Body = http.NoBody
		attempt.ContentLength = 0
	}
	type result struct {
		res *http.Response
		err error
	}
	done := make(chan result, 1)
	go func() {
		if err := attempt.WriteProxy(conn); err != nil {
			done <- result{err: err}
			return
		}
		for {
			res, err := http.ReadResponse(br, attempt)
			if err != nil {
				done <- result{err: err}
				return
			}
			// A 100 Continue is interim; the final response follows it.
			if res.StatusCode == http.StatusContinue {
				drainProxyResponse(res)
				continue
			}
			done <- result{res: res}
			return
		}
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	case got := <-done:
		return got.res, got.err
	}
}

// drainProxyResponse reads an intermediate 407 body so the connection can
// carry the next attempt.
func drainProxyResponse(res *http.Response) {
	if res.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	_ = res.Body.Close()
}

// closingBody closes the proxy connection with the response body.
type closingBody struct {
	io.ReadCloser
	conn net.Conn
}

func (b *closingBody) Close() error {
	return errors.Join(b.ReadCloser.Close(), b.conn.Close())
}
