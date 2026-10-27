package app

import (
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
	"github.com/egoist/mygo/ui"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := settings{
		Dark:             true,
		SidebarWidth:     264,
		TablePercent:     55,
		InspectorLayout:  "right",
		SideTablePercent: 61,
		SidebarHidden:    true,
		Columns:          columnLayout{Order: []string{"status", "url"}, Widths: map[string]float32{"url": 320}},
		HeaderColumn:     "x-request-id",
		KeyLogPath:       "/tmp/keys.log",
		ProxyPort:        9091,
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
			if len(a.flows) != 0 || a.requestsPath == "" || a.envsPath == "" {
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
	if err := saveRequests(filepath.Join(dir, "requests.json"), []savedRequest{{Name: "kept", Method: "GET", URL: "https://example.test"}}); err != nil {
		t.Fatal(err)
	}
	if err := saveEnvs(filepath.Join(dir, "env.json"), []environment{{Name: "dev", Vars: map[string]string{"host": "local"}}}, "dev"); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	defer a.views.stopSearch()
	if a.srv != nil || len(a.requests) != 1 || a.activeVars()["host"] != "local" || len(a.flows) != 0 {
		t.Fatalf("failed proxy hid assets or fabricated traffic: requests %v, vars %v, flows %d", a.requests, a.activeVars(), len(a.flows))
	}
	a.openSaved(a.requests[0].Collection, a.requests[0].Name)
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
	for _, name := range []string{"requests.json", "env.json"} {
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
	a.saveFlowAsRequest(flow{method: "GET", url: "https://example.test"})
	a.openVars()
	a.varsText = "token = new"
	a.saveVars()
	a.applyVars("dev", nil, map[string]string{"token": "from-script"})
	a.varsOpen = false
	for _, name := range []string{"requests.json", "env.json"} {
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
	for _, name := range []string{"requests.json", "env.json"} {
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
	a.varsText = "token = recovered"
	a.saveVars()
	envs, _, err := loadEnvs(filepath.Join(dir, "env.json"))
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
		{SidebarHidden: true, Columns: base.Columns},
	} {
		if sameSettings(base, changed) {
			t.Error("a desktop layout change compared equal")
		}
	}
}
