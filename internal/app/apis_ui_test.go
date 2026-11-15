package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The endpoint reader answers what an operation takes and returns before it
// offers the raw operation object. This test names each section and the
// referenced schema fields a person needs to read a contract.
func TestAPIEndpointReaderShowsParametersBodyAndResponses(t *testing.T) {
	a := snapshotApp()
	a.page = pageAPI
	a.addAPI(apiSpec{ID: "reader-api", Source: referencedOpenAPI}, true)
	a.activeAPI().endpoint = "POST /widgets/{id}"
	tt := ui.NewTester(a.view, 1280, 1000)
	tt.SetLocale("en-US")
	tt.Frame()
	for _, want := range []string{
		"Parameters", "Path parameters", "Query parameters", "Header parameters",
		"limit", "X-Trace", "Required", "Request body", "application/json",
		"Payload", "count", "Responses", "201",
	} {
		if !tt.HasText(want) {
			t.Fatalf("endpoint reader is missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("Operation schema") {
		t.Fatal("the raw operation JSON is still the primary reader")
	}
	if err := tt.Click("API endpoint: Raw"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("Operation schema"); !ok {
		t.Fatal("the raw operation JSON is not available behind its tab")
	}
}

func TestVariablesShowNormalCurrentValuesAndMaskOnlySecrets(t *testing.T) {
	a := snapshotApp()
	a.envs = []environment{{
		Name:        "Local",
		Vars:        map[string]string{"baseUrl": "https://api.example.com", "projectId": "73", "apiToken": "replace-me"},
		Current:     map[string]string{"projectId": "81", "apiToken": "local-token"},
		Credentials: map[string]bool{"apiToken": true},
	}}
	a.activeEnv = "Local"
	a.openVars()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if !tt.HasText("Environment") || !tt.HasText("Environment name") {
		t.Fatal("the environment selector and name field have no visible labels")
	}
	if !(variableRow{Name: "apiToken"}).secret() || (variableRow{Name: "projectId"}).secret() {
		t.Fatal("the secret test does not distinguish a token from a project id")
	}
	if _, ok := tt.Find("Reveal variable 1"); !ok {
		t.Fatal("the credential row has no reveal control")
	}
	if _, ok := tt.Find("Reveal variable 3"); ok {
		t.Fatal("a normal row has a reveal control")
	}
	if err := tt.Click("Variable current value 3"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "81" {
		t.Fatalf("a normal current value is hidden: %q", tt.Clipboard())
	}
	if err := tt.Click("Reveal variable 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Variable current value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "local-token" {
		t.Fatalf("revealing a credential did not show its value: %q", tt.Clipboard())
	}
}

func TestAPIEndpointReaderKeepsDescriptionsWhole(t *testing.T) {
	a := snapshotApp()
	a.page = pageAPI
	a.addAPI(apiSpec{ID: "reader-api", Source: strings.Replace(referencedOpenAPI,
		"count: {type: [integer, 'null']}",
		"count: {type: [integer, 'null'], description: 'How many widgets the response contains.'}", 1)}, true)
	a.activeAPI().endpoint = "POST /widgets/{id}"
	tt := ui.NewTester(a.view, 1280, 1000)
	tt.SetLocale("en-US")
	tt.Frame()
	if !tt.HasText("How many widgets the response contains.") {
		t.Fatal("a schema description was truncated")
	}
}
