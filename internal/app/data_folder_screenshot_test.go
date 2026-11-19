package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestDataFolderScreenshots exports 2x Tester captures of the Settings line
// that names the active data root in both themes. Set
// VSCROXY_DATA_FOLDER_SCREENSHOTS to an output directory.
func TestDataFolderScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_DATA_FOLDER_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_DATA_FOLDER_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	a := &app{dataDir: filepath.Join(home, "portable-data")}

	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			th := uikit.Theme(dark)
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(th)
				ui.Column(c).Fill().Background(uikit.Colors(th).Window).Padding(24).Children(func() {
					uikit.SettingsCard(c, locales.AboutVscroxy(), func() {
						a.dataFolderRow(c, th)
					})
				})
			}, 900, 240)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			for _, text := range []string{locales.DataFolder(), shortenHome(a.dataDir), locales.DataFolderHint()} {
				if !tt.HasText(text) {
					t.Fatalf("missing %q in %v", text, tt.Texts())
				}
			}
			writeFeatureScreenshot(t, dir, "data-folder-"+appearance+".png", tt.Image())
		})
	}
}
