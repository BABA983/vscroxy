package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// DataList owns the list theme and the row marker: a selected row draws the
// marker at its leading edge without the caller doing either.
func TestDataListDrawsTheSelectionMarker(t *testing.T) {
	selected := 1
	state := ui.ListState{Selected: &selected}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		ui.Column(c).Fill().Background(Colors(c.Theme()).Background).Children(func() {
			DataListIndexed(c, &state, 2, func(i int) {
				ui.Text(c, []string{"First", "Second"}[i]).Height(HeightRow).Padding(0, 8)
			}).Height(HeightRow * 2)
		})
	}, 320, 120)
	tt.Frame()
	row, ok := tt.Find("Second")
	if !ok {
		t.Fatal("the selected row is missing")
	}
	img := tt.Image()
	got := img.RGBAAt(int(row.X)+1, int(row.Y+row.H/2))
	want := Colors(Theme(false)).Faint
	if got.R < want.R-2 || got.R > want.R+2 || got.G < want.G-2 || got.G > want.G+2 || got.B < want.B-2 || got.B > want.B+2 {
		t.Fatalf("marker pixel = %v, want the unfocused marker %v", got, want)
	}
}
