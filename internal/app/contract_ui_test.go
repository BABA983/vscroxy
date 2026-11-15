package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

const contractUISpec = `openapi: 3.0.3
info: {title: Projects, version: 1.0.0}
servers: [{url: https://api.example.com}]
paths:
  /projects:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [projects]
                properties:
                  projects: {type: array, items: {type: object}}
`

func contractApp() *app {
	a := workspaceApp()
	a.addAPI(apiSpec{ID: "contract-api", Source: contractUISpec}, true)
	a.inspect.ready = true
	a.inspect.tab[1] = resViewContract
	a.pickedID = a.flows[0].id
	return a
}

func TestInspectorContractView(t *testing.T) {
	a := contractApp()
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Matches the saved contract.") {
		t.Fatalf("clean exchange: %v", tt.Texts())
	}

	a.flows[0].status = 500
	a.contractCache = nil
	tt = ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("status is not supported") || !tt.HasText("1 drift finding") {
		t.Fatalf("drift missing: %v", tt.Texts())
	}

	a.flows[0].url, a.flows[0].path = "https://api.example.com/unknown", "/unknown"
	a.contractCache = nil
	tt = ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("No saved contract serves this exchange.") {
		t.Fatalf("uncovered exchange: %v", tt.Texts())
	}
}
