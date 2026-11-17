package app

import (
	"net/http"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestDiffPoolComparesAnyTwoOfMoreThanTwoCaptures(t *testing.T) {
	a := workspaceApp()
	a.flows = append(a.flows, flow{id: 3, method: "GET", scheme: "https", host: "api.example.com", path: "/third",
		url: "https://api.example.com/third", status: 200, respHeader: http.Header{"Content-Type": {"application/json"}}, resBody: `{"third":true}`})
	a.applyFilter()
	for _, id := range []int{1, 2, 3} {
		a.chosen.Add(id)
	}
	tt := ui.NewTester(a.view, 1400, 900)
	if err := tt.Click("Diff pool"); err != nil {
		t.Fatal(err)
	}
	if a.analysis.tab != 2 || len(a.analysis.pool) != 3 {
		t.Fatalf("pool: tab=%d ids=%v", a.analysis.tab, a.analysis.pool)
	}
	if !tt.HasText("#3 GET /third") {
		t.Fatalf("pool chips missing: %q", tt.Texts())
	}
	a.analysis.poolLeft, a.analysis.poolRight = "#3 GET /third", "#1 GET /projects"
	tt.Frame()
	if a.analysis.pair != [2]int{3, 1} {
		t.Fatalf("compared %v, want the pool's chosen sides", a.analysis.pair)
	}
	if err := tt.Click(locales.RemoveFromPool()); err != nil {
		t.Fatal(err)
	}
	if len(a.analysis.pool) != 2 {
		t.Fatalf("remove left %v", a.analysis.pool)
	}
}
