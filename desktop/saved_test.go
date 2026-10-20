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
