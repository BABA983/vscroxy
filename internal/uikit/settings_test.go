package uikit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A settings row reads left to right: its label holds a fixed column, its
// control sits beside it, and a long hint wraps under the control instead of
// running the width of the page.
func TestSettingsRowKeepsTheLabelColumnAndWrapsTheHint(t *testing.T) {
	hint := strings.TrimSpace(strings.Repeat("A sentence that says what the setting changes. ", 6))
	tt := ui.NewTester(func(c *ui.Context) {
		SettingsRow(c, "System proxy", func() ui.Element {
			return ui.Row(c).Height(HeightControl).Children(func() {
				ui.Text(c, "Control")
			})
		}, hint)
	}, 640, 240)
	label, ok := tt.Find("System proxy")
	if !ok {
		t.Fatal("the row's label is missing")
	}
	control, ok := tt.Find("Control")
	if !ok {
		t.Fatal("the row's control is missing")
	}
	text, ok := tt.Find(hint)
	if !ok {
		t.Fatalf("the row's hint is missing; texts=%q", tt.Texts())
	}
	if want := Scaled(112); label.W != want {
		t.Fatalf("label width = %.0f, want %.0f", label.W, want)
	}
	if label.X >= control.X {
		t.Fatalf("label at x=%.0f, control at x=%.0f: the control is not to its right", label.X, control.X)
	}
	if text.Y < control.Y+control.H-1 {
		t.Fatalf("hint at y=%.0f, control ends at %.0f: the hint is not below it", text.Y, control.Y+control.H)
	}
	if text.X < control.X-1 {
		t.Fatalf("hint at x=%.0f, control at x=%.0f: the hint does not start under the control", text.X, control.X)
	}
	if text.W > Scaled(400)+1 {
		t.Fatalf("hint width = %.0f, want at most %.0f", text.W, Scaled(400))
	}
}

// A single-line control's label sits on the control's line, not above it.
func TestSettingsRowCentersTheLabelOnASingleLineControl(t *testing.T) {
	var value string
	tt := ui.NewTester(func(c *ui.Context) {
		SettingsRow(c, "Port", func() ui.Element {
			return Input(c, &value).Label("Proxy port").Width(100)
		}, "")
	}, 640, 120)
	label, ok := tt.Find("Port")
	if !ok {
		t.Fatal("the row's label is missing")
	}
	control, ok := tt.Find("Proxy port")
	if !ok {
		t.Fatal("the row's control is missing")
	}
	dy := (label.Y + label.H/2) - (control.Y + control.H/2)
	if dy < 0 {
		dy = -dy
	}
	if dy > 1 {
		t.Fatalf("label center %.1f, control center %.1f: they differ by %.1f", label.Y+label.H/2, control.Y+control.H/2, dy)
	}
}
