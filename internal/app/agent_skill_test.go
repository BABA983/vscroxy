package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/skills"
)

func skillDocument(version string) string {
	return "---\nname: vscroxy\ndescription: test\nmetadata:\n  version: " + version + "\n---\n\n# vscroxy\n"
}

func TestAgentSkillStateDecision(t *testing.T) {
	const want = "1.2.3"
	for _, tc := range []struct {
		name      string
		installed string
		exists    bool
		want      agentSkillState
	}{
		{"missing", "", false, skillMissing},
		{"current", skillDocument(want), true, skillCurrent},
		{"older", skillDocument("1.2.2"), true, skillOlder},
		{"no metadata", "---\nname: vscroxy\ndescription: user copy\n---\n\n# mine\n", true, skillModified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentSkillStateFor(tc.installed, tc.exists, want); got != tc.want {
				t.Fatalf("state = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestAgentSkillPathsFollowClientDocs(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		client mcpClientKind
		want   string
	}{
		{mcpClaudeCode, filepath.Join(home, ".claude", "skills", "vscroxy", "SKILL.md")},
		{mcpCodex, filepath.Join(home, ".agents", "skills", "vscroxy", "SKILL.md")},
		{mcpPi, filepath.Join(home, ".agents", "skills", "vscroxy", "SKILL.md")},
		{mcpCursor, filepath.Join(home, ".cursor", "skills", "vscroxy", "SKILL.md")},
	} {
		got, ok := agentSkillPath(tc.client, home)
		if !ok || got != tc.want {
			t.Errorf("client %d path = %q, %v; want %q", tc.client, got, ok, tc.want)
		}
	}
	if path, ok := agentSkillPath(mcpOtherClient, home); ok || path != "" {
		t.Fatalf("Other offered an install path: %q", path)
	}
}

func TestInstallAgentSkillWritesIntoHomeAtomically(t *testing.T) {
	home := t.TempDir()
	path, ok := agentSkillPath(mcpClaudeCode, home)
	if !ok {
		t.Fatal("Claude Code has no install path")
	}
	if err := installAgentSkill(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != skills.Text() {
		t.Fatalf("installed %d bytes; want the embedded skill's %d", len(data), len(skills.Text()))
	}
	if status := readAgentSkill(path, version.Number); status.state != skillCurrent {
		t.Fatalf("installed state = %+v; want current", status)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "SKILL.md" {
		t.Fatalf("skill directory = %v; want only SKILL.md", entries)
	}

	// A second install replaces an older file whose name is already there.
	old := skillDocument("0.0.1")
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installAgentSkill(path); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != skills.Text() || strings.Contains(string(data), "0.0.1") {
		t.Fatalf("replacement = %q, %v", data, err)
	}
}
