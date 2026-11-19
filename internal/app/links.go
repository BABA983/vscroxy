package app

import (
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// cgnat is the shared address space browsers can reach on an operator's
// network. It is not RFC 1918 private, but it must not be trusted by default.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// linkRisk marks what makes a link's destination worth a second look. The
// pieces combine: a private HTTP address and a disguised host show both.
type linkRisk uint8

const (
	linkRiskUserinfo linkRisk = 1 << iota
	linkRiskScheme
	linkRiskAddress
	linkRiskNoHost
	linkRiskMalformedHost
)

// linkBlock is a destination the app never hands to the system browser.
type linkBlock uint8

const (
	linkBlockNone linkBlock = iota
	linkBlockScript
	linkBlockFile
	linkBlockData
)

// linkTarget is the pure classification of a destination found in content.
// Raw is kept for the dialog and for opening: rewriting a URL before the
// person sees it would hide the very thing they are asked to approve.
type linkTarget struct {
	raw      string
	scheme   string
	host     string
	trustKey string
	risk     linkRisk
	block    linkBlock
}

// classifyLink reads a URL without DNS, a browser or the settings. Trust is
// only offered for a well-formed http(s) address without user information or
// a local/private address; its key names the exact scheme, host and port.
func classifyLink(raw string) linkTarget {
	t := linkTarget{raw: raw, scheme: schemeOf(raw)}
	if blockedScheme(t.scheme) {
		t.block = blockForScheme(t.scheme)
		return t
	}

	u, err := url.Parse(raw)
	if err != nil {
		t.risk = linkRiskNoHost | linkRiskScheme
		return t
	}
	if u.Scheme != "" {
		t.scheme = strings.ToLower(u.Scheme)
	}
	host := u.Hostname()
	if host != "" {
		canonical, malformed := canonicalLinkHost(host)
		t.host = canonical
		if malformed {
			t.risk |= linkRiskMalformedHost
		}
	}
	if authority := rawAuthority(raw); authority != "" && containsUserinfo(authority) {
		t.risk |= linkRiskUserinfo
	}
	switch t.scheme {
	case "http", "https":
		if t.host == "" {
			t.risk |= linkRiskNoHost
		}
	case "":
		t.risk |= linkRiskScheme
		if t.host == "" {
			t.risk |= linkRiskNoHost
		}
	default:
		t.risk |= linkRiskScheme
	}
	if t.host != "" && localLinkHost(t.host) {
		t.risk |= linkRiskAddress
	}
	if t.risk == 0 {
		authority := t.host
		if strings.Contains(authority, ":") {
			authority = "[" + authority + "]"
		}
		if port := u.Port(); port != "" && port != defaultLinkPort(t.scheme) {
			authority += ":" + port
		}
		t.trustKey = t.scheme + "://" + authority
	}
	return t
}

// trusted reports a previous approval for this exact scheme, host and port.
func (t linkTarget) trusted(keys []string) bool {
	return t.trustKey != "" && slices.Contains(keys, t.trustKey)
}

func defaultLinkPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// schemeOf also catches schemes whose letters are separated by whitespace or
// control characters: browsers discard those before deciding what to run.
func schemeOf(raw string) string {
	colon := strings.IndexByte(raw, ':')
	if colon < 0 {
		return ""
	}
	prefix := raw[:colon]
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, prefix))
}

func blockedScheme(scheme string) bool { return blockForScheme(scheme) != linkBlockNone }

func blockForScheme(scheme string) linkBlock {
	switch scheme {
	case "javascript":
		return linkBlockScript
	case "file":
		return linkBlockFile
	case "data":
		return linkBlockData
	default:
		return linkBlockNone
	}
}

// rawAuthority is the part of an authority that Go's URL parser can fold into
// User: the second @ in http://user@evil.com@good.com still disguises a host.
func rawAuthority(raw string) string {
	_, rest, ok := strings.Cut(raw, ":")
	if !ok || !strings.HasPrefix(rest, "//") {
		return ""
	}
	rest = rest[2:]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func containsUserinfo(authority string) bool {
	lower := strings.ToLower(authority)
	return strings.Contains(authority, "@") || strings.Contains(lower, "%40")
}

// canonicalLinkHost gives IDN and punycode spellings of one host the same key.
// A numeric host follows the WHATWG IPv4 rule browsers use: a dotted quad is
// the canonical form, so 127.1 cannot pass as a different host from 127.0.0.1.
func canonicalLinkHost(host string) (string, bool) {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if !strings.Contains(h, ":") && looksLikeIPv4(h) {
		addr, ok := parseIPv4Host(h)
		if !ok {
			return h, true
		}
		return addr.String(), false
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		return addr.Unmap().String(), false
	}
	if ascii, err := idna.Lookup.ToASCII(h); err == nil {
		return strings.ToLower(ascii), false
	}
	return h, false
}

// looksLikeIPv4 reports the host form the WHATWG URL parser treats as an IPv4
// address: its last label is decimal, hexadecimal or octal-looking. On failure
// to parse such a host, browsers reject it rather than load a domain.
func looksLikeIPv4(host string) bool {
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if len(last) > 2 && strings.EqualFold(last[:2], "0x") {
		return allHex(last[2:])
	}
	if len(last) > 1 && last[0] == '0' {
		return allDigits(last)
	}
	return last != "" && allDigits(last)
}

func allDigits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func allHex(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !runeIsHex(r)
	}) < 0
}

func runeIsHex(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// parseIPv4Host accepts one to four numeric labels. The last label fills the
// bytes it leaves, so 127.1 is 127.0.0.1 and 0x7f000001 is also 127.0.0.1.
func parseIPv4Host(host string) (netip.Addr, bool) {
	labels := strings.Split(host, ".")
	if len(labels) == 0 || len(labels) > 4 {
		return netip.Addr{}, false
	}
	values := make([]uint64, len(labels))
	for i, label := range labels {
		value, ok := parseIPv4Number(label)
		if !ok || i < len(labels)-1 && value > 255 {
			return netip.Addr{}, false
		}
		values[i] = value
	}
	if values[len(values)-1] >= uint64(1)<<(8*(5-len(values))) {
		return netip.Addr{}, false
	}
	var bytes [4]byte
	for i := 0; i < len(values)-1; i++ {
		bytes[i] = byte(values[i])
	}
	last := len(values) - 1
	for i := 3; i >= last; i-- {
		bytes[i] = byte(values[last] >> (8 * (3 - i)))
	}
	return netip.AddrFrom4(bytes), true
}

func parseIPv4Number(label string) (uint64, bool) {
	base, digits := 10, label
	if len(label) > 2 && strings.EqualFold(label[:2], "0x") {
		base, digits = 16, label[2:]
	} else if len(label) > 1 && label[0] == '0' {
		base, digits = 8, label[1:]
	}
	n, err := strconv.ParseUint(digits, base, 64)
	return n, err == nil
}

// localLinkHost names addresses a browser can reach on this machine or on the
// local network, including the unspecified address, which means "any".
// netip parses IPv6 zones too, as in fe80::1%en0.
func localLinkHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	addr = addr.WithZone("").Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsUnspecified() || addr.Is4() && cgnat.Contains(addr)
}
