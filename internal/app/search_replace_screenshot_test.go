package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestSearchReplaceScreenshots renders the Search view's inline replacement
// and the shared bulk-edit preview at 2x in both themes, through Tester.
// Set VSCROXY_SEARCH_REPLACE_SCREENSHOTS to an absolute output directory.
func TestSearchReplaceScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_SEARCH_REPLACE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_SEARCH_REPLACE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		for _, view := range []string{"results", "preview"} {
			t.Run(appearance+"-"+view, func(t *testing.T) {
				a, _ := searchReplaceFixture(t)
				a.appearance = appearance
				a.sidebarMode = sidebarSearch
				a.flows = []flow{{id: 9, method: "GET", url: "https://api.test/token", reqBody: "token"}}
				prepareSearch(t, a, "token", "scope")
				a.searchAll.replace.open = true
				if view == "preview" {
					a.openSearchReplace()
				}
				tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				if view == "results" && !tt.HasText(locales.ReplaceWith()) {
					t.Fatalf("replace field is missing: %v", tt.Texts())
				}
				if view == "preview" && !tt.HasText(locales.ReplaceAll()) {
					t.Fatalf("bulk-edit preview is missing: %v", tt.Texts())
				}
				path := filepath.Join(dir, "search-replace-"+view+"-"+appearance+".png")
				writeFeatureScreenshot(t, dir, filepath.Base(path), tt.Image())
			})
		}
	}
}
