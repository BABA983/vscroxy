package certs

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// NormalizeHost returns the canonical host pattern, or "" when raw is not a
// valid host. A URL is reduced to its host. Invalid patterns never match.
func NormalizeHost(raw string) string {
	host, err := normalizeHost(raw)
	if err != nil {
		return ""
	}
	return host
}

// ValidateHost explains why raw is not a host pattern. A host may be a name,
// an IP address, or "*" / "*.parent"; an optional port must be 1-65535.
func ValidateHost(raw string) error {
	_, err := normalizeHost(raw)
	return err
}

func normalizeHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("host is empty")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("%q is not a host", raw)
		}
		raw = u.Host
	}
	if strings.ContainsAny(raw, "/\\ \t\r\n") {
		return "", fmt.Errorf("%q is not a host", raw)
	}
	raw = strings.ToLower(raw)
	host, port := splitHostPort(raw)
	if host == "" {
		return "", fmt.Errorf("%q is not a host", raw)
	}
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("%q has an invalid port", raw)
		}
	}
	wildcard := ""
	switch {
	case host == "*":
	case strings.HasPrefix(host, "*."):
		wildcard, host = "*.", strings.TrimPrefix(host, "*.")
		if strings.Contains(host, "*") {
			return "", fmt.Errorf("%q has a wildcard in the middle", raw)
		}
	default:
		if strings.Contains(host, "*") {
			return "", fmt.Errorf("%q has a wildcard in the middle", raw)
		}
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("%q is not a host", raw)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return "", fmt.Errorf("%q has an empty label", raw)
		}
	}
	ascii, err := asciiHost(host)
	if err != nil {
		return "", fmt.Errorf("%q: %w", raw, err)
	}
	host = wildcard + ascii
	if port == "" {
		return host, nil
	}
	return net.JoinHostPort(host, port), nil
}

// asciiHost converts a non-ASCII host name to its IDNA punycode form. An ASCII
// host is kept as written, so underscores and other local names still work.
func asciiHost(host string) (string, error) {
	ascii := true
	for _, r := range host {
		if r > 0x7f {
			ascii = false
			break
		}
	}
	if ascii {
		return host, nil
	}
	converted, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", err
	}
	return converted, nil
}

// Address is the host:port a URL's TLS connection names. The scheme's default
// port fills in a missing one, so a certificate configured for
// "api.example.com:443" matches "https://api.example.com". International host
// names become their punycode form.
func Address(u *url.URL) string {
	if u == nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if converted, err := asciiHost(host); err == nil {
		host = converted
	}
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
// Trailing dots stay for the caller to validate and trim.
func splitHostPort(raw string) (host, port string) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(raw); err == nil {
		return strings.Trim(h, "[]"), p
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return strings.Trim(raw, "[]"), ""
	}
	if strings.Count(raw, ":") > 1 {
		return raw, "" // bare IPv6
	}
	if host, port, ok := strings.Cut(raw, ":"); ok {
		if port == "" {
			return host, ""
		}
		return host, port
	}
	return raw, ""
}
