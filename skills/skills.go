// Package skills embeds the Agent Skill that ships with vscroxy. The MCP
// server serves it as a resource, and Settings → AI agents can install the
// same bytes into a client's user skills directory. The frontmatter version is
// kept equal to internal/version.Number by the MCP server's skill test, since
// go:embed cannot interpolate a Go constant.
package skills

import (
	_ "embed"
	"strings"
)

// Name is the skill's directory and frontmatter name.
const Name = "vscroxy"

//go:embed vscroxy/SKILL.md
var skill string

// Text is the complete SKILL.md, including its frontmatter.
func Text() string { return skill }

// ParseVersion reads the version under a top-level frontmatter metadata map.
// It returns "" for text that is not a frontmatter document or has no
// metadata.version entry.
func ParseVersion(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	inMetadata := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.TrimSpace(line) == "metadata:" {
			inMetadata = true
			continue
		}
		if inMetadata {
			if line != "" && line[0] != ' ' && line[0] != '\t' {
				inMetadata = false
				continue
			}
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "version:"); ok {
				return strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
	}
	return ""
}
