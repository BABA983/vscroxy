package app

import "testing"

// TestClassifyLink is the link guard's classification table. The expected
// values are written by hand: a wrong host, trust key or risk must fail even
// when the link still ends up in a dialog.
func TestClassifyLink(t *testing.T) {
	tests := []struct {
		raw      string
		scheme   string
		host     string
		trustKey string
		risk     linkRisk
		block    linkBlock
	}{
		{"https://example.com/docs", "https", "example.com", "https://example.com", 0, linkBlockNone},
		{"https://example.com:443/x", "https", "example.com", "https://example.com", 0, linkBlockNone},
		{"http://example.com:80/x", "http", "example.com", "http://example.com", 0, linkBlockNone},
		{"http://example.com:8080/x", "http", "example.com", "http://example.com:8080", 0, linkBlockNone},
		{"HTTPS://EXAMPLE.COM./Path", "https", "example.com", "https://example.com", 0, linkBlockNone},
		{"https://例え.テスト/x", "https", "xn--r8jz45g.xn--zckzah", "https://xn--r8jz45g.xn--zckzah", 0, linkBlockNone},
		{"https://xn--r8jz45g.xn--zckzah/x", "https", "xn--r8jz45g.xn--zckzah", "https://xn--r8jz45g.xn--zckzah", 0, linkBlockNone},
		{"https://user@example.com/x", "https", "example.com", "", linkRiskUserinfo, linkBlockNone},
		{"https://user:pass@example.com", "https", "example.com", "", linkRiskUserinfo, linkBlockNone},
		{"http://user@evil.com@good.com", "http", "good.com", "", linkRiskUserinfo, linkBlockNone},
		{"http://127.0.0.1:3000/", "http", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://127.1/", "http", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://2130706433/", "http", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://0x7f.0.0.1/", "http", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://0177.0.0.1/", "http", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://0x7f000001/", "http", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://192.168.0x1.1/", "http", "192.168.1.1", "", linkRiskAddress, linkBlockNone},
		{"http://1.2.3.4.5/", "http", "1.2.3.4.5", "", linkRiskMalformedHost, linkBlockNone},
		{"http://08.0.0.1/", "http", "08.0.0.1", "", linkRiskMalformedHost, linkBlockNone},
		{"https://[::1]/?x=1", "https", "::1", "", linkRiskAddress, linkBlockNone},
		{"https://[0:0:0:0:0:0:0:1]", "https", "::1", "", linkRiskAddress, linkBlockNone},
		{"https://[::ffff:127.0.0.1]/", "https", "127.0.0.1", "", linkRiskAddress, linkBlockNone},
		{"http://[fe80::1%25en0]/", "http", "fe80::1%en0", "", linkRiskAddress, linkBlockNone},
		{"http://localhost", "http", "localhost", "", linkRiskAddress, linkBlockNone},
		{"http://api.localhost:8080", "http", "api.localhost", "", linkRiskAddress, linkBlockNone},
		{"http://10.1.2.3", "http", "10.1.2.3", "", linkRiskAddress, linkBlockNone},
		{"http://172.16.5.4", "http", "172.16.5.4", "", linkRiskAddress, linkBlockNone},
		{"http://192.168.1.1", "http", "192.168.1.1", "", linkRiskAddress, linkBlockNone},
		{"http://100.64.1.1", "http", "100.64.1.1", "", linkRiskAddress, linkBlockNone},
		{"http://169.254.169.254/latest/meta-data", "http", "169.254.169.254", "", linkRiskAddress, linkBlockNone},
		{"http://0.0.0.0", "http", "0.0.0.0", "", linkRiskAddress, linkBlockNone},
		{"https://[::]", "https", "::", "", linkRiskAddress, linkBlockNone},
		{"mailto:someone@example.com", "mailto", "", "", linkRiskScheme, linkBlockNone},
		{"tel:+15550100", "tel", "", "", linkRiskScheme, linkBlockNone},
		{"//cdn.example.com/x", "", "cdn.example.com", "", linkRiskScheme, linkBlockNone},
		{"docs/guide.html", "", "", "", linkRiskScheme | linkRiskNoHost, linkBlockNone},
		{"javascript:alert(document.cookie)", "javascript", "", "", 0, linkBlockScript},
		{"JaVaScRiPt:alert(1)", "javascript", "", "", 0, linkBlockScript},
		{" java\tscript:alert(1)", "javascript", "", "", 0, linkBlockScript},
		{"file:///etc/passwd", "file", "", "", 0, linkBlockFile},
		{"data:text/html,<script>alert(1)</script>", "data", "", "", 0, linkBlockData},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := classifyLink(tt.raw)
			if got.scheme != tt.scheme || got.host != tt.host || got.trustKey != tt.trustKey ||
				got.risk != tt.risk || got.block != tt.block {
				t.Errorf("classifyLink(%q) = scheme %q, host %q, trust %q, risk %b, block %d; want %q, %q, %q, %b, %d",
					tt.raw, got.scheme, got.host, got.trustKey, got.risk, got.block,
					tt.scheme, tt.host, tt.trustKey, tt.risk, tt.block)
			}
			if got.raw != tt.raw {
				t.Errorf("raw = %q, want the original %q", got.raw, tt.raw)
			}
		})
	}
}

func TestClassifiedTrustNeedsAnExactHost(t *testing.T) {
	good := classifyLink("https://example.com:8443/a")
	if good.trustKey == "" || !good.trusted([]string{good.trustKey}) {
		t.Fatal("a public https host cannot be trusted")
	}
	if good.trusted(nil) || good.trusted([]string{"https://example.com"}) || good.trusted([]string{"http://example.com:8443"}) {
		t.Fatal("trust crossed a port or scheme boundary")
	}
	if good.trusted([]string{good.trustKey + "/"}) {
		t.Fatal("a different trust key matched")
	}
	if https := classifyLink("https://example.com:443/x").trustKey; https != classifyLink("https://example.com/x").trustKey {
		t.Errorf("https:443 trust key = %q, want the default key", https)
	}
	if http := classifyLink("http://example.com:80/x").trustKey; http != classifyLink("http://example.com/x").trustKey {
		t.Errorf("http:80 trust key = %q, want the default key", http)
	}
	idn := classifyLink("https://例え.テスト/x")
	if !idn.trusted([]string{"https://xn--r8jz45g.xn--zckzah"}) {
		t.Fatal("an IDN URL did not match its punycode trust key")
	}
	for _, raw := range []string{
		"http://user@evil.com@good.com", "http://127.0.0.1", "javascript:alert(1)",
		"mailto:someone@example.com", "https://[::1]",
	} {
		if target := classifyLink(raw); target.trusted([]string{target.trustKey, "https://example.com"}) {
			t.Errorf("%q was trusted, but it is not a plain http(s) public host", raw)
		}
	}
}
