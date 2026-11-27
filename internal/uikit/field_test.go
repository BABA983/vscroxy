package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNumberFieldShowsItsUnit(t *testing.T) {
	text := "60"
	value := 60.0
	tt := ui.NewTester(func(c *ui.Context) {
		NumberField(c, &text, &value, 0, 65535, 1, "s", "Keep alive", "", nil)
	}, 320, 80)
	tt.Frame()
	if !tt.HasText("s") || !tt.HasText("Keep alive") {
		t.Fatalf("NumberField did not show its value and unit: %q", tt.Texts())
	}
}

func TestNumberFieldStepsWithArrowKeys(t *testing.T) {
	text := "5"
	value := 5.0
	tt := ui.NewTester(func(c *ui.Context) {
		NumberField(c, &text, &value, 1, 6, 2, "hops", "Redirects", "", nil)
	}, 320, 80)
	tt.Frame()
	if err := tt.Click("Redirects"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if value != 6 || text != "6" {
		t.Fatalf("Up on 5 by 2 = %v %q, want 6 at the upper bound", value, text)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if value != 2 || text != "2" {
		t.Fatalf("two Downs on 6 by 2 = %v %q, want 2", value, text)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if value != 1 || text != "1" {
		t.Fatalf("Down below the lower bound = %v %q, want 1", value, text)
	}
}

func TestNumberFieldKeepsOutOfRangeOut(t *testing.T) {
	text := "3"
	value := 3.0
	tt := ui.NewTester(func(c *ui.Context) {
		NumberField(c, &text, &value, 1, 6, 1, "hops", "Redirects", "", nil)
	}, 320, 80)
	tt.Frame()
	if err := tt.Click("Redirects"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("9")
	tt.Frame()
	if value != 3 {
		t.Fatalf("an out-of-range value was applied: %v", value)
	}
	if info, ok := tt.Access("Redirects"); !ok || !info.Invalid {
		t.Fatalf("an out-of-range value is not invalid: %+v, shown %t", info, ok)
	} else if info.Description != "1–6" {
		t.Fatalf("the invalid field's tooltip = %q, want its allowed range", info.Description)
	}
}
