package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
	log "github.com/sirupsen/logrus"
)

type Options struct {
	Debug             int
	Addr              string
	StreamLargeBodies int64 // 当请求或响应体大于此字节时，转为 stream 模式
	SslInsecure       bool
	CaRootPath        string
	NewCaFunc         func() (cert.CA, error) //创建 Ca 的函数
	Upstream          string
	LogFilePath       string // Path to write logs to file
	// SOCKSPeekTimeout bounds how long a SOCKS connection whose target is
	// decrypted may take to send its first bytes. Zero uses 10 seconds.
	SOCKSPeekTimeout time.Duration
	// SOCKSSniffTimeout bounds the peek for a target that is not decrypted,
	// to tell plaintext HTTP from TLS or another protocol. Zero uses 400 ms.
	SOCKSSniffTimeout time.Duration
	// ClientCertificate returns the client certificate to present to an
	// upstream host:port, or nil when none is configured. An error aborts the
	// handshake instead of continuing without the certificate.
	ClientCertificate func(address string) (*tls.Certificate, error)
	// RootCAs returns the roots to verify an upstream certificate. A nil
	// function keeps the system pool; an error aborts the handshake.
	RootCAs func() (*x509.CertPool, error)
}

// HeaderVerifyUpstream on a CONNECT asks the proxy to verify the upstream
// server's certificate instead of accepting any. It can only make an insecure
// inspection proxy stricter, never weaker; the header is not forwarded.
const HeaderVerifyUpstream = "X-Vscroxy-Verify-TLS"

// UpstreamProxy is one place a request can go, in preference order. A nil URL
// is a direct connection. Reason is a user-facing explanation that is recorded
// in the connection log and never contains credentials.
type UpstreamProxy struct {
	URL    *url.URL
	Reason string
}

type Proxy struct {
	Opts    *Options
	Version string
	Addons  []Addon

	entry            *entry
	attacker         *attacker
	webSocketHandler *webSocketHandler
	shouldIntercept  func(req *http.Request) bool                                     // req is received by proxy.server
	dialFallback     func(req *http.Request) bool                                     // see SetDialFallbackRule
	lazyIntercept    func(req *http.Request) bool                                     // see SetLazyInterceptRule
	upstreamProxy    func(req *http.Request, address string) ([]UpstreamProxy, error) // req is received by proxy.server, not client request
	authProxy        func(res http.ResponseWriter, req *http.Request) (bool, error)

	// socksHTTP carries plaintext connections whose target a SOCKS5 CONNECT
	// agreed. It is served by entry.server, which parses their origin-form
	// requests; TLS connections go straight to the interception path.
	socksOnce sync.Once
	socksHTTP *connListener
}

// Version is the release version of the proxy library.
var Version = "1.9.4"

func NewProxy(opts *Options) (*Proxy, error) {
	if opts.StreamLargeBodies <= 0 {
		opts.StreamLargeBodies = 1024 * 1024 * 5 // default: 5mb
	}

	proxy := &Proxy{
		Opts:    opts,
		Version: Version,
		Addons:  make([]Addon, 0),
	}

	proxy.entry = newEntry(proxy)

	attacker, err := newAttacker(proxy)
	if err != nil {
		return nil, err
	}
	proxy.attacker = attacker

	proxy.webSocketHandler = newWebSocketHandler(proxy)

	return proxy, nil
}

func (proxy *Proxy) AddAddon(addon Addon) {
	proxy.Addons = append(proxy.Addons, addon)
}

func (proxy *Proxy) Start() error {
	addr := proxy.Opts.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return proxy.Serve(ln)
}

// Serve serves on an already bound listener and closes it when serving ends.
// This lets callers reserve an OS-assigned port without a close/rebind race.
func (proxy *Proxy) Serve(ln net.Listener) error {
	go func() {
		if err := proxy.attacker.start(); err != nil {
			log.Error(err)
		}
	}()
	return proxy.entry.serve(ln)
}

func (proxy *Proxy) Close() error {
	return proxy.entry.close()
}

func (proxy *Proxy) Shutdown(ctx context.Context) error {
	return proxy.entry.shutdown(ctx)
}

func (proxy *Proxy) GetCertificate() x509.Certificate {
	return *proxy.attacker.ca.GetRootCA()
}

func (proxy *Proxy) GetCertificateByCN(commonName string) (*tls.Certificate, error) {
	return proxy.attacker.ca.GetCert(commonName)
}

func (proxy *Proxy) SetShouldInterceptRule(rule func(req *http.Request) bool) {
	proxy.shouldIntercept = rule
}

// SetDialFallbackRule decides, for an intercepted CONNECT whose server cannot
// be reached, whether to go on with a certificate generated for its host
// instead of failing the CONNECT: addons may send its requests elsewhere.
func (proxy *Proxy) SetDialFallbackRule(rule func(req *http.Request) bool) {
	proxy.dialFallback = rule
}

// SetLazyInterceptRule defers the upstream dial for selected intercepted
// CONNECTs. Local response addons can then answer without contacting the
// original server, even to obtain its certificate. The interception policy
// is still authoritative; this never decrypts a bypassed tunnel.
func (proxy *Proxy) SetLazyInterceptRule(rule func(req *http.Request) bool) {
	proxy.lazyIntercept = rule
}

func (proxy *Proxy) SetUpstreamProxy(fn func(req *http.Request, address string) ([]UpstreamProxy, error)) {
	proxy.upstreamProxy = fn
}

// getUpstreamProxies returns the ordered candidates for a destination. The
// hook is authoritative; without one, Options.Upstream or the environment
// supplies a single proxy.
func (proxy *Proxy) getUpstreamProxies(req *http.Request, address string) ([]UpstreamProxy, error) {
	if proxy.upstreamProxy != nil {
		return proxy.upstreamProxy(req, address)
	}
	if len(proxy.Opts.Upstream) > 0 {
		u, err := url.Parse(proxy.Opts.Upstream)
		if err != nil {
			return nil, err
		}
		return []UpstreamProxy{{URL: u}}, nil
	}
	cReq := &http.Request{URL: &url.URL{Scheme: "https", Host: address}}
	u, err := http.ProxyFromEnvironment(cReq)
	if err != nil {
		return nil, err
	}
	return []UpstreamProxy{{URL: u}}, nil
}

// upstreamTLSConfig builds the client side of an intercepted upstream TLS
// connection: the configured client certificate for address and, when verify
// is on, the configured roots. An error must abort the handshake.
func (proxy *Proxy) upstreamTLSConfig(address string, verify bool) (*tls.Config, error) {
	config := &tls.Config{
		InsecureSkipVerify: !verify,
		KeyLogWriter:       helper.GetTlsKeyLogWriter(),
	}
	if verify && proxy.Opts.RootCAs != nil {
		roots, err := proxy.Opts.RootCAs()
		if err != nil {
			return nil, err
		}
		config.RootCAs = roots
	}
	if proxy.Opts.ClientCertificate != nil {
		cert, err := proxy.Opts.ClientCertificate(address)
		if err != nil {
			return nil, err
		}
		if cert != nil {
			config.Certificates = []tls.Certificate{*cert}
		}
	}
	return config, nil
}

// ErrUpstreamAuth marks an upstream proxy that rejected the configured
// credentials. It stops the candidate fallback: trying the next proxy (or
// DIRECT) after a 407 or a SOCKS5 authentication failure would silently
// bypass the proxy the user configured.
var ErrUpstreamAuth = helper.ErrProxyAuth

// ErrUpstreamResponse marks a proxy's non-200 CONNECT response. Like an
// authentication failure it is final: a 403 or 502 is the proxy's policy, not
// a transport failure that another candidate may bypass.
var ErrUpstreamResponse = helper.ErrProxyResponse

// upstreamDialTimeout bounds one upstream candidate's dial, TLS handshake and
// CONNECT together. A candidate that cannot connect within it yields to the
// next one, including DIRECT.
const upstreamDialTimeout = 8 * time.Second

func (proxy *Proxy) getUpstreamConn(ctx context.Context, req *http.Request) (net.Conn, error) {
	return proxy.dialUpstream(ctx, req, helper.CanonicalAddr(req.URL))
}

// proxyTLSConfig verifies an https upstream proxy with the system roots and
// the app's custom roots. It never skips verification: Basic credentials are
// sent to the proxy after this handshake, so an unverified proxy would receive
// them.
func (proxy *Proxy) proxyTLSConfig() (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if proxy.Opts.RootCAs != nil {
		roots, err := proxy.Opts.RootCAs()
		if err != nil {
			return nil, err
		}
		config.RootCAs = roots
	}
	return config, nil
}

// dialUpstream tries the request's candidates in order and returns the first
// connection. A proxy authentication failure or HTTP response stops the
// fallback; other failures try the next candidate, including a final DIRECT.
// Each candidate has its own total deadline for dial, TLS and CONNECT. Proxy
// TLS roots are loaded only for an https candidate, so an unrelated broken
// certificate setting does not affect a direct request.
func (proxy *Proxy) dialUpstream(ctx context.Context, req *http.Request, address string) (net.Conn, error) {
	cc := req.Context().Value(connContextKey).(*ConnContext)
	ctx = httptrace.WithClientTrace(ctx, cc.trace())
	candidates, err := proxy.getUpstreamProxies(req, address)
	if err != nil {
		cc.RecordEvent("TCP", "Upstream proxy configuration", err)
		return nil, err
	}
	if len(candidates) == 0 {
		candidates = []UpstreamProxy{{}}
	}
	timeout := upstreamDialTimeout
	var failures []error
	for _, candidate := range candidates {
		target := address
		if candidate.URL != nil {
			target += " via proxy " + candidate.URL.Host
		}
		if candidate.Reason != "" {
			cc.RecordEvent("Upstream", candidate.Reason, nil)
		}
		cc.RecordEvent("TCP", "Connecting to "+target, nil)
		candidateCtx, cancel := context.WithTimeout(ctx, timeout)
		var conn net.Conn
		if candidate.URL != nil {
			var proxyTLS *tls.Config
			if candidate.URL.Scheme == "https" {
				proxyTLS, err = proxy.proxyTLSConfig()
				if err != nil {
					cancel()
					cc.RecordEvent("TLS upstream", "Upstream proxy TLS configuration failed", err)
					return nil, err
				}
			}
			conn, err = helper.GetProxyConn(candidateCtx, candidate.URL, address, proxyTLS)
		} else {
			conn, err = (&net.Dialer{}).DialContext(candidateCtx, "tcp", address)
		}
		cancel()
		if err != nil {
			stage := "TCP"
			if _, ok := errors.AsType[*net.DNSError](err); ok {
				stage = "DNS"
			}
			cc.RecordEvent(stage, "Could not connect to "+target, err)
			failures = append(failures, err)
			if errors.Is(err, ErrUpstreamAuth) || errors.Is(err, ErrUpstreamResponse) {
				break
			}
			continue
		}
		cc.RecordEvent("TCP", "Connected to "+conn.RemoteAddr().String(), nil)
		return conn, nil
	}
	if len(failures) == 1 {
		return nil, failures[0]
	}
	return nil, errors.Join(failures...)
}

func (proxy *Proxy) SetAuthProxy(fn func(res http.ResponseWriter, req *http.Request) (bool, error)) {
	proxy.authProxy = fn
}
