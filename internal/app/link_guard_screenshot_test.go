package app

import (
	"image"
	"image/draw"
	"os"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestLinkGuardScreenshots exports 2x Tester captures of the confirmation
// for an ordinary link and for one with a disguised host. Set
// VSCROXY_LINK_GUARD_SCREENSHOTS to an output directory.
func TestLinkGuardScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_LINK_GUARD_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_LINK_GUARD_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name string
		url  string
	}{
		{"confirm", "https://example.com/reports/quarterly?view=summary"},
		{"warning", "http://user@evil.com@good.com/reports/quarterly?view=summary"},
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		for _, variant := range variants {
			t.Run(appearance+"-"+variant.name, func(t *testing.T) {
				a := snapshotApp()
				var c *ui.Context
				tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1440, 900)
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(2)
				tt.Frame()
				a.openCapturedLink(c, variant.url)
				tt.Frame()
				if !tt.HasText(variant.url) || !tt.HasText(locales.ExternalLinkConfirmationTitle()) {
					t.Fatalf("the dialog is missing the full target: %q", tt.Texts())
				}
				wantWarning := variant.name == "warning"
				if got := !tt.HasText(locales.ExternalLinkFromContent()); got != wantWarning {
					t.Fatalf("warning variant = %v, want %v", got, wantWarning)
				}
				panel, ok := tt.AccessByRole(ui.RoleDialog, locales.ExternalLinkConfirmationTitle())
				if !ok {
					t.Fatal("the dialog has no accessible panel")
				}
				const scale = 2
				r := panel.Bounds
				crop := image.Rect(int(r.X-12)*scale, int(r.Y-12)*scale,
					int(r.X+r.W+12)*scale, int(r.Y+r.H+12)*scale).Intersect(tt.Image().Bounds())
				out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
				draw.Draw(out, out.Bounds(), tt.Image(), crop.Min, draw.Src)
				writeFeatureScreenshot(t, dir, "link-"+variant.name+"-"+appearance+".png", out)
			})
		}
	}
}
