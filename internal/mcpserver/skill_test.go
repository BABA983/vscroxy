package mcpserver_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/mcpserver"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/skills"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The resource is local, needs no capabilities, and does not read the desktop
// discovery file: a client can learn the workflows before vscroxy is open.
func TestSkillResourceMatchesEmbeddedBytes(t *testing.T) {
	cs := connect(t, filepath.Join(t.TempDir(), "missing-control.json"), mcpserver.Options{}, "2025-11-25")
	if init := cs.InitializeResult(); init.ServerInfo.Version != version.Number || !strings.Contains(init.Instructions, "vscroxy://skill") {
		t.Fatalf("server info = %+v, instructions = %q", init.ServerInfo, init.Instructions)
	}
	listed, err := cs.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, resource := range listed.Resources {
		if resource.URI == "vscroxy://skill" {
			found = true
			if resource.MIMEType != "text/markdown" || resource.Name == "" {
				t.Fatalf("skill resource = %+v", resource)
			}
		}
	}
	if !found {
		t.Fatalf("resources = %+v; want vscroxy://skill", listed.Resources)
	}
	result, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "vscroxy://skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != skills.Text() || result.Contents[0].MIMEType != "text/markdown" || result.Contents[0].URI != "vscroxy://skill" {
		t.Fatalf("skill contents = %+v", result.Contents)
	}
}

// The shipped skill is the agent-facing guide; a tool renamed in the server
// must not leave behind a recipe that calls a name the client cannot see.
func TestSkillNamesRegisteredTools(t *testing.T) {
	path := endpoint(t, func(context.Context, control.Command) (any, error) { return nil, nil })
	cs := connect(t, path, mcpserver.Options{AllowWrite: mcpserver.WriteAll, AllowSensitive: true}, "2025-11-25")
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	known := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		known[tool.Name] = true
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "vscroxy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\nname: vscroxy\n") {
		t.Fatalf("SKILL.md does not start with the vscroxy frontmatter: %q", strings.SplitN(text, "\n", 3)[0])
	}
	if got := skills.ParseVersion(text); got != version.Number {
		t.Fatalf("SKILL.md metadata version = %q; want the app version %q", got, version.Number)
	}
	names := regexp.MustCompile(`\b[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*\b`).FindAllString(text, -1)
	if len(names) == 0 {
		t.Fatal("SKILL.md names no MCP tools")
	}
	namespaces := map[string]bool{}
	for name := range known {
		if namespace, _, ok := strings.Cut(name, "."); ok {
			namespaces[namespace] = true
		}
	}
	for _, name := range names {
		if known[name] {
			continue
		}
		// Only a dotted name in a real tool namespace can be a renamed or
		// misspelled tool. File names (control.json), host names and report
		// fields (report.failed) are not part of this check.
		if namespace, _, ok := strings.Cut(name, "."); ok && namespaces[namespace] {
			t.Errorf("SKILL.md names unregistered tool %q", name)
		}
	}
	for _, name := range []string{"flows.replay", "requests.save", "request.run", "flows.list", "flows.get", "runs.get", "collections.list"} {
		if !strings.Contains(text, name) {
			t.Errorf("SKILL.md does not describe %s", name)
		}
	}
}
