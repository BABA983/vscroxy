package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/internal/clientsetup"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestClientSetupAndroidScreenshots exports the Android section at 2x in both
// themes. Set VSCROXY_CLIENT_SETUP_SCREENSHOTS to an output directory to export.
func TestClientSetupAndroidScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_CLIENT_SETUP_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_CLIENT_SETUP_SCREENSHOTS to an output directory")
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
			a := oneClickApp(t)
			a.clientUI.target = clientTargetAndroid
			a.clientUI.androidLoaded = true
			a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
			a.clientUI.androidSerial = "emulator-5554"
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(uikit.Theme(dark))
				a.view(c)
			}, 1100, 760)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText(locales.InstallingASystemCaRebootBehavior()) {
				t.Fatalf("new CA note is missing: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "client-setup-android-"+appearance+".png", tt.Image())
		})
	}
}
