package certs

import (
	"net"
	"net/url"
	"strings"
)

// NormalizeHost turns a host pattern or address into the form matching uses:
// lowercase, no trailing dot, and host:port when a port is present. A leading
// "*." is kept. A URL is reduced to its host.
func NormalizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			raw = u.Host
		}
	}
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.ToLower(raw)
	host, port := splitHostPort(raw)
	if host == "" {
		return ""
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

// Address is the host:port a URL's TLS connection names. The scheme's default
// port fills in a missing one, so a certificate configured for
// "api.example.com:443" matches "https://api.example.com".
func Address(u *url.URL) string {
	if u == nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https", "wss":
			port = "443"
		case "http", "ws":
			port = "80"
		}
	}
	if host == "" {
		return ""
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

// Match reports whether a certificate configured for pattern applies to
// address. The pattern is a host, optionally with a port; a leading "*."
// matches one or more labels under the remaining name. A pattern without a
// port matches every port.
func Match(pattern, address string) bool {
	patternHost, patternPort := splitHostPort(NormalizeHost(pattern))
	addressHost, addressPort := splitHostPort(NormalizeHost(address))
	if patternHost == "" || addressHost == "" {
		return false
	}
	if patternPort != "" && patternPort != addressPort {
		return false
	}
	switch {
	case patternHost == "*":
		return true
	case strings.HasPrefix(patternHost, "*."):
		parent := strings.TrimPrefix(patternHost, "*.")
		return parent != "" && strings.HasSuffix(addressHost, "."+parent) && addressHost != parent
	default:
		return patternHost == addressHost
	}
}

// score orders matches so an exact host wins over a wildcard, a wildcard over
// "*", and a port-specific entry over one that applies to every port.
func score(pattern, address string) (int, bool) {
	if !Match(pattern, address) {
		return 0, false
	}
	host, port := splitHostPort(NormalizeHost(pattern))
	value := 0
	switch {
	case host == "*":
		value = 0
	case strings.HasPrefix(host, "*."):
		value = 500 + len(host)
	default:
		value = 1000 + len(host)
	}
	if port != "" {
		value += 100
	}
	return value, true
}

// splitHostPort separates host and port without treating a bare IPv6 address
// as a host with a port. Brackets around a bare IPv6 address are removed.
func splitHostPort(raw string) (host, port string) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(raw); err == nil {
		return strings.TrimSuffix(strings.Trim(h, "[]"), "."), p
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return strings.TrimSuffix(strings.Trim(raw, "[]"), "."), ""
	}
	if strings.Count(raw, ":") > 1 {
		return raw, "" // bare IPv6
	}
	if host, port, ok := strings.Cut(raw, ":"); ok {
		host = strings.TrimSuffix(host, ".")
		if port == "" {
			return host, ""
		}
		return host, port
	}
	return strings.TrimSuffix(raw, "."), ""
}
