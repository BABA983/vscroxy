package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// The AI agents category owns the moved local automation switch and is found
// by the same words it used in General.
func TestAgentsSettingsOwnsLocalControl(t *testing.T) {
	a := settingsTestApp()
	a.agents.skillHome = t.TempDir()
	a.settingsOpen = true
	hits := settingsFind(a, "local automation")
	if len(hits) == 0 || hits[0].entry.spec.id != "general.localControl" || hits[0].entry.spec.section != settingsAgents {
		t.Fatalf("local automation search = %v", hitIDs(hits))
	}
	a.revealSetting("general.localControl")
	if a.settingsTab != 7 || a.settingsChoice != "7" {
		t.Fatalf("revealed tab %d, choice %q", a.settingsTab, a.settingsChoice)
	}
	tt := ui.NewTester(a.settingsView, 1080, 680)
	tt.Frame()
	tt.Frame()
	for _, want := range []string{locales.AiAgents(), locales.AllowAgentsToControl(), locales.McpServer(), locales.ClaudeCode(), locales.CaptureAgentCommands()} {
		if !tt.HasText(want) {
			t.Fatalf("AI agents page is missing %q: %v", want, tt.Texts())
		}
	}
	if a.settingsQuery != "" {
		t.Fatalf("the page kept the search query %q", a.settingsQuery)
	}
}

// The page's tabs and checkboxes shape the generated snippet; they are not
// saved settings.
func TestAgentsSettingsControlsShapeTheSnippet(t *testing.T) {
	a := settingsTestApp()
	a.agents.skillHome = t.TempDir()
	a.settingsOpen, a.settingsTab, a.settingsChoice = true, 7, "7"
	tt := ui.NewTester(a.settingsView, 1080, 1000)
	tt.Frame()
	tt.Frame()
	if err := tt.Click(locales.Cursor()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.agents.client != int(mcpCursor) || !tt.HasText(`"mcpServers"`) || !tt.HasText(`"type": "stdio"`) {
		t.Fatalf("Cursor tab did not select its JSON: client=%d texts=%v", a.agents.client, tt.Texts())
	}
	if err := tt.Click(locales.AllowChanges()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.agents.allowWrite || !tt.HasText(`"--allow-write"`) {
		t.Fatalf("Allow changes did not reach the snippet: %v", tt.Texts())
	}
	if err := tt.Click(locales.AllowRawAgentData()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.agents.allowSensitive || !tt.HasText(`"--allow-sensitive"`) {
		t.Fatalf("Allow raw data did not reach the snippet: %v", tt.Texts())
	}
	if a.saved.LocalControl {
		t.Fatal("a snippet-only choice was persisted")
	}
}

// TestMCPClientSnippet pins each client's own registration form, the shell
// quoting of a path with a space and an apostrophe, and both capability flags.
func TestMCPClientSnippet(t *testing.T) {
	const unixPath = "/Users/O'Brien/Library/Application Support/vscroxy-mcp"
	const unixWord = `'/Users/O'\''Brien/Library/Application Support/vscroxy-mcp'`
	const windowsPath = `C:\Program Files\vscroxy\vscroxy-mcp.exe`
	for _, tc := range []struct {
		name      string
		client    mcpClientKind
		path      string
		write     bool
		sensitive bool
		windows   bool
		want      string
	}{
		{
			name: "claude/no-flags", client: mcpClaudeCode, path: unixPath,
			want: "claude mcp add vscroxy --transport stdio --scope user -- " + unixWord,
		},
		{
			name: "claude/write", client: mcpClaudeCode, path: unixPath, write: true,
			want: "claude mcp add vscroxy --transport stdio --scope user -- " + unixWord + " --allow-write",
		},
		{
			name: "claude/sensitive", client: mcpClaudeCode, path: unixPath, sensitive: true,
			want: "claude mcp add vscroxy --transport stdio --scope user -- " + unixWord + " --allow-sensitive",
		},
		{
			name: "claude/both", client: mcpClaudeCode, path: unixPath, write: true, sensitive: true,
			want: "claude mcp add vscroxy --transport stdio --scope user -- " + unixWord + " --allow-write --allow-sensitive",
		},
		{
			name: "codex/both", client: mcpCodex, path: unixPath, write: true, sensitive: true,
			want: "codex mcp add vscroxy -- " + unixWord + " --allow-write --allow-sensitive",
		},
		{
			name: "cursor/no-flags", client: mcpCursor, path: unixPath,
			want: "{\n" +
				`  "mcpServers": {` + "\n" +
				`    "vscroxy": {` + "\n" +
				`      "type": "stdio",` + "\n" +
				`      "command": "/Users/O'Brien/Library/Application Support/vscroxy-mcp",` + "\n" +
				`      "args": []` + "\n" +
				"    }\n" +
				"  }\n" +
				"}",
		},
		{
			name: "cursor/both", client: mcpCursor, path: unixPath, write: true, sensitive: true,
			want: "{\n" +
				`  "mcpServers": {` + "\n" +
				`    "vscroxy": {` + "\n" +
				`      "type": "stdio",` + "\n" +
				`      "command": "/Users/O'Brien/Library/Application Support/vscroxy-mcp",` + "\n" +
				`      "args": [` + "\n" +
				`        "--allow-write",` + "\n" +
				`        "--allow-sensitive"` + "\n" +
				"      ]\n" +
				"    }\n" +
				"  }\n" +
				"}",
		},
		{
			name: "other/both", client: mcpOtherClient, path: unixPath, write: true, sensitive: true,
			want: "{\n" +
				`  "mcpServers": {` + "\n" +
				`    "vscroxy": {` + "\n" +
				`      "command": "/Users/O'Brien/Library/Application Support/vscroxy-mcp",` + "\n" +
				`      "args": [` + "\n" +
				`        "--allow-write",` + "\n" +
				`        "--allow-sensitive"` + "\n" +
				"      ]\n" +
				"    }\n" +
				"  }\n" +
				"}",
		},
		{
			name: "windows/claude-both", client: mcpClaudeCode, path: windowsPath, write: true, sensitive: true, windows: true,
			want: `claude mcp add vscroxy --transport stdio --scope user -- "C:\Program Files\vscroxy\vscroxy-mcp.exe" --allow-write --allow-sensitive`,
		},
		{
			name: "windows/cursor-sensitive", client: mcpCursor, path: windowsPath, sensitive: true, windows: true,
			want: "{\n" +
				`  "mcpServers": {` + "\n" +
				`    "vscroxy": {` + "\n" +
				`      "type": "stdio",` + "\n" +
				`      "command": "C:\\Program Files\\vscroxy\\vscroxy-mcp.exe",` + "\n" +
				`      "args": [` + "\n" +
				`        "--allow-sensitive"` + "\n" +
				"      ]\n" +
				"    }\n" +
				"  }\n" +
				"}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpClientSnippet(tc.client, tc.path, tc.write, tc.sensitive, tc.windows); got != tc.want {
				t.Fatalf("snippet:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestCLICommands covers the terminal block's two commands: the CLI's quoted
// absolute path, PowerShell on Windows, and the placeholder used when the
// lookup fails.
func TestCLICommands(t *testing.T) {
	const unixPath = "/Users/O'Brien/Library/Application Support/vscroxy-cli"
	const unixWord = `'/Users/O'\''Brien/Library/Application Support/vscroxy-cli'`
	const windowsPath = `C:\Program Files\vscroxy\vscroxy-cli.exe`
	for _, tc := range []struct {
		name    string
		path    string
		windows bool
		want    string
	}{
		{
			name: "posix", path: unixPath,
			want: "eval \"$(" + unixWord + " env)\"\n" + unixWord + " exec -- npm test",
		},
		{
			name: "windows", path: windowsPath, windows: true,
			want: `& "C:\Program Files\vscroxy\vscroxy-cli.exe" env --shell powershell | Invoke-Expression` + "\n" +
				`& "C:\Program Files\vscroxy\vscroxy-cli.exe" exec -- npm test`,
		},
		{
			name: "posix placeholder", path: cliPlaceholderPath(false),
			want: "eval \"$('/path/to/vscroxy-cli' env)\"\n'/path/to/vscroxy-cli' exec -- npm test",
		},
		{
			name: "windows placeholder", path: cliPlaceholderPath(true), windows: true,
			want: `& "C:\path\to\vscroxy-cli.exe" env --shell powershell | Invoke-Expression` + "\n" +
				`& "C:\path\to\vscroxy-cli.exe" exec -- npm test`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cliCommands(tc.path, tc.windows); got != tc.want {
				t.Fatalf("commands:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// The generated POSIX line must survive a path with a space and an
// apostrophe when the shell parses it, not only when the string is compared.
func TestCLICommandsRunInPOSIXShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	dir := filepath.Join(t.TempDir(), "O'Brien dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "vscroxy-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'X=ok\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(cliCommands(path, false), "\n")
	cmd := exec.Command("/bin/sh", "-c", line+"\nprintf '%s' \"$X\"")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if string(out) != "ok" {
		t.Fatalf("shell set X=%q, want ok; line: %s", out, line)
	}
}

func TestMCPBinaryCandidates(t *testing.T) {
	got := mcpBinaryCandidates("/Applications/vscroxy.app/Contents/MacOS/vscroxy", "/repo/resources", "darwin")
	want := []string{
		"/Applications/vscroxy.app/Contents/MacOS/vscroxy-mcp",
		"/Applications/vscroxy.app/Contents/Resources/vscroxy-mcp",
		"/Applications/vscroxy.app/Contents/Resources/bin/vscroxy-mcp",
		"/repo/resources/vscroxy-mcp",
		"/repo/resources/bin/vscroxy-mcp",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
	if name := mcpExecutableName("windows"); name != "vscroxy-mcp.exe" {
		t.Fatalf("Windows helper = %q", name)
	}
	cli := helperBinaryCandidates("/Applications/vscroxy.app/Contents/MacOS/vscroxy", "/repo/resources", "darwin", cliExecutableName("darwin"))
	wantCLI := []string{
		"/Applications/vscroxy.app/Contents/MacOS/vscroxy-cli",
		"/Applications/vscroxy.app/Contents/Resources/vscroxy-cli",
		"/Applications/vscroxy.app/Contents/Resources/bin/vscroxy-cli",
		"/repo/resources/vscroxy-cli",
		"/repo/resources/bin/vscroxy-cli",
	}
	if len(cli) != len(wantCLI) {
		t.Fatalf("CLI candidates = %v, want %v", cli, wantCLI)
	}
	for i := range wantCLI {
		if cli[i] != wantCLI[i] {
			t.Fatalf("CLI candidates = %v, want %v", cli, wantCLI)
		}
	}
	if name := cliExecutableName("windows"); name != "vscroxy-cli.exe" {
		t.Fatalf("Windows CLI = %q", name)
	}
}

// The installed file is re-read when the page is shown, and installing from
// the row refreshes the state without another visit.
func TestAgentSkillRowInstallsAndRefreshes(t *testing.T) {
	home := t.TempDir()
	a := settingsTestApp()
	a.agents.skillHome = home
	a.settingsOpen, a.settingsTab, a.settingsChoice = true, 7, "7"
	path, ok := agentSkillPath(mcpClaudeCode, home)
	if !ok {
		t.Fatal("Claude Code has no install path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(skillDocument("0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.settingsView, 1080, 680)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	visible := false
	for i := 0; i < 30; i++ {
		if r, ok := tt.Find(locales.AgentSkillUpdate()); ok && r.Y >= 0 && r.Y+r.H <= 620 {
			visible = true
			break
		}
		tt.Scroll(540, 340, 0, 200)
		tt.Frame()
	}
	if !visible {
		t.Fatalf("Update is not visible: %v", tt.Texts())
	}
	if err := tt.Click(locales.AgentSkillUpdate()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if status := readAgentSkill(path, version.Number); status.state != skillCurrent {
		t.Fatalf("installed state = %+v; want current", status)
	}
	if !tt.HasText(locales.AgentSkillInstalled("")) {
		t.Fatalf("installed row did not refresh: %v", tt.Texts())
	}

	// A file changed outside the app is picked up when the page is shown again.
	if err := os.WriteFile(path, []byte(skillDocument("9.9.9")), 0o600); err != nil {
		t.Fatal(err)
	}
	a.openSettings(7)
	tt.Frame()
	tt.Frame()
	if !tt.HasText(locales.AgentSkillOlder()) {
		t.Fatalf("reopened page kept the old skill state: %v", tt.Texts())
	}
}
