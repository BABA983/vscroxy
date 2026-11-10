package app

import (
	"slices"
	"testing"
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// The header menu is the table's column manager: the traffic's headers become
// columns in one click, a column hides without losing its definition, and the
// reset puts the default table back. Submenu items are chosen through the
// tester's menu tree: a drawn submenu of a table header is laid out in more
// than one place, so clicking its label by rectangle picks the wrong one.
func TestHeaderMenuAddsATrafficHeaderColumnAndHidesIt(t *testing.T) {
	a := workspaceApp()
	a.flows[0].reqHeader.Set("X-Trace", "trace-1")
	tt := ui.NewTester(a.view, 1920, 900)
	tt.Frame()
	column := customColumn{Source: sourceRequestHeader, Name: "X-Trace"}
	open := func() {
		t.Helper()
		if err := tt.RightClick("Host"); err != nil {
			t.Fatal(err)
		}
	}
	open()
	if err := tt.ChooseMenuItem("Request headers", "X-Trace"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.customColumns, []customColumn{column}) {
		t.Fatalf("custom columns = %+v, want the chosen header", a.customColumns)
	}
	if !tt.HasText("X-Trace (request)") {
		t.Fatalf("the added column is not in the table: %v", tt.Texts())
	}
	// The same header again hides its column and keeps the definition, so a
	// column is never lost by unchecking it.
	open()
	if err := tt.ChooseMenuItem("Request headers", "X-Trace"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.customColumns, []customColumn{column}) || !slices.Contains(a.hiddenColumns, column.id()) {
		t.Fatalf("custom columns %+v, hidden %v", a.customColumns, a.hiddenColumns)
	}
	if tt.HasText("X-Trace (request)") {
		t.Fatal("a hidden column is still in the table")
	}
	if slices.ContainsFunc(a.captureColumns(), func(col captureColumn) bool { return col.id == column.id() }) {
		t.Fatal("CSV export kept a hidden column")
	}
	// The column's own header still removes it for good.
	open()
	if err := tt.ChooseMenuItem("Request headers", "X-Trace"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.RightClick("X-Trace (request)"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Remove “X-Trace (request)” column"); err != nil {
		t.Fatal(err)
	}
	if len(a.customColumns) != 0 {
		t.Fatalf("custom columns = %+v after removing the column", a.customColumns)
	}
}

func TestHeaderMenuResetRestoresTheDefaultTable(t *testing.T) {
	a := workspaceApp()
	a.hiddenColumns = []string{"status"}
	a.customColumns = []customColumn{{Source: sourceRequestHeader, Name: "X-Trace"}}
	a.columns.Order = []string{"method", "id"}
	tt := ui.NewTester(a.view, 1920, 900)
	tt.Frame()
	if err := tt.RightClick("Host"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Reset all columns"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.hiddenColumns) != 0 || len(a.customColumns) != 0 || len(a.columns.Order) != 0 {
		t.Fatalf("after the reset: hidden %v, custom %+v, order %v", a.hiddenColumns, a.customColumns, a.columns.Order)
	}
	number, _ := tt.Find("#")
	status, _ := tt.Find("Status")
	method, _ := tt.Find("Method")
	if number.X >= status.X || status.X >= method.X {
		t.Fatalf("the reset did not restore the default column order: # at %v, Status at %v, Method at %v", number.X, status.X, method.X)
	}
}

// A column read from a body shows its values again once the table shows it,
// rather than staying blank because its cells were never read while hidden.
func TestHeaderMenuReadsABodyColumnShownAgain(t *testing.T) {
	a := workspaceApp()
	a.flows[0].resBody = `{"data":{"user":{"id":"u-1"}}}`
	column := customColumn{Source: sourceResponseJSON, Name: "/data/user/id"}
	a.customColumns = []customColumn{column}
	a.hiddenColumns = []string{column.id()}
	tt := ui.NewTester(a.view, 1440, 860)
	tt.Frame()
	if err := tt.RightClick("Host"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("/data/user/id (response JSON)"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); a.columnRead.dirty || a.columnRead.answers != nil || len(a.columnRead.queue) > 0; {
		if time.Now().After(deadline) {
			t.Fatal("the column did not finish reading its body")
		}
		tt.Frame()
	}
	tt.Frame()
	if !tt.HasText("u-1") {
		t.Fatalf("the column shown again has no value: %v", tt.Texts())
	}
}

// A credential header would put a token on screen, so the menu leaves it to
// Add column…, where it is added by name.
func TestHeaderMenuLeavesCredentialHeadersToAddColumn(t *testing.T) {
	a := workspaceApp()
	a.flows[0].reqHeader.Set("Authorization", "Bearer secret")
	var headers action
	for _, x := range a.columnActions("") {
		if x.label == locales.RequestHeaders() {
			headers = x
		}
	}
	if headers.items == nil {
		t.Fatal("the header menu has no Request headers submenu")
	}
	var offered []string
	for _, x := range headers.items {
		offered = append(offered, x.label)
	}
	if slices.Contains(offered, "Authorization") {
		t.Fatalf("credential header offered as a column: %v", offered)
	}
	if !slices.Contains(offered, "Accept") {
		t.Fatalf("ordinary headers missing from the submenu: %v", offered)
	}
	if !slices.Contains(offered, locales.CredentialHeadersAreAddedByName()) {
		t.Fatalf("the submenu does not say where credential headers come from: %v", offered)
	}
}
