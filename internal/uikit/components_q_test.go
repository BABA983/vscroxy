package uikit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A dialog's panel carries the title the caller gives Modal, so assistive
// technology announces what opened, not just an unnamed dialog.
func TestModalNamesItsPanel(t *testing.T) {
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		Modal(c, &open, "Delete 3 requests?", func() {
			DialogColumn(c, DialogSmall).Children(func() {
				DialogTitle(c, "Delete 3 requests?")
				DialogFooter(c).Children(func() {
					SecondaryButton(c, "Cancel")
					DestructiveButton(c, "Delete")
				})
			})
		})
	}, 640, 400)
	tt.Frame()
	for _, a := range tt.AccessAll() {
		if a.Role == ui.RoleDialog {
			if a.Name != "Delete 3 requests?" {
				t.Fatalf("dialog name = %q, want its title", a.Name)
			}
			return
		}
	}
	t.Fatal("the modal did not expose a dialog")
}

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

// DataList owns the list theme and the row marker: a selected row draws the
// marker at its leading edge without the caller doing either.
func TestDataListDrawsTheSelectionMarker(t *testing.T) {
	selected := 1
	state := ui.ListState{Selected: &selected}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		ui.Column(c).Fill().Background(Colors(c.Theme()).Canvas).Children(func() {
			DataListIndexed(c, &state, 2, func(i int) {
				ui.Text(c, []string{"First", "Second"}[i]).Height(HeightControl).Padding(0, 8)
			}).Height(HeightControl * 2)
		})
	}, 320, 120)
	tt.Frame()
	row, ok := tt.Find("Second")
	if !ok {
		t.Fatal("the selected row is missing")
	}
	img := tt.Image()
	got := img.RGBAAt(int(row.X)+1, int(row.Y+row.H/2))
	want := Colors(Theme(false)).TextFaint
	if got.R < want.R-2 || got.R > want.R+2 || got.G < want.G-2 || got.G > want.G+2 || got.B < want.B-2 || got.B > want.B+2 {
		t.Fatalf("marker pixel = %v, want the unfocused marker %v", got, want)
	}
}
