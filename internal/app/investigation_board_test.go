package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestInvestigationBoardKeepsImmutableSnapshotsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "investigations.json")
	a := workspaceApp()
	if err := a.board.load(path); err != nil {
		t.Fatal(err)
	}
	a.flows[0].resBody = `{"live":1}`
	a.flows[0].reqHeader.Set("Authorization", "Bearer captured-secret")
	inv, err := a.board.add(a.flows[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Items) != 1 || inv.Title != "Untitled investigation" {
		t.Fatalf("first capture did not start an investigation: %+v", inv)
	}
	// Live traffic and headers move on; the investigation's evidence does not.
	a.flows[0].resBody = `{"live":2}`
	a.flows[0].reqHeader.Set("Authorization", "Bearer changed")
	if got := inv.Items[0].Source.ResBody; got != `{"live":1}` {
		t.Fatalf("the snapshot followed live traffic: %q", got)
	}
	if got := inv.Items[0].Source.ReqHeader.Get("Authorization"); got != "Bearer captured-secret" {
		t.Fatalf("the snapshot followed live headers: %q", got)
	}
	inv.Title, inv.Notes = "Checkout regression", "422 on quantity 0; replay returned 201"
	if err := a.board.save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("board file permissions: %v, %v", info, err)
	}
	reloaded := &investigationBoard{}
	if err := reloaded.load(path); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.items) != 1 || reloaded.items[0].Title != "Checkout regression" ||
		reloaded.items[0].Notes != "422 on quantity 0; replay returned 201" {
		t.Fatalf("title or notes did not survive the restart: %+v", reloaded.items)
	}
	item := reloaded.items[0].Items[0]
	if item.Source.ResBody != `{"live":1}` || item.Source.ReqHeader.Get("Authorization") != "Bearer captured-secret" {
		t.Fatal("reopening the board lost the captured evidence")
	}
	// Removing an exchange is explicit and persists.
	reloaded.removeItem(item.ID)
	if len(reloaded.items[0].Items) != 0 {
		t.Fatal("removed exchange stayed in the investigation")
	}
	again := &investigationBoard{}
	if err := again.load(path); err != nil || len(again.items[0].Items) != 0 {
		t.Fatalf("removal did not persist: %v %+v", err, again.items)
	}
}

func TestInvestigationBoardAddsFromCaptureComparesAndReviewsEvidence(t *testing.T) {
	a := workspaceApp()
	a.board.path = filepath.Join(t.TempDir(), "investigations.json")
	tt := ui.NewTester(a.view, 1440, 860)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	add := func() {
		t.Helper()
		click("Inspector options")
		if err := tt.ChooseMenuItem("Add to investigation…"); err != nil {
			t.Fatal(err)
		}
	}
	add()
	if len(a.board.items) != 1 || len(a.board.items[0].Items) != 1 {
		t.Fatal("adding from the inspector did not reach the board")
	}
	click("Capture")
	click("api.example.com/login")
	add()
	if len(a.board.items[0].Items) != 2 {
		t.Fatal("a second capture was not added to the same investigation")
	}
	click("Untitled investigation")
	if !tt.HasText("GET https://api.example.com/projects") || !tt.HasText("POST https://api.example.com/login") {
		t.Fatalf("the board did not list both exchanges: %q", tt.Texts())
	}
	click("Choose POST https://api.example.com/login")
	click("Choose GET https://api.example.com/projects")
	click("Compare two")
	if !tt.HasText("Compare two exchanges") || !tt.HasText("Back to evidence") {
		t.Fatal("the comparison did not open on the chosen exchanges")
	}
	click("Back to evidence")
	click("Export evidence…")
	if !tt.HasText("RAW EVIDENCE · NOT AUTOMATICALLY SANITIZED") || !tt.HasText("Confirm full evidence review") {
		t.Fatalf("export skipped the review: %q", tt.Texts())
	}
	click("Close without exporting")
	if a.investigations.open {
		t.Fatal("closing the review left the evidence modal open")
	}
	// Deleting asks first and keeps the investigation when cancelled.
	click("Investigation options")
	if err := tt.ChooseMenuItem("Delete investigation…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Delete investigation?") {
		t.Fatal("deleting an investigation was not confirmed")
	}
	click("Cancel")
	if len(a.board.items) != 1 || len(a.board.items[0].Items) != 2 {
		t.Fatal("cancelling deleted the investigation")
	}
}

func TestInvestigationBoardFailureEvidenceStaysLayered(t *testing.T) {
	a := workspaceApp()
	a.board.path = filepath.Join(t.TempDir(), "investigations.json")
	f := flow{id: 9, method: "GET", scheme: "https", host: "api.example.com", path: "/checkout",
		url: "https://api.example.com/checkout", process: "Browser",
		conn: conn{error: "connection reset by peer", errorStage: "TCP", log: "dial failed"}}
	a.flows = []flow{f}
	a.applyFilter()
	if _, err := a.board.add(f); err != nil {
		t.Fatal(err)
	}
	a.workspace, a.captureSection = 0, 1
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Recorded failure · TCP · connection reset by peer") {
		t.Fatalf("the recorded stage and raw error are missing: %q", tt.Texts())
	}
	if !tt.HasText("Possible causes · not confirmed") || !tt.HasText("What to try") {
		t.Fatal("possible causes were presented as a diagnosis")
	}
	if tt.HasText("Client TLS handshake failed") {
		t.Fatal("a TCP failure was explained as a certificate problem")
	}
}
