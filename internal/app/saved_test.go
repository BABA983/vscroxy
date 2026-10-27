package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequestIdentitySurvivesLegacyLoadRenameMoveAndImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	legacy := []byte(`[{"name":"Login","collection":"Dev","method":"POST","url":"https://example.test/login"}]`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := loadRequests(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadRequests(path)
	if err != nil || first[0].ID == "" || first[0].ID != second[0].ID {
		t.Fatal("legacy loads changed identity", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(legacy) {
		t.Fatal("loading rewrote the legacy file")
	}
	a := workspaceApp()
	a.requests, a.requestsPath = first, path
	a.openSaved(first[0].key())
	id, document := a.assetID, a.requestDocument
	a.setDebugSource(newDebugSource(a.flows[0]))
	sourceKey := a.origin.Key
	a.composeName, a.composeCollection = "Authenticate", "Staging/Auth"
	if !a.saveComposed() {
		t.Fatal(a.message)
	}
	if len(a.requests) != 1 || a.assetID != id {
		t.Fatal("rename/move duplicated or replaced asset identity")
	}
	a.openSaved(a.requests[0].key())
	if a.requestDocument != document {
		t.Fatal("rename opened a second tab for the same asset")
	}
	loaded, err := loadRequests(path)
	if err != nil || loaded[0].ID != id || loaded[0].Name != "Authenticate" {
		t.Fatalf("restart lost identity: %+v %v", loaded, err)
	}
	if loaded[0].Source == nil || loaded[0].Source.Key != sourceKey || loaded[0].Source.Exchange.URL != "https://api.example.com/projects" {
		t.Fatal("saved asset lost its immutable capture provenance on restart")
	}
	foreign := savedRequest{ID: "another-asset", Name: "Authenticate", Collection: "Staging/Auth", URL: "https://other.test"}
	merged := mergeRequests(loaded, []savedRequest{foreign})
	merged = mergeRequests(merged, []savedRequest{foreign})
	if len(merged) != 2 || merged[0].ID != id || merged[0].URL != "https://example.test/login" || merged[1].Name != "Authenticate (2)" || merged[1].ID != foreign.ID {
		t.Fatalf("import conflated identities or was not idempotent: %+v", merged)
	}
	if err := os.WriteFile(path, []byte(`[{"id":"same","name":"one"},{"id":"same","name":"two"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRequests(path); err == nil {
		t.Fatal("ambiguous identities accepted")
	}
}

func TestUpsertRequest(t *testing.T) {
	list := upsertRequest(nil, savedRequest{Name: "B", URL: "http://b/"})
	list = upsertRequest(list, savedRequest{Name: "A", URL: "http://a/"})
	list = upsertRequest(list, savedRequest{Name: "B", URL: "http://b/2"})
	if len(list) != 2 {
		t.Fatalf("list = %+v, want two requests", list)
	}
	for _, r := range list {
		if r.Name == "B" && r.URL != "http://b/2" {
			t.Errorf("B = %q, want the replacement", r.URL)
		}
	}
}

func TestUpsertKeepsCollectionsApart(t *testing.T) {
	list := upsertRequest(nil, savedRequest{Name: "A", Collection: "one", URL: "http://1/"})
	list = upsertRequest(list, savedRequest{Name: "A", Collection: "two", URL: "http://2/"})
	if len(list) != 2 {
		t.Fatalf("list = %+v, want the name in both collections", list)
	}
	list = upsertRequest(list, savedRequest{Name: "A", Collection: "one", URL: "http://3/"})
	if len(list) != 2 {
		t.Fatalf("list = %+v, want a replacement, not an addition", list)
	}
	for _, r := range list {
		if r.Collection == "one" && r.URL != "http://3/" {
			t.Errorf("one = %q, want the replacement", r.URL)
		}
	}
}

func TestGroupRequests(t *testing.T) {
	groups := groupRequests([]savedRequest{
		{Name: "a", Collection: "api"},
		{Name: "b"},
		{Name: "c", Collection: "api"},
	})
	if len(groups["api"]) != 2 || len(groups[""]) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestUniqueRequestName(t *testing.T) {
	list := []savedRequest{{Name: "GET example.com/"}}
	if got := uniqueRequestName(list, "GET example.com/"); got != "GET example.com/ (2)" {
		t.Errorf("name = %q, want the count", got)
	}
	if got := uniqueRequestName(list, "POST example.com/"); got != "POST example.com/" {
		t.Errorf("name = %q, want it as it is", got)
	}
	list = append(list, savedRequest{Name: "GET example.com/ (2)"})
	if got := uniqueRequestName(list, "GET example.com/"); got != "GET example.com/ (3)" {
		t.Errorf("name = %q, want the next count", got)
	}
}

func TestSaveAndLoadRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	want := []savedRequest{
		{Name: "second", Method: "POST", URL: "https://example.com/b", Headers: "A: 1", Body: `{"x":1}`},
		{Name: "first", Method: "GET", URL: "https://example.com/a"},
	}
	if err := saveRequests(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadRequests(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0].Name != "first" || got[1].Body != `{"x":1}` {
		t.Fatalf("loaded = %+v", got)
	}
}

func TestLoadRequestsWithoutAFile(t *testing.T) {
	got, err := loadRequests(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || got != nil {
		t.Fatalf("load = %+v, %v; want none and no error", got, err)
	}
}
