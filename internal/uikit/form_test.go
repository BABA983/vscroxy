package uikit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// FormRow binds the label, description and error to the control MyGo sees,
// and an invalid input says so to assistive technology.
func TestFormRowBindsDescriptionAndError(t *testing.T) {
	value := "70000"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		FormRow(c, "Port", "Blank is automatic; 1024–65535.", "Port must be 1024–65535.", func() {
			InputState(c, &value, true)
		})
	}, 480, 160)
	tt.Frame()
	for _, a := range tt.AccessAll() {
		if a.Name != "Port" || !a.Focusable {
			continue
		}
		if !a.Invalid {
			t.Fatal("the field's control is not invalid")
		}
		if !strings.Contains(a.Description, "1024–65535") || !strings.Contains(a.Description, "must be") {
			t.Fatalf("description = %q, want the hint and the error", a.Description)
		}
		return
	}
	t.Fatal("the field's control is missing")
}

// FormRowTall draws its error once, under the control: the error is the
// control's message for assistive technology, not a second line of text.
func TestFormRowTallDrawsItsErrorOnce(t *testing.T) {
	const message = "Syntax error on line 3."
	value := "{"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		FormRowTall(c, "Script", "", message, func() ui.Element {
			return TextArea(c, &value)
		})
	}, 480, 200)
	tt.Frame()
	drawn := 0
	for _, s := range tt.Texts() {
		if s == message {
			drawn++
		}
	}
	if drawn != 1 {
		t.Fatalf("the error is drawn %d times, want once; texts=%q", drawn, tt.Texts())
	}
}
