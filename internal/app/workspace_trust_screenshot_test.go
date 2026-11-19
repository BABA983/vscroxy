package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestWorkspaceTrustScreenshots captures the prompt and the Settings trust
// list in both themes. Set VSCROXY_WORKSPACE_TRUST_SCREENSHOTS to a directory.
func TestWorkspaceTrustScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_WORKSPACE_TRUST_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_WORKSPACE_TRUST_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	other := t.TempDir()
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run("prompt-"+appearance, func(t *testing.T) {
			a := workspaceApp()
			key := trustPath(project)
			a.trustPending = map[string]chan struct{}{key: make(chan struct{})}
			a.trustAskDir, a.trustAskOpen = key, true
			tt := ui.NewTester(a.view, 1180, 760)
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText(locales.WorkspaceTrustPrompt()) || !tt.HasText(key) {
				t.Fatalf("prompt is missing its title or project: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "workspace-trust-prompt-"+appearance+".png", tt.Image())
		})
		t.Run("list-"+appearance, func(t *testing.T) {
			a := workspaceApp()
			a.trustedProjects, a.untrustedProjects = []string{trustPath(project)}, []string{trustPath(other)}
			a.trustManageOpen = true
			tt := ui.NewTester(a.view, 1180, 760)
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			for _, want := range []string{locales.TrustedProjects(), locales.UntrustedProjects(), trustPath(project), trustPath(other)} {
				if !tt.HasText(want) {
					t.Fatalf("trust list is missing %q: %v", want, tt.Texts())
				}
			}
			writeFeatureScreenshot(t, dir, "workspace-trust-list-"+appearance+".png", tt.Image())
		})
	}
}
