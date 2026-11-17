package uikit

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A checkbox is named by its label, and a click on the label checks it. A
// disabled one keeps its value.
func TestCheckboxTogglesAndNamesItself(t *testing.T) {
	on, off := false, false
	tt := ui.NewTester(func(c *ui.Context) {
		Checkbox(c, &on, "Keep traffic")
		Checkbox(c, &off, "Locked").Disabled(true)
	}, 320, 120)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleCheckBox, "Keep traffic"); !ok {
		t.Fatalf("no check box named %q; access=%v", "Keep traffic", tt.AccessAll())
	}
	if err := tt.Click("Keep traffic"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !on {
		t.Fatal("a click on the label did not check the box")
	}
	if err := tt.Click("Locked"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if off {
		t.Fatal("a disabled check box toggled")
	}
}

// A radio button is named by its label, and a click chooses its value.
func TestRadioChoosesItsValue(t *testing.T) {
	selected := "alpha"
	tt := ui.NewTester(func(c *ui.Context) {
		RadioGroup(c, func() {
			Radio(c, &selected, "alpha", "Alpha")
			Radio(c, &selected, "beta", "Beta")
		})
	}, 320, 120)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleRadio, "Beta"); !ok {
		t.Fatalf("no radio button named %q; access=%v", "Beta", tt.AccessAll())
	}
	if err := tt.Click("Beta"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if selected != "beta" {
		t.Fatalf("selected = %q, want beta", selected)
	}
}

// A busy primary button keeps its name and the width its label had, so the
// row it sits in does not move when work starts.
func TestBusyButtonKeepsWidthAndName(t *testing.T) {
	busy := false
	tt := ui.NewTester(func(c *ui.Context) {
		PrimaryButtonBusy(c, "Save", busy)
	}, 300, 60)
	tt.Frame()
	idle, ok := tt.AccessByRole(ui.RoleButton, "Save")
	if !ok {
		t.Fatal("the idle button has no name Save")
	}
	busy = true
	tt.Frame()
	working, ok := tt.AccessByRole(ui.RoleButton, "Save")
	if !ok {
		t.Fatal("the busy button has no name Save")
	}
	if !working.Disabled {
		t.Fatal("the busy button is not disabled")
	}
	if working.Bounds.W != idle.Bounds.W {
		t.Fatalf("busy width = %.1f, idle width = %.1f: the button moved", working.Bounds.W, idle.Bounds.W)
	}
}

// Select, SelectLabels and MenuSelect are one trigger: in a row of them, every
// trigger is the same height.
func TestSelectTypesShareTriggerHeight(t *testing.T) {
	picked, labelled := "Pretty", "Pretty"
	options := []string{"Pretty", "Raw"}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Gap(8).Children(func() {
			Select(c, &picked, options).Label("Plain select")
			SelectLabels(c, &labelled, options, func(s string) string { return s }).Label("Labelled select")
			MenuSelect(c, "Pretty", func(m *ui.Menu) {
				m.Item("Pretty")
				m.Item("Raw")
			}).Label("Menu select")
		})
	}, 640, 60)
	tt.Frame()
	var heights []float32
	for _, name := range []string{"Plain select", "Labelled select", "Menu select"} {
		info, ok := tt.Access(name)
		if !ok {
			t.Fatalf("no trigger named %q; access=%v", name, tt.AccessAll())
		}
		heights = append(heights, info.Bounds.H)
	}
	for i, h := range heights[1:] {
		if h != heights[0] {
			t.Fatalf("trigger heights = %v: trigger %d differs from the first", heights, i+1)
		}
	}
}

// Each callout kind draws its icon in its ink, left of its sentence, and shows
// the sentence. The icon has no accessible name, so the test reads the pixels
// the icon's box holds.
func TestCalloutKindsShowIconAndText(t *testing.T) {
	const sentence = "The proxy could not bind 127.0.0.1:7890."
	p := Colors(Theme(false))
	kinds := []struct {
		name string
		kind CalloutKind
		ink  ui.Color
	}{
		{"info", CalloutInfo, p.Info},
		{"success", CalloutSuccess, p.Success},
		{"warning", CalloutWarning, p.Warning},
		{"danger", CalloutDanger, p.Danger},
	}
	for _, k := range kinds {
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(Theme(false))
			Callout(c, k.kind, sentence, nil)
		}, 480, 80)
		tt.Frame()
		text, ok := tt.Find(sentence)
		if !ok {
			t.Fatalf("%s: the sentence is missing; texts=%q", k.name, tt.Texts())
		}
		img := tt.Image()
		inked := false
		// The icon sits one gap left of the text, centered on the first line.
		for y := int(text.Y); y < int(text.Y+text.H); y++ {
			for x := int(text.X) - IconLarge - 8; x < int(text.X)-4; x++ {
				if closeTo(img.RGBAAt(x, y), k.ink) {
					inked = true
				}
			}
		}
		if !inked {
			t.Fatalf("%s: no pixel left of the sentence has the kind's ink %v", k.name, k.ink)
		}
	}
}

// closeTo reports whether a pixel is the ink, within a few levels per channel,
// as an icon's solid strokes are.
func closeTo(px color.RGBA, ink ui.Color) bool {
	near := func(a, b uint8) bool {
		if a > b {
			return a-b <= 6
		}
		return b-a <= 6
	}
	return px.A == 255 && near(px.R, ink.R) && near(px.G, ink.G) && near(px.B, ink.B)
}

// A progress bar reports its fraction, from 0 to 1, as its value.
func TestProgressBarReportsItsValue(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ProgressBar(c, 0.25, "Uploading")
	}, 240, 40)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleProgress, "Uploading")
	if !ok {
		t.Fatalf("no progress bar named %q; access=%v", "Uploading", tt.AccessAll())
	}
	if info.Min != 0 || info.Max != 1 || info.Now != 0.25 {
		t.Fatalf("progress = min %v, now %v, max %v; want 0, 0.25, 1", info.Min, info.Now, info.Max)
	}
}
