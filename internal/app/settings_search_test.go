package app

// Contract tests for Settings search: the registry is the only source of
// setting ids and defaults, search reads localized text from one snapshot,
// and reset writes the default through the settings save path.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/datadir"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func useMessagesLocale(t *testing.T, tag string) {
	t.Helper()
	previous := i18n.Locale()
	i18n.SetLocaleFunc(func() string { return tag })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return previous }) })
}

func settingsTestApp() *app {
	a := workspaceApp()
	a.updateCheck, a.guide = true, true // the defaults of a fresh settings file
	return a
}

func hitIDs(hits []settingsSearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.entry.spec.id)
	}
	return ids
}

func TestSettingsSearchFindsDescriptionAndIDInBothLanguages(t *testing.T) {
	for _, tag := range []string{"en", "zh-Hans"} {
		t.Run(tag, func(t *testing.T) {
			useMessagesLocale(t, tag)
			a := settingsTestApp()
			description := locales.LanguageDescription()
			if tag == "zh-Hans" && !strings.Contains(description, "语言") {
				t.Fatalf("Chinese description is not Chinese: %q", description)
			}
			byDescription := hitIDs(settingsFind(a, description))
			if len(byDescription) == 0 || byDescription[0] != "general.language" {
				t.Fatalf("description %q found %v, want general.language first", description, byDescription)
			}
			byID := hitIDs(settingsFind(a, "general.language"))
			if len(byID) == 0 || byID[0] != "general.language" {
				t.Fatalf("id search found %v, want general.language first", byID)
			}
			// A stable id is searchable even with the sections in Chinese.
			byPartialID := hitIDs(settingsFind(a, "upstream.pac"))
			if len(byPartialID) == 0 || byPartialID[0] != "proxy.upstream.pac" {
				t.Fatalf("partial id search found %v, want proxy.upstream.pac", byPartialID)
			}
			// Prose must be a contiguous substring, not scattered letters.
			for _, id := range hitIDs(settingsFind(a, "language")) {
				if id == "general.localControl" {
					t.Fatalf("scattered description letters matched %s", id)
				}
			}
		})
	}
}

func TestSettingsSearchModifiedFilterUsesRegisteredDefaults(t *testing.T) {
	a := settingsTestApp()
	a.appearance = "dark"
	a.proxyPort = 9123
	a.socksPassword = "never-in-search"
	hits := settingsFind(a, "@modified")
	want := []string{"appearance.theme", "proxy.port", "proxy.socks.password"}
	if got := hitIDs(hits); !equalStrings(got, want) {
		t.Fatalf("@modified found %v, want %v", got, want)
	}
	// Text can narrow the filter, as VS Code's @modified query does.
	if got := hitIDs(settingsFind(a, "@modified port")); !equalStrings(got, []string{"proxy.port"}) {
		t.Fatalf("@modified port found %v", got)
	}
	// Changing a value through the registry's own default clears the marker.
	// The port's own save path needs a settings file, as it does in the app.
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	if !a.resetSetting("proxy.port") {
		t.Fatal("reset of proxy.port failed")
	}
	if got := hitIDs(settingsFind(a, "@modified")); !equalStrings(got, []string{"appearance.theme", "proxy.socks.password"}) {
		t.Fatalf("after reset @modified found %v", got)
	}
	if a.proxyPort != 0 {
		t.Fatalf("proxy port after reset = %d", a.proxyPort)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSettingsResetWritesTheSingleDefault(t *testing.T) {
	a := settingsTestApp()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.backupDir = filepath.Join(dir, "backups")
	a.saved = a.currentSettings()
	a.appearance = "dark"
	a.language = "zh-Hans"
	a.proxyPort = 9901
	a.socksPassword = "a-secret-value"
	if err := a.persistSettings(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"appearance.theme", "general.language", "proxy.port", "proxy.socks.password"} {
		if !a.resetSetting(id) {
			t.Fatalf("reset %s failed", id)
		}
	}
	if a.appearance != "" || a.language != "" || a.proxyPort != 0 || a.socksPassword != "" {
		t.Fatalf("values after reset: appearance=%q language=%q port=%d password=%q",
			a.appearance, a.language, a.proxyPort, a.socksPassword)
	}
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"appearance", "language", "proxyPort", "socksPassword"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("default %s was still persisted: %s", unwanted, data)
		}
	}
	if hits := settingsFind(a, "@modified"); len(hits) != 0 {
		t.Fatalf("reset settings still marked modified: %v", hitIDs(hits))
	}
}

func TestFreshSettingsFileMatchesEveryRegisteredDefault(t *testing.T) {
	root := t.TempDir()
	datadir.Reset()
	t.Cleanup(datadir.Reset)
	t.Setenv(datadir.Env, root)
	useTempHome(t, t.TempDir())

	a := newApp()
	if a.srv != nil {
		t.Cleanup(func() { _ = a.srv.Close() })
	}
	var modified []string
	for _, spec := range settingSpecs() {
		if spec.modified(a) {
			modified = append(modified, spec.id)
		}
	}
	if len(modified) > 0 {
		t.Fatalf("a fresh app reports modified settings: %v", modified)
	}
}

func TestResetKeyLogClosesTheEngineWriter(t *testing.T) {
	dir := t.TempDir()
	a := settingsTestApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.saved = a.currentSettings()
	logPath := filepath.Join(dir, "keys.log")
	t.Setenv("SSLKEYLOGFILE", logPath)
	capture.ClearTLSKeyLogWriter()
	t.Cleanup(func() {
		_ = os.Unsetenv("SSLKEYLOGFILE")
		capture.ClearTLSKeyLogWriter()
	})

	old := capture.TLSKeyLogWriter()
	if old == nil {
		t.Fatal("the key-log writer was not opened")
	}
	a.keyLogPath = logPath
	if !a.resetSetting("https.keyLog") {
		t.Fatal("key-log reset failed")
	}
	if _, err := old.Write([]byte("tls secret")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old key-log writer error = %v, want a closed file", err)
	}
	if got := capture.TLSKeyLogWriter(); got != nil {
		t.Fatalf("new handshakes still get a key-log writer: %v", got)
	}
	if os.Getenv("SSLKEYLOGFILE") != "" {
		t.Fatal("SSLKEYLOGFILE is still set after reset")
	}
}

func TestResetUpstreamChangesProxyAndClearsSealedPassword(t *testing.T) {
	dir := t.TempDir()
	a := settingsTestApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.saved = a.currentSettings()
	a.srv = &capture.Server{}
	proxy := "http://proxy.example:8080"
	a.upstreamCfg = upstream.Settings{
		Default: proxy, Credentials: []upstream.Credential{{Proxy: proxy, Username: "alice"}},
	}
	a.upstreamSecrets = map[string]string{proxy: "sealed-secret"}
	if err := a.srv.SetUpstreamConfig(a.upstreamCfg, a.upstreamSecrets); err != nil {
		t.Fatal(err)
	}
	if err := upstream.SecretStoreFor(a.settingsPath).Save(a.upstreamSecrets); err != nil {
		t.Fatal(err)
	}

	if !a.resetSetting("proxy.upstream.credentials") {
		t.Fatal("credential reset failed")
	}
	if got := a.srv.UpstreamConfig(); len(got.Credentials) != 0 {
		t.Fatalf("proxy still has upstream credentials: %+v", got)
	}
	if len(a.upstreamSecrets) != 0 {
		t.Fatalf("runtime secrets were not cleared: %v", a.upstreamSecrets)
	}
	sealed, err := upstream.SecretStoreFor(a.settingsPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 0 {
		t.Fatalf("sealed passwords were not cleared: %v", sealed)
	}
	if !a.resetSetting("proxy.upstream.default") {
		t.Fatal("upstream default reset failed")
	}
	if got := a.srv.UpstreamConfig(); got.Default != "" {
		t.Fatalf("proxy still uses %q", got.Default)
	}
}

func TestResetSOCKSStopsTheListener(t *testing.T) {
	dir := t.TempDir()
	a := settingsTestApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.saved = a.currentSettings()
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.SetSOCKS(capture.SOCKSConfig{Enabled: true, Addr: "127.0.0.1:0", Username: "alice", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a.srv = srv
	a.socksOn, a.socksAuth = true, true
	a.socksUsername, a.socksPassword = "alice", "secret"
	a.launchSOCKS = a.socksEntry()
	if srv.SOCKSAddr() == "" {
		t.Fatal("test SOCKS listener did not start")
	}
	if !a.resetSetting("proxy.socks.enabled") {
		t.Fatal("SOCKS reset failed")
	}
	if got := srv.SOCKSAddr(); got != "" {
		t.Fatalf("SOCKS listener is still running at %s", got)
	}
	if !srv.Running() {
		t.Fatal("resetting SOCKS also stopped the HTTP proxy")
	}
}

func TestSettingsSearchNeverIndexesSecretValues(t *testing.T) {
	a := settingsTestApp()
	const socksSecret = "socks-password-do-not-show"
	const upstreamSecret = "upstream-password-do-not-show"
	a.socksPassword = socksSecret
	a.upstreamSecrets = map[string]string{"http://proxy.example:8080": upstreamSecret}
	a.upstreamCfg.Credentials = []upstream.Credential{{Proxy: "http://proxy.example:8080", Username: "alice"}}
	a.certificates.Secrets = map[string]string{"one": "cert-passphrase-do-not-show", "two": "server-passphrase-do-not-show"}
	a.certificates.Clients = []certs.Client{{ID: "one", Host: "api.example.com"}}
	a.certificates.Servers = []certs.Server{{ID: "two", Host: "api.example.com"}}
	a.keyLogPath = "/tmp/token-do-not-show/keys.log"
	a.settingsQuery = locales.Password()
	a.settingsOpen = true

	idx := a.settingsSearch()
	for _, entry := range idx.entries {
		for _, text := range []string{entry.title, entry.description, entry.spec.id} {
			for _, secret := range []string{socksSecret, upstreamSecret, "cert-passphrase-do-not-show", "server-passphrase-do-not-show", "token-do-not-show"} {
				if strings.Contains(text, secret) {
					t.Errorf("index entry %s leaked %q", entry.spec.id, secret)
				}
			}
		}
	}
	tt := ui.NewTester(func(c *ui.Context) { a.settingsView(c) }, 900, 700)
	tt.Frame()
	for _, text := range tt.Texts() {
		for _, secret := range []string{socksSecret, upstreamSecret, "cert-passphrase-do-not-show", "server-passphrase-do-not-show", "token-do-not-show"} {
			if strings.Contains(text, secret) {
				t.Errorf("rendered settings search leaked %q: %q", secret, text)
			}
		}
	}
}

func TestSettingsIndexBuildsOncePerLanguageAndChange(t *testing.T) {
	previous := i18n.Locale()
	i18n.SetLocaleFunc(func() string { return previous })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := settingsTestApp()
	a.settingsOpen, a.settingsQuery = true, "language"
	tt := ui.NewTester(a.settingsView, 900, 700)
	for range 12 {
		tt.Frame()
	}
	if got := a.settingsIndex.builds; got != 1 {
		t.Fatalf("index built %d times across 12 frames, want 1", got)
	}

	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	tt.Frame()
	if got := a.settingsIndex.builds; got != 2 {
		t.Fatalf("language change built the index %d times, want 2", got)
	}
	for range 5 {
		tt.Frame()
	}
	if got := a.settingsIndex.builds; got != 2 {
		t.Fatalf("Chinese frames rebuilt the index %d times, want 2", got)
	}

	i18n.SetLocaleFunc(func() string { return previous })
	a.appearance = "dark"
	a.persistSettings()
	a.settingsSearch()
	if got := a.settingsIndex.builds; got != 3 {
		t.Fatalf("settings change built the index %d times, want 3", got)
	}
}

func TestOpenSettingsCommandFocusesSearch(t *testing.T) {
	a := settingsTestApp()
	tt := ui.NewTester(a.view, 1100, 760)
	tt.Frame()
	if key := keybindingOf("app.settings"); key.key != ui.KeyComma || key.mods != ui.Cmd {
		t.Fatalf("Cmd+, is not app.settings: %+v", key)
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	tt.Frame()
	if !a.settingsOpen {
		t.Fatal("Cmd+, did not open Settings")
	}
	if !tt.Focused(locales.SettingsSearchLabel()) {
		t.Fatalf("Cmd+, did not focus the settings search: %v", tt.Texts())
	}
}

// TestSettingsSearchScreenshots renders the search and @modified states at
// 2x in both themes. Set VSCROXY_SETTINGS_SEARCH_SCREENSHOTS to a directory.
func TestSettingsSearchScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_SETTINGS_SEARCH_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_SETTINGS_SEARCH_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance+"-search", func(t *testing.T) {
			a := settingsTestApp()
			a.appearance, a.settingsOpen, a.settingsQuery = appearance, true, "language"
			a.settingsFocusSearch++
			tt := ui.NewTester(a.settingsView, 1000, 720)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText("ID: general.language") {
				t.Fatalf("no highlighted search result: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "settings-search-"+appearance+".png", tt.Image())
		})
		t.Run(appearance+"-modified-menu", func(t *testing.T) {
			a := settingsTestApp()
			a.appearance, a.settingsOpen = appearance, true
			a.proxyPort, a.settingsQuery = 9876, "@modified"
			a.socksPassword = "not-a-result"
			tt := ui.NewTester(a.settingsView, 1000, 720)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if tt.Click(locales.SettingsSettingActions(locales.ProxyPort())) != nil {
				t.Fatalf("no reset menu: %v", tt.Texts())
			}
			tt.Frame()
			if a.settingsQuery != "@modified" {
				t.Fatalf("the ⋯ press left the search and opened %q", a.settingsQuery)
			}
			if !tt.HasText(locales.SettingsResetToDefault()) {
				t.Fatalf("reset action is missing: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "settings-modified-"+appearance+".png", tt.Image())
		})
	}
}

func TestSettingsSearchEscapeClearsQuery(t *testing.T) {
	a := settingsTestApp()
	a.settingsOpen = true
	a.settingsQuery = "language"
	a.settingsFocusSearch++
	tt := ui.NewTester(a.settingsView, 900, 700)
	tt.Frame()
	if !tt.HasText(locales.SettingsSettingID("general.language")) {
		t.Fatalf("search result is missing: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settingsQuery != "" {
		t.Fatalf("Escape left query %q", a.settingsQuery)
	}
	if !tt.HasText(locales.General()) {
		t.Fatalf("clearing search did not restore the sections: %v", tt.Texts())
	}
}
