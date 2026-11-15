package openapi

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeSpec(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadResolvesLocalRefsAndLints(t *testing.T) {
	dir := t.TempDir()
	root := writeSpec(t, dir, "api.yaml", `openapi: 3.0.3
info: {title: Shop, version: 1.0.0}
servers:
  - url: http://api.example.com
paths:
  /items/{id}:
    get:
      operationId: getItem
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: './schemas.yaml#/Item'
  /items:
    post:
      operationId: createItem
      tags: [missing]
      responses:
        '201': {description: created}
components:
  schemas:
    Unused: {type: string}
`)
	writeSpec(t, dir, "schemas.yaml", `Item:
  type: object
  properties:
    id: {type: string}
`)
	spec, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Files) != 2 || !slices.ContainsFunc(spec.Files, func(p string) bool { return filepath.Base(p) == "schemas.yaml" }) {
		t.Fatalf("files = %v", spec.Files)
	}
	issues := Lint(spec.Doc)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, want := range []string{"operation-summary-missing", "tag-undefined", "server-insecure", "component-unused", "response-schema-missing"} {
		if !codes[want] {
			t.Errorf("missing lint %q: %+v", want, issues)
		}
	}
}

func TestLoadRefusesRemoteAndEscapingRefs(t *testing.T) {
	dir := t.TempDir()
	for name, ref := range map[string]string{
		"remote":   "https://example.com/schemas.yaml#/Item",
		"escape":   "../outside.yaml#/Item",
		"absolute": "/etc/passwd#/Item",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeSpec(t, dir, name+".yaml", `openapi: 3.0.3
info: {title: X, version: 1.0.0}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: '`+ref+`'
`)
			_, err := Load(root)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ref") && !strings.Contains(err.Error(), "outside") {
				t.Fatalf("ref %q: err = %v", ref, err)
			}
		})
	}
}

func TestLoadRefusesSymlinkEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	target := writeSpec(t, outside, "secret.yaml", "Item: {type: string}\n")
	if err := os.Symlink(target, filepath.Join(dir, "link.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root := writeSpec(t, dir, "api.yaml", `openapi: 3.0.3
info: {title: X, version: 1.0.0}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: './link.yaml#/Item'
`)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("symlink escape: %v", err)
	}
}
