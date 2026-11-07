package certs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/testcert"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, address string
		want             bool
	}{
		{"api.example.com", "api.example.com:443", true},
		{"API.Example.com.", "api.example.com:443", true},
		{"api.example.com", "other.example.com:443", false},
		{"api.example.com:8443", "api.example.com:8443", true},
		{"api.example.com:8443", "api.example.com:443", false},
		{"api.example.com", "api.example.com:8443", true},
		{"*.example.com", "api.example.com:443", true},
		{"*.example.com", "a.b.example.com:443", true},
		{"*.example.com", "example.com:443", false},
		{"*.example.com:8443", "api.example.com:8443", true},
		{"*.example.com:8443", "api.example.com:443", false},
		{"*.", "api.example.com:443", false},
		{"*", "anything.test:443", true},
		{"[::1]:8443", "[::1]:8443", true},
		{"::1", "[::1]:443", true},
	} {
		if got := Match(tc.pattern, tc.address); got != tc.want {
			t.Errorf("Match(%q, %q) = %t, want %t", tc.pattern, tc.address, got, tc.want)
		}
	}
}

func TestAddressFillsTheDefaultPort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.example.com":        "api.example.com:443",
		"https://api.example.com:8443/x": "api.example.com:8443",
		"wss://socket.example.com":       "socket.example.com:443",
		"http://127.0.0.1:8080":          "127.0.0.1:8080",
		"https://[::1]/":                 "[::1]:443",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := Address(u); got != want {
			t.Errorf("Address(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestValidateHost(t *testing.T) {
	for _, host := range []string{
		"api.example.com", "api.example.com:8443", "*.example.com", "*.example.com:8443",
		"*", "[::1]:8443", "::1", "bücher.example", "api.example.com.",
	} {
		if err := ValidateHost(host); err != nil {
			t.Errorf("ValidateHost(%q) = %v", host, err)
		}
	}
	for _, host := range []string{
		"", "*.", "*..example.com", "*.*.example.com", "api.example.com/v1",
		"api example.com", "api.example.com:0", "api.example.com:65536",
		"api.example.com:abc", ".example.com", "api..example.com",
	} {
		if err := ValidateHost(host); err == nil {
			t.Errorf("ValidateHost(%q) accepted an invalid host", host)
		}
	}
	if got := NormalizeHost("*.example.com."); got != "*.example.com" {
		t.Fatalf("trailing dot wildcard = %q", got)
	}
	if got := NormalizeHost("*."); got != "" {
		t.Fatalf("bare wildcard dot = %q, want empty", got)
	}
}

func TestIDNAHosts(t *testing.T) {
	if got := NormalizeHost("bücher.example"); got != "xn--bcher-kva.example" {
		t.Fatalf("IDN normalization = %q", got)
	}
	if !Match("bücher.example", "xn--bcher-kva.example") {
		t.Fatal("IDN pattern did not match its punycode address")
	}
	u, err := url.Parse("https://bücher.example/")
	if err != nil {
		t.Fatal(err)
	}
	if got := Address(u); got != "xn--bcher-kva.example:443" {
		t.Fatalf("IDN address = %q", got)
	}
}

func TestDefaultPortCertificateMatchesCanonicalAddress(t *testing.T) {
	u, err := url.Parse("https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !Match("api.example.com:443", Address(u)) {
		t.Fatal("a :443 certificate did not match the canonical address")
	}
	if Match("api.example.com:443", u.Host) {
		t.Fatal("a :443 certificate matched the raw URL host without its port")
	}
}

func TestClientCertificateLoadsPEMAndPKCS12(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	certPEM, keyPEM, cert := ca.Issue(t, "client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "client", certPEM, keyPEM)
	pfxPath := testcert.WriteFile(t, dir, "client.p12", ca.PFX(t, cert, "pfx-secret"))

	settings := Settings{
		Clients: []Client{
			{ID: "pem", Host: "api.example.com", Format: FormatPEM, CertFile: certPath, KeyFile: keyPath},
			{ID: "pfx", Host: "*.example.com:8443", Format: FormatPKCS12, CertFile: pfxPath},
		},
		Secrets: map[string]string{"pfx": "pfx-secret"},
	}
	if err := settings.Validate(); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	got, err := settings.ClientCertificate("api.example.com:443")
	if err != nil || got == nil || got.Leaf == nil || got.Leaf.Subject.CommonName != "client" {
		t.Fatalf("PEM certificate: %v %+v", err, got)
	}
	if got, err := settings.ClientCertificate("other.example.com:443"); err != nil || got != nil {
		t.Fatalf("wildcard matched another port: %v %+v", err, got)
	}
	if got, err := settings.ClientCertificate("other.example.com:8443"); err != nil || got == nil {
		t.Fatalf("PKCS#12 certificate: %v %+v", err, got)
	}

	wrong := settings.Clone()
	wrong.Secrets["pfx"] = "wrong-secret"
	if _, err := wrong.ClientCertificate("other.example.com:8443"); err == nil || strings.Contains(err.Error(), "wrong-secret") {
		t.Fatalf("wrong passphrase error = %v", err)
	}

	missing := settings.Clone()
	missing.Clients[0].CertFile = filepath.Join(dir, "missing.pem")
	if _, err := missing.ClientCertificate("api.example.com:443"); err == nil {
		t.Fatal("a missing certificate file did not fail")
	}
}

func TestTLSConfigTrustsCustomRootsAndKeepsInsecureIndependent(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	_, _, server := ca.Issue(t, "server", []string{"api.example.com"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	leaf := server.Leaf

	verify := func(config *tls.Config) error {
		opts := x509.VerifyOptions{Roots: config.RootCAs, DNSName: "api.example.com"}
		_, err := leaf.Verify(opts)
		return err
	}
	custom := Settings{RootCAs: []string{caPath}}
	config, err := custom.TLSConfig("api.example.com:443", nil, false)
	if err != nil {
		t.Fatalf("custom root config: %v", err)
	}
	if err := verify(config); err != nil {
		t.Fatalf("custom root did not verify: %v", err)
	}
	systemOnly := Settings{TrustSystemRoots: new(false)}
	config, err = systemOnly.TLSConfig("api.example.com:443", nil, false)
	if err != nil {
		t.Fatalf("system-off config: %v", err)
	}
	if err := verify(config); err == nil {
		t.Fatal("system roots off still verified a private CA")
	}
	both := Settings{RootCAs: []string{caPath}, TrustSystemRoots: new(false)}
	config, err = both.TLSConfig("api.example.com:443", nil, false)
	if err != nil || verify(config) != nil {
		t.Fatalf("custom root with system off: %v", err)
	}
	broken := Settings{RootCAs: []string{filepath.Join(dir, "missing.pem")}}
	if _, err := broken.TLSConfig("api.example.com:443", nil, false); err == nil {
		t.Fatal("a missing custom CA did not fail a verified configuration")
	}
	insecure, err := broken.TLSConfig("api.example.com:443", nil, true)
	if err != nil {
		t.Fatalf("verification off still read the CA: %v", err)
	}
	if !insecure.InsecureSkipVerify {
		t.Fatal("verification off did not set InsecureSkipVerify")
	}
	proxy, err := Settings{}.TLSConfig("api.example.com:443", ca.Cert.Raw, false)
	if err != nil || verify(proxy) != nil {
		t.Fatalf("proxy CA did not verify: %v", err)
	}
}

func TestSettingsJSONKeepsPassphrasesOut(t *testing.T) {
	settings := Settings{
		Clients: []Client{{ID: "one", Host: "api.example.com", Format: FormatPKCS12, CertFile: "/tmp/client.p12"}},
		Secrets: map[string]string{"one": "not-in-the-file"},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "not-in-the-file") || strings.Contains(string(data), "Secrets") {
		t.Fatalf("settings JSON contains a passphrase: %s", data)
	}
	var back Settings
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Secrets != nil || !back.Equal(settings) {
		t.Fatalf("round trip changed the non-secret settings: %+v", back)
	}
}

func TestSecretStoreRefusesToReplaceUnreadableSecrets(t *testing.T) {
	dir := t.TempDir()
	store := SecretStoreFor(filepath.Join(dir, "settings.json"))
	if err := store.Save(map[string]string{"one": "keep-me"}); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(store.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.KeyPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(map[string]string{}); err == nil {
		t.Fatal("an unreadable sealed file was removed")
	}
	if _, err := os.Stat(store.Path); err != nil {
		t.Fatalf("the sealed file disappeared: %v", err)
	}
	if err := store.Save(map[string]string{"one": "replacement"}); err == nil {
		t.Fatal("an unreadable sealed file was replaced")
	} else if !strings.Contains(err.Error(), store.KeyPath) || !strings.Contains(err.Error(), "delete "+store.Path) {
		t.Fatalf("the refusal does not explain how to recover: %v", err)
	}
	if err := os.WriteFile(store.KeyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got["one"] != "keep-me" {
		t.Fatalf("sealed passphrases changed: %v %v", got, err)
	}
}

func TestSecretStoreSealsAndNeedsItsKey(t *testing.T) {
	dir := t.TempDir()
	store := SecretStoreFor(filepath.Join(dir, "settings.json"))
	want := map[string]string{"one": "first-secret", "two": "second-secret"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(store.KeyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode: %v %v", info, err)
	}
	if data, err := os.ReadFile(store.Path); err != nil || strings.Contains(string(data), "first-secret") {
		t.Fatalf("sealed file: %q %v", data, err)
	}
	got, err := store.Load()
	if err != nil || len(got) != 2 || got["one"] != "first-secret" || got["two"] != "second-secret" {
		t.Fatalf("loaded %v, %v", got, err)
	}
	if err := os.Remove(store.KeyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("a sealed file without its key loaded")
	}
}
