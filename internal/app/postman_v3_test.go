package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/postman"
)

func TestImportPostmanV3Directory(t *testing.T) {
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "environments.json")
	col, err := postman.ReadCollectionDir(filepath.Join("..", "postman", "testdata", "v3", "shop"))
	if err != nil {
		t.Fatal(err)
	}
	a.importPostmanCollection(col)
	if len(a.requests) != 25 {
		t.Fatalf("requests = %d, want 25", len(a.requests))
	}
	if len(a.folders) != 2 {
		t.Fatalf("folders = %+v, want the collection and Orders", a.folders)
	}
	var found, mqtt bool
	for _, r := range a.requests {
		if r.Name == "MQTT request" && r.Protocol == "MQTT" && len(r.MQTT.Topics) == 2 {
			mqtt = true
		}
		if r.Collection == "Shop API/Orders" && r.Name == "List orders" {
			found = true
			if r.URL != "{{baseUrl}}/orders?status=open" || r.Auth.Type != "Bearer" {
				t.Errorf("List orders = %+v", r)
			}
			if len(r.Examples) != 2 {
				t.Errorf("List orders examples = %d", len(r.Examples))
			}
		}
	}
	if !found {
		t.Fatal("List orders was not imported")
	}
	if !mqtt {
		t.Fatal("MQTT request was not imported")
	}
	if !a.importReport.open {
		t.Errorf("import report did not open: %+v", a.importReport)
	}
	for _, line := range a.importReport.lines {
		if strings.Contains(line, "MQTT") && strings.Contains(line, "Skipped") {
			t.Errorf("MQTT still reported as skipped: %q", line)
		}
	}
	// Importing the same directory again updates the same requests.
	a.importPostmanCollection(col)
	if len(a.requests) != 25 {
		t.Errorf("re-import made %d requests", len(a.requests))
	}
}

func TestImportPostmanV3SecretVariableStaysLocal(t *testing.T) {
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "environments.json")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Secrets\nvariables:\n  - key: token\n    value: local-secret\n    secret: true\n",
		"One.request.yaml":           "$kind: http-request\nname: One\nurl: https://example.com/one\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	col, err := postman.ReadCollectionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.importPostmanCollection(col)
	if got := a.folderCurrent["Secrets"]["token"]; got != "local-secret" {
		t.Fatalf("local secret = %q, want local-secret; folders %+v", got, a.folders)
	}
	for _, folder := range a.folders {
		for _, v := range folder.Variables {
			if v.Name == "token" || v.Current != "" || v.Local || v.Credential {
				t.Fatalf("library folder holds a local value: %+v", folder.Variables)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(a.library.root, "Secrets", "folder.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || strings.Contains(string(data), "local-secret") {
		t.Fatalf("folder.json contains the local secret: %s", data)
	}
}

func TestCollectionDirKindBrunoWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bruno.json"), []byte(`{"version":"1","name":"B","type":"collection"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "A", ".resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A", ".resources", "definition.yaml"), []byte("$kind: collection\nname: A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := collectionDirKindOf(dir); got != collectionDirBruno {
		t.Errorf("kind = %v, want Bruno", got)
	}
}

func TestCollectionDirKindFindsDeepV3(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "A", "B"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A", "B", "One.request.yaml"), []byte("$kind: http-request\nname: One\nurl: https://example.com/one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := collectionDirKindOf(dir); got != collectionDirV3 {
		t.Errorf("kind = %v, want V3", got)
	}
}

func TestDroppedImportKindRoutesCollectionFolders(t *testing.T) {
	v3 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(v3, ".resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v3, ".resources", "definition.yaml"), []byte("$kind: collection\nname: V3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := droppedImportKind(v3, true); got != droppedCollectionDir {
		t.Errorf("v3 folder = %v, want droppedCollectionDir", got)
	}
	bruno := t.TempDir()
	if err := os.WriteFile(filepath.Join(bruno, "bruno.json"), []byte(`{"version":"1","name":"B","type":"collection"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := droppedImportKind(bruno, true); got != droppedCollectionDir {
		t.Errorf("Bruno folder = %v, want droppedCollectionDir", got)
	}
	if got := droppedImportKind(t.TempDir(), true); got != droppedUnknown {
		t.Errorf("empty folder = %v, want droppedUnknown", got)
	}
}

func writeV3Dir(t *testing.T, dir string, files map[string]string) {
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

func TestImportV3DuplicateIDStableAcrossOrder(t *testing.T) {
	dir := t.TempDir()
	files := func(orderA, orderB string) map[string]string {
		return map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Stable\n",
			"A.request.yaml":             fmt.Sprintf("$kind: http-request\nid: 5d1c\nname: Get user\nurl: https://example.com/a\norder: %s\n", orderA),
			"B.request.yaml":             fmt.Sprintf("$kind: http-request\nid: 5d1c\nname: Get user\nurl: https://example.com/b\norder: %s\n", orderB),
		}
	}
	writeV3Dir(t, dir, files("2000", "1000"))
	col, err := postman.ReadCollectionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "environments.json")
	a.importPostmanCollection(col)
	if len(a.requests) != 2 {
		t.Fatalf("first import made %d requests: %+v", len(a.requests), a.requests)
	}

	writeV3Dir(t, dir, files("1000", "2000"))
	col, err = postman.ReadCollectionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.importPostmanCollection(col)
	if len(a.requests) != 2 {
		t.Fatalf("order change left %d requests: %+v", len(a.requests), a.requests)
	}
	for _, r := range a.requests {
		if r.URL == "https://example.com/a" && r.ID != "5d1c" {
			t.Errorf("path-first request id = %q, want 5d1c", r.ID)
		}
	}
}
