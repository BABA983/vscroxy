package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/upstream"
)

func desktopResult(t *testing.T, client controlClient, action string, params any) (jsontext.Value, error) {
	t.Helper()
	var value jsontext.Value
	err := client.Call(t.Context(), action, params, &value)
	return value, err
}

// controlClient is the part of the desktop control client these tests use.
type controlClient interface {
	Call(context.Context, string, any, any) error
}

// Application refusals arrive as a JSON result, not a transport error.
func refused(value jsontext.Value) bool {
	var failure struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(value, &failure) == nil && failure.Error != ""
}

func TestControlSettingsAllowlistRejectsSecretsAndUnlistedIds(t *testing.T) {
	secret := "seed-secret-a9f31c"
	root := t.TempDir()
	a := &app{
		settingsPath:     filepath.Join(root, "settings.json"),
		updateCheck:      true,
		socksPassword:    secret,
		upstreamCfg:      upstream.Settings{Credentials: []upstream.Credential{{Proxy: "http://proxy.example.com", Username: "user"}}},
		upstreamSecrets:  map[string]string{"http://proxy.example.com": secret},
		trustedLinkHosts: []string{"https://example.com/?token=" + secret},
	}
	client := desktopClient(t, a)
	rows, err := desktopResult(t, client, "settings.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(rows, &entries); err != nil || len(entries) == 0 {
		t.Fatalf("settings.list: %s, %v", rows, err)
	}
	allowed := []string{"general.checkForUpdates", "general.setupGuide", "https.allDomains", "https.domains",
		"capture.flowLimit", "capture.memoryLimit", "capture.diskLimit", "capture.historyLimit", "capture.historySizeMB"}
	var ids []string
	for _, entry := range entries {
		if !slices.Contains(allowed, entry.ID) || entry.Kind == "" {
			t.Fatalf("unexpected or untyped setting %+v", entry)
		}
		ids = append(ids, entry.ID)
	}
	if !slices.Contains(ids, "capture.flowLimit") {
		t.Fatalf("missing allowed setting: %s", rows)
	}
	value, err := desktopResult(t, client, "settings.get", map[string]any{"id": "capture.flowLimit"})
	if err != nil || !strings.Contains(string(value), `"id":"capture.flowLimit"`) {
		t.Fatalf("settings.get: %s, %v", value, err)
	}

	for _, id := range []string{"proxy.socks.password", "general.trustedLinkHosts", "certificates.signingCA", "not.a.setting"} {
		value, err := desktopResult(t, client, "settings.get", map[string]any{"id": id})
		if err != nil || !refused(value) {
			t.Fatalf("secret or unlisted setting %s was readable: %s, %v", id, value, err)
		}
		value, err = desktopResult(t, client, "settings.set", map[string]any{"id": id, "value": secret, "confirm": true})
		if err != nil || !refused(value) || strings.Contains(string(value), secret) {
			t.Fatalf("secret or unlisted setting %s was writable: %s, %v", id, value, err)
		}
	}
	if a.socksPassword != secret || a.upstreamSecrets["http://proxy.example.com"] != secret || !slices.Contains(a.trustedLinkHosts, "https://example.com/?token="+secret) {
		t.Fatal("a refused write changed a secret or trust decision")
	}

	for _, tc := range []struct {
		params any
		name   string
	}{
		{map[string]any{"id": "general.checkForUpdates", "value": true}, "missing confirmation"},
		{map[string]any{"id": "capture.flowLimit", "value": "1000", "confirm": true}, "wrong value type"},
		{map[string]any{"id": "capture.flowLimit", "value": 99999, "confirm": true}, "out-of-range value"},
		{map[string]any{"id": "https.domains", "value": []string{"https://example.com"}, "confirm": true}, "URL as a host"},
	} {
		value, err := desktopResult(t, client, "settings.set", tc.params)
		if err != nil || !refused(value) {
			t.Fatalf("%s accepted: %s, %v", tc.name, value, err)
		}
	}
	value, err = desktopResult(t, client, "settings.set", map[string]any{"id": "general.checkForUpdates", "value": false, "confirm": true})
	var applied struct {
		ID    string `json:"id"`
		Value bool   `json:"value"`
	}
	if err != nil || json.Unmarshal(value, &applied) != nil || applied.ID != "general.checkForUpdates" || applied.Value {
		t.Fatalf("confirmed write: %s, %v", value, err)
	}
	if a.updateCheck {
		t.Fatal("settings.set did not apply the value")
	}
	data, err := os.ReadFile(a.settingsPath)
	if err != nil || !strings.Contains(string(data), `"disableUpdateCheck":true`) {
		t.Fatalf("setting was not saved: %s, %v", data, err)
	}
	value, err = desktopResult(t, client, "settings.set", map[string]any{"id": "https.domains", "value": []string{"API.example.com", "*.internal.test"}, "confirm": true})
	if err != nil || !strings.Contains(string(value), `"api.example.com"`) {
		t.Fatalf("domain write: %s, %v", value, err)
	}
	if a.sslHosts != "api.example.com\n*.internal.test" || !slices.Equal(splitHosts(a.sslHosts), []string{"api.example.com", "*.internal.test"}) {
		t.Fatalf("domains were not normalized: %q", a.sslHosts)
	}
}

func TestControlSettingsSaveFailureRestoresValue(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{
		settingsPath: filepath.Join(blocker, "settings.json"),
		updateCheck:  true, guide: false, flowLimit: 5000, historyLimit: 100,
	}
	client := desktopClient(t, a)
	for _, tc := range []struct {
		id    string
		value any
	}{
		{"general.checkForUpdates", false},
		{"general.setupGuide", true},
		{"capture.flowLimit", 1000},
		{"capture.historyLimit", 250},
	} {
		value, err := desktopResult(t, client, "settings.set", map[string]any{"id": tc.id, "value": tc.value, "confirm": true})
		if err != nil || !refused(value) {
			t.Fatalf("%s reported success on a failed save: %s, %v", tc.id, value, err)
		}
	}
	if !a.updateCheck || a.guide || a.flowLimit != 5000 || a.historyLimit != 100 {
		t.Fatalf("failed save left new values: updates=%v guide=%v flow=%d history=%d",
			a.updateCheck, a.guide, a.flowLimit, a.historyLimit)
	}
}

func TestControlCertificatesStatusAndSSLHosts(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(37), Subject: pkix.Name{CommonName: "Untrusted localhost test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	fingerprint := hex.EncodeToString(sum[:])

	serverFile := filepath.Join(t.TempDir(), "private-directory", "server.p12")
	a := &app{caPath: path, sslAll: false, sslHosts: "api.example.com\n*.internal.test",
		socksPassword: "seed-secret-ca7d2c",
		certificates: certs.Settings{
			Servers: []certs.Server{{ID: "server-1", Host: "api.example.com", CertFile: serverFile}},
			Secrets: map[string]string{"server-1": "server-passphrase"},
		}}
	client := desktopClient(t, a)
	value, err := desktopResult(t, client, "certificates.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Trusted            bool   `json:"trusted"`
		Path               string `json:"path"`
		Fingerprint        string `json:"fingerprint"`
		Problem            string `json:"problem"`
		ServerCertificates []struct {
			Host string `json:"host"`
			File string `json:"file"`
		} `json:"serverCertificates"`
		SSL struct {
			All   bool     `json:"all"`
			Hosts []string `json:"hosts"`
			Apps  int      `json:"apps"`
		} `json:"ssl"`
	}
	if err := json.Unmarshal(value, &got); err != nil {
		t.Fatal(err)
	}
	if got.Trusted || got.Problem == "" || got.Path != path || got.Fingerprint != fingerprint ||
		got.SSL.All || !slices.Equal(got.SSL.Hosts, []string{"api.example.com", "*.internal.test"}) || got.SSL.Apps != 0 {
		t.Fatalf("certificates.get = %s", value)
	}
	if len(got.ServerCertificates) != 1 || got.ServerCertificates[0].Host != "api.example.com" || got.ServerCertificates[0].File != "server.p12" {
		t.Fatalf("server certificates = %+v", got.ServerCertificates)
	}
	for _, secret := range []string{"seed-secret-ca7d2c", "server-passphrase", "private-directory", string(der)} {
		if strings.Contains(string(value), secret) {
			t.Fatalf("certificate status disclosed %q", secret)
		}
	}
	if value, err := desktopResult(t, client, "certificates.get", map[string]any{"path": "/etc/passwd"}); err != nil ||
		!refused(value) {
		t.Fatalf("certificates.get accepted parameters: %s, %v", value, err)
	}
	a.caPath = ""
	value, err = desktopResult(t, client, "certificates.get", nil)
	if err != nil || !strings.Contains(string(value), `"problem":"unavailable"`) {
		t.Fatalf("missing CA: %s, %v", value, err)
	}
}

func TestControlSessionsCloseKeepsSavedSession(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{}
	for _, key := range []string{"a.json", "b.json", "c.json"} {
		path := filepath.Join(root, key)
		paths[key] = path
		if err := os.WriteFile(path, []byte(`{"version":1,"title":"Saved","flows":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The active tab (b) is deliberately not the first open item, so a
	// background close must not fall back to the first item's key.
	a := &app{sessions: sessionWorkspace{dir: root, active: "b.json", items: []*savedSession{
		{key: "a.json", path: paths["a.json"], open: true, loaded: true},
		{key: "b.json", path: paths["b.json"], open: true, loaded: true, count: 1},
		{key: "c.json", path: paths["c.json"], open: true, loaded: true},
	}}, socksPassword: "seed-secret-session"}
	client := desktopClient(t, a)
	value, err := desktopResult(t, client, "sessions.close", map[string]any{"key": "c.json"})
	if err != nil || !refused(value) {
		t.Fatalf("unconfirmed close accepted: %s, %v", value, err)
	}
	value, err = desktopResult(t, client, "sessions.close", map[string]any{"key": "c.json", "confirm": true})
	if err != nil || string(value) != `{"closed":true}` {
		t.Fatalf("background close: %s, %v", value, err)
	}
	if a.sessions.active != "b.json" || a.sessions.items[2].open || !a.sessions.items[0].open || !a.sessions.items[1].open {
		t.Fatalf("background close changed the wrong tab: active=%q, open=%v/%v/%v",
			a.sessions.active, a.sessions.items[0].open, a.sessions.items[1].open, a.sessions.items[2].open)
	}
	if _, err := os.Stat(paths["c.json"]); err != nil {
		t.Fatalf("background close deleted the saved session: %v", err)
	}

	value, err = desktopResult(t, client, "sessions.close", map[string]any{"key": "b.json", "confirm": true})
	if err != nil || string(value) != `{"closed":true}` {
		t.Fatalf("close active: %s, %v", value, err)
	}
	if a.sessions.active != "a.json" || a.sessions.items[1].open || a.sessions.items[1].loaded {
		t.Fatalf("closing active did not pick another tab: active=%q, open=%v", a.sessions.active, a.sessions.items[1].open)
	}
	if _, err := os.Stat(paths["b.json"]); err != nil {
		t.Fatalf("close deleted the saved session: %v", err)
	}
	value, err = desktopResult(t, client, "sessions.close", map[string]any{"key": "missing.json", "confirm": true})
	if err != nil || string(value) != `{"closed":false}` || strings.Contains(string(value), "seed-secret-session") {
		t.Fatalf("missing close: %s, %v", value, err)
	}
}

func TestControlBreadthDoesNotDiscloseSeededSecret(t *testing.T) {
	secret := "seed-secret-breadth-51f8"
	a := &app{
		requests: []savedRequest{{Request: request.Request{ID: "r", Collection: "Shop", Name: "Example",
			Examples: []request.Example{{Name: "ok", Status: 200, Body: "{}"}}}}},
		socksPassword:   secret,
		upstreamCfg:     upstream.Settings{Credentials: []upstream.Credential{{Proxy: "http://proxy.example.com", Username: "user"}}},
		upstreamSecrets: map[string]string{"http://proxy.example.com": secret},
	}
	client := desktopClient(t, a)
	values := [][]byte{}
	call := func(action string, params any) {
		t.Helper()
		value, err := desktopResult(t, client, action, params)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		values = append(values, value)
	}
	call("settings.list", nil)
	call("settings.get", map[string]any{"id": "capture.flowLimit"})
	call("certificates.get", nil)
	call("ssl.get", nil)
	call("sessions.list", nil)
	call("mock.get", nil)
	call("mock.start", map[string]any{"collection": "Shop", "confirm": true})
	call("mock.stop", map[string]any{"collection": "Shop", "confirm": true})
	for _, value := range values {
		if strings.Contains(string(value), secret) {
			t.Fatalf("seeded secret appeared in control output: %s", value)
		}
	}
}
