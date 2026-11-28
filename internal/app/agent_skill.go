package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/skills"
)

// agentSkillState is what the installed skill file means for this app build.
type agentSkillState int

const (
	skillMissing agentSkillState = iota
	skillCurrent
	skillOlder
	skillModified
	skillUnreadable
)

type agentSkillStatus struct {
	path  string
	state agentSkillState
}

// agentSkillPath is the documented user-level skill file for each confirmed
// client. Other clients have no path this app can install into.
func agentSkillPath(client mcpClientKind, home string) (string, bool) {
	var dir string
	switch client {
	case mcpClaudeCode:
		dir = ".claude"
	case mcpCodex, mcpPi:
		// Codex and pi both discover the Agent Skills standard location.
		dir = ".agents"
	case mcpCursor:
		dir = ".cursor"
	default:
		return "", false
	}
	return filepath.Join(home, dir, "skills", skills.Name, "SKILL.md"), true
}

// agentSkillStateFor decides from the installed text alone, so the rule is
// testable without a home directory or filesystem.
func agentSkillStateFor(installed string, exists bool, wantVersion string) agentSkillState {
	if !exists {
		return skillMissing
	}
	switch version := skills.ParseVersion(installed); {
	case version == "":
		return skillModified
	case version == wantVersion:
		return skillCurrent
	default:
		return skillOlder
	}
}

func readAgentSkill(path, wantVersion string) agentSkillStatus {
	data, err := os.ReadFile(path)
	if err != nil {
		state := skillUnreadable
		if errors.Is(err, fs.ErrNotExist) {
			state = skillMissing
		}
		return agentSkillStatus{path: path, state: state}
	}
	return agentSkillStatus{
		path:  path,
		state: agentSkillStateFor(string(data), true, wantVersion),
	}
}

// installAgentSkill writes the embedded skill atomically, creating the
// client's skills directory when needed.
func installAgentSkill(path string) error {
	return store.WriteFile(path, []byte(skills.Text()))
}

// currentAgentSkill reads the installed file once per page visit, client
// switch or install action; drawing a frame does not read it again.
func (a *app) currentAgentSkill() (agentSkillStatus, bool) {
	client := mcpClientKind(a.agents.client)
	home := a.agents.skillHome
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return agentSkillStatus{state: skillUnreadable}, client != mcpOtherClient
		}
	}
	path, ok := agentSkillPath(client, home)
	if !ok {
		return agentSkillStatus{}, false
	}
	if a.agents.skillChecked && a.agents.skillPath == path {
		return a.agents.skill, true
	}
	a.agents.skill = readAgentSkill(path, version.Number)
	a.agents.skillPath, a.agents.skillChecked = path, true
	return a.agents.skill, true
}

func (a *app) resetAgentSkill() {
	a.agents.skillChecked = false
	a.agents.skillPath = ""
}
