package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

// imported is what a test compares of a saved request; the ID is new and
// random, so only its presence is checked.
type imported struct {
	Collection, Name, Method, URL, Headers, Body string
	Auth                                         request.Auth
}

func importedRequests(t *testing.T, list []savedRequest) []imported {
	t.Helper()
	var out []imported
	for _, r := range list {
		if r.ID == "" {
			t.Errorf("request %q has no ID", r.Name)
		}
		out = append(out, imported{r.Collection, r.Name, r.Method, r.URL, r.Headers, r.Body, r.Auth})
	}
	return out
}

const harFixture = `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[
{"startedDateTime":"2026-10-01T10:00:00Z","time":12,"request":{"method":"GET","url":"https://api.example.com/users?page=1","httpVersion":"HTTP/2",
 "headers":[{"name":":authority","value":"api.example.com"},{"name":"Authorization","value":"Bearer abc123"},{"name":"Accept","value":"application/json"}]},
 "response":{"status":200,"content":{"size":2,"mimeType":"application/json","text":"[]"}}},
{"startedDateTime":"2026-10-01T10:00:01Z","time":5,"request":{"method":"CONNECT","url":"api.example.com:443","headers":[]},"response":{"status":200,"content":{}}},
{"startedDateTime":"2026-10-01T10:00:02Z","time":30,"request":{"method":"POST","url":"https://api.example.com/users","headers":[
 {"name":"Content-Type","value":"application/json"},{"name":"Content-Length","value":"14"},{"name":"Connection","value":"keep-alive"}],
 "postData":{"mimeType":"application/json","text":"{\"name\":\"Ada\"}"}},"response":{"status":201,"content":{}}},
{"startedDateTime":"2026-10-01T10:00:03Z","time":8,"request":{"method":"POST","url":"https://cdn.example.org/upload","headers":[{"name":"Content-Type","value":"application/octet-stream"}],
 "postData":{"mimeType":"application/octet-stream","text":"//4A","encoding":"base64"}},"response":{"status":204,"content":{}}},
{"startedDateTime":"2026-10-01T10:00:04Z","time":9,"request":{"method":"GET","url":"https://api.example.com/users?page=2","headers":[]},"response":{"status":200,"content":{}}},
{"startedDateTime":"2026-10-01T10:00:05Z","time":3,"request":{"method":"GET","url":"wss://api.example.com/live","headers":[]},"response":{"status":101,"content":{}}},
{"startedDateTime":"2026-10-01T10:00:06Z","time":4,"request":{"method":"GET","url":"https://cdn.example.org/logo.png","headers":[]},"response":{"status":200,"content":{}}}
]}}`

func TestImportHARMakesACollectionLikeSaveAsRequest(t *testing.T) {
	a := libraryApp(t)
	a.requests = []savedRequest{{Request: request.Request{ID: "old", Name: "Kept", Collection: "traffic", Method: "GET", URL: "https://example.com"}}}
	a.importData([]byte(harFixture), "/tmp/traffic.har")
	want := []imported{
		{Collection: "traffic", Name: "Kept", Method: "GET", URL: "https://example.com"},
		{Collection: "traffic 2", Name: "GET api.example.com/users", Method: "GET", URL: "https://api.example.com/users?page=1",
			Headers: "Accept: application/json", Auth: request.Auth{Type: "Bearer", Token: "abc123"}},
		{Collection: "traffic 2", Name: "POST api.example.com/users", Method: "POST", URL: "https://api.example.com/users",
			Headers: "Content-Type: application/json", Body: `{"name":"Ada"}`},
		{Collection: "traffic 2", Name: "GET api.example.com/users (2)", Method: "GET", URL: "https://api.example.com/users?page=2"},
		{Collection: "traffic 2", Name: "GET cdn.example.org/logo.png", Method: "GET", URL: "https://cdn.example.org/logo.png"},
	}
	if got := importedRequests(t, a.requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests\n got %+v\nwant %+v", got, want)
	}
	if !a.importReport.open || a.importReport.title != "Imported “traffic 2” · 4 requests" {
		t.Fatalf("report %+v, message %q", a.importReport, a.message)
	}
	wantWarnings := []string{
		"1 entry skipped: CONNECT tunnel, no HTTP request to save",
		"1 entry skipped: non-UTF-8 request bodies cannot be opened in the text request editor",
		"1 entry skipped: WebSocket connection",
	}
	if !reflect.DeepEqual(a.importReport.lines, wantWarnings) {
		t.Errorf("warnings %q", a.importReport.lines)
	}
	// The library on disk holds them after a restart.
	reloaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 5 {
		t.Errorf("reloaded %d requests", len(reloaded))
	}
}

func TestImportHARLimitsEntries(t *testing.T) {
	a := libraryApp(t)
	entry := `{"request":{"method":"GET","url":"https://api.example.com/n","headers":[]},"response":{"status":200,"content":{}}}`
	data := `{"log":{"entries":[` + strings.Repeat(entry+",", maxHAREntries+2) + entry + `]}}`
	a.importData([]byte(data), "many.har")
	if len(a.requests) != maxHAREntries || a.requests[maxHAREntries-1].Name != "GET api.example.com/n (1000)" {
		t.Fatalf("%d requests", len(a.requests))
	}
	if !reflect.DeepEqual(a.importReport.lines, []string{"3 entries beyond the first 1000 were not imported"}) {
		t.Errorf("warnings %q", a.importReport.lines)
	}
}

const openAPIFixture = `openapi: 3.0.3
info:
  title: Pet Store
  version: "1.0"
servers:
  - url: https://petstore.example.com/v1
paths:
  /pets:
    get:
      tags: [pets]
      summary: List pets
      responses: {"200": {description: ok}}
    post:
      tags: [pets]
      operationId: createPet
      responses: {"201": {description: created}}
  /pets/{petId}:
    get:
      tags: [pets, admin]
      parameters:
        - {name: petId, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: ok}}
  /stores:
    get:
      tags: [stores]
      summary: List pets
      responses: {"200": {description: ok}}
  /health:
    get:
      responses: {"200": {description: ok}}
`

func TestImportOpenAPIMakesACollectionWithAFolderPerTag(t *testing.T) {
	a := libraryApp(t)
	a.importData([]byte(openAPIFixture), "petstore.yaml")
	base := "https://petstore.example.com/v1"
	want := []imported{
		{Collection: "Pet Store", Name: "GET /health", Method: "GET", URL: base + "/health"},
		{Collection: "Pet Store/pets", Name: "List pets", Method: "GET", URL: base + "/pets"},
		{Collection: "Pet Store/pets", Name: "createPet", Method: "POST", URL: base + "/pets"},
		{Collection: "Pet Store/pets", Name: "GET /pets/{petId}", Method: "GET", URL: base + "/pets/{{petId}}"},
		{Collection: "Pet Store/stores", Name: "List pets", Method: "GET", URL: base + "/stores"},
	}
	if got := importedRequests(t, a.requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests\n got %+v\nwant %+v", got, want)
	}
	if a.message != "Imported “Pet Store” · 5 requests" || len(a.folders) != 0 {
		t.Errorf("message %q, folders %+v", a.message, a.folders)
	}
	// Importing it again makes a second collection; the API editor's
	// button does the same for the contract it shows.
	a.importData([]byte(openAPIFixture), "petstore.yaml")
	if len(a.requests) != 10 || a.requests[9].Collection != "Pet Store 2/stores" {
		t.Errorf("second import %+v", importedRequests(t, a.requests[5:]))
	}
}

func TestImportOpenAPIWithoutServerAddsBaseURLVariable(t *testing.T) {
	a := libraryApp(t)
	a.importData([]byte(`{"openapi":"3.1.0","info":{"title":"Tiny","version":"1"},"paths":{"/ping":{"get":{"tags":["ops"],"responses":{"200":{"description":"ok"}}}}}}`), "tiny.json")
	if got := importedRequests(t, a.requests); !reflect.DeepEqual(got, []imported{{Collection: "Tiny/ops", Name: "GET /ping", Method: "GET", URL: "{{base_url}}/ping"}}) {
		t.Fatalf("requests %+v", got)
	}
	if !reflect.DeepEqual(a.folders, []request.Folder{{Path: "Tiny", Variables: []request.Field{{Name: "base_url"}}}}) {
		t.Errorf("folders %+v", a.folders)
	}
	if _, ok := a.inheritance("Tiny/ops").Values(nil)["base_url"]; !ok {
		t.Errorf("base_url is not a collection variable: %+v", a.folders)
	}
}

func TestImportRejectsUnknownFilesListingEveryKind(t *testing.T) {
	a := libraryApp(t)
	a.importData([]byte(`{"swagger":"2.0"}`), "old.json")
	if !strings.HasPrefix(a.message, "Import: not a Postman collection, environment or globals file, a HAR file, an OpenAPI 3.0/3.1 document or a vscroxy request list") || len(a.requests) != 0 {
		t.Errorf("message %q, requests %d", a.message, len(a.requests))
	}
}

func TestSaveChosenRowsToCollection(t *testing.T) {
	a := libraryApp(t)
	a.flows = append(a.flows,
		flow{id: 3, method: "CONNECT", scheme: "https", host: "api.example.com:443", url: "https://api.example.com:443", status: 200, kind: "connect"},
		flow{id: 4, method: "GET", scheme: "https", host: "cdn.example.org", path: "/app.js?v=2", url: "https://cdn.example.org/app.js?v=2", status: 200,
			reqHeader: map[string][]string{"Authorization": {"Bearer xyz"}}},
	)
	a.applyFilter()
	for id := range 4 {
		a.chosen.Add(id + 1)
	}
	tt := ui.NewTester(a.view, 1200, 760)
	if err := tt.RightClick("api.example.com/login"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Save 4 requests to collection…"); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	if !tt.HasText("Save 4 requests to collection") || !a.bulkSaveUI.perHost || a.bulkSaveUI.name != "Captured api.example.com" {
		t.Fatalf("dialog %+v", a.bulkSaveUI)
	}
	if err := tt.Click("Collection name"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Shop")
	if err := tt.Click("Save requests"); err != nil {
		t.Fatal(err)
	}
	want := []imported{
		{Collection: "Shop/api.example.com", Name: "GET api.example.com/projects", Method: "GET", URL: "https://api.example.com/projects", Headers: "Accept: application/json"},
		{Collection: "Shop/api.example.com", Name: "POST api.example.com/login", Method: "POST", URL: "https://api.example.com/login"},
		{Collection: "Shop/cdn.example.org", Name: "GET cdn.example.org/app.js", Method: "GET", URL: "https://cdn.example.org/app.js?v=2", Auth: request.Auth{Type: "Bearer", Token: "xyz"}},
	}
	if got := importedRequests(t, a.requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests\n got %+v\nwant %+v", got, want)
	}
	if a.requests[0].Source == nil {
		t.Error("the capture source was not kept, as Save as request keeps it")
	}
	if a.bulkSaveUI.open || !a.importReport.open || a.importReport.title != "Saved 3 requests to “Shop”; not sent" ||
		!reflect.DeepEqual(a.importReport.lines, []string{"1 row skipped: CONNECT tunnel, no HTTP request to save"}) {
		t.Errorf("report %+v", a.importReport)
	}
	// Saving the same rows again appends to the collection, each name kept
	// unique within its folder.
	a.saveFlowsToCollection(a.selectedFlows()[:1], "Shop", false)
	a.saveFlowsToCollection(a.selectedFlows()[:1], "Shop", true)
	if n := len(a.requests); n != 5 || a.requests[3].Collection != "Shop" || a.requests[3].Name != "GET api.example.com/projects" ||
		a.requests[4].Name != "GET api.example.com/projects (2)" {
		t.Errorf("appended %+v", importedRequests(t, a.requests[3:]))
	}
}
