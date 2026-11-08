package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func mustConfig(t *testing.T, s Settings, passwords map[string]string) *Config {
	t.Helper()
	cfg, err := New(s, passwords)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cfg
}

func decision(t *testing.T, cfg *Config, rawURL, host string, port int) Decision {
	t.Helper()
	d, err := cfg.Resolve(rawURL, host, port)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", host, err)
	}
	return d
}

func TestHostRulesFirstMatchAndDefault(t *testing.T) {
	cfg := mustConfig(t, Settings{
		Default: "http://default.example:8080",
		Rules: []Rule{
			{Host: "*.internal.test", Proxy: "socks5://socks.example:1080"},
			{Host: "api.example.com:8443", Proxy: "http://secure.example:3128"},
			{Host: "api.example.com", Proxy: "direct"},
		},
	}, nil)

	for _, tc := range []struct {
		name string
		host string
		port int
		want string
	}{
		{"wildcard", "a.internal.test", 443, "socks5://socks.example:1080"},
		{"wildcard does not match the apex", "internal.test", 443, "http://default.example:8080"},
		{"port-specific rule", "api.example.com", 8443, "http://secure.example:3128"},
		{"port-specific rule misses another port", "api.example.com", 443, "DIRECT"},
		{"direct rule", "api.example.com", 80, "DIRECT"},
		{"default", "elsewhere.example", 443, "http://default.example:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := decision(t, cfg, "https://"+tc.host+"/", tc.host, tc.port)
			if len(d.Candidates) != 1 {
				t.Fatalf("candidates = %+v", d.Candidates)
			}
			if got := d.Candidates[0].Proxy.Display(); got != tc.want {
				t.Fatalf("upstream = %q, want %q (reason %q)", got, tc.want, d.Candidates[0].Reason)
			}
			if d.Candidates[0].Reason == "" {
				t.Fatal("a selected upstream must explain itself")
			}
		})
	}
}

func TestBypassRunsBeforeRulesAndDefault(t *testing.T) {
	cfg := mustConfig(t, Settings{
		Default: "http://default.example:8080",
		Rules:   []Rule{{Host: "*", Proxy: "http://rule.example:8080"}},
		Bypass:  []string{"localhost", "*.internal.test", ".example.org", "10.0.0.0/8", "192.0.2.7", "<local>", "api.example.com:8443"},
	}, nil)
	for _, tc := range []struct {
		name string
		host string
		port int
		want bool
	}{
		{"localhost", "localhost", 443, true},
		{"loopback IPv4", "127.0.0.1", 443, true},
		{"loopback IPv6", "::1", 443, true},
		{"wildcard subdomain", "a.internal.test", 443, true},
		{"wildcard apex", "internal.test", 443, false},
		{"leading dot apex", "example.org", 443, true},
		{"leading dot subdomain", "www.example.org", 443, true},
		{"CIDR", "10.9.8.7", 443, true},
		{"outside CIDR", "11.9.8.7", 443, false},
		{"exact IP", "192.0.2.7", 443, true},
		{"local name", "printer", 443, true},
		{"local name is not a dotted name", "printer.lan", 443, false},
		{"host with port", "api.example.com", 8443, true},
		{"host with port misses", "api.example.com", 443, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := decision(t, cfg, "https://"+tc.host+"/", tc.host, tc.port)
			bypassed := strings.Contains(d.Candidates[0].Reason, "bypass")
			if bypassed != tc.want {
				t.Fatalf("bypassed = %v, want %v (%+v)", bypassed, tc.want, d.Candidates)
			}
			if tc.want && d.Candidates[0].Proxy != nil {
				t.Fatalf("a bypassed request must be direct: %+v", d.Candidates[0])
			}
		})
	}
}

func TestNoConfigurationMeansNoSelection(t *testing.T) {
	cfg := mustConfig(t, Settings{}, nil)
	if d := decision(t, cfg, "https://example.com/", "example.com", 443); len(d.Candidates) != 0 {
		t.Fatalf("empty settings selected %+v", d.Candidates)
	}
	cfg = mustConfig(t, Settings{Bypass: []string{"example.com"}}, nil)
	d := decision(t, cfg, "https://example.com/", "example.com", 443)
	if len(d.Candidates) != 1 || d.Candidates[0].Proxy != nil || d.Candidates[0].Reason != "DIRECT · bypass example.com" {
		t.Fatalf("bypass-only settings = %+v", d.Candidates)
	}
}

func TestCredentialsAreAttachedAtRuntime(t *testing.T) {
	cfg := mustConfig(t, Settings{
		Default: "http://proxy.example:8080",
		Credentials: []Credential{
			{Proxy: "http://proxy.example:8080", Username: "alice"},
		},
	}, map[string]string{"http://proxy.example:8080": "s3cret"})
	d := decision(t, cfg, "https://example.com/", "example.com", 443)
	u := d.Candidates[0].Proxy.URL
	if u.User == nil {
		t.Fatal("credentials were not attached")
	}
	if user := u.User.Username(); user != "alice" {
		t.Fatalf("username = %q", user)
	}
	if pass, _ := u.User.Password(); pass != "s3cret" {
		t.Fatalf("password = %q", pass)
	}
	if got := d.Candidates[0].Reason; strings.Contains(got, "alice") || strings.Contains(got, "s3cret") {
		t.Fatalf("reason leaks credentials: %q", got)
	}
	if strings.Contains(cfg.Settings().Default, "alice") {
		t.Fatal("settings expose credentials")
	}
}

func TestSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  Settings
	}{
		{"default without a scheme", Settings{Default: "proxy.example:8080"}},
		{"default without a port", Settings{Default: "http://proxy.example"}},
		{"default with credentials", Settings{Default: "http://alice:secret@proxy.example:8080"}},
		{"rule without a host", Settings{Rules: []Rule{{Proxy: "direct"}}}},
		{"rule without an upstream", Settings{Rules: []Rule{{Host: "example.com"}}}},
		{"unknown scheme", Settings{Default: "ftp://proxy.example:21"}},
		{"bad CIDR", Settings{Bypass: []string{"10.0.0.0/33"}}},
		{"duplicate credentials", Settings{Credentials: []Credential{{Proxy: "http://p:1"}, {Proxy: "http://p:1"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.set.Validate(); err == nil {
				t.Fatalf("%+v was accepted", tc.set)
			}
		})
	}
}

func TestParsePACResult(t *testing.T) {
	candidates, display, err := parsePACResult("PROXY 127.0.0.1:8080; SOCKS 127.0.0.1:1080; HTTPS proxy.example:8443; DIRECT;")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://127.0.0.1:8080", "socks5://127.0.0.1:1080", "https://proxy.example:8443", "DIRECT"}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %+v", candidates)
	}
	for i, w := range want {
		if got := candidates[i].Proxy.Display(); got != w {
			t.Fatalf("candidate %d = %q, want %q", i, got, w)
		}
	}
	if display != "PROXY 127.0.0.1:8080; SOCKS5 127.0.0.1:1080; HTTPS proxy.example:8443; DIRECT" {
		t.Fatalf("display = %q", display)
	}
	for _, raw := range []string{
		"", "   ", "PROXY 127.0.0.1", "PROXY 127.0.0.1:0", "SOCKS4 127.0.0.1:1080",
		"DIRECT 127.0.0.1:8080", "BOGUS 127.0.0.1:8080", "PROXY",
	} {
		if _, _, err := parsePACResult(raw); err == nil {
			t.Errorf("parsePACResult(%q) accepted an invalid result", raw)
		}
	}
}

func TestPACResultOrderIsTheFallbackOrder(t *testing.T) {
	cfg := mustConfig(t, Settings{PAC: writePAC(t, `function FindProxyForURL(url, host) {
		return "PROXY 127.0.0.1:8080; SOCKS5 127.0.0.1:1080; DIRECT";
	}`)}, nil)
	d := decision(t, cfg, "https://example.com/", "example.com", 443)
	if len(d.Candidates) != 3 {
		t.Fatalf("candidates = %+v", d.Candidates)
	}
	if d.Candidates[0].Proxy.Display() != "http://127.0.0.1:8080" ||
		d.Candidates[1].Proxy.Display() != "socks5://127.0.0.1:1080" ||
		d.Candidates[2].Proxy != nil {
		t.Fatalf("fallback order = %+v", d.Candidates)
	}
}

func TestRulesWinBeforePAC(t *testing.T) {
	cfg := mustConfig(t, Settings{
		PAC:   writePAC(t, `function FindProxyForURL() { return "PROXY 127.0.0.1:8080"; }`),
		Rules: []Rule{{Host: "direct.example", Proxy: "direct"}},
	}, nil)
	if d := decision(t, cfg, "https://direct.example/", "direct.example", 443); d.Candidates[0].Proxy != nil {
		t.Fatalf("rule did not win: %+v", d.Candidates)
	}
	if d := decision(t, cfg, "https://other.example/", "other.example", 443); d.Candidates[0].Proxy.Display() != "http://127.0.0.1:8080" {
		t.Fatalf("PAC did not apply: %+v", d.Candidates)
	}
}

func TestPACFailureIsAnErrorNotDirect(t *testing.T) {
	cfg := mustConfig(t, Settings{PAC: writePAC(t, `function FindProxyForURL() { throw new Error("boom"); }`)}, nil)
	if _, err := cfg.Resolve("https://example.com/", "example.com", 443); err == nil {
		t.Fatal("a throwing PAC became a direct connection")
	}
}

func TestPACNullMeansDirect(t *testing.T) {
	cfg := mustConfig(t, Settings{PAC: writePAC(t, `function FindProxyForURL() { return null; }`)}, nil)
	d := decision(t, cfg, "https://example.com/", "example.com", 443)
	if len(d.Candidates) != 1 || d.Candidates[0].Proxy != nil {
		t.Fatalf("null PAC result = %+v", d.Candidates)
	}
}

func TestPACHelpers(t *testing.T) {
	vm := goja.New()
	value := func(v any) goja.Value { return vm.ToValue(v) }
	args := func(v ...any) []goja.Value {
		out := make([]goja.Value, len(v))
		for i := range v {
			out[i] = value(v[i])
		}
		return out
	}
	fixed := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC) // Thursday

	for _, tc := range []struct {
		name string
		got  bool
		want bool
	}{
		{"shExpMatch star", globMatch("http://home.netscape.com/people/ari/index.html", "*/ari/*"), true},
		{"shExpMatch miss", globMatch("http://home.netscape.com/people/montulli/index.html", "*/ari/*"), false},
		{"shExpMatch question", globMatch("abc", "a?c"), true},
		{"shExpMatch class", globMatch("b", "[a-c]"), true},
		{"shExpMatch negated class", globMatch("d", "[!a-c]"), true},
		{"shExpMatch literal regex metacharacter", globMatch("a+b", "a+b"), true},
		{"shExpMatch literal dot", globMatch("a.b", "a.b"), true},
		{"shExpMatch star matches slashes", globMatch("/a/b", "*"), true},
		{"weekday single", weekdayRange(fixed, args("THU", "GMT")), true},
		{"weekday range", weekdayRange(fixed, args("MON", "FRI", "GMT")), true},
		{"weekday reversed excludes Thursday", weekdayRange(fixed, args("FRI", "MON", "GMT")), false},
		{"date single day", dateRange(fixed, args(15, "GMT")), true},
		{"date day range", dateRange(fixed, args(1, 15, "GMT")), true},
		{"date day range misses", dateRange(fixed, args(16, 31, "GMT")), false},
		{"date month range", dateRange(fixed, args("JAN", "MAR", "GMT")), true},
		{"date month range misses", dateRange(fixed, args("FEB", "MAR", "GMT")), false},
		{"date year range", dateRange(fixed, args(2020, 2030, "GMT")), true},
		{"date year", dateRange(fixed, args(2026, "GMT")), true},
		{"date day and month", dateRange(fixed, args(15, "JAN", "GMT")), true},
		{"date day and month misses", dateRange(fixed, args(24, "DEC", "GMT")), false},
		{"date day and month range", dateRange(fixed, args(1, "JAN", 31, "JAN", "GMT")), true},
		{"date month and year range", dateRange(fixed, args("OCT", 2025, "MAR", 2026, "GMT")), true},
		{"date full range", dateRange(fixed, args(1, "JAN", 2026, 31, "JAN", 2026, "GMT")), true},
		{"date reversed month", dateRange(fixed, args("DEC", "JAN", "GMT")), true},
		{"date reversed day", dateRange(fixed, args(20, 10, "GMT")), false},
		{"time single hour", timeRange(fixed, args(10, "GMT")), true},
		{"time hour range", timeRange(fixed, args(9, 17, "GMT")), true},
		{"time hour range misses", timeRange(fixed, args(11, 12, "GMT")), false},
		{"time hour and minute range", timeRange(fixed, args(8, 30, 17, 0, "GMT")), true},
		{"time second range", timeRange(fixed, args(10, 30, 0, 10, 30, 30, "GMT")), true},
		{"time reversed range misses", timeRange(fixed, args(10, 31, 10, 29, "GMT")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %v, want %v", tc.got, tc.want)
			}
		})
	}

	late := time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)
	if !timeRange(late, args(23, 0, "GMT")) {
		t.Fatal("timeRange(23, 0) should wrap across midnight")
	}
	early := time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC)
	if !dateRange(early, args(20, 10, "GMT")) {
		t.Fatal("dateRange(20, 10) should wrap across the month")
	}
	if dateRange(fixed, args("JAN", 2025, "GMT")) {
		t.Fatal("a month/year pair must match both")
	}
}

func TestPACSourceIsRedactedInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := strings.TrimPrefix(server.URL, "http://")
	server.Close()
	source := "http://alice:s3cret@" + addr + "/proxy.pac?token=TOKEN123"
	_, err := New(Settings{PAC: source}, nil)
	if err == nil {
		t.Fatal("an unreachable PAC URL was accepted")
	}
	for _, secret := range []string{"s3cret", "TOKEN123"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("PAC error leaked %q: %v", secret, err)
		}
	}
	if !strings.Contains(err.Error(), "http://"+addr+"/proxy.pac") {
		t.Fatalf("PAC error does not name the sanitized source: %v", err)
	}
	if got := PACDisplay("https://user:pass@example.com/a.pac?q=secret#frag"); got != "https://example.com/a.pac" {
		t.Fatalf("PACDisplay = %q", got)
	}
	if got := PACDisplay("/tmp/local.pac"); got != "/tmp/local.pac" {
		t.Fatalf("PACDisplay local path = %q", got)
	}
}

func TestPACNetworkHelpers(t *testing.T) {
	p, err := newPAC(`function FindProxyForURL(url, host) {
		if (isPlainHostName(host) !== (host.indexOf(".") < 0)) return "PROXY bad:1";
		if (dnsDomainIs(host, ".example.com") !== host.endsWith(".example.com")) return "PROXY bad:2";
		if (localHostOrDomainIs(host, "www.example.com") !== (host === "www.example.com" || host === "www")) return "PROXY bad:3";
		if (dnsDomainLevels("www.example.com") !== 2) return "PROXY bad:4";
		if (dnsResolve("host.example") !== "10.1.2.3") return "PROXY bad:5";
		if (isResolvable("missing.example")) return "PROXY bad:6";
		if (!isResolvable("host.example")) return "PROXY bad:7";
		if (!isInNet("host.example", "10.0.0.0", "255.0.0.0")) return "PROXY bad:8";
		if (isInNet("192.168.1.5", "10.0.0.0", "255.0.0.0")) return "PROXY bad:9";
		if (!isInNet("10.1.2.3", "10.1.2.0", "255.255.255.0")) return "PROXY bad:10";
		if (convert_addr("10.0.0.1") !== 167772161) return "PROXY bad:11";
		if (myIpAddress() !== "203.0.113.9") return "PROXY bad:12";
		if (isPlainHostName("::1")) return "PROXY bad:13";
		if (!isPlainHostName("localhost")) return "PROXY bad:14";
		return "DIRECT";
	}`, "helpers.pac", PACOptions{
		Lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "host.example" {
				return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
			}
			return nil, errors.New("not found")
		},
		MyIP: func() netip.Addr { return netip.MustParseAddr("203.0.113.9") },
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates, _, err := p.findProxy(context.Background(), "https://www.example.com/", "www.example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Proxy != nil {
		t.Fatalf("a helper disagreed: %s", candidates[0].Reason)
	}
}

func TestPACTimeout(t *testing.T) {
	p, err := newPAC(`function FindProxyForURL() { while (true) {} }`, "slow.pac", PACOptions{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err = p.findProxy(context.Background(), "https://example.com/", "example.com", 443)
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestPACSyntaxErrorAtLoad(t *testing.T) {
	if _, err := newPAC(`function FindProxyForURL( {`, "broken.pac", PACOptions{}); err == nil {
		t.Fatal("a syntax error was accepted")
	}
	p, err := newPAC(`var x = 1;`, "missing.pac", PACOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.findProxy(context.Background(), "https://example.com/", "example.com", 443); err == nil {
		t.Fatal("a PAC without FindProxyForURL evaluated")
	}
}

func TestPACResultIsCachedByDestination(t *testing.T) {
	p, err := newPAC(`function FindProxyForURL() { return "DIRECT"; }`, "cache.pac", PACOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, _, err := p.findProxy(context.Background(), "https://example.com/a", "example.com", 443); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.evaluations.Load(); got != 1 {
		t.Fatalf("evaluations = %d, want one per host", got)
	}
}

func TestPACCacheIncludesSchemeAndPort(t *testing.T) {
	p, err := newPAC(`function FindProxyForURL(url, host) {
		return url.startsWith("https:") ? "PROXY proxy.example:8080" : "DIRECT";
	}`, "scheme.pac", PACOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if candidates, _, err := p.findProxy(context.Background(), "https://example.com/", "example.com", 443); err != nil || candidates[0].Proxy == nil {
		t.Fatalf("https result = %+v, %v", candidates, err)
	}
	if candidates, _, err := p.findProxy(context.Background(), "http://example.com/", "example.com", 80); err != nil || candidates[0].Proxy != nil {
		t.Fatalf("http result = %+v, %v", candidates, err)
	}
	if got := p.evaluations.Load(); got != 2 {
		t.Fatalf("evaluations = %d, want one per scheme", got)
	}
	// The same scheme on another port is another destination.
	if _, _, err := p.findProxy(context.Background(), "https://example.com/", "example.com", 8443); err != nil {
		t.Fatal(err)
	}
	if got := p.evaluations.Load(); got != 3 {
		t.Fatalf("evaluations = %d, want one per port", got)
	}
}

func TestPACTimeDependentResultIsNotCached(t *testing.T) {
	clock := time.Date(2026, 1, 15, 8, 59, 0, 0, time.UTC)
	p, err := newPAC(`function FindProxyForURL() {
		return timeRange(9, 17, "GMT") ? "DIRECT" : "PROXY proxy.example:8080";
	}`, "time.pac", PACOptions{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	candidates, _, err := p.findProxy(context.Background(), "https://example.com/", "example.com", 443)
	if err != nil || candidates[0].Proxy == nil {
		t.Fatalf("08:59 result = %+v, %v", candidates, err)
	}
	clock = time.Date(2026, 1, 15, 9, 1, 0, 0, time.UTC)
	candidates, _, err = p.findProxy(context.Background(), "https://example.com/", "example.com", 443)
	if err != nil || candidates[0].Proxy != nil {
		t.Fatalf("09:01 result = %+v, %v", candidates, err)
	}
	if got := p.evaluations.Load(); got != 2 {
		t.Fatalf("evaluations = %d, a time-dependent result must not be cached", got)
	}
}

func TestLoadPACFromFileAndURL(t *testing.T) {
	path := writePAC(t, `function FindProxyForURL() { return "DIRECT"; }`)
	if source, err := loadPACSource(path); err != nil || !strings.Contains(source, "FindProxyForURL") {
		t.Fatalf("file: %q, %v", source, err)
	}
	if source, err := loadPACSource("file://" + path); err != nil || !strings.Contains(source, "FindProxyForURL") {
		t.Fatalf("file URL: %q, %v", source, err)
	}
	if _, err := loadPACSource(t.TempDir() + "/missing.pac"); err == nil {
		t.Fatal("a missing file was accepted")
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func writePAC(t *testing.T, script string) string {
	t.Helper()
	path := t.TempDir() + "/proxy.pac"
	if err := writeFile(path, script); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPACFromURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.pac" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `function FindProxyForURL() { return "DIRECT"; }`)
	}))
	defer server.Close()
	source, err := loadPACSource(server.URL + "/proxy.pac")
	if err != nil || !strings.Contains(source, "FindProxyForURL") {
		t.Fatalf("URL: %q, %v", source, err)
	}
	if _, err := loadPACSource(server.URL + "/missing.pac"); err == nil {
		t.Fatal("a missing PAC URL was accepted")
	}
}

func TestSecretStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := SecretStoreFor(path)
	secrets := map[string]string{"http://proxy.example:8080": "s3cret", "socks5://proxy.example:1080": "other"}
	if err := store.Save(secrets); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["http://proxy.example:8080"] != "s3cret" || loaded["socks5://proxy.example:1080"] != "other" {
		t.Fatalf("loaded = %+v", loaded)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") {
		t.Fatal("the sealed file contains a plaintext password")
	}
	if err := os.Remove(store.KeyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("a missing key did not make the sealed file unreadable")
	}
	if err := store.Save(map[string]string{"http://proxy.example:8080": "new"}); err == nil {
		t.Fatal("a sealed file with a missing key was replaced")
	}
}

func TestPACRecursionStopsAtTheCallStackLimit(t *testing.T) {
	p, err := newPAC(`function FindProxyForURL() { function f() { return f(); } return f(); }`, "recursion.pac", PACOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, err := p.findProxy(context.Background(), "https://example.com/", "example.com", 443); err == nil {
		t.Fatal("unbounded recursion was accepted")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("recursion took %s", elapsed)
	}
}
