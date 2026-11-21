package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestVPNSettingsScreenshots exports 2x Tester captures of the two Settings
// pages used in the corporate VPN guide. Set VSCROXY_VPN_SCREENSHOTS to an
// absolute output directory. No real proxy or trust setting is touched.
func TestVPNSettingsScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_VPN_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_VPN_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run("upstream-"+appearance, func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearance
			snapshotSettings(a, int(settingsProxy))
			a.upstreamCfg = upstream.Settings{
				Default: "http://proxy.example:8080",
				Rules:   []upstream.Rule{{Host: "*.internal.example", Proxy: "direct"}},
				Bypass:  []string{"sso.example.com", "vpn.example.com", "localhost"},
				PAC:     "https://proxy.example/proxy.pac",
				Credentials: []upstream.Credential{{
					Proxy: "http://proxy.example:8080", Username: "alice",
					Auth: upstream.AuthNTLM, Domain: "CORP", Workstation: "WS01",
				}},
			}
			a.upstreamUI.loaded = false
			tt := ui.NewTester(a.view, 1100, 1000)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			tt.Scroll(550, 500, 0, 900)
			tt.Frame()
			for _, label := range []string{
				locales.Upstream2(), locales.DefaultUpstream(), locales.HostRules(),
				locales.BypassList(), locales.PacFileOrUrl(), locales.SaveUpstream(),
				locales.Authentication(), locales.AuthNtlm(), locales.Domain(),
				locales.Workstation(), locales.NtlmProxyHint(),
			} {
				if !tt.HasText(label) {
					t.Fatalf("upstream settings do not show %q", label)
				}
			}
			writeFeatureScreenshot(t, dir, "vpn-upstream-"+appearance+".png", tt.Image())
		})
		t.Run("roots-"+appearance, func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearance
			snapshotSettings(a, int(settingsCertificates))
			a.certificates = certs.Settings{RootCAs: []string{"/Users/me/certs/company-root.pem"}}
			a.certificateUI.loaded = false
			tt := ui.NewTester(a.view, 900, 700)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			tt.Scroll(450, 350, 0, 1200)
			tt.Frame()
			for _, label := range []string{locales.TrustedRoots(), locales.TrustSystemRoots(), locales.SaveCertificates()} {
				if !tt.HasText(label) {
					t.Fatalf("certificate settings do not show %q", label)
				}
			}
			if len(a.certificateUI.roots) != 1 || a.certificateUI.roots[0].Path != "/Users/me/certs/company-root.pem" {
				t.Fatalf("root editor loaded %+v", a.certificateUI.roots)
			}
			writeFeatureScreenshot(t, dir, "vpn-roots-"+appearance+".png", tt.Image())
		})
	}
}
