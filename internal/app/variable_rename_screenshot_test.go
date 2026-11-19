package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestVariableRenameScreenshots captures the bulk-edit preview and the
// References view at 2x in both themes, through the same Tester the other
// screenshot tests use. Set VSCROXY_VARIABLE_SCREENSHOTS to an output folder.
func TestVariableRenameScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_VARIABLE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_VARIABLE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		for _, view := range []string{"preview", "references"} {
			t.Run(appearance+"-"+view, func(t *testing.T) {
				a, _ := variableRenameFixture(t)
				a.appearance = appearance
				if view == "preview" {
					a.openVariableRename(target)
					a.variableRename.input = "token_renamed"
					a.prepareVariableRename()
				} else {
					a.openVariableReferences(target)
				}
				tt := ui.NewTester(a.view, 1440, 900)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				if view == "preview" && !tt.HasText(locales.ApplyRename()) {
					t.Fatalf("no rename preview: %v", tt.Texts())
				}
				if view == "references" && !tt.HasText(locales.FindAllReferences()) {
					t.Fatalf("no references view: %v", tt.Texts())
				}
				path := filepath.Join(dir, "variable-"+view+"-"+appearance+".png")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err := png.Encode(file, tt.Image()); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", path)
			})
		}
	}
}
