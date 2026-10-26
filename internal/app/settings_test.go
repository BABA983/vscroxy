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
		})
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
