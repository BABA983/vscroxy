// Package upstream decides where captured and authored traffic goes next:
// directly, through a proxy selected by host, or through a proxy list a PAC
// script returns. It owns no listener and never imports MyGo.
//
// Selection order is fixed: a bypass entry sends the request directly, then
// the first matching host rule wins, then a configured PAC script decides,
// then the default upstream applies. A rule or the default may say DIRECT.
package upstream

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/ntlm"
)

// Rule sends requests whose host matches Host through Proxy. Proxy is a proxy
// address or "direct". Host is an exact host, a *.example.com wildcard, or *;
// an optional :port limits the rule to that port.
type Rule struct {
	Host  string `json:"host"`
	Proxy string `json:"proxy"`
}

// The authentication methods one proxy endpoint may use. An empty Auth is
// Basic, so a settings file written before NTLM existed stays valid.
const (
	AuthBasic = "basic"
	AuthNTLM  = "ntlm"
)

// Credential names the account for one proxy endpoint. The password is kept
// separately, sealed at rest, and never written to settings or exports.
// Auth is empty (or AuthBasic) for HTTP Basic, and AuthNTLM for NTLMv2;
// Domain and Workstation apply only to NTLM.
type Credential struct {
	Proxy       string `json:"proxy"` // scheme://host:port
	Username    string `json:"username,omitempty"`
	Auth        string `json:"auth,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Workstation string `json:"workstation,omitempty"`
}

// Settings is the persisted upstream configuration. It never holds a
// password: Passwords are sealed by SecretStore and supplied at runtime.
type Settings struct {
	// Default is the proxy used when nothing else applies, or empty for a
	// direct connection.
	Default string `json:"default,omitempty"`
	// Rules are tried in order; the first matching host wins.
	Rules []Rule `json:"rules,omitempty"`
	// Bypass sends matching hosts directly before any rule or PAC runs.
	Bypass []string `json:"bypass,omitempty"`
	// PAC is a local file path or an http(s) URL.
	PAC string `json:"pac,omitempty"`
	// Credentials are the usernames for proxy endpoints. Passwords are not
	// part of this structure.
	Credentials []Credential `json:"credentials,omitempty"`
}

// Clone returns a copy that shares no slices with s.
func (s Settings) Clone() Settings {
	s.Rules = slices.Clone(s.Rules)
	s.Bypass = slices.Clone(s.Bypass)
	s.Credentials = slices.Clone(s.Credentials)
	return s
}

// Enabled reports whether any upstream selection is configured.
func (s Settings) Enabled() bool {
	return s.Default != "" || len(s.Rules) > 0 || s.PAC != ""
}

// Summary names the active configuration in one word for a toolbar chip.
func (s Settings) Summary() string {
	switch {
	case s.Default != "":
		return s.Default
	case len(s.Rules) > 0:
		return "Rules"
	case s.PAC != "":
		return "PAC"
	default:
		return ""
	}
}

// Normalize trims the values an editor may leave padded and drops empty
// bypass and credential rows. It does not validate addresses.
func (s Settings) Normalize() Settings {
	s = s.Clone()
	s.Default = strings.TrimSpace(s.Default)
	s.PAC = strings.TrimSpace(s.PAC)
	rules := s.Rules[:0]
	for _, r := range s.Rules {
		r.Host, r.Proxy = strings.TrimSpace(r.Host), strings.TrimSpace(r.Proxy)
		if r.Host == "" && r.Proxy == "" {
			continue
		}
		rules = append(rules, r)
	}
	s.Rules = rules
	bypass := s.Bypass[:0]
	for _, b := range s.Bypass {
		if b = strings.TrimSpace(b); b != "" && !strings.HasPrefix(b, "#") {
			bypass = append(bypass, b)
		}
	}
	s.Bypass = bypass
	creds := s.Credentials[:0]
	for _, c := range s.Credentials {
		c.Proxy, c.Username = strings.TrimSpace(c.Proxy), strings.TrimSpace(c.Username)
		c.Auth = strings.ToLower(strings.TrimSpace(c.Auth))
		c.Domain, c.Workstation = strings.TrimSpace(c.Domain), strings.TrimSpace(c.Workstation)
		if c.Auth == AuthBasic {
			c.Auth = ""
		}
		if c.Auth != AuthNTLM {
			c.Domain, c.Workstation = "", ""
		}
		if c.Proxy == "" {
			continue
		}
		creds = append(creds, c)
	}
	s.Credentials = creds
	return s
}

// Validate checks addresses, host patterns and PAC syntax without touching the
// network. New performs the same checks and also loads a PAC script.
func (s Settings) Validate() error {
	_, err := compile(s.Normalize(), nil, false)
	return err
}

// Candidate is one place a request can go, in preference order. A nil Proxy
// is a direct connection.
type Candidate struct {
	Proxy  *Proxy
	Reason string
}

// Decision is the ordered candidate list for one request. An empty list means
// no upstream selection applies and the request connects directly.
type Decision struct {
	Candidates []Candidate
	Reason     string
}

// Proxy is one upstream endpoint. URL carries the runtime credentials, if any;
// Display never does.
type Proxy struct {
	URL *url.URL
}

// Display is the endpoint without credentials, for logs and reasons.
func (p *Proxy) Display() string {
	if p == nil || p.URL == nil {
		return "DIRECT"
	}
	return p.URL.Scheme + "://" + p.URL.Host
}

var (
	// ErrAuthRejected marks an NTLM exchange whose answer the proxy refused.
	ErrAuthRejected = errors.New("the upstream proxy rejected the NTLM credentials")
	// ErrAuthUnsupported marks a 407 that offered neither NTLM nor Negotiate.
	ErrAuthUnsupported = errors.New("the upstream proxy did not offer NTLM or Negotiate")
)

// NTLMSession is one NTLMv2 exchange with an upstream proxy. NTLM ties the
// challenge to the connection, so a session is used for exactly one
// connection and must not be shared between requests.
type NTLMSession struct {
	creds ntlm.Credentials
	type1 []byte
	// scheme is where the Type 1 message went. The preemptive header uses
	// NTLM; a proxy that offers only Negotiate receives the same messages
	// there.
	scheme string
	// type1Count bounds a proxy that keeps sending a bare challenge.
	type1Count int
	type3      bool
}

// NTLMSession returns the exchange for a candidate, or nil when the endpoint
// uses Basic or has no credentials.
func (c *Config) NTLMSession(px *Proxy) (*NTLMSession, error) {
	if c == nil || px == nil || px.URL == nil || px.URL.User == nil {
		return nil, nil
	}
	key := px.URL.Scheme + "://" + px.URL.Host
	cred, ok := c.credentials[key]
	if !ok || cred.auth != AuthNTLM {
		return nil, nil
	}
	password, _ := px.URL.User.Password()
	creds := ntlm.Credentials{
		Username: px.URL.User.Username(), Password: password,
		Domain: cred.domain, Workstation: cred.workstation,
	}
	type1, err := creds.NegotiateMessage()
	if err != nil {
		return nil, fmt.Errorf("NTLM for %s: %w", key, err)
	}
	return &NTLMSession{creds: creds, type1: type1, scheme: ntlm.SchemeNTLM, type1Count: 1}, nil
}

// Initial is the Proxy-Authorization value for the first request.
func (s *NTLMSession) Initial() string {
	return ntlm.SchemeNTLM + " " + base64.StdEncoding.EncodeToString(s.type1)
}

// Answer returns the Proxy-Authorization value for a 407's challenges. An
// error means the exchange cannot continue; the 407 is then the result.
func (s *NTLMSession) Answer(challenges []string) (string, error) {
	scheme, type2, ok := ntlm.ProxyChallenge(challenges)
	if !ok {
		return "", ErrAuthUnsupported
	}
	if s.type3 {
		return "", ErrAuthRejected
	}
	if !strings.EqualFold(scheme, s.scheme) {
		// A proxy that offers only Negotiate receives the same NTLM messages
		// in that scheme.
		s.scheme, s.type1Count = scheme, 0
	}
	if len(type2) == 0 {
		if s.type1Count >= 2 {
			return "", ErrAuthRejected
		}
		s.type1Count++
		return s.scheme + " " + base64.StdEncoding.EncodeToString(s.type1), nil
	}
	message, err := s.creds.AuthenticateMessage(type2)
	if err != nil {
		return "", err
	}
	s.type3 = true
	return s.scheme + " " + base64.StdEncoding.EncodeToString(message), nil
}

// Config is a validated, immutable upstream configuration. Resolve is safe
// for concurrent use.
type Config struct {
	settings    Settings
	defaultPx   *Proxy
	rules       []hostRule
	bypass      []bypassRule
	pac         *PAC
	credentials map[string]credential
}

type credential struct {
	username    string
	password    string
	auth        string
	domain      string
	workstation string
}

type hostRule struct {
	raw    string
	host   string
	port   int
	glob   bool
	proxy  *Proxy
	direct bool
}

type bypassKind int

const (
	bypassExact bypassKind = iota
	bypassSubdomains
	bypassDomain
	bypassAny
	bypassLocal
	bypassLoopback
	bypassCIDR
)

type bypassRule struct {
	raw    string
	kind   bypassKind
	host   string
	port   int
	prefix netip.Prefix
}

// New validates settings, loads its PAC script when one is configured, and
// attaches passwords by proxy endpoint. A PAC load or syntax error is
// returned: callers keep the previous configuration rather than silently
// falling back to a direct connection.
func New(settings Settings, passwords map[string]string) (*Config, error) {
	settings = settings.Normalize()
	return compile(settings, passwords, true)
}

func compile(settings Settings, passwords map[string]string, loadPAC bool) (*Config, error) {
	cfg := &Config{settings: settings, credentials: make(map[string]credential)}
	for _, c := range settings.Credentials {
		key, err := canonicalProxyKey(c.Proxy)
		if err != nil {
			return nil, fmt.Errorf("credential %q: %w", c.Proxy, err)
		}
		if _, ok := cfg.credentials[key]; ok {
			return nil, fmt.Errorf("credentials for %s are listed twice", key)
		}
		auth := strings.ToLower(strings.TrimSpace(c.Auth))
		if auth == AuthBasic {
			auth = ""
		}
		switch auth {
		case "":
		case AuthNTLM:
			if !strings.HasPrefix(key, "http://") && !strings.HasPrefix(key, "https://") {
				return nil, fmt.Errorf("credential %s: NTLM needs an http:// or https:// upstream", key)
			}
			if strings.TrimSpace(c.Username) == "" {
				return nil, fmt.Errorf("credential %s: NTLM needs a username", key)
			}
		default:
			return nil, fmt.Errorf("credential %s: unknown authentication %q", key, c.Auth)
		}
		cfg.credentials[key] = credential{
			username: c.Username, password: passwords[key], auth: auth,
			domain: c.Domain, workstation: c.Workstation,
		}
	}
	if settings.Default != "" {
		px, err := cfg.parseProxy(settings.Default)
		if err != nil {
			return nil, fmt.Errorf("default upstream: %w", err)
		}
		cfg.defaultPx = px
	}
	for i, r := range settings.Rules {
		rule, err := cfg.compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		cfg.rules = append(cfg.rules, rule)
	}
	for i, raw := range settings.Bypass {
		rule, err := compileBypass(raw)
		if err != nil {
			return nil, fmt.Errorf("bypass entry %d: %w", i+1, err)
		}
		cfg.bypass = append(cfg.bypass, rule)
	}
	if settings.PAC != "" && loadPAC {
		display := PACDisplay(settings.PAC)
		source, err := loadPACSource(settings.PAC)
		if err != nil {
			return nil, fmt.Errorf("PAC %s: %w", display, err)
		}
		pac, err := newPAC(source, display, PACOptions{})
		if err != nil {
			return nil, fmt.Errorf("PAC %s: %w", display, err)
		}
		cfg.pac = pac
	}
	return cfg, nil
}

func (c *Config) compileRule(r Rule) (hostRule, error) {
	pattern, port, err := splitHostPortPattern(r.Host)
	if err != nil {
		return hostRule{}, err
	}
	if pattern == "" {
		return hostRule{}, errors.New("a host pattern is required")
	}
	rule := hostRule{raw: r.Host, host: pattern, port: port}
	switch {
	case strings.EqualFold(strings.TrimSpace(r.Proxy), "direct"):
		rule.direct = true
	case strings.TrimSpace(r.Proxy) == "":
		return hostRule{}, errors.New("an upstream address or direct is required")
	default:
		px, err := c.parseProxy(r.Proxy)
		if err != nil {
			return hostRule{}, err
		}
		rule.proxy = px
	}
	if strings.ContainsAny(pattern, "*?[") {
		if _, err := path.Match(pattern, "example.com"); err != nil {
			return hostRule{}, fmt.Errorf("host pattern %q: %w", pattern, err)
		}
		rule.glob = true
	}
	return rule, nil
}

// Resolve returns the candidates for a request. rawURL is what a PAC script
// sees; host and port are the destination after any rewrite.
func (c *Config) Resolve(rawURL, host string, port int) (Decision, error) {
	return c.ResolveContext(context.Background(), rawURL, host, port)
}

// ResolveContext is Resolve with a context that cancels PAC evaluation and
// DNS lookups.
func (c *Config) ResolveContext(ctx context.Context, rawURL, host string, port int) (Decision, error) {
	if c == nil {
		return Decision{}, nil
	}
	host = normalizeHost(host)
	if host == "" {
		return Decision{}, errors.New("upstream: the request has no host")
	}
	if reason, ok := c.bypasses(host, port); ok {
		return Decision{Candidates: []Candidate{{Reason: "DIRECT · bypass " + reason}}, Reason: "bypass"}, nil
	}
	for _, r := range c.rules {
		if !r.matches(host, port) {
			continue
		}
		reason := "rule " + r.raw + " · "
		if r.direct {
			return Decision{Candidates: []Candidate{{Reason: reason + "DIRECT"}}, Reason: "rule"}, nil
		}
		return Decision{Candidates: c.withCredentials([]Candidate{{Proxy: r.proxy, Reason: reason + r.proxy.Display()}})}, nil
	}
	if c.pac != nil {
		candidates, display, err := c.pac.findProxy(ctx, rawURL, host, port)
		if err != nil {
			return Decision{}, fmt.Errorf("PAC: %w", err)
		}
		candidates = c.withCredentials(candidates)
		for i := range candidates {
			candidates[i].Reason = "PAC · " + display
		}
		return Decision{Candidates: candidates, Reason: "PAC"}, nil
	}
	if c.defaultPx != nil {
		return Decision{Candidates: c.withCredentials([]Candidate{{Proxy: c.defaultPx, Reason: "default · " + c.defaultPx.Display()}})}, nil
	}
	if c.settings.Enabled() {
		return Decision{Candidates: []Candidate{{Reason: "default · DIRECT"}}, Reason: "default"}, nil
	}
	return Decision{}, nil
}

// Settings returns a copy of the configuration without passwords.
func (c *Config) Settings() Settings {
	if c == nil {
		return Settings{}
	}
	return c.settings.Clone()
}

func (c *Config) parseProxy(raw string) (*Proxy, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	case "":
		return nil, errors.New("a proxy address needs http://, https:// or socks5://")
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("a proxy address needs a host")
	}
	if u.User != nil {
		return nil, errors.New("put credentials in the credentials list, not in the address")
	}
	port := u.Port()
	if port == "" {
		return nil, errors.New("a proxy address needs a port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("proxy port %q must be 1 to 65535", port)
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("a proxy address cannot have a path, query or fragment")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "socks5h" {
		scheme = "socks5"
	}
	out := &url.URL{Scheme: scheme, Host: strings.ToLower(net.JoinHostPort(u.Hostname(), port))}
	key := out.Scheme + "://" + out.Host
	if cred, ok := c.credentials[key]; ok {
		out.User = url.UserPassword(cred.username, cred.password)
	}
	return &Proxy{URL: out}, nil
}

func (c *Config) bypasses(host string, port int) (string, bool) {
	for _, b := range c.bypass {
		if b.matches(host, port) {
			return b.raw, true
		}
	}
	return "", false
}

func (r hostRule) matches(host string, port int) bool {
	if r.port != 0 && r.port != port {
		return false
	}
	if r.glob {
		ok, _ := path.Match(r.host, host)
		return ok
	}
	return host == r.host
}

func (b bypassRule) matches(host string, port int) bool {
	if b.port != 0 && b.port != port {
		return false
	}
	switch b.kind {
	case bypassAny:
		return true
	case bypassLocal:
		return !strings.Contains(host, ".") && !isIP(host)
	case bypassLoopback:
		if host == "localhost" {
			return true
		}
		ip, err := netip.ParseAddr(host)
		return err == nil && ip.Unmap().IsLoopback()
	case bypassExact:
		return host == b.host
	case bypassSubdomains:
		return strings.HasSuffix(host, "."+b.host)
	case bypassDomain:
		return host == b.host || strings.HasSuffix(host, "."+b.host)
	case bypassCIDR:
		ip, err := netip.ParseAddr(host)
		return err == nil && b.prefix.Contains(ip.Unmap())
	default:
		return false
	}
}

func compileBypass(raw string) (bypassRule, error) {
	entry := strings.TrimSpace(raw)
	rule := bypassRule{raw: entry}
	switch {
	case entry == "":
		return rule, errors.New("an entry cannot be empty")
	case strings.EqualFold(entry, "<local>"):
		rule.kind = bypassLocal
		return rule, nil
	case strings.EqualFold(entry, "localhost"):
		rule.kind = bypassLoopback
		return rule, nil
	case entry == "*":
		rule.kind = bypassAny
		return rule, nil
	case strings.Contains(entry, "/"):
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return rule, fmt.Errorf("%q is not a CIDR range: %w", entry, err)
		}
		rule.kind, rule.prefix = bypassCIDR, prefix.Masked()
		return rule, nil
	}
	host, port, err := splitHostPortPattern(entry)
	if err != nil {
		return rule, err
	}
	rule.host, rule.port = host, port
	switch {
	case host == "*":
		rule.kind = bypassAny
	case strings.EqualFold(host, "<local>"):
		rule.kind = bypassLocal
	case strings.EqualFold(host, "localhost"):
		rule.kind = bypassLoopback
	case strings.HasPrefix(host, "*."):
		rule.kind, rule.host = bypassSubdomains, strings.TrimPrefix(host, "*.")
	case strings.HasPrefix(host, "."):
		rule.kind, rule.host = bypassDomain, strings.TrimPrefix(host, ".")
	default:
		rule.kind = bypassExact
	}
	if rule.host == "" {
		return rule, fmt.Errorf("%q has no host", entry)
	}
	return rule, nil
}

// splitHostPortPattern reads host:port, [ipv6]:port or a bare host/pattern.
// A zero port means any port.
func splitHostPortPattern(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, nil
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("port %q must be 1 to 65535", port)
		}
		return normalizePattern(host), n, nil
	}
	// A single colon and a numeric suffix is a port; a bare IPv6 address is
	// not, and is kept whole.
	if i := strings.LastIndexByte(raw, ':'); i > 0 && strings.Count(raw, ":") == 1 {
		if n, err := strconv.Atoi(raw[i+1:]); err == nil {
			if n < 1 || n > 65535 {
				return "", 0, fmt.Errorf("port %q must be 1 to 65535", raw[i+1:])
			}
			return normalizePattern(raw[:i]), n, nil
		}
	}
	return normalizePattern(raw), 0, nil
}

func normalizePattern(host string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

func isIP(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}

// canonicalProxyKey validates an endpoint and returns scheme://host:port,
// without credentials, as credentials are keyed.
func canonicalProxyKey(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	if u.Hostname() == "" || u.Port() == "" {
		return "", errors.New("a proxy address needs a host and a port")
	}
	if n, err := strconv.Atoi(u.Port()); err != nil || n < 1 || n > 65535 {
		return "", errors.New("the proxy port must be 1 to 65535")
	}
	if u.User != nil {
		return "", errors.New("put credentials in the credentials list, not in the address")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "socks5h" {
		scheme = "socks5"
	}
	return scheme + "://" + strings.ToLower(net.JoinHostPort(u.Hostname(), u.Port())), nil
}

// ValidateAddress checks one proxy address as the settings editor spells it.
func ValidateAddress(raw string) error {
	if strings.EqualFold(strings.TrimSpace(raw), "direct") || strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	case "":
		return errors.New("use http://, https:// or socks5://")
	default:
		return fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	if u.User != nil {
		return errors.New("put credentials in the credentials list, not in the address")
	}
	if u.Hostname() == "" {
		return errors.New("add a host")
	}
	if u.Port() == "" {
		return errors.New("add a port")
	}
	if n, err := strconv.Atoi(u.Port()); err != nil || n < 1 || n > 65535 {
		return errors.New("the port must be 1 to 65535")
	}
	return nil
}

// CanonicalAddress validates a proxy endpoint and returns scheme://host:port,
// without credentials. Credentials are keyed by this string.
func CanonicalAddress(raw string) (string, error) { return canonicalProxyKey(raw) }

// ValidateBypass checks one bypass entry as the settings editor spells it.
func ValidateBypass(raw string) error {
	_, err := compileBypass(raw)
	return err
}

// ValidateHostPattern checks one rule's host pattern.
func ValidateHostPattern(raw string) error {
	host, _, err := splitHostPortPattern(raw)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("add a host pattern")
	}
	if strings.ContainsAny(host, "*?[") {
		if _, err := path.Match(host, "example.com"); err != nil {
			return fmt.Errorf("host pattern: %w", err)
		}
	}
	return nil
}
