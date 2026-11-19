package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestInputHistoryUpAndDownRestoreTheDraft(t *testing.T) {
	var h inputHistory
	h.add("https://older.example")
	h.add("https://newer.example")
	draft := "https://in-progress.example"

	for _, want := range []string{"https://newer.example", "https://older.example"} {
		got, ok := h.up(draft)
		if !ok || got != want {
			t.Fatalf("Up = %q, %v; want %q", got, ok, want)
		}
	}
	for _, want := range []string{"https://newer.example", draft} {
		got, ok := h.down()
		if !ok || got != want {
			t.Fatalf("Down = %q, %v; want %q", got, ok, want)
		}
	}
	if _, ok := h.down(); ok {
		t.Fatal("Down past the draft changed the field")
	}
}

func TestInputHistoryReAddingMovesAnEntryToTheEnd(t *testing.T) {
	var h inputHistory
	for _, value := range []string{"a", "b", "c"} {
		h.add(value)
	}
	h.add("a")
	if !slices.Equal(h.entries, []string{"b", "c", "a"}) {
		t.Fatalf("entries = %v; want [b c a]", h.entries)
	}
	if got, _ := h.up(""); got != "a" {
		t.Fatalf("newest = %q, want a", got)
	}
	if got, _ := h.up(""); got != "c" {
		t.Fatalf("next older = %q, want c", got)
	}
}

func TestInputHistoryEvictsOldestAtTheCap(t *testing.T) {
	var h inputHistory
	for i := range 51 {
		h.add(fmt.Sprintf("entry-%d", i))
	}
	if len(h.entries) != inputHistoryLimit || h.entries[0] != "entry-1" || h.entries[len(h.entries)-1] != "entry-50" {
		t.Fatalf("entries = %v", h.entries)
	}
	if got, _ := h.up(""); got != "entry-50" {
		t.Fatalf("newest after eviction = %q, want entry-50", got)
	}
}

func TestInputHistoryIgnoresEmptyAndWhitespace(t *testing.T) {
	var h inputHistory
	for _, value := range []string{"", " ", "\t\n  "} {
		if h.add(value) {
			t.Fatalf("added %q", value)
		}
	}
	if len(h.entries) != 0 {
		t.Fatalf("entries = %v", h.entries)
	}
	if !h.add("  https://example.test  ") || h.entries[0] != "  https://example.test  " {
		t.Fatalf("authored text was trimmed: %v", h.entries)
	}
}

func TestRedactHistoryURLKeepsOnlySafeAuthoredText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://api.example.com/p?token=abc&page=2", "https://api.example.com/p?token=&page=2"},
		{"https://{{host}}/a/{{id}}?token=abc&page=2", "https://{{host}}/a/{{id}}?token=&page=2"},
		{"{{base}}/users/{{id}}?api_key=xyz", "{{base}}/users/{{id}}?api_key="},
		{"https://api.test/x y?token=abc&page=2", "https://api.test/x y?token=&page=2"},
		{"https://api.example.com/p?q={{token}}&page=2", "https://api.example.com/p?q={{token}}&page=2"},
		{"https://api.example.com/p?TOKEN=abc&code=xyz&page=2", "https://api.example.com/p?TOKEN=&code=&page=2"},
		{"https://api.example.com/p?token=abc#frag?token=abc", "https://api.example.com/p?token=#frag?token=abc"},
		{"https://user:secret@api.test/p", "https://user:@api.test/p"},
		{"https://user:{{password}}@api.test/p", "https://user:{{password}}@api.test/p"},
		{"api.test/p?to%ZZken=abc", "api.test/p?to%ZZken="},
	} {
		if got := redactHistoryURL(tc.in); got != tc.want {
			t.Errorf("redactHistoryURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSentURLIsAuthoredAndRedactedInHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	openDraft(a, a.flows[0])
	a.composeURL = upstream.URL + "?token=secret-token&page=2&ref={{token}}"
	a.globals = map[string]string{"token": "resolved-credential"}
	a.sendComposed()
	waitAppSignal(t, a, func() bool { return !a.composeWaiting }, a.drain,
		func() string { return "the send did not finish" })

	if len(a.urlHistory.entries) != 1 {
		t.Fatalf("URL history = %v", a.urlHistory.entries)
	}
	got := a.urlHistory.entries[0]
	for _, secret := range []string{"secret-token", "resolved-credential"} {
		if strings.Contains(got, secret) {
			t.Fatalf("URL history disclosed %q: %q", secret, got)
		}
	}
	if !strings.Contains(got, "token=&page=2") || !strings.Contains(got, "ref={{token}}") {
		t.Fatalf("URL history lost authored text: %q", got)
	}
}

func TestInputHistorySurvivesSettingsSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{settingsPath: path}
	a.urlHistory.add("https://older.example")
	a.urlHistory.add("https://newer.example")
	a.filterHistory.add("status:5xx")
	a.saveInputHistory()

	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.URLHistory, []string{"https://older.example", "https://newer.example"}) ||
		!slices.Equal(saved.FilterHistory, []string{"status:5xx"}) {
		t.Fatalf("saved histories = %v / %v", saved.URLHistory, saved.FilterHistory)
	}
	var reloaded inputHistory
	reloaded.load(saved.URLHistory)
	if got, _ := reloaded.up(""); got != "https://newer.example" {
		t.Fatalf("reloaded newest = %q", got)
	}
}

func TestInputHistorySurvivesAppRestart(t *testing.T) {
	home := t.TempDir()
	useTempHome(t, home)
	root := filepath.Join(home, ".vscroxy")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	wantURL := []string{"https://older.example", "https://older.example/?page=2"}
	wantFilter := []string{"status:5xx", "login"}
	if err := writeSettings(filepath.Join(root, "settings.json"), settings{
		URLHistory: wantURL, FilterHistory: wantFilter,
	}, ""); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	defer a.views.stopSearch()
	if a.srv != nil {
		defer a.srv.Close()
	}
	if !slices.Equal(a.urlHistory.entries, wantURL) || !slices.Equal(a.filterHistory.entries, wantFilter) {
		t.Fatalf("reloaded histories = %v / %v", a.urlHistory.entries, a.filterHistory.entries)
	}
}

func TestURLHistoryYieldsToVariableCompletion(t *testing.T) {
	a := variableAssistApp()
	a.urlHistory.add("https://older.example")
	openDraft(a, a.flows[0])
	a.composeURL = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.urlInput.Focus()
	tt.Frame()
	tt.Type("{{te")
	tt.Frame()
	if !a.varsComplete.open {
		t.Fatalf("completion did not open: %v", tt.Texts())
	}
	before := a.varsComplete.selected
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if !a.varsComplete.open || a.varsComplete.selected == before || a.composeURL != "{{te" {
		t.Fatalf("Up did not move the completion: selected %d -> %d, URL %q", before, a.varsComplete.selected, a.composeURL)
	}
	if a.urlHistory.cursor != len(a.urlHistory.entries) {
		t.Fatalf("Up moved the URL history cursor: %d", a.urlHistory.cursor)
	}
	// With the completion gone, the same key belongs to the URL history.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.composeURL != "https://older.example" {
		t.Fatalf("Up after Escape = %q, want the recalled URL", a.composeURL)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.composeURL != "{{te" {
		t.Fatalf("Down after recall = %q, want the in-progress text", a.composeURL)
	}
}

func TestFilterHistoryRecordsEnterAndBlur(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	tt := ui.NewTester(a.view, 1280, 700)
	tt.Frame()
	if err := tt.Click("Search host or path"); err != nil {
		t.Fatal(err)
	}
	tt.Type("login")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !slices.Equal(a.filterHistory.entries, []string{"login"}) {
		t.Fatalf("filter history after Enter = %v", a.filterHistory.entries)
	}

	if err := tt.Click("Search host or path"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("projects")
	if err := tt.Click(locales.Filters()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.filterHistory.entries, []string{"login", "projects"}) {
		t.Fatalf("filter history after blur = %v", a.filterHistory.entries)
	}
}

func TestFilterHistoryYieldsToOpenSuggestions(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	a.filterHistory.add("https://first.example")
	tt := ui.NewTester(a.view, 1280, 700)
	tt.Frame()
	if err := tt.Click("Search host or path"); err != nil {
		t.Fatal(err)
	}
	tt.Type("sta")
	tt.Frame()
	if !a.queryOpen {
		t.Fatalf("the filter suggestion list did not open: %v", tt.Texts())
	}
	for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown} {
		tt.Key(0, key)
		tt.Frame()
		if a.query != "sta" {
			t.Fatalf("%v changed the filter text to %q instead of the suggestion", key, a.query)
		}
	}
	if a.filterHistory.cursor != len(a.filterHistory.entries) {
		t.Fatal("the filter history moved while the suggestion list was open")
	}
}

func TestFilterHistoryIgnoresASuggestionPick(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	tt := ui.NewTester(a.view, 1280, 700)
	tt.Frame()
	if err := tt.Click("Search host or path"); err != nil {
		t.Fatal(err)
	}
	tt.Type("sta")
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.query != "status:" {
		t.Fatalf("suggestion pick left query %q", a.query)
	}
	if len(a.filterHistory.entries) != 0 {
		t.Fatalf("picking a suggestion recorded a filter: %v", a.filterHistory.entries)
	}
}

func TestClearInputHistoryActionEmptiesBothHistories(t *testing.T) {
	a := &app{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	a.urlHistory.add("https://example.test")
	a.filterHistory.add("status:2xx")
	a.clearInputHistory()
	if len(a.urlHistory.entries) != 0 || len(a.filterHistory.entries) != 0 {
		t.Fatalf("histories not cleared: %v / %v", a.urlHistory.entries, a.filterHistory.entries)
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil || len(saved.URLHistory) != 0 || len(saved.FilterHistory) != 0 {
		t.Fatalf("cleared histories persisted = %v / %v, err %v", saved.URLHistory, saved.FilterHistory, err)
	}
	if locales.ClearInputHistory() == "" {
		t.Fatal("the clear action has no label")
	}
}
