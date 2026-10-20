package main

import (
	"path/filepath"
	"testing"
)

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
