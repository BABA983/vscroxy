package app

import (
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// A custom-selected list row must show the 7% ink wash, not the solid Accent
// MyGo paints for its own selection: the row's body text is body-colored and
// disappears over the solid fill in both appearances.
func TestChosenScriptRowUsesSelectionWash(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a := NewGallery("tools-scripts")
		a.SetDark(dark)
		tt := ui.NewTester(a.View, 1280, 900)
		tt.SetLocale("en-US")
		tt.Frame()
		a.Close()

		r, ok := tt.Find("onRequest · onResponse")
		if !ok {
			t.Fatalf("dark=%t: chosen script row missing: %q", dark, tt.Texts())
		}
		p := &uikit.Light
		if dark {
			p = &uikit.Dark
		}
		want := p.Accent.Over(p.Background)
		img := tt.Image()
		found := 0
		for y := max(img.Bounds().Min.Y, int(r.Y)-14); y < min(img.Bounds().Max.Y, int(r.Y+r.H)+14); y++ {
			for x := max(img.Bounds().Min.X, int(r.X)-24); x < min(img.Bounds().Max.X, int(r.X+r.W)+24); x++ {
				got := img.RGBAAt(x, y)
				if got.R == want.R && got.G == want.G && got.B == want.B {
					found++
				}
			}
		}
		if found < 100 {
			t.Errorf("dark=%t: only %d pixels of the chosen row use the %v wash", dark, found, want)
		}
	}
}
