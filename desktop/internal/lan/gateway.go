// Package lan opens the capture core to devices on the local network without
// moving the core off loopback. A raw TCP gateway on the interface address
// forwards bytes to the core's own listener, so HTTP, CONNECT, WebSocket and
// streaming all work the way the client wrote them, and a read-only HTTP
// endpoint on the same address serves the CA certificate a device installs.
// Both listeners admit only the allowed source addresses. The core keeps its
// address and its pending state; turning the gateway off only closes this
// package's own listeners and connections.
package lan

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// caPath is the only file the certificate endpoint serves.
	caPath = "/vscroxy-ca.cer"

	// certHeaderTimeout bounds how long a client may take to send its request
	// headers, and certIdleTimeout how long an idle keep-alive connection
	// stays open. Both bound a client that connects and then says nothing.
	certHeaderTimeout = 5 * time.Second
	certIdleTimeout   = 60 * time.Second
	certMaxHeader     = 16 << 10

	// dialTimeout bounds one dial to the core. It is long enough for a busy
	// core to accept and short enough that a stuck dial ends on its own.
	dialTimeout = 10 * time.Second
)

// helpText is what the certificate endpoint's root says: the two things a
// device needs, and nothing about the machine it runs on.
const helpText = `vscroxy certificate service

GET /vscroxy-ca.cer   the CA certificate, DER, to install on the device
GET /health           readiness of this endpoint
`

// Config is what the UI hands Start: where to listen, which core to forward
// to, who may connect, and which certificate to publish. Start copies what it
// keeps, so the caller may reuse or change its slices afterwards.
type Config struct {
	// ListenAddr is the proxy gateway's address as a literal ip:port on the
	// interface the device reaches. Port 0 picks a free port.
	ListenAddr string
	// CertificateAddr is the CA endpoint's address, on the same IP as
	// ListenAddr.
	CertificateAddr string
	// ProxyAddr is the capture core's listener, normally 127.0.0.1:7890.
	ProxyAddr string
	// Allowed lists the client addresses that may use either listener, as
	// exact IPs or CIDR ranges.
	Allowed []string
	// Certificate is the public CA certificate in DER form. PEM bundles are
	// not accepted; the capture core provides public DER without its key.
	Certificate []byte
}

// Status is what the gateway has seen: the last client that passed the gate on
// each listener. LastProxyAt is when that client's TCP connection was accepted,
// which says nothing yet about a request it may send or whether it succeeds.
type Status struct {
	LastProxyClient       string
	LastProxyAt           time.Time
	LastCertificateClient string
	LastCertificateAt     time.Time
}

// Gateway is the running pair of listeners. Start it once and close it when the
// UI turns the LAN access off or changes its configuration; the capture core is
// untouched either way.
type Gateway struct {
	addr      string
	certAddr  string
	proxyAddr string
	certDER   []byte

	proxyLn *gatedListener
	certLn  *gatedListener
	cert    *http.Server

	dialer *net.Dialer
	conns  *connSet

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	status atomic.Pointer[Status]

	closeOnce sync.Once
	closeErr  error
}

// Start validates cfg, binds both listeners, and serves them in the background.
// It returns only once both are bound, so a caller that sees an error knows
// neither is left listening.
func Start(cfg Config) (*Gateway, error) {
	s, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	g := &Gateway{
		proxyAddr: s.proxyAddr,
		certDER:   s.certificate,
		dialer:    &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second},
		conns:     newConnSet(),
	}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	g.status.Store(&Status{})

	proxyLn, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		g.cancel()
		return nil, fmt.Errorf("listen on %s: %w", s.listenAddr, err)
	}
	certLn, err := net.Listen("tcp", s.certAddr)
	if err != nil {
		// The proxy listener is bound already: release it, so a failed start
		// leaves no address held.
		_ = proxyLn.Close()
		g.cancel()
		return nil, fmt.Errorf("listen on %s: %w", s.certAddr, err)
	}

	g.addr = proxyLn.Addr().String()
	g.certAddr = certLn.Addr().String()
	g.proxyLn = &gatedListener{Listener: proxyLn, gate: s.gate, gateway: g, kind: proxyListener}
	g.certLn = &gatedListener{Listener: certLn, gate: s.gate, gateway: g, kind: certificateListener}
	g.cert = &http.Server{
		Handler:           g.certificateHandler(),
		ReadHeaderTimeout: certHeaderTimeout,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       certIdleTimeout,
		MaxHeaderBytes:    certMaxHeader,
		ErrorLog:          slog.NewLogLogger(slog.DiscardHandler, slog.LevelError),
	}

	g.wg.Go(g.acceptProxy)
	g.wg.Go(func() { _ = g.cert.Serve(g.certLn) })
	return g, nil
}

// Addr is the address the proxy gateway listens on, with the port it got.
func (g *Gateway) Addr() string { return g.addr }

// CertificateAddr is the address the certificate endpoint listens on.
func (g *Gateway) CertificateAddr() string { return g.certAddr }

// Status is a snapshot of the last clients each listener admitted.
func (g *Gateway) Status() Status {
	if s := g.status.Load(); s != nil {
		return *s
	}
	return Status{}
}

// Close stops both listeners and every connection they hold, on both sides of
// the forward, waits for the workers to finish, and leaves the core alone.
// Calling it again returns the same result.
func (g *Gateway) Close() error {
	g.closeOnce.Do(func() {
		// Listeners first: no new connection may join while the rest is
		// being torn down. The certificate listener is closed here as well
		// as through its server, since Close may run before Serve has
		// registered it.
		proxyErr := keepError(g.proxyLn.Close())
		certErr := keepError(g.certLn.Close())
		serverErr := keepError(g.cert.Close())
		// Cancel before closing the connections, so a dial still in flight
		// returns instead of waiting for its timeout.
		g.cancel()
		g.conns.closeAll()
		g.wg.Wait()
		g.closeErr = errors.Join(proxyErr, certErr, serverErr)
	})
	return g.closeErr
}

// acceptProxy hands each admitted connection to a forward worker. A connection
// the gate refused never reaches this loop: the listener closes it.
func (g *Gateway) acceptProxy() {
	for {
		client, err := g.proxyLn.accept()
		if err != nil {
			return
		}
		g.wg.Go(func() { g.forward(client) })
	}
}

// forward dials the core for one client and copies bytes both ways until both
// directions end. A dial the close interrupts never reaches the core.
func (g *Gateway) forward(client *trackedConn) {
	defer client.Close()

	raw, err := g.dialer.DialContext(g.ctx, "tcp", g.proxyAddr)
	if err != nil {
		return
	}
	upstream, ok := g.conns.add(raw)
	if !ok {
		// The gateway closed while this dial was finishing.
		_ = raw.Close()
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go pipe(upstream, client, done)
	go pipe(client, upstream, done)
	<-done
	<-done
}

// pipe copies one direction and passes its end on as a half-close, so a client
// that stops sending still gets its response and a core that finishes still
// lets the client finish sending.
func pipe(dst, src *trackedConn, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	_ = dst.CloseWrite()
	done <- struct{}{}
}

// note records the client each listener admitted, for the UI's status line.
func (g *Gateway) note(kind listenerKind, client string) {
	for {
		old := g.status.Load()
		next := Status{}
		if old != nil {
			next = *old
		}
		at := time.Now()
		switch kind {
		case proxyListener:
			next.LastProxyClient, next.LastProxyAt = client, at
		case certificateListener:
			next.LastCertificateClient, next.LastCertificateAt = client, at
		}
		if g.status.CompareAndSwap(old, &next) {
			return
		}
	}
}

// certificateHandler serves the certificate endpoint: the CA in DER form, a
// readiness answer, and a short help page. Anything else is not found, so no
// file next to the certificate is reachable. A GET pattern also answers HEAD.
func (g *Gateway) certificateHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+caPath, g.serveCertificate)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, "ok\n")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, helpText)
	})
	return mux
}

// serveCertificate returns exactly the DER the caller handed Start: no key, no
// bundle, nothing read from disk.
func (g *Gateway) serveCertificate(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "application/x-x509-ca-cert")
	h.Set("Content-Disposition", `attachment; filename="vscroxy-ca.cer"`)
	h.Set("Content-Length", strconv.Itoa(len(g.certDER)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(g.certDER)
}

// writeText answers with plain text, never HTML: the endpoint is read by a
// browser the user points at it, not rendered as a page.
func writeText(w http.ResponseWriter, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, body)
}

// keepError drops the error a close of an already closed listener reports, so
// a normal shutdown is not reported as a failure.
func keepError(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// settings is the validated form of Config: addresses ready to bind, the gate,
// and the bytes to serve.
type settings struct {
	listenAddr  string
	certAddr    string
	proxyAddr   string
	gate        gate
	certificate []byte
}

// validate checks everything Start needs before a socket exists, so a
// configuration that cannot work fails without leaving a listener behind.
func (c Config) validate() (settings, error) {
	listenIP, listenAddr, err := listenerAddr("ListenAddr", c.ListenAddr)
	if err != nil {
		return settings{}, err
	}
	certIP, certAddr, err := listenerAddr("CertificateAddr", c.CertificateAddr)
	if err != nil {
		return settings{}, err
	}
	if listenIP != certIP {
		return settings{}, fmt.Errorf("ListenAddr %q and CertificateAddr %q are on different addresses", c.ListenAddr, c.CertificateAddr)
	}
	coreAddr, err := coreAddress(c.ProxyAddr)
	if err != nil {
		return settings{}, err
	}
	if err := caCertificate(c.Certificate); err != nil {
		return settings{}, err
	}
	gate, err := newGate(c.Allowed)
	if err != nil {
		return settings{}, err
	}
	return settings{
		listenAddr:  listenAddr,
		certAddr:    certAddr,
		proxyAddr:   coreAddr,
		gate:        gate,
		certificate: bytes.Clone(c.Certificate),
	}, nil
}

// listenerAddr parses an address that must name one interface address
// literally. A host name would be resolved to whatever it means now, and the
// wildcard address would listen beyond the interface the UI chose.
func listenerAddr(field, raw string) (netip.Addr, string, error) {
	if raw == "" {
		return netip.Addr{}, "", fmt.Errorf("%s is empty", field)
	}
	ap, err := netip.ParseAddrPort(raw)
	if err != nil {
		return netip.Addr{}, "", fmt.Errorf("%s %q is not a literal ip:port address: %w", field, raw, err)
	}
	if ap.Addr().Unmap().IsUnspecified() {
		return netip.Addr{}, "", fmt.Errorf("%s %q listens on every interface", field, raw)
	}
	return ap.Addr(), ap.String(), nil
}

// coreAddress checks the core's address: a host and a real port. The wildcard
// address is refused, since dialling it would reach this machine's own
// listeners rather than the core.
func coreAddress(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("ProxyAddr is empty")
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("ProxyAddr %q: %w", raw, err)
	}
	if host == "" {
		return "", fmt.Errorf("ProxyAddr %q has no host", raw)
	}
	if p, err := strconv.ParseUint(port, 10, 16); err != nil || p == 0 {
		return "", fmt.Errorf("ProxyAddr %q has no usable port", raw)
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Unmap().IsUnspecified() {
		return "", fmt.Errorf("ProxyAddr %q names every interface", raw)
	}
	return net.JoinHostPort(host, port), nil
}

// caCertificate checks that the bytes are one CA certificate in DER form. PEM
// is refused rather than decoded: the caller reads the file, this package only
// publishes what it was given.
func caCertificate(der []byte) error {
	if len(der) == 0 {
		return errors.New("Certificate is empty")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("Certificate is not a DER certificate: %w", err)
	}
	if !cert.BasicConstraintsValid || !cert.IsCA {
		return errors.New("Certificate is not a CA certificate")
	}
	return nil
}

// gate decides whether a source address may use either listener. It is built
// once and never changes: a new configuration is a new gateway.
type gate struct {
	prefixes []netip.Prefix
}

// newGate parses the allowed addresses. An empty list is refused rather than
// read as "everyone": the gateway exists to be narrow.
func newGate(allowed []string) (gate, error) {
	var g gate
	for i, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		prefix, err := allowedPrefix(entry)
		if err != nil {
			return gate{}, fmt.Errorf("Allowed[%d]: %w", i, err)
		}
		g.prefixes = append(g.prefixes, prefix)
	}
	if len(g.prefixes) == 0 {
		return gate{}, errors.New("Allowed is empty: no client may use the gateway")
	}
	return g, nil
}

// allowedPrefix reads one entry: an exact address, or a CIDR range. A range
// with no bits set would allow every address, so it is refused.
func allowedPrefix(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		p, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a CIDR range: %w", entry, err)
		}
		p = p.Masked()
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			// Keep the range comparable with the unmapped addresses of
			// clients that connect over IPv4.
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		if p.Bits() == 0 {
			return netip.Prefix{}, fmt.Errorf("%q allows every address", entry)
		}
		return p, nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or CIDR range: %w", entry, err)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// allows reports whether a client address may connect. An IPv4-mapped address
// is compared as the IPv4 address it names.
func (g gate) allows(addr netip.Addr) bool {
	addr = addr.Unmap()
	return slices.ContainsFunc(g.prefixes, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// listenerKind says which listener admitted a client, for the status line.
type listenerKind int

const (
	proxyListener listenerKind = iota
	certificateListener
)

// gatedListener refuses a client the gate does not allow before the proxy dials
// anything or the HTTP server reads a byte. Only the TCP source address counts:
// no request header can change it.
type gatedListener struct {
	net.Listener
	gate    gate
	gateway *Gateway
	kind    listenerKind
}

// Accept implements net.Listener for the HTTP server, which needs the
// connections the gate admitted and tracked.
func (l *gatedListener) Accept() (net.Conn, error) {
	conn, err := l.accept()
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// accept returns a tracked connection for an allowed client, and keeps looking
// past the ones it refuses.
func (l *gatedListener) accept() (*trackedConn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		addr, ok := sourceAddr(conn)
		if !ok || !l.gate.allows(addr) {
			_ = conn.Close()
			continue
		}
		tracked, ok := l.gateway.conns.add(conn)
		if !ok {
			// The gateway closed while this connection was being accepted:
			// close it here rather than hand it to a worker that cannot
			// finish.
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		l.gateway.note(l.kind, tracked.RemoteAddr().String())
		return tracked, nil
	}
}

// sourceAddr is the address a connection came from. A connection whose address
// cannot be read is refused.
func sourceAddr(conn net.Conn) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr(), true
}

// connSet tracks every connection the gateway holds, on both sides of each
// forward, so Close can stop them all and no connection outlives the gateway.
type connSet struct {
	mu     sync.Mutex
	closed bool
	active map[*trackedConn]struct{}
}

func newConnSet() *connSet {
	return &connSet{active: make(map[*trackedConn]struct{})}
}

// add tracks a connection, unless the set is already closed: then the caller
// owns closing it.
func (c *connSet) add(conn net.Conn) (*trackedConn, bool) {
	tracked := &trackedConn{Conn: conn, set: c}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false
	}
	c.active[tracked] = struct{}{}
	return tracked, true
}

// drop forgets a connection that closed.
func (c *connSet) drop(conn *trackedConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, conn)
}

// closeAll refuses further connections and closes the ones held now. A
// connection added after this call is refused by add, so nothing leaks.
func (c *connSet) closeAll() {
	c.mu.Lock()
	c.closed = true
	held := slices.Collect(maps.Keys(c.active))
	c.mu.Unlock()
	for _, conn := range held {
		_ = conn.Close()
	}
}

// trackedConn is one connection the gateway holds. Closing it, from either
// side, also takes it out of the set.
type trackedConn struct {
	net.Conn
	set  *connSet
	once sync.Once
}

// Close closes the connection and forgets it, whichever side closes first.
func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.set.drop(c) })
	return err
}

// CloseWrite ends this direction and leaves the other one open, which is what
// a proxy does with a client that has finished sending.
func (c *trackedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}
