package app

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestPushHistoryPutsTheNewestFirst(t *testing.T) {
	list := pushHistory(nil, historyEntry{Method: "GET", URL: "http://a/"})
	list = pushHistory(list, historyEntry{Method: "GET", URL: "http://b/"})
	if len(list) != 2 || list[0].URL != "http://b/" {
		t.Fatalf("list = %+v, want b first", list)
	}
}

func TestPushHistoryDropsAnEarlierSend(t *testing.T) {
	list := pushHistory(nil, historyEntry{Method: "GET", URL: "http://a/", Body: "old"})
	list = pushHistory(list, historyEntry{Method: "POST", URL: "http://a/"})
	list = pushHistory(list, historyEntry{Method: "GET", URL: "http://a/", Body: "new"})
	if len(list) != 2 {
		t.Fatalf("list = %+v, want the GET replaced and the POST kept", list)
	}
	if list[0].Method != "GET" || list[0].Body != "new" {
		t.Fatalf("first = %+v, want the new GET", list[0])
	}
}

func TestPushHistoryKeepsTheListShort(t *testing.T) {
	var list []historyEntry
	for i := range maxHistory + 20 {
		list = pushHistory(list, historyEntry{Method: "GET", URL: fmt.Sprintf("http://example.com/%d", i)})
	}
	if len(list) != maxHistory {
		t.Fatalf("length = %d, want %d", len(list), maxHistory)
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	want := []historyEntry{{Method: "POST", URL: "https://example.com/x", Body: "{}", At: time.Unix(1, 0).UTC()}}
	if err := saveHistory(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadHistory(path)
	if err != nil || len(got) != 1 || got[0].URL != want[0].URL || got[0].Method != "POST" {
		t.Fatalf("load = %+v, %v", got, err)
	}
}

func TestHistoryLabel(t *testing.T) {
	e := historyEntry{Method: "GET", URL: "https://api.github.com/zen?x=1"}
	if got := e.label(); got != "GET api.github.com/zen?x=1" {
		t.Errorf("label = %q", got)
	}
}
