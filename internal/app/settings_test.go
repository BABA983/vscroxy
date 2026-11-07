package app

import (
	"encoding/json/v2"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo/ui"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := settings{
		Appearance:       "dark",
		SidebarWidth:     264,
		TablePercent:     55,
		InspectorLayout:  "right",
		SideTablePercent: 61,
		DiffSplit:        58,
		SidebarHidden:    true,
		Columns:          columnLayout{Order: []string{"status", "url"}, Widths: map[string]float32{"url": 320}},
		CustomColumns:    []customColumn{{Source: sourceQuery, Name: "Page"}, {Source: sourceResponseJSON, Name: "/data/ID"}, {Source: sourceRequestJSON, Name: "/id"}, graphqlColumn},
		HiddenColumns:    []string{"client"},
		KeyLogPath:       "/tmp/keys.log",
		ProxyPort:        9091,
		Certificates: certs.Settings{
			Clients:          []certs.Client{{ID: "one", Host: "api.example.com", Format: certs.FormatPEM, CertFile: "/tmp/client.pem", KeyFile: "/tmp/client.key"}},
			RootCAs:          []string{"/tmp/root.pem"},
			TrustSystemRoots: new(false),
		},
	}
	if err := writeSettings(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !sameSettings(got, want) {
		t.Fatalf("loaded = %+v, want %+v", got, want)
	}
	if layout := got.Columns.layout(); layout.Widths["url"] != 320 || layout.Order[1] != "url" {
		t.Fatalf("layout = %+v", layout)
	}
	changed := want
	changed.Certificates.RootCAs = []string{"/tmp/other.pem"}
	if sameSettings(want, changed) {
		t.Fatal("a changed certificate root did not change the settings")
	}
}

func TestCertificatePassphrasesNeverReachSettingsOrExports(t *testing.T) {
	settingsValue := settings{Certificates: certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "api.example.com", Format: certs.FormatPKCS12, CertFile: "/tmp/client.p12"}},
		Secrets: map[string]string{"one": "never-export-me"},
	}}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeSettings(path, settingsValue); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "never-export-me") {
		t.Fatalf("settings.json contains the passphrase: %s", data)
	}
	a := &app{certificates: settingsValue.Certificates}
	bundle, _ := a.exportConfig()
	data, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "never-export-me") {
		t.Fatalf("configuration export contains the passphrase: %s", data)
	}
}

func TestLoadSettingsWithoutAFile(t *testing.T) {
	got, err := loadSettings(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || !sameSettings(got, settings{}) {
		t.Fatalf("load = %+v, %v; want the defaults", got, err)
	}
}

func TestStartupUsesSavedProxyPort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".vscroxy", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	addr := busy.Addr().String()
	_, raw, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(raw)
	if err := writeSettings(path, settings{ProxyPort: port}); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	if a.srv == nil {
		t.Fatal(a.errText)
	}
	defer a.srv.Close()
	defer a.views.stopSearch()
	if a.srv.Addr() != addr || a.proxyPortInput != raw {
		t.Fatalf("saved port not loaded: address %s, input %q", a.srv.Addr(), a.proxyPortInput)
	}
	if err := a.srv.Start(); err == nil || a.srv.Running() {
		t.Fatal("a fixed occupied port must fail instead of silently falling back")
	}
	if err := busy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.srv.Start(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "fixed port works")
	}))
	defer upstream.Close()
	f, err := a.srv.Replay(t.Context(), capture.Flow{Method: "GET", URL: upstream.URL})
	if err != nil || f.ResponseBody != "fixed port works" || a.srv.Addr() != addr {
		t.Fatalf("request through saved port: body %q, address %s, error %v", f.ResponseBody, a.srv.Addr(), err)
	}
}

func TestStartupRejectsInvalidSavedProxyPorts(t *testing.T) {
	for _, port := range []int{-1, 80, 1023, 65536} {
		t.Run(strconv.Itoa(port), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, ".vscroxy", "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeSettings(path, settings{ProxyPort: port}); err != nil {
				t.Fatal(err)
			}
			a := newApp()
			defer a.views.stopSearch()
			if a.srv != nil {
				defer a.srv.Close()
				t.Fatal("invalid saved port must not construct a listening proxy")
			}
			if !strings.Contains(a.errText, "1024 and 65535") {
				t.Fatalf("invalid port error = %q", a.errText)
			}
			if len(a.flows) != 0 || a.library.root == "" || a.envsPath == "" {
				t.Fatal("proxy failure must not seed traffic or hide local asset paths")
			}
		})
	}
}

func TestStartupFailureKeepsLocalAssetsUsable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".vscroxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSettings(filepath.Join(dir, "settings.json"), settings{ProxyPort: 80}); err != nil {
		t.Fatal(err)
	}
	library := &app{}
	library.library.root = filepath.Join(dir, "collections")
	if err := library.persistRequests([]savedRequest{{Request: request.Request{ID: "kept", Name: "kept", Method: "GET", URL: "https://example.test"}}}); err != nil {
		t.Fatal(err)
	}
	if err := saveEnvs(&store.File{Path: filepath.Join(dir, "env.json")}, envFile{Active: "dev", Envs: []environment{{Name: "dev", Vars: map[string]string{"host": "local"}}}}); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	defer a.views.stopSearch()
	if a.srv != nil || len(a.requests) != 1 || a.activeVars()["host"] != "local" || len(a.flows) != 0 {
		t.Fatalf("failed proxy hid assets or fabricated traffic: requests %v, vars %v, flows %d", a.requests, a.activeVars(), len(a.flows))
	}
	a.openSaved(a.requests[0].key())
	a.composeName = "edited offline"
	if !a.saveComposed() {
		t.Fatal("offline request editing is unavailable")
	}
}

func TestDamagedAssetsStayUntouchedUntilExplicitReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".vscroxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "collections", "Shop"), 0o700); err != nil {
		t.Fatal(err)
	}
	damaged := []string{filepath.Join("collections", "Shop", "Bad.request.json"), "env.json"}
	for _, name := range damaged {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{damaged"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newApp()
	defer a.views.stopSearch()
	if a.srv != nil {
		defer a.srv.Close()
	}
	a.openCompose()
	a.composeName, a.composeURL = "new", "https://example.test"
	if a.saveComposed() {
		t.Fatal("saving must be refused after a request library load error")
	}
	a.saveCapture(flow{method: "GET", url: "https://example.test"})
	a.openVars()
	a.varsRows = []variableRow{{Name: "token", Initial: "new", HasInitial: true}}
	a.saveVars()
	a.applyVars("dev", nil, map[string]string{"token": "from-script"})
	a.varsOpen = false
	for _, name := range damaged {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "{damaged" {
			t.Fatalf("%s overwritten: %q, %v", name, data, err)
		}
	}
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Local assets protected · saving disabled") {
		t.Fatal("load failure must remain visible")
	}
	// Recovery is external repair/move-aside plus a read-only retry, never an
	// implicit overwrite or an automatic destructive reset.
	for _, name := range damaged {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, name+".backup")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("Reload local assets"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Local assets protected · saving disabled") || !a.saveComposed() {
		t.Fatal("explicit reload did not restore saving after files were moved aside")
	}
	a.openVars()
	a.varsRows = []variableRow{{Name: "token", Initial: "recovered", HasInitial: true}}
	a.saveVars()
	f, err := loadEnvs(&store.File{Path: filepath.Join(dir, "env.json")})
	envs := f.Envs
	if err != nil || len(envs) != 1 || envs[0].Vars["token"] != "recovered" {
		t.Fatalf("environment recovery: %v, %v", envs, err)
	}
}

func TestSameSettingsSeesColumnChanges(t *testing.T) {
	base := settings{Columns: columnLayout{Order: []string{"url"}, Widths: map[string]float32{"url": 300}}}
	moved := settings{Columns: columnLayout{Order: []string{"client", "url"}, Widths: map[string]float32{"url": 300}}}
	wider := settings{Columns: columnLayout{Order: []string{"url"}, Widths: map[string]float32{"url": 380}}}
	if sameSettings(base, moved) {
		t.Error("a moved column compared equal")
	}
	if sameSettings(base, wider) {
		t.Error("a wider column compared equal")
	}
	if !sameSettings(base, base) {
		t.Error("the same settings compared different")
	}
	for _, changed := range []settings{
		{InspectorLayout: "bottom", Columns: base.Columns},
		{SideTablePercent: 61, Columns: base.Columns},
		{DiffSplit: 61, Columns: base.Columns},
		{SidebarHidden: true, Columns: base.Columns},
	} {
		if sameSettings(base, changed) {
			t.Error("a desktop layout change compared equal")
		}
	}
}

// TestHandEditedConfigIsNotOverwritten checks that a config file edited on
// disk after the app read it is never replaced by a save from stale state,
// that Reload picks the edit up, and that a damaged file stays untouched.
func TestHandEditedConfigIsNotOverwritten(t *testing.T) {
	a := workspaceApp()
	a.sslPath = filepath.Join(t.TempDir(), "ssl.json")
	var err error
	a.srv, err = capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.srv.Close() })
	a.reloadConfig()
	if !a.saveSSL(capture.SSLConfig{Hosts: []string{"app.test"}}) {
		t.Fatal("first save failed")
	}
	edited := []byte(`{"hosts":["hand.test"]}` + "\n")
	if err := os.WriteFile(a.sslPath, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if a.saveSSL(capture.SSLConfig{Hosts: []string{"stale.test"}}) {
		t.Fatal("a save from stale state replaced a hand edit")
	}
	if disk, _ := os.ReadFile(a.sslPath); string(disk) != string(edited) {
		t.Fatalf("hand edit overwritten: %s", disk)
	}
	last := a.notices[len(a.notices)-1]
	if last.action != "Reload" || last.run == nil {
		t.Fatalf("changed file offered no reload: %+v", last)
	}
	last.run()
	if a.sslHosts != "hand.test" || !a.srv.SSL().Decrypts("hand.test") {
		t.Fatalf("reload did not pick up the edit: %q", a.sslHosts)
	}
	if !a.saveSSL(capture.SSLConfig{Hosts: []string{"hand.test", "next.test"}}) {
		t.Fatal("save after reload failed")
	}
	if info, err := os.Stat(a.sslPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file is not private: %v %v", info.Mode(), err)
	}

	damaged := []byte(`{"hosts":`)
	if err := os.WriteFile(a.sslPath, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if tt := ui.NewTester(a.view, 960, 600); !tt.HasText(a.sslPath + " could not be read and is left untouched") {
		t.Fatalf("damaged file not reported: %q", tt.Texts())
	}
	if a.saveSSL(capture.SSLConfig{All: true}) {
		t.Fatal("a damaged file was replaced")
	}
	if disk, _ := os.ReadFile(a.sslPath); string(disk) != string(damaged) {
		t.Fatalf("damaged file changed: %s", disk)
	}
}
