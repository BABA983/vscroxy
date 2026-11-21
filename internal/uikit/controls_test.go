package uikit

import (
	"fmt"
	"image"
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

// A disabled radio button keeps the value it had, as a disabled check box does.
func TestDisabledRadioKeepsItsValue(t *testing.T) {
	selected := "alpha"
	tt := ui.NewTester(func(c *ui.Context) {
		RadioGroup(c, func() {
			Radio(c, &selected, "alpha", "Alpha")
			Radio(c, &selected, "beta", "Beta").Disabled(true)
		})
	}, 320, 120)
	tt.Frame()
	if err := tt.Click("Beta"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if selected != "alpha" {
		t.Fatalf("a disabled radio button chose %q", selected)
	}
}

// A progress bar reports its fraction, from 0 to 1, as its value.
func TestProgressBarReportsItsValue(t *testing.T) {
	theme := Theme(false)
	track := Colors(theme).MutedForeground.Alpha(0.2).Over(theme.Background)
	fill := Colors(theme).Success.Alpha(0.7).Over(track)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Fill().Background(theme.Background).Children(func() {
			ProgressBar(c, 0.25, "Uploading")
		})
	}, 240, 40)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleProgress, "Uploading")
	if !ok {
		t.Fatalf("no progress bar named %q; access=%v", "Uploading", tt.AccessAll())
	}
	if info.Min != 0 || info.Max != 1 || info.Now != 0.25 {
		t.Fatalf("progress = min %v, now %v, max %v; want 0, 0.25, 1", info.Min, info.Now, info.Max)
	}
	if got := info.Bounds.H; got != progressHeight {
		t.Fatalf("progress height = %v, want %v", got, progressHeight)
	}
	// The fill rounds only its left cap: the right edge stays flat where the
	// remaining track begins, including the rows just inside the top and
	// bottom.
	img := tt.Image()
	end := int(info.Bounds.X + info.Bounds.W*0.25)
	for _, x := range []int{end - 2, end - 1} {
		for _, y := range []int{int(info.Bounds.Y) + 1, int(info.Bounds.Y+info.Bounds.H) - 2} {
			if px := img.RGBAAt(x, y); !closeTo(px, fill) {
				t.Fatalf("fill edge at (%v,%v) = %v, want %v", x, y, px, fill)
			}
		}
	}
	// A finished bar rounds its right cap with the track instead of keeping
	// the flat edge a partial fill has.
	full := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Fill().Background(theme.Background).Children(func() {
			ProgressBar(c, 1, "Complete")
		})
	}, 240, 40)
	full.Frame()
	info, ok = full.AccessByRole(ui.RoleProgress, "Complete")
	if !ok {
		t.Fatal("no progress bar named Complete")
	}
	img = full.Image()
	right := int(info.Bounds.X+info.Bounds.W) - 1
	for _, y := range []int{int(info.Bounds.Y) + 1, int(info.Bounds.Y+info.Bounds.H) - 2} {
		if px := img.RGBAAt(right, y); closeTo(px, fill) {
			t.Fatalf("full fill kept a square corner at (%v,%v) = %v", right, y, px)
		}
		if px := img.RGBAAt(right-4, y); !closeTo(px, fill) {
			t.Fatalf("full fill corner at (%v,%v) = %v, want %v", right-4, y, px, fill)
		}
	}
	mid := img.RGBAAt(right-4, int(info.Bounds.Y+info.Bounds.H/2))
	if !closeTo(mid, fill) {
		t.Fatalf("full fill edge = %v, want %v", mid, fill)
	}
}

// A select's popup keeps its options inside the panel: the panel has its
// inset and each option its own, so an option's text never sits on the
// popup's edge.
func TestSelectPopupInsetsItsOptions(t *testing.T) {
	selected := "Pretty"
	theme := Theme(false)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Padding(20).Children(func() {
			Select(c, &selected, []string{"Pretty", "Raw"}).Label("Format").Width(160)
		})
	}, 320, 200)
	tt.Frame()
	trigger, ok := tt.Access("Format")
	if !ok {
		t.Fatal("no trigger named Format")
	}
	if err := tt.Click("Format"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	option, ok := tt.Find("Raw")
	if !ok {
		t.Fatalf("the popup did not open; texts=%q", tt.Texts())
	}
	want := theme.Space(SpaceXS) + theme.Space(SpaceM)
	if inset := option.X - trigger.Bounds.X; inset < want-1 || inset > want+1 {
		t.Fatalf("option text starts %.1f from the popup's edge, want about %.1f", inset, want)
	}
	var panel ui.Rect
	for _, a := range tt.AccessAll() {
		r := a.Bounds
		if r.Y > trigger.Bounds.Y+trigger.Bounds.H-1 && r.W >= trigger.Bounds.W && r.H > option.H && r.H > panel.H {
			panel = r
		}
	}
	if gap := panel.Y - (trigger.Bounds.Y + trigger.Bounds.H); gap < 3 || gap > 5 {
		t.Fatalf("popup gap from trigger = %.1f, want about 4", gap)
	}
}

// A value longer than its field stops at the field's inset, not at the field's
// edge: the band of the right inset holds the face and no ink, while the text
// still draws in the field.
func TestInputTextStopsAtItsRightInset(t *testing.T) {
	value := "https://api.example.com/v1/projects/a-path-that-does-not-fit-the-field"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		Input(c, &value).Width(160)
	}, 160, 28)
	tt.Frame()
	checkTextStopsAtRightInset(t, tt.Image(), 160)
}

// A field's edge runs round its whole box, the right side included: the right
// inset is a border, and the edge must not stop at the padding edge short of
// it. The left and right edge pixels match, and both differ from the well.
func TestFieldEdgeReachesItsRightSide(t *testing.T) {
	for _, tc := range []struct {
		name           string
		invalid, focus bool
	}{
		{"focused", false, true},
		{"invalid", true, false},
		{"invalid and focused", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			empty := ""
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(Theme(false))
				InputState(c, &empty, tc.invalid).Width(160)
			}, 160, 40)
			tt.Frame()
			if tc.focus {
				tt.ClickAt(80, 20)
				tt.Frame()
			}
			img := tt.Image()
			lr, lg, lb, _ := img.At(0, 20).RGBA()
			rr, rg, rb, _ := img.At(159, 20).RGBA()
			wr, wg, wb, _ := img.At(80, 20).RGBA()
			if [3]uint32{lr, lg, lb} == [3]uint32{wr, wg, wb} {
				t.Fatal("the left edge draws no edge over the well")
			}
			if [3]uint32{rr, rg, rb} != [3]uint32{lr, lg, lb} {
				t.Fatalf("right edge %v, left edge %v: the edge stops short of the right side", [3]uint32{rr, rg, rb}, [3]uint32{lr, lg, lb})
			}
		})
	}
}

// A combobox's text stops ControlPaddingX short of its arrow, so a value
// longer than the field never runs up to the arrow. In the field's ink the
// arrow is the last run of columns and the text is the run before it.
func TestComboboxTextStopsBeforeItsArrow(t *testing.T) {
	value := "https://api.example.com/v1/projects/a-path-that-does-not-fit-the-field"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		Combobox(c, &value, []string{value}).Width(160)
	}, 160, 28)
	tt.Frame()
	img := tt.Image()
	inked := func(x int) bool {
		for y := 8; y < 20; y++ {
			if r, g, b, _ := img.At(x, y).RGBA(); (r+g+b)/3 < 0xb000 {
				return true
			}
		}
		return false
	}
	var runs [][2]int
	for x := 2; x < 158; x++ {
		if !inked(x) {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1][1] == x-1 {
			runs[n-1][1] = x
		} else {
			runs = append(runs, [2]int{x, x})
		}
	}
	if len(runs) < 2 {
		t.Fatalf("want text and an arrow in the field, got ink runs %v", runs)
	}
	text, arrow := runs[len(runs)-2], runs[len(runs)-1]
	if gap := arrow[0] - text[1] - 1; gap < ControlPaddingX {
		t.Fatalf("the text ends %d px before the arrow, want at least %d", gap, ControlPaddingX)
	}
}

// checkTextStopsAtRightInset fails when a 160-wide field draws no text, or
// draws ink in the band of its right inset.
func checkTextStopsAtRightInset(t *testing.T, img image.Image, width int) {
	t.Helper()
	ink := func(x0, x1 int) bool {
		for y := 8; y < 20; y++ {
			for x := x0; x < x1; x++ {
				if r, _, _, _ := img.At(x, y).RGBA(); r < 0x8000 {
					return true
				}
			}
		}
		return false
	}
	if !ink(ControlPaddingX, width-ControlPaddingX) {
		t.Fatal("the field draws no text")
	}
	if ink(width-ControlPaddingX, width-2) {
		t.Fatal("the text runs into the right inset")
	}
}

// The field keeps its 15% ink edge at rest and adds one Ring point while
// focused. The switch draws a 20% muted-text track and muted knob when off,
// and an ink track with a pale knob when on; Dark keeps a lighter on track so
// the knob stays visible.
func TestFieldsAndSwitchesStates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			theme := Theme(dark)
			p := Colors(theme)
			text := ""
			on, off := true, false
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				ui.Column(c).Gap(12).Background(theme.Background).Padding(8).Children(func() {
					Input(c, &text).Label("Host")
					Switch(c, &off).Label("Off")
					Switch(c, &on).Label("On")
				})
			}, 400, 260)
			tt.Frame()
			rest := tt.Image()
			if err := tt.Click("Host"); err != nil {
				t.Fatal(err)
			}
			focused := tt.Image()
			check := func(name string, img image.Image, x, y float32, want ui.Color) {
				t.Helper()
				got := img.(*image.RGBA).RGBAAt(int(x), int(y))
				for _, channel := range []struct {
					name      string
					got, want uint8
				}{
					{"R", got.R, want.R}, {"G", got.G, want.G}, {"B", got.B, want.B},
				} {
					d := int(channel.got) - int(channel.want)
					if d < -3 || d > 3 {
						t.Errorf("%s %s = %d, want %d", name, channel.name, channel.got, channel.want)
					}
				}
			}
			field, ok := tt.AccessByRole(ui.RoleTextField, "Host")
			if !ok {
				t.Fatal("the field is missing")
			}
			fy := field.Bounds.Y + field.Bounds.H/2
			check("field rest edge", rest, field.Bounds.X, fy, p.Foreground.Alpha(0.15).Over(p.Background))
			check("field focus ring", focused, field.Bounds.X-1, fy, p.Ring)
			for _, sw := range []struct {
				name string
				on   bool
			}{
				{"Off", false}, {"On", true},
			} {
				info, ok := tt.AccessByRole(ui.RoleSwitch, sw.name)
				if !ok {
					t.Fatalf("%s switch is missing", sw.name)
				}
				r := info.Bounds
				y := r.Y + r.H/2
				knob := p.MutedForeground
				track := p.MutedForeground.Alpha(0.2).Over(theme.Background)
				if sw.on {
					knob, track = ui.RGB(255, 255, 255), p.SwitchOn
					if !dark {
						knob = p.PrimaryForeground
					}
				}
				check(sw.name+" track", focused, r.X+1, y, track)
				x := r.X + 10
				if sw.on {
					x = r.X + 26
				}
				check(sw.name+" knob", focused, x, y, knob)
			}
		})
	}
}

// The checkbox is a clear box with a Primary edge when off and a Primary fill
// with a pale mark when on, with no hover face. Selects use the panel-colored
// field face and a 15% ink edge instead of a button face.
func TestCheckboxAndSelectStates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			theme := Theme(dark)
			p := Colors(theme)
			checked := true
			selected := "A"
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				ui.Column(c).Gap(8).Background(theme.Background).Padding(8).Children(func() {
					Checkbox(c, &checked, "Checked")
					Checkbox(c, new(bool), "Unchecked")
					Select(c, &selected, []string{"A", "B"})
				})
			}, 400, 220)
			tt.Frame()
			image := tt.Image()
			sample := func(name string, x, y float32) ui.Color {
				t.Helper()
				a, ok := tt.Access(name)
				if !ok {
					t.Fatalf("%s is missing", name)
				}
				got := image.RGBAAt(int(a.Bounds.X+x), int(a.Bounds.Y+y))
				return ui.RGB(got.R, got.G, got.B)
			}
			for _, box := range []struct {
				name string
				fill ui.Color
			}{
				{"Checked", p.Primary},
				{"Unchecked", theme.Background},
			} {
				if got, want := sample(box.name, 0, 10), p.Primary; got != want {
					t.Errorf("%s edge = %v, want %v", box.name, got, want)
				}
				if got, want := sample(box.name, 14, 10), box.fill; got != want {
					t.Errorf("%s fill = %v, want %v", box.name, got, want)
				}
			}
			if got, want := sample("A", 10, 14), p.Background; got != want {
				t.Errorf("select face = %v, want %v", got, want)
			}
			edge := sample("A", 24, 0)
			want := p.Foreground.Alpha(0.15).Over(p.Background)
			if edge != want {
				t.Errorf("select edge = %v, want %v", edge, want)
			}
		})
	}
}
