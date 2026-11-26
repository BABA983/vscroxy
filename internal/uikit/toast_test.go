package uikit

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A toast sits above the footer, not over the notification bell and the
// tools that stay reachable there.
func TestToastsSitAboveTheFooter(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		c.AddToast(ui.Toast{ID: "copied", Title: "Copied as cURL"})
		ui.Column(c).Fill().Children(func() {
			ui.Spacer(c)
			ui.Row(c).Height(HeightFooter).Label("Footer")
		})
		Toasts(c)
	}, 800, 600)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Frame()
	toast, ok := tt.Find("Copied as cURL")
	if !ok {
		t.Fatalf("toast is not drawn: %q", tt.Texts())
	}
	footer, ok := tt.Find("Footer")
	if !ok {
		t.Fatalf("footer is not drawn: %q", tt.Texts())
	}
	if gap := footer.Y - (toast.Y + toast.H); gap < 8 {
		t.Fatalf("toast bottom %.0f is %.0f from the footer top %.0f, want a spacing token", toast.Y+toast.H, gap, footer.Y)
	}
}
