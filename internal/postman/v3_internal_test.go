package postman

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeInternalV3(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestV3TotalSizeBudget(t *testing.T) {
	old := v3MaxBytes
	v3MaxBytes = 1 << 20
	t.Cleanup(func() { v3MaxBytes = old })

	dir := t.TempDir()
	body := strings.Repeat("x", 600<<10)
	writeInternalV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Budget\n",
		"One.request.yaml":           "$kind: http-request\nname: One\nurl: https://example.com/one\nexamples: ./.resources/One.resources/examples\n",
		".resources/One.resources/examples/a.example.yaml": "$kind: http-example\nname: a\n" +
			"response:\n  statusCode: 200\n  statusText: OK\n  body:\n    type: text\n    content: " + body + "\n",
		".resources/One.resources/examples/b.example.yaml": "$kind: http-example\nname: b\n" +
			"response:\n  statusCode: 200\n  statusText: OK\n  body:\n    type: text\n    content: " + body + "\n",
	})
	col, err := ReadCollectionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(col.Warnings, "Stopped reading at the 1 MiB total-size limit") {
		t.Errorf("warnings = %q", col.Warnings)
	}
	if len(col.Requests) != 1 || len(col.Requests[0].Examples) != 1 {
		t.Errorf("requests = %+v", col.Requests)
	}
}

func TestV3RejectedFileCountsTowardLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.request.yaml")
	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &v3reader{reader: reader{warn: tally{}}}
	r.files = 1
	if _, err := r.readLimit(path, 50, "document size"); err == nil {
		t.Fatal("oversized file was accepted")
	}
	if r.files != 2 {
		t.Errorf("files = %d, want the rejected file counted", r.files)
	}
}

func TestV3FolderEntriesCountTowardLimit(t *testing.T) {
	old := v3MaxFiles
	v3MaxFiles = 4
	t.Cleanup(func() { v3MaxFiles = old })

	dir := t.TempDir()
	writeInternalV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Folders\n",
		"A/One.request.yaml":         "$kind: http-request\nname: One\nurl: https://example.com/one\n",
		"B/Two.request.yaml":         "$kind: http-request\nname: Two\nurl: https://example.com/two\n",
	})
	col, err := ReadCollectionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(col.Warnings, "Stopped reading at the 4-entry limit") {
		t.Errorf("warnings = %q", col.Warnings)
	}
	if len(col.Requests) != 1 {
		t.Errorf("folders were not counted: requests = %+v", col.Requests)
	}
}
