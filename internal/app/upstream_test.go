package app

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/egoist/mygo/ui"
)

func TestUpstreamSettingsPersistWithoutPasswords(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.upstreamUI.loaded = false
	a.upstreamUI.defaultAddr = "http://proxy.example:8080"
	a.upstreamUI.rules = "*.internal.test socks5://127.0.0.1:1080\napi.example.com:443 direct"
	a.upstreamUI.bypass = "localhost\n10.0.0.0/8\n<local>"
	a.upstreamUI.pac = ""
	a.upstreamUI.credentials = []upstreamCredentialRow{
		{Proxy: "http://proxy.example:8080", Username: "alice", Password: "s3cret"},
	}
	a.saveUpstream()
	if a.upstreamUI.problem != "" {
		t.Fatalf("save: %s", a.upstreamUI.problem)
	}
	if a.upstreamCfg.Default != "http://proxy.example:8080" || len(a.upstreamCfg.Rules) != 2 || len(a.upstreamCfg.Bypass) != 3 {
		t.Fatalf("active upstream = %+v", a.upstreamCfg)
	}
	if a.upstream != "http://proxy.example:8080" {
		t.Fatalf("summary = %q", a.upstream)
	}
	if a.upstreamSecrets["http://proxy.example:8080"] != "s3cret" {
		t.Fatalf("runtime secrets = %+v", a.upstreamSecrets)
	}
	if got := a.srv.UpstreamConfig(); got.Default != a.upstreamCfg.Default || len(got.Rules) != 2 {
		t.Fatalf("capture config = %+v", got)
	}

	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") {
		t.Fatalf("settings contain the proxy password: %s", data)
	}
	if !strings.Contains(string(data), `"username":"alice"`) {
		t.Fatalf("settings do not keep the username: %s", data)
	}
	loaded, err := upstream.SecretStoreFor(a.settingsPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["http://proxy.example:8080"] != "s3cret" {
		t.Fatalf("sealed passwords = %+v", loaded)
	}

	bundle, _ := a.exportConfig()
	if bundle.Settings == nil {
		t.Fatal("export has no settings")
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "s3cret") {
		t.Fatal("a configuration export contains the proxy password")
	}
	if bundle.Settings.Upstream.Default != a.upstreamCfg.Default {
		t.Fatalf("exported upstream = %+v", bundle.Settings.Upstream)
	}
}

func TestUpstreamEditorRejectsInvalidDrafts(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	for _, tc := range []struct {
		name  string
		edit  func(*upstreamEditor)
		match string
	}{
		{"default without a scheme", func(u *upstreamEditor) { u.defaultAddr = "proxy.example:8080" }, "default:"},
		{"default with credentials", func(u *upstreamEditor) { u.defaultAddr = "http://alice:secret@proxy.example:8080" }, "credentials"},
		{"rule with one field", func(u *upstreamEditor) { u.rules = "*.example.com" }, "rule 1:"},
		{"rule with a bad port", func(u *upstreamEditor) { u.rules = "*.example.com http://proxy.example:0" }, "rule 1:"},
		{"bad bypass", func(u *upstreamEditor) { u.bypass = "10.0.0.0/33" }, "bypass"},
		{"credential without a port", func(u *upstreamEditor) { u.credentials = []upstreamCredentialRow{{Proxy: "http://proxy.example"}} }, "credential 1:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.upstreamUI = upstreamEditor{loaded: true}
			tc.edit(&a.upstreamUI)
			a.saveUpstream()
			if !strings.Contains(a.upstreamUI.problem, tc.match) {
				t.Fatalf("problem = %q, want %q", a.upstreamUI.problem, tc.match)
			}
		})
	}
}

func TestClearUpstreamKeepsBypassAndCredentials(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.upstreamCfg = upstream.Settings{
		Default:     "http://proxy.example:8080",
		Rules:       []upstream.Rule{{Host: "*.example.com", Proxy: "direct"}},
		Bypass:      []string{"localhost"},
		Credentials: []upstream.Credential{{Proxy: "http://proxy.example:8080", Username: "alice"}},
	}
	a.upstreamSecrets = map[string]string{"http://proxy.example:8080": "s3cret"}
	a.upstream = a.upstreamCfg.Summary()
	a.clearUpstream()
	if a.upstream != "" || a.upstreamCfg.Enabled() {
		t.Fatalf("cleared upstream = %q %+v", a.upstream, a.upstreamCfg)
	}
	if len(a.upstreamCfg.Bypass) != 1 || len(a.upstreamCfg.Credentials) != 1 {
		t.Fatalf("clearing dropped bypass or credentials: %+v", a.upstreamCfg)
	}
}

func TestSystemProxyBypassPersistsAndReapplies(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyOff}}
	a := proxyTestApp(t, fake)
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.systemProxyBypassSet = true
	a.systemProxyBypassText = "localhost\n*.corp.example\n10.0.0.0/8"
	a.systemProxy.mu.Lock()
	a.systemProxy.ownedTarget = a.srv.Addr()
	a.systemProxy.ownedNow.Store(true)
	a.systemProxy.mu.Unlock()

	tt := ui.NewTester(a.view, 1280, 800)
	a.saveSystemProxyBypass()
	settleProxy(t, a, tt)
	if fake.writes != 1 || !fake.lastOn {
		t.Fatalf("writes = %d, on = %v; want one reapply", fake.writes, fake.lastOn)
	}
	want := []string{"localhost", "*.corp.example", "10.0.0.0/8"}
	if len(a.systemProxyBypass) != len(want) {
		t.Fatalf("bypass = %+v", a.systemProxyBypass)
	}
	for i := range want {
		if a.systemProxyBypass[i] != want[i] {
			t.Fatalf("bypass = %+v, want %+v", a.systemProxyBypass, want)
		}
	}
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "*.corp.example") || !strings.Contains(string(data), `"systemProxyBypassSet":true`) {
		t.Fatalf("settings do not keep the bypass list: %s", data)
	}
}

func TestParseSystemProxyBypass(t *testing.T) {
	got := parseSystemProxyBypass("localhost, *.corp.example\n10.0.0.0/8; <local>\n# comment\n")
	want := []string{"localhost", "*.corp.example", "10.0.0.0/8", "<local>"}
	if len(got) != len(want) {
		t.Fatalf("bypass = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bypass = %+v, want %+v", got, want)
		}
	}
}

func TestUpstreamSettingsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = path
	a.upstreamUI.loaded = false
	a.upstreamUI.defaultAddr = "http://proxy.example:8080"
	a.upstreamUI.credentials = []upstreamCredentialRow{{Proxy: "http://proxy.example:8080", Username: "alice", Password: "s3cret"}}
	a.saveUpstream()
	if a.upstreamUI.problem != "" {
		t.Fatalf("save: %s", a.upstreamUI.problem)
	}

	// The next launch reads the settings and the sealed password separately.
	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Upstream.Default != "http://proxy.example:8080" || len(saved.Upstream.Credentials) != 1 {
		t.Fatalf("saved upstream = %+v", saved.Upstream)
	}
	secrets, err := upstream.SecretStoreFor(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := upstream.New(saved.Upstream, secrets)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := cfg.Resolve("https://example.com/", "example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Candidates[0].Proxy == nil || decision.Candidates[0].Proxy.URL.User == nil {
		t.Fatalf("restored decision = %+v", decision.Candidates)
	}
}

func TestUpstreamSettingsEditorAppliesRulesAndBypass(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	tt := ui.NewTester(a.view, 1280, 900)
	for _, label := range []string{"Settings", "Proxy"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	scrollTo := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no element shows %q", label)
		}
		tt.Scroll(850, 400, 0, r.Y-250)
		tt.Frame()
	}
	scrollTo("Default upstream")
	if err := tt.Click("Default upstream"); err != nil {
		t.Fatal(err)
	}
	tt.Type("http://proxy.example:8080")
	tt.Key(0, ui.KeyTab)
	tt.Type("*.internal.test socks5://127.0.0.1:1080")
	tt.Key(0, ui.KeyTab)
	tt.Type("localhost\n10.0.0.0/8")
	scrollTo("Save upstream")
	if err := tt.Click("Save upstream"); err != nil {
		t.Fatal(err)
	}
	if a.upstreamUI.problem != "" {
		t.Fatalf("save: %s", a.upstreamUI.problem)
	}
	if a.upstreamCfg.Default != "http://proxy.example:8080" ||
		len(a.upstreamCfg.Rules) != 1 || a.upstreamCfg.Rules[0].Proxy != "socks5://127.0.0.1:1080" ||
		len(a.upstreamCfg.Bypass) != 2 {
		t.Fatalf("active upstream = %+v", a.upstreamCfg)
	}
	if got := a.srv.UpstreamConfig(); got.Default != a.upstreamCfg.Default || len(got.Rules) != 1 {
		t.Fatalf("capture config = %+v", got)
	}
}
