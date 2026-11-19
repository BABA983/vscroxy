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

// TestAttentionSignalsScreenshots exports 2x Tester captures of the
// "Attention signals" row in both themes. The row is cropped from the
// Settings window so the label, segmented choice and hint are easy to read.
// Set VSCROXY_ATTENTION_SCREENSHOTS to an absolute output directory.
func TestAttentionSignalsScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_ATTENTION_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_ATTENTION_SCREENSHOTS to an output directory")
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
			a.settingsOpen, a.settingsTab, a.settingsChoice = true, 0, "0"
			tt := ui.NewTester(a.view, 1100, 760)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			label, ok := tt.Find(locales.AttentionSignals())
			if !ok {
				t.Fatalf("row label %q is not drawn", locales.AttentionSignals())
			}
			full := tt.Image()
			scale := 2
			crop := image.Rect(
				int(label.X-24)*scale, int(label.Y-42)*scale,
				int(label.X+615)*scale, int(label.Y+74)*scale,
			).Intersect(full.Bounds())
			out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
			draw.Draw(out, out.Bounds(), full, crop.Min, draw.Src)
			var b bytes.Buffer
			if err := png.Encode(&b, out); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "attention-signals-row-"+appearance+".png")
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
