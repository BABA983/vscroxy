package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestResponseReferenceScreenshots exports the suggestion step of the token
// link dialog at 2x in both themes, cropped to the dialog. Set
// VSCROXY_RESPONSE_REF_SCREENSHOTS to an absolute output directory.
func TestResponseReferenceScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_RESPONSE_REF_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_RESPONSE_REF_SCREENSHOTS to an output directory")
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
			a := responseRefPlanApp(t)
			a.appearance = appearance
			tt := ui.NewTester(a.view, 1180, 760)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			title, ok := tt.Find(locales.TokenLinks())
			if !ok {
				t.Fatalf("dialog title %q is not drawn", locales.TokenLinks())
			}
			if !tt.HasText("Response reference suggestions") || tt.HasText("preview-secret-token-123") {
				t.Fatalf("suggestion view is missing or unmasked: %v", tt.Texts())
			}
			const scale = 2
			crop := image.Rect(
				int(title.X-24)*scale, int(title.Y-40)*scale,
				int(title.X+830)*scale, int(title.Y+580)*scale,
			).Intersect(tt.Image().Bounds())
			out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
			draw.Draw(out, out.Bounds(), tt.Image(), crop.Min, draw.Src)
			var b bytes.Buffer
			if err := png.Encode(&b, out); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "response-reference-suggestions-"+appearance+".png")
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Log(path)
		})
	}
}
