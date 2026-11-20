package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestServerCertificateScreenshots exports 2x Tester captures of the new
// Settings → Certificates card in both themes. Set
// VSCROXY_SERVER_CERT_SCREENSHOTS to an absolute output directory.
func TestServerCertificateScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_SERVER_CERT_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_SERVER_CERT_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearance
			snapshotSettings(a, 6)
			a.certificates = certs.Settings{
				Servers: []certs.Server{{
					ID: "server", Host: "api.example.com:8443", Format: certs.FormatPEM,
					CertFile: "/Users/me/certs/api.crt", KeyFile: "/Users/me/certs/api.key",
				}},
				Secrets: map[string]string{"server": "fixture-passphrase"},
			}
			a.certificateUI.loaded, a.certificateUI.open = false, map[string]bool{}
			tt := ui.NewTester(a.view, 900, 700)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			for range 12 {
				title, hasTitle := tt.Find(locales.ServerCertificates())
				row, hasRow := tt.Find("PEM · api.crt")
				if hasTitle && hasRow && title.Y >= 40 && row.Y+row.H < 590 {
					break
				}
				tt.Scroll(450, 350, 0, 180)
				tt.Frame()
			}
			if _, ok := tt.Find(locales.ServerCertificates()); !ok {
				t.Fatal("the server certificates card is not drawn")
			}
			if !tt.HasText("PEM · api.crt") {
				t.Fatal("the saved server certificate row is not drawn")
			}
			writeFeatureScreenshot(t, dir, "server-certificates-"+appearance+".png", tt.Image())
		})
	}
}
