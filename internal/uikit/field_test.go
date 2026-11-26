package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNumberFieldShowsItsUnit(t *testing.T) {
	text := "60"
	value := 60.0
	tt := ui.NewTester(func(c *ui.Context) {
		NumberField(c, &text, &value, 0, 65535, "s", "Keep alive", "", nil)
	}, 320, 80)
	tt.Frame()
	if !tt.HasText("s") || !tt.HasText("Keep alive") {
		t.Fatalf("NumberField did not show its value and unit: %q", tt.Texts())
	}
}
