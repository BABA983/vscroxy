package app

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	openSettingsFromBar(t, tt)
	if err := tt.Click("Proxy"); err != nil {
		t.Fatal(err)
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

func TestDrainUpstreamReportsStartupResult(t *testing.T) {
	a := workspaceApp()
	a.upstreamCfg = upstream.Settings{Default: "http://proxy.example:8080"}
	a.upstreamPending = make(chan upstreamLoadResult, 1)
	a.upstreamPending <- upstreamLoadResult{applied: true, err: errors.New("PAC https://pac.example/proxy.pac: dial tcp: connection refused"), settings: a.upstreamCfg}
	a.drainUpstream()
	if a.upstreamProblem == "" || a.upstream != "" {
		t.Fatalf("failed startup = problem %q, summary %q", a.upstreamProblem, a.upstream)
	}
	a.upstreamPending = make(chan upstreamLoadResult, 1)
	a.upstreamPending <- upstreamLoadResult{applied: true, settings: a.upstreamCfg}
	a.drainUpstream()
	if a.upstreamProblem != "" || a.upstream != "http://proxy.example:8080" {
		t.Fatalf("successful startup = problem %q, summary %q", a.upstreamProblem, a.upstream)
	}
}

func TestStaleStartupResultDoesNotReplaceASave(t *testing.T) {
	a := workspaceApp()
	a.upstreamCfg = upstream.Settings{Default: "http://good.example:8080"}
	a.upstreamEpoch.Store(2)
	a.upstreamProblem = ""
	a.upstream = a.upstreamCfg.Summary()
	a.upstreamPending = make(chan upstreamLoadResult, 1)
	a.upstreamPending <- upstreamLoadResult{applied: true, epoch: 1, err: errors.New("stale PAC failure"), settings: upstream.Settings{PAC: "https://pac.example/proxy.pac"}}
	a.drainUpstream()
	if a.upstreamProblem != "" || a.upstream != "http://good.example:8080" {
		t.Fatalf("stale startup result changed the save: problem %q, summary %q", a.upstreamProblem, a.upstream)
	}
}

func TestApplyUpstreamSettingsDoesNotCommitOnFailure(t *testing.T) {
	dir := t.TempDir()
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.upstreamCfg = upstream.Settings{Default: "http://old.example:8080"}
	a.upstreamSecrets = map[string]string{"http://old.example:8080": "old"}
	deadPAC := "http://" + freeLoopbackAddr(t) + "/proxy.pac"
	a.applyUpstreamSettings(upstream.Settings{
		Default: "http://new.example:8080",
		PAC:     deadPAC,
	}, map[string]string{"http://new.example:8080": "new"}, "saved")
	if a.upstreamCfg.Default != "http://old.example:8080" {
		t.Fatalf("a failed PAC load committed the new config: %+v", a.upstreamCfg)
	}
	if a.upstreamUI.problem == "" {
		t.Fatal("the PAC load error is not shown")
	}
	if _, err := os.Stat(a.settingsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settings were written after a failed PAC load: %v", err)
	}
	if secrets, err := upstream.SecretStoreFor(a.settingsPath).Load(); err != nil || len(secrets) != 0 {
		t.Fatalf("secrets were sealed before the PAC loaded: %+v, %v", secrets, err)
	}
}

func TestUpstreamErrorChipStaysActionable(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.upstreamCfg = upstream.Settings{Default: "http://old.example:8080"}
	a.upstream = ""
	a.upstreamProblem = "PAC https://pac.example/proxy.pac: GET returned 503 Service Unavailable"
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Upstream error") {
		t.Fatal("a failed upstream has no danger chip")
	}
	if err := tt.Click("Disable Upstream error"); err != nil {
		t.Fatal(err)
	}
	if a.upstreamProblem != "" || a.upstreamCfg.Enabled() || a.srv.UpstreamConfig().Enabled() {
		t.Fatalf("Upstream off did not clear the failed configuration: problem %q, cfg %+v", a.upstreamProblem, a.upstreamCfg)
	}
}

func TestSettingsUpstreamErrorRedactsPACSecrets(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	source := "http://alice:s3cret@" + freeLoopbackAddr(t) + "/proxy.pac?token=TOKEN123"
	a.applyUpstreamSettings(upstream.Settings{PAC: source}, nil, "saved")
	if a.upstreamUI.problem == "" {
		t.Fatal("the PAC error is not shown in Settings")
	}
	for _, secret := range []string{"s3cret", "TOKEN123"} {
		if strings.Contains(a.upstreamUI.problem, secret) {
			t.Fatalf("Settings leaked %q: %s", secret, a.upstreamUI.problem)
		}
	}
}

func TestCloseControlStopsUpstreamRetry(t *testing.T) {
	a := workspaceApp()
	a.upstreamRetry.timer = time.AfterFunc(time.Hour, func() {})
	a.closeControl()
	if a.upstreamRetry.timer != nil {
		t.Fatal("closing the app left the upstream retry timer running")
	}
}

func TestSaveResetsRetryState(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.upstreamUI = upstreamEditor{loaded: true, defaultAddr: "http://good.example:8080"}
	a.upstreamRetry = upstreamRetry{
		attempt:  3,
		retrying: true,
		settings: upstream.Settings{PAC: "https://pac.example/proxy.pac"},
	}
	a.saveUpstream()
	if a.upstreamRetry.attempt != 0 || a.upstreamRetry.retrying || a.upstreamRetry.timer != nil || a.upstreamRetry.settings.Enabled() {
		t.Fatalf("save inherited retry state: %+v", a.upstreamRetry)
	}
}

func TestSaveFailureKeepsTheRunningConfiguration(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	dir := t.TempDir()
	good := filepath.Join(dir, "good.pac")
	if err := os.WriteFile(good, []byte(`function FindProxyForURL() { return "DIRECT"; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.pac")
	if err := os.WriteFile(bad, []byte(`function FindProxyForURL( {`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("still working")) }))
	defer target.Close()
	a.applyUpstreamSettings(upstream.Settings{PAC: good}, nil, "good")
	if a.srv.UpstreamError() != nil || a.upstreamCfg.PAC != good {
		t.Fatalf("good PAC did not install: %v, %+v", a.srv.UpstreamError(), a.upstreamCfg)
	}
	a.applyUpstreamSettings(upstream.Settings{PAC: bad}, nil, "bad")
	if a.srv.UpstreamError() != nil {
		t.Fatalf("a failed save changed the running configuration: %v", a.srv.UpstreamError())
	}
	if got := a.srv.UpstreamConfig().PAC; got != good {
		t.Fatalf("running PAC = %q, want %q", got, good)
	}
	if a.upstreamCfg.PAC != good || a.upstreamProblem != "" {
		t.Fatalf("failed save committed state: config %+v, problem %q", a.upstreamCfg, a.upstreamProblem)
	}
	if a.upstreamUI.problem == "" {
		t.Fatal("the bad PAC error is not shown in the form")
	}
	if a.upstreamRetry.timer != nil || a.upstreamRetry.settings.Enabled() {
		t.Fatalf("a normal save failure entered retry: %+v", a.upstreamRetry)
	}
	proxyURL, _ := url.Parse("http://" + a.srv.Addr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "still working" {
		t.Fatalf("traffic after the failed save = %d %q", resp.StatusCode, body)
	}
}

func TestUpstreamRetryBackoff(t *testing.T) {
	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{0, 5 * time.Second},
		{1, 15 * time.Second},
		{2, 60 * time.Second},
		{3, 5 * time.Minute},
		{99, 5 * time.Minute},
	} {
		if got := upstreamRetryDelay(tc.attempt); got != tc.want {
			t.Errorf("retry delay %d = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

func TestUpstreamRetryAppliesTheFailedConfiguration(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.upstreamCfg = upstream.Settings{Default: "http://old.example:8080"}
	a.upstreamProblem = "PAC failed"
	a.upstream = ""
	a.upstreamRetry = upstreamRetry{
		epoch:    a.upstreamEpoch.Load(),
		settings: upstream.Settings{Default: "http://good.example:8080"},
	}
	a.retryUpstream()
	if a.upstreamProblem != "" || a.upstream != "http://good.example:8080" || a.upstreamCfg.Default != "http://good.example:8080" {
		t.Fatalf("retry did not install the configuration: problem %q, summary %q, config %+v", a.upstreamProblem, a.upstream, a.upstreamCfg)
	}
	if a.upstreamRetry.timer != nil {
		t.Fatal("successful retry left a timer")
	}
}

func TestUpstreamRetryIsDroppedAfterANewerSave(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.upstreamCfg = upstream.Settings{Default: "http://new.example:8080"}
	a.upstream = a.upstreamCfg.Summary()
	a.upstreamEpoch.Store(3)
	a.upstreamRetry = upstreamRetry{
		epoch:    2,
		settings: upstream.Settings{Default: "http://stale.example:8080"},
	}
	a.retryUpstream()
	if a.upstreamCfg.Default != "http://new.example:8080" || a.upstream != "http://new.example:8080" {
		t.Fatalf("stale retry changed the active configuration: %+v, %q", a.upstreamCfg, a.upstream)
	}
}

func TestImportAppliesUpstreamBeforeCommittingSettings(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.sslPath = filepath.Join(t.TempDir(), "ssl.json")
	a.ignorePath = filepath.Join(t.TempDir(), "ignore.txt")
	a.upstreamCfg = upstream.Settings{Default: "http://old.example:8080"}
	deadPAC := "http://" + freeLoopbackAddr(t) + "/proxy.pac"
	b := configBundle{
		Settings: &settings{Upstream: upstream.Settings{Default: "http://new.example:8080", PAC: deadPAC}},
		SSL:      &capture.SSLConfig{All: true},
		Lists:    map[string][]string{filepath.Base(a.ignorePath): {"imported.example"}},
	}
	if err := a.importConfig(b, [3]bool{false, false, true}); err == nil {
		t.Fatal("an import with an unreachable PAC was accepted")
	}
	if a.upstreamCfg.Default != "http://old.example:8080" {
		t.Fatalf("a failed import committed the upstream: %+v", a.upstreamCfg)
	}
	if _, err := os.Stat(a.sslPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SSL settings were written before the upstream check: %v", err)
	}
	if _, err := os.Stat(a.ignorePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lists were written before the upstream check: %v", err)
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
