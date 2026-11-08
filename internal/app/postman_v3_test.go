package app

import (
	"os"
	"path/filepath"
	"slices"
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
	if len(a.requests) != 24 {
		t.Fatalf("requests = %d, want 24", len(a.requests))
	}
	if len(a.folders) != 2 {
		t.Fatalf("folders = %+v, want the collection and Orders", a.folders)
	}
	var found bool
	for _, r := range a.requests {
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
	if !a.importReport.open || !slices.Contains(a.importReport.lines, "Skipped 1 MQTT request; vscroxy does not support them") {
		t.Errorf("import report = %+v", a.importReport)
	}
	// Importing the same directory again updates the same requests.
	a.importPostmanCollection(col)
	if len(a.requests) != 24 {
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
