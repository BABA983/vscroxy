package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
)

// loadLibraryRequests is what a restart reads back from the tree at root.
func loadLibraryRequests(root string) ([]savedRequest, error) {
	requests, _, _, err := loadLibrary(root)
	return requests, err
}

// libraryRoundTrip saves list as a library and reads it back, as a restart does.
func libraryRoundTrip(t *testing.T, list []savedRequest) []savedRequest {
	t.Helper()
	a := &app{library: libraryState{root: filepath.Join(t.TempDir(), "collections")}}
	if err := a.persistRequests(list); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// libraryTree lists the files under root, slash-separated.
func libraryTree(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(files)
	return files
}

func libraryApp(t *testing.T) *app {
	t.Helper()
	a := workspaceApp()
	a.requests, a.folders, a.documents, a.requestDocument = nil, nil, nil, nil
	a.library = libraryState{root: filepath.Join(t.TempDir(), "collections")}
	return a
}

func TestLibraryKeepsOneFilePerRequestAndFolder(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "b", Name: "Second: create?", Collection: "Shop", Method: "POST"}},
		{Request: request.Request{ID: "a", Name: "first", Collection: "Shop", Method: "GET"}},
		{Request: request.Request{ID: "c", Name: ".hidden-looking", Collection: "Shop/CON", Method: "GET"}},
		{Request: request.Request{ID: "d", Name: "Loose", Method: "GET"}},
		{Request: request.Request{ID: "e", Name: "first", Collection: "Shop/50% off", Method: "GET"}},
	}
	folders := []request.Folder{
		{Path: "Shop", Auth: request.Auth{Type: "Bearer", Token: "{{vault:t}}"}, Variables: []request.Variable{{Name: "base", Initial: "https://shop"}}},
		{Path: "Shop/CON", Pre: `console.log("con")`},
		{Path: "Empty", Description: "settings, no requests"},
	}
	if err := a.writeLibrary(requests, folders); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Empty/folder.json",
		"Loose.request.json",
		"Shop/%43ON/%2Ehidden-looking.request.json",
		"Shop/%43ON/folder.json",
		"Shop/50%25 off/first.request.json",
		"Shop/50%25 off/folder.json",
		"Shop/Second%3A create%3F.request.json",
		"Shop/first.request.json",
		"Shop/folder.json",
		"folder.json",
	}
	if got := libraryTree(t, a.library.root); !slices.Equal(got, want) {
		t.Fatalf("files =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	data, _ := os.ReadFile(filepath.Join(a.library.root, "Shop", "first.request.json"))
	if strings.Contains(string(data), `"collection"`) || !strings.Contains(string(data), "\n  \"name\": \"first\"") {
		t.Errorf("a request file names its collection or is not indented:\n%s", data)
	}
	loaded, loadedFolders, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range loaded {
		got = append(got, r.ID+"@"+r.Collection+"/"+r.Name)
	}
	// Order within a directory is the saved order, not the name order.
	if want := []string{"d@/Loose", "b@Shop/Second: create?", "a@Shop/first", "e@Shop/50% off/first", "c@Shop/CON/.hidden-looking"}; !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
	var paths []string
	for _, f := range loadedFolders {
		paths = append(paths, f.Path)
	}
	if slices.Sort(paths); !slices.Equal(paths, []string{"Empty", "Shop", "Shop/CON"}) {
		t.Errorf("folders with settings = %q", paths)
	}
	if i := slices.IndexFunc(loadedFolders, func(f request.Folder) bool { return f.Path == "Shop" }); loadedFolders[i].Auth.Token != "{{vault:t}}" || loadedFolders[i].Variables[0].Value() != "https://shop" {
		t.Errorf("collection settings = %+v", loadedFolders[i])
	}
}

func TestEscapedNamesRoundTrip(t *testing.T) {
	for _, name := range []string{"a/b", `C:\x`, "50%", "%41", "..", ". lead", "trail. ", "nul", "Com1.txt", "LPT", "tab\there", "∕ fine 中文", "?*|<>\""} {
		escaped := escapeName(name)
		if strings.ContainsAny(escaped, "/\\:*?\"<>|\t") || strings.HasPrefix(escaped, ".") || strings.HasSuffix(escaped, ".") || strings.HasSuffix(escaped, " ") {
			t.Errorf("escapeName(%q) = %q is not a portable file name", name, escaped)
		}
		if got := unescapeName(escaped); got != name {
			t.Errorf("unescapeName(escapeName(%q)) = %q", name, got)
		}
	}
	if got := unescapeName("100%-done %zz"); got != "100%-done %zz" {
		t.Errorf("a stray %% must stay literal: %q", got)
	}
}

// Renaming a collection moves its settings, requests and folders together,
// and open tabs follow it, so nothing is left behind under the old name.
func TestRenamingACollectionMovesEverythingInIt(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "1", Name: "List", Collection: "Shop", Method: "GET", Auth: request.Auth{Type: request.AuthInherit}}},
		{Request: request.Request{ID: "2", Name: "Get", Collection: "Shop/Orders", Method: "GET"}},
		{Request: request.Request{ID: "3", Name: "Other", Collection: "Shopping", Method: "GET"}},
	}
	folders := []request.Folder{{Path: "Shop", Auth: request.Auth{Type: "Basic", Username: "u"}}, {Path: "Shop/Orders", Pre: "1"}, {Path: "Shopping", Pre: "2"}}
	if err := a.writeLibrary(requests, folders); err != nil {
		t.Fatal(err)
	}
	a.addDocument(a.requests[1], a.requests[1].key())
	d := a.requestDocument
	a.runner.collection = "Shop/Orders"
	if err := a.renameFolder("Shop", "Store"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Shopping/Other.request.json", "Shopping/folder.json", "Store/List.request.json", "Store/Orders/Get.request.json", "Store/Orders/folder.json", "Store/folder.json"}
	if got := libraryTree(t, a.library.root); !slices.Equal(got, want) {
		t.Fatalf("files after rename = %q", got)
	}
	if d.composeCollection != "Store/Orders" || d.dirty() || a.runner.collection != "Store/Orders" {
		t.Errorf("the open tab or Runner did not follow: %q dirty %v, runner %q", d.composeCollection, d.dirty(), a.runner.collection)
	}
	if auth, from := a.inheritance("Store").InheritAuth(request.Auth{Type: request.AuthInherit}); auth.Username != "u" || from != "Store" {
		t.Errorf("settings stayed behind: %+v from %q", auth, from)
	}
	if err := a.renameFolder("Store/Orders", "Shopping"); err != nil {
		t.Errorf("a folder may share a name with a collection elsewhere: %v", err)
	}
	if err := a.renameFolder("Store", "Shopping"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("renaming onto an existing collection = %v", err)
	}
	if err := a.renameFolder("Store", "A/B"); err == nil {
		t.Error("a name with a slash was accepted")
	}
}

// Files someone else changed are never overwritten or deleted, files that
// are not the library's are left alone, and new request files appear on
// reload after the ones the saved order lists.
func TestLibraryLeavesOutsideChangesAlone(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "1", Name: "One", Collection: "Shop", Method: "GET"}},
		{Request: request.Request{ID: "2", Name: "Two", Collection: "Shop", Method: "GET"}},
	}
	if err := a.writeLibrary(requests, nil); err != nil {
		t.Fatal(err)
	}
	root := a.library.root
	os.WriteFile(filepath.Join(root, "Shop", "README.md"), []byte("notes"), 0o600)
	os.MkdirAll(filepath.Join(root, ".git"), 0o700)
	edited := []byte(`{"id":"1","name":"One","method":"PUT"}`)
	os.WriteFile(filepath.Join(root, "Shop", "One.request.json"), edited, 0o600)

	changed := slices.Clone(a.requests)
	changed[0].URL = "https://mine"
	if err := a.persistRequests(changed); err == nil || !strings.Contains(err.Error(), "changed outside vscroxy") {
		t.Fatalf("overwrote an outside edit: %v", err)
	}
	renamed := slices.Clone(a.requests)
	renamed[0].Name = "Uno"
	if err := a.persistRequests(renamed); err == nil {
		t.Fatal("deleted an outside edit by renaming")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "Shop", "One.request.json")); string(data) != string(edited) {
		t.Fatalf("the edited file changed: %s", data)
	}
	// A refused save touches nothing, not even files it could have written.
	if got := libraryTree(t, root); slices.Contains(got, "Shop/Uno.request.json") {
		t.Fatal("a refused save wrote part of its changes")
	}

	os.WriteFile(filepath.Join(root, "Shop", "Added by hand.request.json"), []byte(`{"name":"Added by hand","method":"GET"}`), 0o600)
	a.loadRequestLibrary()
	var names []string
	for _, r := range a.requests {
		names = append(names, r.Name+":"+r.Method)
	}
	if !slices.Equal(names, []string{"One:PUT", "Two:GET", "Added by hand:GET"}) || a.requests[2].ID == "" {
		t.Fatalf("after reload: %q, %+v", names, a.requests)
	}
	if err := a.persistRequests(a.requests[1:2]); err != nil {
		t.Fatal(err)
	}
	if got := libraryTree(t, root); !slices.Equal(got, []string{"Shop/README.md", "Shop/Two.request.json", "Shop/folder.json"}) {
		t.Errorf("after deleting two requests: %q", got)
	}
}

func TestLibraryLoadFailureInvalidatesPaletteIndex(t *testing.T) {
	a := libraryApp(t)
	if err := a.writeLibrary([]savedRequest{{Request: request.Request{ID: "visible", Name: "Visible", Method: "GET", URL: "https://api.example.com/visible"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := a.paletteSearchEntries("visible"); len(got) != 1 {
		t.Fatalf("initial palette search = %+v", got)
	}
	gen := a.requestGen
	path := filepath.Join(a.library.root, "Visible.request.json")
	if err := os.WriteFile(path, []byte("{damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadRequestLibrary()
	if a.requestsProblem == "" || a.requestGen == gen {
		t.Fatalf("load failure did not invalidate the index: problem=%q gen=%d", a.requestsProblem, a.requestGen)
	}
	if got := a.paletteSearchEntries("visible"); len(got) != 0 {
		t.Fatalf("palette still lists removed requests: %+v", got)
	}
}

func TestLibraryRefusesNamesOnlyLetterCaseApart(t *testing.T) {
	a := libraryApp(t)
	err := a.writeLibrary([]savedRequest{
		{Request: request.Request{Name: "a", Collection: "Shop/Orders"}},
		{Request: request.Request{Name: "b", Collection: "shop"}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "letter case") {
		t.Fatalf("err = %v", err)
	}
	// Requests are not paths: two requests a case apart get distinct files.
	if err := a.writeLibrary([]savedRequest{
		{Request: request.Request{ID: "1", Name: "List", Collection: "Shop"}},
		{Request: request.Request{ID: "2", Name: "list", Collection: "Shop"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if got := libraryTree(t, a.library.root); !slices.Equal(got, []string{"Shop/List.request.json", "Shop/folder.json", "Shop/list (2).request.json"}) {
		t.Errorf("files = %q", got)
	}
}

// Reloading shows outside changes in clean tabs, so saving one cannot undo
// them; a tab with unsaved edits keeps its edits.
func TestReloadRefreshesCleanTabs(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "1", Name: "Clean", Collection: "Shop", Method: "GET", URL: "https://one"}},
		{Request: request.Request{ID: "2", Name: "Edited", Collection: "Shop", Method: "GET", URL: "https://two"}},
	}
	if err := a.writeLibrary(requests, nil); err != nil {
		t.Fatal(err)
	}
	a.addDocument(a.requests[0], a.requests[0].key())
	clean := a.requestDocument
	a.addDocument(a.requests[1], a.requests[1].key())
	edited := a.requestDocument
	edited.composeURL = "https://mine"
	for name, url := range map[string]string{"Clean": "https://pulled-one", "Edited": "https://pulled-two"} {
		file := filepath.Join(a.library.root, "Shop", name+".request.json")
		data, _ := os.ReadFile(file)
		os.WriteFile(file, []byte(strings.Replace(string(data), "https://"+map[string]string{"Clean": "one", "Edited": "two"}[name], url, 1)), 0o600)
	}
	a.loadRequestAssets()
	if clean.composeURL != "https://pulled-one" || clean.dirty() {
		t.Errorf("clean tab shows %q, dirty %v", clean.composeURL, clean.dirty())
	}
	if edited.composeURL != "https://mine" || !edited.dirty() || !strings.Contains(a.message, "1 open tab has unsaved changes") {
		t.Errorf("edited tab shows %q, message %q", edited.composeURL, a.message)
	}
	a.requestDocument = clean
	if !a.saveComposed() {
		t.Fatal(a.message)
	}
	if got, _ := loadLibraryRequests(a.library.root); got[0].URL != "https://pulled-one" {
		t.Errorf("saving the clean tab undid the outside change: %q", got[0].URL)
	}
}

// Descriptions and transport settings are part of the request and folder
// files, so a restart reads back exactly what was saved.
func TestDescriptionAndSettingsSurviveDisk(t *testing.T) {
	a := libraryApp(t)
	timeout, max := 12, 3
	verify, follow := false, false
	wantRequest := savedRequest{Request: request.Request{
		ID: "one", Name: "One", Collection: "Docs", Method: "GET", URL: "https://example.com",
		Description: "# Notes\n\nHello.",
		Settings: request.Settings{
			TimeoutSeconds:  &timeout,
			FollowRedirects: &follow,
			MaxRedirects:    &max,
			VerifyTLS:       &verify,
		},
	}}
	wantFolder := request.Folder{Path: "Docs", Description: "Collection docs", Settings: request.Settings{VerifyTLS: &verify}}
	a.folders = []request.Folder{wantFolder}
	if err := a.persistRequests([]savedRequest{wantRequest}); err != nil {
		t.Fatal(err)
	}
	requests, folders, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || !reflect.DeepEqual(requests[0], wantRequest) {
		t.Fatalf("request = %+v, want %+v", requests, wantRequest)
	}
	if len(folders) != 1 || !reflect.DeepEqual(folders[0], wantFolder) {
		t.Fatalf("folder = %+v, want %+v", folders, wantFolder)
	}
}

// An explicit request timeout of 0 removes the editor's 30-second budget
// instead of falling back to a folder's longer limit.
func TestSendBudgetHonorsExplicitUnlimited(t *testing.T) {
	a := libraryApp(t)
	folderTimeout, zero := 600, 0
	a.folders = []request.Folder{{Path: "Docs", Settings: request.Settings{TimeoutSeconds: &folderTimeout}}}
	if got := a.sendBudget(savedRequest{Request: request.Request{Collection: "Docs"}}); got != 602*time.Second {
		t.Fatalf("folder budget = %s, want the 600s limit plus a moment", got)
	}
	if got := a.sendBudget(savedRequest{Request: request.Request{Collection: "Docs", Settings: request.Settings{TimeoutSeconds: &zero}}}); got != 0 {
		t.Fatalf("explicit unlimited budget = %s, want no editor deadline", got)
	}
	if got := a.sendBudget(savedRequest{Request: request.Request{}}); got != 30*time.Second {
		t.Fatalf("default budget = %s, want 30s", got)
	}
}

func TestLibraryDoesNotRewriteUnchangedSchemaFiles(t *testing.T) {
	a := libraryApp(t)
	big := strings.Repeat("x", 1<<20)
	requests := []savedRequest{
		{Request: request.Request{ID: "gql", Name: "GraphQL", Method: "POST", URL: "https://api.example.com/graphql",
			BodyMode: "GraphQL", GraphQL: request.GraphQLBody{Query: "{ viewer { id } }", Schema: big}}},
		{Request: request.Request{ID: "other", Name: "Other", Method: "GET", URL: "https://api.example.com/other"}},
	}
	if err := a.persistRequests(requests); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.library.root, "GraphQL.request.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := slices.Clone(requests)
	changed[1].URL = "https://api.example.com/changed"
	if err := a.persistRequests(changed); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("saving another request rewrote the file holding a 1 MiB schema")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), big) {
		t.Fatalf("schema was not saved once: %v", err)
	}
}

func TestLibraryRoundTripsMCPDocument(t *testing.T) {
	want := savedRequest{Request: request.Request{
		ID: "mcp", Name: "Debug server", Collection: "Workspace", Protocol: "MCP", Method: "POST",
		URL: "https://mcp.example.com/mcp", Headers: "X-Trace: 1",
		Auth: request.Auth{Type: "Bearer", Token: "{{token}}"},
		MCP: request.MCPConfig{
			Transport: request.MCPTransportStdio, Command: "/usr/local/bin/mcp-demo",
			Args: "-y\nserver\n/tmp", WorkingDir: "/tmp",
			Env:  []request.Field{{Name: "TOKEN", Value: "{{token}}"}, {Name: "OFF", Value: "1", Disabled: true}},
			Tool: "echo", ToolArgs: `{"text":"hi"}`, ResourceURI: "memo://welcome",
			Prompt: "greet", PromptArgs: `{"name":"Ada"}`, TimeoutSeconds: 45, ProtocolVersion: "2025-11-25",
		},
	}}
	got := libraryRoundTrip(t, []savedRequest{want})
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("MCP document changed: got %+v, want %+v", got, want)
	}
}

func TestProjectFolderKeepsTheLibraryInsideTheProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := libraryApp(t)
	a.settingsPath = filepath.Join(home, ".vscroxy", "settings.json")
	project := t.TempDir()

	a.setProjectDir(project)
	if a.library.root != filepath.Join(project, ".vscroxy", "collections") || a.projectDir != project {
		t.Fatalf("project folder not used: root %q, projectDir %q", a.library.root, a.projectDir)
	}
	if err := a.writeLibrary([]savedRequest{{Request: request.Request{ID: "one", Name: "One", Method: "GET", URL: "https://api.example.com/one"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".vscroxy", "collections", "One.request.json")); err != nil {
		t.Fatalf("request not written into the project: %v", err)
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil || saved.ProjectDir != project {
		t.Fatalf("project folder not saved: %q, %v", saved.ProjectDir, err)
	}

	missing := filepath.Join(project, "missing")
	a.setProjectDir(missing)
	if a.projectDir != project || len(a.requests) != 1 {
		t.Fatalf("a missing folder changed the library: projectDir %q, %d requests", a.projectDir, len(a.requests))
	}

	a.setProjectDir("")
	if a.projectDir != "" || a.library.root != filepath.Join(home, ".vscroxy", "collections") {
		t.Fatalf("default not restored: root %q, projectDir %q", a.library.root, a.projectDir)
	}
	if len(a.requests) != 0 {
		t.Fatalf("default library should be empty here, got %d requests", len(a.requests))
	}
}

func TestProjectFolderHoldsOpenAPISpecsAndAddons(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := libraryApp(t)
	a.settingsPath = filepath.Join(home, ".vscroxy", "settings.json")
	a.envsPath = filepath.Join(home, ".vscroxy", "env.json")
	project := t.TempDir()
	addons := filepath.Join(project, ".vscroxy", "addons")
	if err := os.MkdirAll(addons, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(addons, "shared.js"), []byte("export const shared = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a.setProjectDir(project)
	if a.addons.dir != addons || !slices.Contains(a.addons.names, "shared") {
		t.Fatalf("addons not read from the project: dir %q, names %v", a.addons.dir, a.addons.names)
	}

	spec := `{"openapi":"3.0.3","info":{"title":"Shop","version":"1"},"paths":{}}`
	a.addAPI(apiSpec{Source: spec}, false)
	a.saveAPI(a.activeAPI())
	apis := filepath.Join(project, ".vscroxy", "apis.json")
	if _, err := os.Stat(apis); err != nil {
		t.Fatalf("spec not saved into the project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".vscroxy", "apis.json")); err == nil {
		t.Fatal("spec also saved under the home folder")
	}

	// A spec file changed outside the app is not replaced by the next save.
	outside := []byte("[]\n")
	if err := os.WriteFile(apis, outside, 0o600); err != nil {
		t.Fatal(err)
	}
	a.saveAPI(a.activeAPI())
	if data, _ := os.ReadFile(apis); string(data) != string(outside) {
		t.Fatalf("the spec file changed outside the app was overwritten: %s", data)
	}
}
