package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestRestoreLastSessionScreenshots exports 2x Tester captures of a restored
// launch with both capture groups and of the new setting, in both themes.
// Set VSCROXY_RESTORE_SCREENSHOTS to an absolute output directory.
func TestRestoreLastSessionScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_RESTORE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_RESTORE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedSession := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		seed := relaunch(t, root)
		seed.appearance = "light"
		seed.flows = []flow{
			{id: 41, method: "GET", scheme: "https", host: "api.example.com", path: "/v1/users?page=2", url: "https://api.example.com/v1/users?page=2", status: 200, millis: 42, reqHeaders: "Accept: application/json", resHeaders: "Content-Type: application/json", reqBody: "GET /v1/users HTTP/1.1", resBody: `{"users":[{"id":1,"name":"Ada"}]}`},
			{id: 42, method: "POST", scheme: "https", host: "other.test", path: "/checkout", url: "https://other.test/checkout", status: 201, millis: 128, reqHeaders: "Content-Type: application/json", resHeaders: "Content-Type: application/json", reqBody: `{"cart":["one"]}`, resBody: `{"ok":true,"order":"A-12"}`},
			{id: 43, method: "POST", scheme: "https", host: "api.example.com", path: "/login", url: "https://api.example.com/login", status: 500, millis: 301, reqHeaders: "Content-Type: application/json", resHeaders: "Content-Type: application/json", reqBody: `{"user":"ada"}`, resBody: `{"error":"bad gateway"}`},
		}
		seed.recount()
		seed.applyFilter()
		seed.views.splitRight(seed)
		groupFilter(seed, 0, "errors", "")
		groupFilter(seed, 1, "host:other.test", "")
		seed.views.focusCaptureGroup(seed, 0)
		seed.picked, seed.pickedID = 0, 43
		seed.views.capture(seed)
		seed.views.focusCaptureGroup(seed, 1)
		seed.picked, seed.pickedID = 0, 42
		seed.views.capture(seed)
		quitForSession(t, seed)
		return root
	}

	write := func(name string, tt *ui.Tester) {
		t.Helper()
		path := filepath.Join(dir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := png.Encode(file, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := relaunch(t, seedSession(t))
			a.appearance = appearance
			waitLastSessionFinish(t, a)
			if !a.views.split {
				t.Fatal("the restored launch did not have split capture groups")
			}
			second := a.views.viewByID(a.views.groups[1])
			if second == nil || second.filter != "host:other.test" || second.pickedID != 42 {
				t.Fatalf("restored group 2 = %+v, want the saved filter and row", second)
			}
			tt := ui.NewTester(a.view, 1500, 900)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			tt.Frame()
			for _, label := range []string{locales.CaptureGroup(1), locales.CaptureGroup(2)} {
				if !tt.HasText(label) {
					t.Fatalf("restored launch is missing %q: %v", label, tt.Texts())
				}
			}
			write("restored-launch-"+appearance+".png", tt)

			a.settingsTab = int(settingsCapture)
			a.settingsChoice = settingsCapture.id()
			a.revealSetting("capture.restoreLastSession")
			settings := ui.NewTester(a.settingsView, 1000, 900)
			settings.SetLocale("en-US")
			settings.SetDark(dark)
			settings.SetScale(2)
			settings.Frame()
			settings.Frame()
			for _, label := range []string{locales.Startup(), locales.RestoreLastSession()} {
				if !settings.HasText(label) {
					t.Fatalf("setting screen is missing %q: %v", label, settings.Texts())
				}
			}
			write("restore-last-session-setting-"+appearance+".png", settings)
		})
	}
}
