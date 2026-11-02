package library_test

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/request"
)

func writeFile(t *testing.T, root, name, data string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeRequest(data []byte, collection string) (request.Request, error) {
	var r request.Request
	err := json.Unmarshal(data, &r)
	r.Collection = collection
	return r, err
}

func TestReadTree(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"folder.json":                      `{"description":"root settings are not inherited","order":["z","missing","a","z"]}`,
		"a.request.json":                   `{"name":"A","collection":"not the directory","id":"same"}`,
		"z.request.json":                   `{"name":"Z","id":"same"}`,
		"b.request.json":                   `{"name":"B"}`,
		"c.request.json":                   `{"name":"C"}`,
		"%5A/folder.json":                  `{"path":"wrong","auth":{"type":"Bearer","token":"token"},"pre":"before","post":"after","variables":[{"name":"base","value":"example"}]}`,
		"%5A/one.request.json":             `{"name":"In Z"}`,
		"%5A/50%25%20off/two.request.json": `{"name":"Nested"}`,
		"Alpha/folder.json":                `{"description":"collection"}`,
		"Alpha/one.request.json":           `{"name":"In Alpha"}`,
		"Empty/folder.json":                `{"path":"ignored","order":["stale"]}`,
		"Settings/folder.json":             `{"description":"no requests"}`,
	}
	for name, data := range files {
		writeFile(t, root, name, data)
	}
	for _, name := range []string{".hidden.request.json", ".git/bad.request.json", "Alpha/.hidden/bad.request.json", "README.md", "Alpha/other.json"} {
		writeFile(t, root, name, "not JSON")
	}
	// A symlink with a request suffix is not a regular request file.
	if err := os.Symlink(filepath.Join(root, "a.request.json"), filepath.Join(root, "link.request.json")); err != nil {
		t.Fatal(err)
	}
	requests, folders, known, err := library.Read(root, decodeRequest)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range requests {
		names = append(names, r.Collection+"/"+r.Name)
	}
	// Duplicate order entries keep their last rank, unlisted requests follow,
	// and Alpha precedes the escaped directory %5A (Z), not vice versa.
	want := []string{"/A", "/Z", "/B", "/C", "Alpha/In Alpha", "Z/In Z", "Z/50% off/Nested"}
	if !slices.Equal(names, want) {
		t.Errorf("requests = %q, want %q", names, want)
	}
	if requests[0].ID != "same" || requests[1].ID != "same" || requests[2].ID != "" {
		t.Errorf("reader assigned or changed identities: %+v", requests[:3])
	}
	wantFolders := []request.Folder{
		{Path: "Alpha", Description: "collection"},
		{Path: "Settings", Description: "no requests"},
		{Path: "Z", Auth: request.Auth{Type: "Bearer", Token: "token"}, Pre: "before", Post: "after", Variables: []request.Field{{Name: "base", Value: "example"}}},
	}
	if !reflect.DeepEqual(folders, wantFolders) {
		t.Errorf("folders = %+v, want %+v", folders, wantFolders)
	}
	if len(known) != len(files) {
		t.Errorf("known has %d files, want %d", len(known), len(files))
	}
	for name, data := range files {
		if string(known[name]) != data {
			t.Errorf("known[%q] = %q, want original %q", name, known[name], data)
		}
	}
}

func TestReadWithCallerMetadata(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Shop/one.request.json", `{"name":"One","source":{"key":"capture"}}`)
	type asset struct {
		request.Request
		Source struct {
			Key string `json:"key"`
		} `json:"source"`
	}
	assets, _, _, err := library.Read(root, func(data []byte, collection string) (asset, error) {
		var r asset
		err := json.Unmarshal(data, &r)
		r.Collection = collection
		return r, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].Source.Key != "capture" || assets[0].Collection != "Shop" {
		t.Fatalf("caller metadata or collection lost: %+v", assets)
	}
	requests, _, _, err := library.Read(root, decodeRequest)
	if err != nil || len(requests) != 1 || requests[0].Collection != "Shop" || requests[0].Name != "One" {
		t.Fatalf("portable decoding = %+v, %v", requests, err)
	}
}

func TestReadEmpty(t *testing.T) {
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		requests, folders, known, err := library.Read(root, func([]byte, string) (int, error) {
			t.Fatal("empty library called decode")
			return 0, nil
		})
		if err != nil || requests != nil || folders != nil || known == nil || len(known) != 0 {
			t.Errorf("Read(%q) = %v, %v, %v, %v", root, requests, folders, known, err)
		}
	}
}

func TestReadErrorsDiscardPartialResults(t *testing.T) {
	for _, name := range []string{"Z/bad.request.json", "Z/folder.json"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "first.request.json", `{"name":"First"}`)
			writeFile(t, root, name, "invalid JSON")
			requests, folders, known, err := library.Read(root, decodeRequest)
			if err == nil || !strings.HasPrefix(err.Error(), name+": ") {
				t.Fatalf("error = %v, want file path %q", err, name)
			}
			if requests != nil || folders != nil || known != nil {
				t.Fatalf("partial results returned: %v, %v, %v", requests, folders, known)
			}
		})
	}
	root := t.TempDir()
	writeFile(t, root, "reject.request.json", "anything")
	rejected := errors.New("decoder rejected request")
	_, _, _, err := library.Read(root, func([]byte, string) (int, error) { return 0, rejected })
	if !errors.Is(err, rejected) || !strings.HasPrefix(err.Error(), "reject.request.json: ") {
		t.Fatalf("decoder error was not wrapped with file path: %v", err)
	}
	_, _, _, err = library.Read(filepath.Join(root, "reject.request.json"), decodeRequest)
	if err == nil {
		t.Fatal("non-directory root accepted")
	}
}
