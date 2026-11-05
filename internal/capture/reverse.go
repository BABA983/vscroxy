package capture

// Reverse proxies: a loopback port that takes plaintext HTTP and forwards it
// to a configured remote base URL. The request is sent through this app's own
// proxy listener, so the engine's rules, scripts, breakpoints and capture
// pipeline apply exactly as they do to a client that used the proxy directly.
// An HTTPS remote is reached by this app's TLS client; the engine decrypts
// that tunnel for capture and verifies the real remote certificate unless the
// entry allows a self-signed one.

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

const (
	// reverseTokenHeader marks a request this app's reverse proxy sent
	// through its own listener. It forces decryption for the HTTPS tunnel
	// and is removed before rules, scripts or the table see the request.
	reverseTokenHeader = "X-Vscroxy-Reverse"
	// reverseHostHeader carries the Host the local client sent when the
	// entry keeps it; the request itself names the remote host so the
	// engine dials and records the right URL.
	reverseHostHeader = "X-Vscroxy-Original-Host"
)

// ReverseProxy maps one loopback port to a remote base URL. Port 0 asks the
// OS for a port (the desktop UI requires 1024–65535); the bound address is in
// ReverseProxyStatus. Target must be an http or https URL; the local request
// path is appended to its path.
type ReverseProxy struct {
	ID       string
	Port     int
	Target   string
	KeepHost bool
	Enabled  bool
	// Insecure turns off certificate verification for the remote HTTPS
	// target. It is off by default; only a development server with a
	// self-signed certificate should set it.
	Insecure bool
}

// ReverseProxyStatus is one entry's runtime state: the bound address, or why
// it is not listening.
type ReverseProxyStatus struct {
	ID    string
	Addr  string
	Error string
}

type reverseEntry struct {
	cfg       ReverseProxy
	target    *url.URL
	listener  net.Listener
	server    *http.Server
	transport *http.Transport
}

// SetReverseProxies replaces the reverse proxy entries. Invalid configuration
// is refused as a whole. A port that cannot be bound is reported per entry
// and leaves the other entries running; the returned error names each one.
// The change applies at once when capture is running, and at the next Start
// otherwise.
func (s *Server) SetReverseProxies(list []ReverseProxy) error {
	validated := make([]ReverseProxy, len(list))
	targets := make(map[string]*url.URL, len(list))
	ports := make(map[int]string, len(list))
	ids := make(map[string]bool, len(list))
	for i, r := range list {
		if r.ID == "" {
			return fmt.Errorf("reverse proxy %d: an ID is required", i+1)
		}
		if ids[r.ID] {
			return fmt.Errorf("reverse proxy %d: duplicate ID %q", i+1, r.ID)
		}
		ids[r.ID] = true
		if r.Port != 0 && (r.Port < 1024 || r.Port > 65535) {
			return fmt.Errorf("reverse proxy %s: port %d must be 1024 to 65535", r.ID, r.Port)
		}
		if r.Enabled {
			if other, ok := ports[r.Port]; ok {
				return fmt.Errorf("reverse proxy %s: port %d is already used by %s", r.ID, r.Port, other)
			}
			ports[r.Port] = r.ID
		}
		u, err := reverseTarget(r.Target)
		if err != nil {
			return fmt.Errorf("reverse proxy %s: %w", r.ID, err)
		}
		validated[i] = r
		targets[r.ID] = u
	}

	httpAddr, socksAddr := s.Addr(), s.SOCKSAddr()
	s.reverseMu.Lock()
	defer s.reverseMu.Unlock()
	s.closeReversesLocked()
	s.reverseCfg = validated
	s.reverseTargets = targets
	s.reverseStatus = make(map[string]ReverseProxyStatus, len(validated))
	if !s.Running() {
		return nil
	}
	var failures []error
	for _, r := range validated {
		if r.Enabled {
			if err := s.startReverseLocked(r, httpAddr, socksAddr); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// ReverseProxyStatus is the runtime state of each configured entry.
func (s *Server) ReverseProxyStatus() map[string]ReverseProxyStatus {
	s.reverseMu.Lock()
	defer s.reverseMu.Unlock()
	status := make(map[string]ReverseProxyStatus, len(s.reverseStatus))
	for id, st := range s.reverseStatus {
		status[id] = st
	}
	return status
}

func (s *Server) startReverseProxies(httpAddr, socksAddr string) {
	s.reverseMu.Lock()
	defer s.reverseMu.Unlock()
	for _, r := range s.reverseCfg {
		if r.Enabled {
			_ = s.startReverseLocked(r, httpAddr, socksAddr)
		}
	}
}

func (s *Server) stopReverseProxies() {
	s.reverseMu.Lock()
	defer s.reverseMu.Unlock()
	s.closeReversesLocked()
}

func (s *Server) closeReversesLocked() {
	for _, entry := range s.reverses {
		if entry.server != nil {
			_ = entry.server.Close()
		} else if entry.listener != nil {
			_ = entry.listener.Close()
		}
		if entry.transport != nil {
			entry.transport.CloseIdleConnections()
		}
	}
	s.reverses = nil
	s.reverseStatus = make(map[string]ReverseProxyStatus)
}

// startReverseLocked binds one entry and serves it. The caller holds reverseMu.
func (s *Server) startReverseLocked(r ReverseProxy, httpAddr, socksAddr string) error {
	target := s.reverseTargets[r.ID]
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port))
	if r.Port != 0 {
		if port, err := strconv.Atoi(portOf(httpAddr)); err == nil && port == r.Port {
			err := fmt.Errorf("reverse proxy %s: port %d is used by the HTTP proxy", r.ID, r.Port)
			s.reverseStatus[r.ID] = ReverseProxyStatus{ID: r.ID, Error: err.Error()}
			return err
		}
		if port, err := strconv.Atoi(portOf(socksAddr)); err == nil && port == r.Port {
			err := fmt.Errorf("reverse proxy %s: port %d is used by the SOCKS5 proxy", r.ID, r.Port)
			s.reverseStatus[r.ID] = ReverseProxyStatus{ID: r.ID, Error: err.Error()}
			return err
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		err = fmt.Errorf("reverse proxy %s on %s: %w", r.ID, addr, err)
		s.reverseStatus[r.ID] = ReverseProxyStatus{ID: r.ID, Error: err.Error()}
		return err
	}

	connectHeader := http.Header{reverseTokenHeader: {s.reverseToken}}
	if !r.Insecure {
		// The engine verifies the real remote certificate, not just this
		// proxy's own interception certificate. The header is the same one a
		// verified request send uses; it is removed before the flow exists.
		connectHeader.Set(proxy.HeaderVerifyUpstream, "1")
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: httpAddr}),
		TLSClientConfig: &tls.Config{
			// The peer must be this proxy's own interception certificate,
			// not the real server: the reverse proxy always captures.
			RootCAs:            s.caPool,
			InsecureSkipVerify: isIPLiteral(target.Hostname()),
			VerifyConnection: func(cs tls.ConnectionState) error {
				if !isIPLiteral(target.Hostname()) {
					return nil // the normal chain and host name check ran
				}
				// The engine signs a DNS name, not an IP SAN, for an
				// IP-literal target. Check the chain without a host name.
				if len(cs.PeerCertificates) == 0 {
					return errors.New("no peer certificate")
				}
				opts := x509.VerifyOptions{Roots: s.caPool, Intermediates: x509.NewCertPool()}
				for _, cert := range cs.PeerCertificates[1:] {
					opts.Intermediates.AddCert(cert)
				}
				_, err := cs.PeerCertificates[0].Verify(opts)
				return err
			},
		},
		ForceAttemptHTTP2:  true,
		DisableCompression: true,
		ProxyConnectHeader: connectHeader,
		MaxIdleConns:       16,
		IdleConnTimeout:    90 * time.Second,
	}
	handler := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := *target
			out.Path, out.RawPath = joinReversePath(target, pr.In.URL)
			out.RawQuery = pr.In.URL.RawQuery
			pr.Out.URL = &out
			// Name the remote in the request line and Host, so the engine
			// dials and records it; the client's Host, when kept, travels
			// beside it for the capture addon to restore.
			pr.Out.Host = target.Host
			pr.Out.Header.Set(reverseTokenHeader, s.reverseToken)
			if r.KeepHost && pr.In.Host != "" {
				pr.Out.Header.Set(reverseHostHeader, pr.In.Host)
			}
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "vscroxy reverse proxy: "+err.Error(), http.StatusBadGateway)
		},
	}
	var serve http.Handler = handler
	if !r.KeepHost {
		// A web page can resolve its own name to 127.0.0.1 and reach this
		// listener. With the remote Host there is no reason to accept such a
		// name: only localhost or a loopback literal may call it.
		serve = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !loopbackHost(req.Host) {
				http.Error(w, "vscroxy reverse proxy: use localhost or a loopback address", http.StatusForbidden)
				return
			}
			handler.ServeHTTP(w, req)
		})
	}
	server := &http.Server{
		Handler:           serve,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.DiscardHandler, slog.LevelError),
	}
	entry := &reverseEntry{cfg: r, target: target, listener: ln, server: server, transport: transport}
	s.reverses = append(s.reverses, entry)
	s.reverseStatus[r.ID] = ReverseProxyStatus{ID: r.ID, Addr: ln.Addr().String()}
	go func() { _ = server.Serve(ln) }()
	return nil
}

// reverseTarget validates the base URL of one entry.
func reverseTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("target %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("target %q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("target %q has no host", raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("target %q must not carry credentials", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("target %q must not carry a query or fragment", raw)
	}
	return u, nil
}

// joinReversePath appends the local request path to the target's path,
// preserving escaping. It follows the same rules as httputil's reverse proxy.
func joinReversePath(base, req *url.URL) (path, rawPath string) {
	reqPath := req.Path
	if reqPath == "" {
		reqPath = "/"
	}
	reqRaw := req.EscapedPath()
	if reqRaw == "" {
		reqRaw = reqPath
	}
	if base.RawPath == "" && req.RawPath == "" {
		return singleJoiningSlash(base.Path, reqPath), ""
	}
	basePath := base.EscapedPath()
	switch {
	case strings.HasSuffix(basePath, "/") && strings.HasPrefix(reqRaw, "/"):
		return base.Path + reqPath[1:], basePath + reqRaw[1:]
	case !strings.HasSuffix(basePath, "/") && !strings.HasPrefix(reqRaw, "/"):
		return base.Path + "/" + reqPath, basePath + "/" + reqRaw
	}
	return base.Path + reqPath, basePath + reqRaw
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func isIPLiteral(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}

// loopbackHost reports whether a Host header names this machine. It accepts
// localhost and any loopback literal, with or without a port, and rejects a
// name that merely resolves to a loopback address (DNS rebinding).
func loopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// portOf returns the numeric port of an address, or "" when it has none.
func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}
