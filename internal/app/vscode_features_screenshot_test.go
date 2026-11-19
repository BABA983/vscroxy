package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// TestVSCodeFeaturesScreenshots uses the repository's opt-in Tester/png
// workflow. Set VSCROXY_FEATURES_SCREENSHOTS to an absolute output directory
// to capture quick-access prefixes and the native View menu's navigation
// entries in both themes and languages, including zh-Hans.
func TestVSCodeFeaturesScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_FEATURES_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_FEATURES_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	capture := func(t *testing.T, suffix string, scenes []struct{ name, query, want string }) {
		t.Helper()
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			for _, scene := range scenes {
				t.Run(appearance+"-"+scene.name, func(t *testing.T) {
					a := paletteTestApp()
					a.appearance = appearance
					tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
					tt.SetScale(2)
					tt.Key(ui.Cmd, ui.KeyK)
					tt.Type(scene.query)
					tt.Frame()
					if !tt.HasText(scene.want) {
						t.Fatalf("missing %q in palette: %v", scene.want, tt.Texts())
					}
					writeFeatureScreenshot(t, dir, "palette-"+scene.name+suffix+"-"+appearance+".png", tt.Image())
				})
			}
			t.Run(appearance+"-view-menu", func(t *testing.T) {
				a := workspaceApp()
				menu := a.menu()
				var view []*mygo.MenuItem
				for _, top := range menu.Items() {
					if top.Label == locales.View() {
						view = top.Submenu
					}
				}
				if len(view) == 0 {
					t.Fatal("the menu has no View submenu")
				}
				var back, forward *mygo.MenuItem
				for _, item := range view {
					if item.ID == "navigation.back" {
						back = item
					}
					if item.ID == "navigation.forward" {
						forward = item
					}
				}
				if back == nil || forward == nil || back.Label != locales.GoBack() || forward.Label != locales.GoForward() {
					t.Fatalf("navigation menu entries = %+v / %+v", back, forward)
				}
				// The native menu bar is outside the window image, so render
				// its View entries in a tester panel using the real menu.
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(uikit.Theme(dark).WithPreferences(c.Preferences()))
					ui.Column(c).Fill().Padding(24).Gap(8).Children(func() {
						uikit.Heading(c, locales.View())
						ui.Column(c).Width(340).Background(c.Theme().Surface).Border(1, uikit.Colors(c.Theme()).Border).Radius(uikit.RadiusControl).Children(func() {
							for _, item := range view {
								if item.Type == mygo.MenuItemSeparator {
									ui.Divider(c)
									continue
								}
								ui.Row(c).Height(34).Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
									color := c.Theme().Text
									if item.ID == "navigation.back" || item.ID == "navigation.forward" {
										color = uikit.Colors(c.Theme()).Primary
									}
									uikit.Body(c, item.Label).TextColor(color).Grow(1)
									if item.Accelerator != "" {
										uikit.Kbd(c, item.Accelerator)
									}
								})
							}
						})
					})
				}, 620, 860)
				tt.SetScale(2)
				tt.Frame()
				if !tt.HasText(locales.GoBack()) || !tt.HasText(locales.GoForward()) {
					t.Fatalf("menu panel missing navigation: %v", tt.Texts())
				}
				writeFeatureScreenshot(t, dir, "view-menu"+suffix+"-"+appearance+".png", tt.Image())
			})
		}
	}
	scenes := []struct{ name, query, want string }{
		{"prefix-help", "?", locales.QuickAccessEnvironmentsAndVariables()},
		{"prefix-commands", ">layout", locales.LayoutSideBySide()},
		{"prefix-requests", "#create", "Create order"},
		{"prefix-environments", "$base_url", "base_url"},
	}
	t.Run("en", func(t *testing.T) { capture(t, "", scenes) })
	t.Run("zh-Hans", func(t *testing.T) {
		i18n.SetLocaleFunc(func() string { return "zh-Hans" })
		t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
		// The command registry keeps the language it was first built in;
		// the help rows and the native menu are read at render time.
		capture(t, "-zh-Hans", []struct{ name, query, want string }{
			{"prefix-help", "?", locales.QuickAccessEnvironmentsAndVariables()},
		})
	})
}

func writeFeatureScreenshot(t *testing.T, dir, name string, img *image.RGBA) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
