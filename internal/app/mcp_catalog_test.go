package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The catalog follows the request's Tools/Resources/Prompts tab: it has no
// second segmented navigation of its own.
func TestMCPCatalogFollowsTheRequestTab(t *testing.T) {
	a := workspaceApp()
	galleryMCP(a)
	u := a.requestDocument.mcpState()
	u.responseTab = 2

	u.tab = mcpTabResources
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Welcome") || tt.HasText("Echo") {
		t.Fatalf("resources tab did not show the resources catalog: %v", tt.Texts())
	}

	u.tab = mcpTabTools
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Echo") || tt.HasText("Welcome") {
		t.Fatalf("tools tab did not show the tools catalog: %v", tt.Texts())
	}
}
