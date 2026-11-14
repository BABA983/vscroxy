package gallery

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/app"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

const (
	plateWidth  = 1120
	plateHeight = 600
)

// plateSize is the canvas an entry exports at.
func plateSize(e *Entry) (int, int) {
	w, h := plateWidth, plateHeight
	if e.Width > 0 {
		w = e.Width
	}
	if e.Height > 0 {
		h = e.Height
	}
	return w, h
}

// plateView is the view an entry exports: the app's own view for a screen,
// the plate's builder on the gallery canvas for everything else.
func plateView(t *testing.T, e *Entry, dark bool) (func(*ui.Context), func()) {
	t.Helper()
	uikit.SetTextScale(1)
	if e.Group == "screens" {
		s := app.NewGallery(e.ID)
		if s == nil {
			t.Fatalf("screen %q is not built by internal/app", e.ID)
		}
		s.SetDark(dark)
		return s.View, s.Close
	}
	view := func(c *ui.Context) {
		uikit.SetTextScale(1)
		c.SetTheme(uikit.Theme(dark))
		ui.Column(c).Fill().Background(uikit.Colors(c.Theme()).Canvas).Padding(28).Children(func() {
			e.build(c)
		})
	}
	return view, func() {}
}

// TestGalleryRenders draws every plate once. It is the check that runs
// without an output directory: each entry builds, draws a frame and shows
// something, and a screen the app cannot build fails by name.
func TestGalleryRenders(t *testing.T) {
	g := New()
	seen := make(map[string]bool)
	for _, e := range g.Entries() {
		if seen[e.ID] {
			t.Fatalf("duplicate entry %q", e.ID)
		}
		seen[e.ID] = true
		t.Run(e.ID, func(t *testing.T) {
			view, close := plateView(t, e, false)
			defer close()
			w, h := plateSize(e)
			tt := ui.NewTester(view, w, h)
			tt.SetLocale("en-US")
			tt.Frame()
			if len(tt.Texts()) == 0 {
				t.Fatalf("%s drew no text", e.ID)
			}
		})
	}
	if len(g.groups) != 3 {
		t.Fatalf("catalog has %d groups", len(g.groups))
	}
	for _, group := range g.groups {
		if len(group.Entries) == 0 {
			t.Fatalf("%s is empty", group.ID)
		}
	}
}

// TestGallerySnapshots writes one plate per entry and appearance, the same
// opt-in way TestUISnapshots does:
//
//	VSCROXY_GALLERY_OUT="$PWD/.amp/in/artifacts/gallery" go test ./internal/gallery -run '^TestGallerySnapshots$' -count=1
//
// The exported plates are then scaled and reduced for docs/design; see
// docs/ui-redesign.md.
func TestGallerySnapshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_GALLERY_OUT")
	if dir == "" {
		t.Skip("set VSCROXY_GALLERY_OUT to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range New().Entries() {
		for _, dark := range []bool{false, true} {
			view, close := plateView(t, e, dark)
			w, h := plateSize(e)
			tt := ui.NewTester(view, w, h)
			tt.SetLocale("en-US")
			tt.Frame()
			forceStates(t, tt, e)
			tt.Advance(250 * time.Millisecond)
			tt.Frame()
			img := tt.Image()
			if e.Group != "screens" {
				img = cropPlate(img, 28)
			}
			name := e.Group + "-" + e.ID + "-" + themeName(dark) + ".png"
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				close()
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				close()
				t.Fatal(err)
			}
			close()
		}
	}
}

// forceStates puts the pointer on the entry's hover sample and tabs to its
// focus sample, then lets a tooltip's delay pass, so the plate shows the
// states a pointer and a keyboard show.
func forceStates(t *testing.T, tt *ui.Tester, e *Entry) {
	t.Helper()
	if e.Hover != "" {
		r, ok := tt.Find(e.Hover)
		if !ok {
			t.Fatalf("%s: no hover sample %q", e.ID, e.Hover)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
	}
	if e.Focus != "" {
		r, ok := tt.Find(e.Focus)
		if !ok {
			t.Fatalf("%s: no focus sample %q", e.ID, e.Focus)
		}
		focused := false
		for range 60 {
			if focusWithin(tt, r) {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("%s: Tab never reached the focus sample %q", e.ID, e.Focus)
		}
	}
	if e.Tip {
		tt.Advance(700 * time.Millisecond)
	}
	tt.Frame()
}

// focusWithin reports whether the keyboard focus is on an element inside r.
func focusWithin(tt *ui.Tester, r ui.Rect) bool {
	for _, a := range tt.AccessAll() {
		if !a.Focused {
			continue
		}
		b := a.Bounds
		x, y := b.X+b.W/2, b.Y+b.H/2
		if x >= r.X && x <= r.X+r.W && y >= r.Y && y <= r.Y+r.H {
			return true
		}
	}
	return false
}

// cropPlate trims the empty canvas under a plate: the canvas is one flat
// color, so the plate ends at the last row that is not it, plus its padding.
func cropPlate(img *image.RGBA, pad int) *image.RGBA {
	b := img.Bounds()
	bg := img.RGBAAt(b.Min.X, b.Min.Y)
	last := b.Min.Y
	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		row := false
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y) != bg {
				row = true
				break
			}
		}
		if row {
			last = y
			break
		}
	}
	height := min(b.Max.Y, last+pad+1)
	if height <= b.Min.Y {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), height))
	for y := range height {
		copy(out.Pix[y*out.Stride:y*out.Stride+b.Dx()*4], img.Pix[(b.Min.Y+y)*img.Stride:(b.Min.Y+y)*img.Stride+b.Dx()*4])
	}
	return out
}

func themeName(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}
