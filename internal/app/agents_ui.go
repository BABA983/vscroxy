package app

// The AI agents page connects coding agents to the running desktop: the local
// automation endpoint, the MCP stdio server, and a terminal wrapper that sends
// an agent's commands through the capture proxy.

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/clientsetup"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/update"
	"github.com/baba983/vscroxy/locales"
	"github.com/baba983/vscroxy/skills"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// agentsPanel is the page's own state. The two capability switches only shape
// the generated snippet; they are not settings and are never saved.
type agentsPanel struct {
	client         int
	allowWrite     bool
	allowSensitive bool
	// The helper lookups are cached so a drawing frame does not stat PATH
	// again; Check again repeats them.
	mcpPath    string
	mcpFound   bool
	mcpChecked bool
	cliPath    string
	cliFound   bool
	cliChecked bool
	// The installed skill is read once per page visit, client switch or
	// install action, never on every frame.
	skillChecked bool
	skillPath    string
	skill        agentSkillStatus
	// skillHome overrides the user home in tests; empty means os.UserHomeDir.
	skillHome string
	// The two code blocks' Copy buttons show their check from these.
	mcpFlash uikit.CopyFlash
	cliFlash uikit.CopyFlash
}

// mcpClientKind is one tab of the MCP client picker.
type mcpClientKind int

const (
	mcpClaudeCode mcpClientKind = iota
	mcpCodex
	mcpCursor
	mcpOtherClient
)

type mcpServerConfig struct {
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type mcpConfig struct {
	Servers map[string]mcpServerConfig `json:"mcpServers"`
}

func (a *app) agentsSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsCardPage(c, locales.AiAgents(), locales.AiAgentsHint(), func() {
		uikit.SettingsCard(c, locales.LocalAutomation(), func() {
			a.settingRow(c, "general.localControl", locales.AllowAgentsToControl(), func() ui.Element {
				on := a.controlRunning()
				toggle := uikit.SettingsSwitch(c, &on, locales.AllowAgentsToControl())
				if toggle.Changed() {
					a.setLocalControl(on)
				}
				return toggle
			}, locales.AllowAgentsToControlHint())
			settingsCardRow(c, locales.ControlStatus(), func() ui.Element {
				return uikit.Meta(c, a.controlStatusText()).SingleLine()
			}, "")
			if a.controlRunning() {
				uikit.SettingsCardBlock(c, locales.DiscoveryFile(), "", func() {
					uikit.Tip(c, uikit.Mono(c, shortenHome(a.controlPath)).Selectable().MaxLines(2), a.controlPath)
				})
			}
		})
		a.agentsMCPSettings(c, t)
		a.agentsTerminalSettings(c, t)
		ui.Row(c).Children(func() {
			if uikit.LinkButton(c, locales.LearnMore()).Clicked() {
				c.Services().OpenURL("https://github.com/" + update.Repository + "/blob/main/docs/automation.md#connect-an-ai-agent")
			}
		})
	})
}

// controlStatusText says whether the endpoint is listening and when an agent
// last called it after authenticating.
func (a *app) controlStatusText() string {
	state := locales.ControlEndpointOff()
	if a.controlRunning() {
		state = locales.ControlEndpointOn()
	}
	return state + " · " + lastUsedText(a.controlLastUsed)
}

func lastUsedText(at time.Time) string {
	if at.IsZero() {
		return locales.NotUsedYet()
	}
	d := time.Since(at)
	switch {
	case d < time.Minute:
		return locales.LastUsedJustNow()
	case d < time.Hour:
		return locales.LastUsedMinutes(int(d.Minutes()))
	case d < 48*time.Hour:
		return locales.LastUsedHours(int(d.Hours()))
	}
	return locales.LastUsedDays(int(d.Hours() / 24))
}

// mcpExecutable and cliExecutable remember one lookup per page visit; Check
// again repeats it after someone builds or copies the binary.
func (a *app) mcpExecutable() (string, bool) {
	if !a.agents.mcpChecked {
		a.agents.mcpPath, a.agents.mcpFound = locateMCPBinary()
		a.agents.mcpChecked = true
	}
	return a.agents.mcpPath, a.agents.mcpFound
}

func (a *app) cliExecutable() (string, bool) {
	if !a.agents.cliChecked {
		a.agents.cliPath, a.agents.cliFound = locateCLIBinary()
		a.agents.cliChecked = true
	}
	return a.agents.cliPath, a.agents.cliFound
}

func (a *app) agentsMCPSettings(c *ui.Context, t *ui.Theme) {
	path, found := a.mcpExecutable()
	uikit.SettingsCard(c, locales.McpServer(), func() {
		uikit.SettingsCardBlock(c, locales.McpExecutable(), "", func() {
			if found {
				uikit.Tip(c, uikit.Mono(c, path).Selectable().MaxLines(2), path)
			} else {
				uikit.Hint(c, locales.McpExecutableMissing())
				uikit.CodeBlock(c, "go build -o bin/vscroxy-mcp ./cmd/vscroxy-mcp", nil, nil)
			}
			if !found {
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
					if uikit.LinkButton(c, locales.CheckAgain()).Clicked() {
						a.agents.mcpChecked = false
					}
					if uikit.LinkButton(c, locales.DownloadRelease()).Clicked() {
						c.Services().OpenURL("https://github.com/" + update.Repository + "/releases")
					}
				})
			}
		})
		uikit.SettingsCardBlock(c, locales.McpClient(), "", func() {
			// Access first, then the client: the reader chooses what the
			// snippet may do before choosing where it goes.
			ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Hint(c, locales.AgentSnippetRestartHint())
				uikit.Checkbox(c, &a.agents.allowWrite, locales.AllowChanges())
				uikit.Hint(c, locales.AllowChangesHint())
				uikit.Checkbox(c, &a.agents.allowSensitive, locales.AllowRawAgentData())
				uikit.Hint(c, locales.AllowRawAgentDataHint())
			})
			labels := []string{locales.ClaudeCode(), locales.Codex(), locales.Cursor(), locales.OtherMcpClient()}
			selected := a.agents.client
			if selected < 0 || selected >= len(labels) {
				selected = 0
			}
			uikit.Segmented(c, &selected, labels...).Label(locales.McpClient())
			if selected != a.agents.client {
				a.agents.client = selected
				a.resetAgentSkill()
			}
			snippetPath := path
			if !found {
				snippetPath = mcpPlaceholderPath(runtime.GOOS == "windows")
			}
			snippet := mcpClientSnippet(mcpClientKind(a.agents.client), snippetPath, a.agents.allowWrite, a.agents.allowSensitive, runtime.GOOS == "windows")
			uikit.Hint(c, agentsSnippetHint(mcpClientKind(a.agents.client)))
			uikit.CodeBlock(c, snippet, func() {
				c.Services().WriteClipboard(snippet)
				a.done(locales.CopiedAgentSetup())
			}, &a.agents.mcpFlash)
		})
		a.agentSkillSettings(c)
	})
}

func agentsSnippetHint(kind mcpClientKind) string {
	switch kind {
	case mcpCursor:
		return locales.AgentCursorSnippetHint()
	case mcpOtherClient:
		return locales.AgentOtherSnippetHint()
	}
	return locales.AgentCommandSnippetHint()
}

// agentSkillSettings installs the bundled skill for the selected client, or
// copies it when there is no confirmed user skills directory.
func (a *app) agentSkillSettings(c *ui.Context) {
	status, installable := a.currentAgentSkill()
	copySkill := func() {
		c.Services().WriteClipboard(skills.Text())
		a.done(locales.AgentSkillCopied())
	}
	install := func() {
		if err := installAgentSkill(status.path); err != nil {
			a.fail(locales.AgentSkill(), err)
			return
		}
		a.resetAgentSkill()
		a.done(locales.AgentSkillInstalledNotice())
	}
	if !installable {
		uikit.SettingsCardRow(c, locales.AgentSkill(), "", func() {
			if uikit.SecondaryButton(c, locales.Copy()).Clicked() {
				copySkill()
			}
		})
		return
	}
	switch status.state {
	case skillMissing:
		uikit.SettingsCardRow(c, locales.AgentSkill(), "", func() {
			if uikit.SecondaryButton(c, locales.AgentSkillInstall()).Clicked() {
				install()
			}
		})
	case skillCurrent:
		uikit.SettingsCardRow(c, locales.AgentSkill(), locales.AgentSkillInstalled(shortenHome(status.path)), func() {
			if uikit.LinkButton(c, locales.Copy()).Clicked() {
				copySkill()
			}
		})
	case skillOlder:
		uikit.SettingsCardRow(c, locales.AgentSkill(), locales.AgentSkillOlder(), func() {
			if uikit.SecondaryButton(c, locales.AgentSkillUpdate()).Clicked() {
				install()
			}
		})
	case skillModified:
		uikit.SettingsCardRow(c, locales.AgentSkill(), locales.AgentSkillModified(), func() {
			if uikit.SecondaryButton(c, locales.AgentSkillReplace()).Clicked() {
				install()
			}
		})
	default:
		uikit.SettingsCardRow(c, locales.AgentSkill(), locales.AgentSkillUnreadable(), func() {
			if uikit.LinkButton(c, locales.Copy()).Clicked() {
				copySkill()
			}
		})
	}
}

func (a *app) agentsTerminalSettings(c *ui.Context, t *ui.Theme) {
	path, found := a.cliExecutable()
	windows := runtime.GOOS == "windows"
	uikit.SettingsCard(c, locales.CaptureAgentCommands(), func() {
		uikit.SettingsCardBlock(c, "", locales.CaptureAgentCommandsHint(), func() {
			if !found {
				uikit.Hint(c, locales.CliExecutableMissing())
				uikit.CodeBlock(c, "go build -o bin/vscroxy-cli ./cmd/vscroxy-cli", nil, nil)
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
					if uikit.LinkButton(c, locales.CheckAgain()).Clicked() {
						a.agents.cliChecked = false
					}
					if uikit.LinkButton(c, locales.DownloadRelease()).Clicked() {
						c.Services().OpenURL("https://github.com/" + update.Repository + "/releases")
					}
				})
				path = cliPlaceholderPath(windows)
			}
			commands := cliCommands(path, windows)
			uikit.Hint(c, locales.CaptureAgentCommandsShellHint())
			uikit.CodeBlock(c, commands, func() {
				c.Services().WriteClipboard(commands)
				a.done(locales.CopiedAgentSetup())
			}, &a.agents.cliFlash)
		})
	})
}

// mcpClientSnippet renders the client's own registration form. shell paths are
// quoted for the platform's shell and JSON is escaped by the encoder; windows
// selects the command line syntax, not the JSON escaping. It is pure so the
// tests cover every flag combination on both platforms.
func mcpClientSnippet(client mcpClientKind, path string, allowWrite, allowSensitive, windows bool) string {
	flags := make([]string, 0, 2)
	if allowWrite {
		flags = append(flags, "--allow-write")
	}
	if allowSensitive {
		flags = append(flags, "--allow-sensitive")
	}
	quote := clientsetup.POSIXQuote
	if windows {
		quote = clientsetup.WindowsQuote
	}
	command := quote(path)
	if len(flags) > 0 {
		command += " " + strings.Join(flags, " ")
	}
	switch client {
	case mcpClaudeCode:
		return "claude mcp add vscroxy --transport stdio --scope user --" + " " + command
	case mcpCodex:
		return "codex mcp add vscroxy --" + " " + command
	}
	server := mcpServerConfig{Command: path, Args: flags}
	if client == mcpCursor {
		server.Type = "stdio"
	}
	data, err := json.Marshal(mcpConfig{Servers: map[string]mcpServerConfig{"vscroxy": server}})
	if err != nil {
		return "{}"
	}
	value := jsontext.Value(data)
	if err := value.Indent(jsontext.WithIndent("  ")); err == nil {
		return string(value)
	}
	return string(data)
}

func mcpPlaceholderPath(windows bool) string {
	return helperPlaceholderPath("vscroxy-mcp", windows)
}

func cliPlaceholderPath(windows bool) string {
	return helperPlaceholderPath("vscroxy-cli", windows)
}

func helperPlaceholderPath(name string, windows bool) string {
	if windows {
		return `C:\path\to\` + name + `.exe`
	}
	return "/path/to/" + name
}

// cliCommands is the terminal block's two commands with the CLI's own quoted
// path. POSIX uses sh/zsh's eval; Windows uses PowerShell, where stdout is
// piped to Invoke-Expression.
func cliCommands(path string, windows bool) string {
	if windows {
		quoted := clientsetup.WindowsQuote(path)
		return "& " + quoted + " env --shell powershell | Invoke-Expression\n" +
			"& " + quoted + " exec -- npm test"
	}
	quoted := clientsetup.POSIXQuote(path)
	return "eval \"$(" + quoted + " env)\"\n" + quoted + " exec -- npm test"
}

// locateMCPBinary and locateCLIBinary look beside the running executable, in
// the macOS bundle's Resources (where MyGo packages helpers), and on PATH.
func locateMCPBinary() (string, bool) {
	return locateHelperBinary(runtime.GOOS, mcpExecutableName(runtime.GOOS))
}

func locateCLIBinary() (string, bool) {
	return locateHelperBinary(runtime.GOOS, cliExecutableName(runtime.GOOS))
}

func locateHelperBinary(goos, name string) (string, bool) {
	var exe, resources string
	if path, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		exe = path
	}
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		resources = dir
	}
	for _, candidate := range helperBinaryCandidates(exe, resources, goos, name) {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() &&
			(goos == "windows" || info.Mode().Perm()&0o111 != 0) {
			return candidate, true
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	return "", false
}

func mcpExecutableName(goos string) string {
	return helperExecutableName("vscroxy-mcp", goos)
}

func cliExecutableName(goos string) string {
	return helperExecutableName("vscroxy-cli", goos)
}

func helperExecutableName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// mcpBinaryCandidates lists the places an app package puts a helper. The
// macOS bundle keeps its executable in Contents/MacOS and resources in
// Contents/Resources; elsewhere MyGo puts resources beside the executable.
func mcpBinaryCandidates(exe, resources, goos string) []string {
	return helperBinaryCandidates(exe, resources, goos, mcpExecutableName(goos))
}

func helperBinaryCandidates(exe, resources, goos, name string) []string {
	var candidates []string
	add := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, existing := range candidates {
			if existing == path {
				return
			}
		}
		candidates = append(candidates, path)
	}
	if exe != "" {
		dir := filepath.Dir(exe)
		add(filepath.Join(dir, name))
		if goos == "darwin" && filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
			contents := filepath.Dir(dir)
			add(filepath.Join(contents, "Resources", name))
			add(filepath.Join(contents, "Resources", "bin", name))
		}
	}
	if resources != "" {
		add(filepath.Join(resources, name))
		add(filepath.Join(resources, "bin", name))
	}
	return candidates
}
