package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func compareScreenshotApp(t *testing.T) *app {
	t.Helper()
	a := workspaceApp()
	a.page = pageLive
	a.applyFilter()
	a.flows[0].reqHeaders = "Content-Type: application/json\nX-Trace: one"
	a.flows[0].resHeaders = "X-Trace: one\nContent-Type: application/json"
	a.flows[0].resBody = `{"z":1,"items":[3,1,2],"nest":{"b":2,"a":1}}`
	a.flows[1].reqHeaders = "X-Trace: two\nContent-Type: application/json"
	a.flows[1].resHeaders = "Content-Type: application/json\nX-Trace: two"
	a.flows[1].resBody = `{"items":[1,2,3],"nest":{"a":1,"b":2},"z":9}`
	a.flows[1].status = 500
	a.chosen.Add(a.flows[0].id)
	a.chosen.Add(a.flows[1].id)
	return a
}

// TestCompareScreenshots renders the two-row Compare flow at 2x in both
// themes. Set VSCROXY_COMPARE_SCREENSHOTS to an output directory to export.
func TestCompareScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_COMPARE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_COMPARE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := compareScreenshotApp(t)
			theme := uikit.Theme(dark)
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				a.view(c)
			}, 1300, 820)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if err := tt.Click(locales.Compare()); err != nil {
				t.Fatal(err)
			}
			waitAppSignal(t, a, func() bool { return a.compare.ready }, func() { tt.Frame() },
				func() string { return "the comparison worker did not finish" })
			tt.Frame()
			if !tt.HasText(locales.CompareExchanges()) || !a.compare.open {
				t.Fatalf("compare view did not open: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "compare-"+appearance+".png", tt.Image())
		})
	}
}
