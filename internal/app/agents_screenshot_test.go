package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestAgentsSettingsScreenshots exports the AI agents page in both themes,
// with the Codex client selected so the TOML alternative is visible and the pi
// tab is on screen. Set VSCROXY_AGENTS_SCREENSHOTS to an output directory.
func TestAgentsSettingsScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_AGENTS_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_AGENTS_SCREENSHOTS to an output directory")
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
			a := workspaceApp()
			a.appearance = appearance
			snapshotSettings(a, int(settingsAgents))
			a.agents.skillHome = t.TempDir()
			a.agents.client = int(mcpCodex)
			a.agents.allowWrite = true
			a.agents.mcpChecked, a.agents.mcpFound = true, true
			a.agents.mcpPath = "/Applications/vscroxy.app/Contents/Resources/bin/vscroxy-mcp"
			a.agents.cliChecked, a.agents.cliFound = true, true
			a.agents.cliPath = "/Applications/vscroxy.app/Contents/Resources/bin/vscroxy-cli"
			tt := ui.NewTester(a.view, 1100, 1100)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			tt.Scroll(550, 550, 0, 330)
			tt.Frame()
			for _, want := range []string{
				locales.AiAgents(), locales.McpClient(), locales.Pi(), locales.Codex(),
				locales.AllowChanges(), locales.FullAccess(), locales.AgentCodexTomlHint(),
				"[mcp_servers.vscroxy]",
			} {
				if !tt.HasText(want) {
					t.Fatalf("AI agents screenshot is missing %q", want)
				}
			}
			writeFeatureScreenshot(t, dir, "agents-"+appearance+".png", tt.Image())
		})
	}
}
