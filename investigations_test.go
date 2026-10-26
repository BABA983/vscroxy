package main

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

func TestEvidencePublicFileContractRetainsLosslessSelectedData(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	spill := filepath.Join(dir, "private-spill")
	if err := os.WriteFile(spill, []byte("\x80\x00response"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := flow{
		id: 7, method: "POST", url: "https://example.test/path?token=raw-secret", status: 206,
		reqBody: "\xff\x00request", resBody: "preview-only", resFile: spill,
		reqSize: 100, resSize: 200, reqError: "incomplete request", resError: "incomplete response",
		reqHeader: http.Header{"Authorization": {"Bearer raw-secret"}}, respHeader: http.Header{"Set-Cookie": {"a=secret", "b=secret"}},
		reqTrailer: http.Header{"X-Upload": {"partial"}}, resTrailer: http.Header{"Grpc-Status": {"13"}},
		conn: conn{proto: "HTTP/2", error: "upstream failed", errorStage: "TLS", log: "diagnosis"}, resProto: "HTTP/2",
		streamEnd: capture.StreamEnd{At: at, Messages: 12}, at: at, note: "private original note", replayOf: 3,
		actions: []string{"recorded rule action, not executable"},
	}
	frames := map[int][]frame{7: {{seq: 4, direction: "in", kind: "binary", data: "\xff\x00frame", size: 900, at: at, eventID: "42", eventName: "delta"}}}
	paths := []string{filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")}
	if err := writeSession(paths[0], []flow{original, {id: 8, url: "https://unselected.test/DO-NOT-EXPORT", resBody: "unselected-secret"}}, frames); err != nil {
		t.Fatal(err)
	}
	if err := writeSession(paths[1], []flow{{id: 7, method: "GET", url: "https://other.test/", status: 200, resBody: "other-source"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spill); err != nil {
		t.Fatal(err)
	}
	loaded := loadInvestigationFiles(paths, 0, 0)
	if loaded.err != nil || len(loaded.sources) != 2 {
		t.Fatalf("import: %v", loaded.err)
	}
	legacyFlows, _, err := readSession(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := investigationFlow(loaded.sources[0].Session.Flows[0]); !reflect.DeepEqual(got, legacyFlows[0]) {
		t.Fatal("historical reader disagrees with existing lossless session codec")
	}
	data, err := buildEvidence(loaded.sources, []investigationPick{{0, 0}, {1, 0}, {0, 0}}, "Failure across releases", "Reviewer notes")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("unselected-secret")) || bytes.Contains(data, []byte(dir)) || bytes.Contains(data, []byte("preview-only")) {
		t.Fatal("bundle leaked an unselected exchange, a spill path or a bounded preview")
	}
	if !bytes.Contains(data, []byte("raw-secret")) {
		t.Fatal("raw evidence must not silently claim sanitization")
	}
	out := filepath.Join(dir, "evidence.json")
	if err := os.WriteFile(out, []byte("old destination"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEvidenceBundle(out, data, false); err == nil {
		t.Fatal("unreviewed export succeeded")
	}
	if unchanged, _ := os.ReadFile(out); string(unchanged) != "old destination" {
		t.Fatal("unreviewed export touched the destination")
	}
	if err := WriteEvidenceBundle(out, data, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("export permissions: %v, %v", info, err)
	}
	reopened, err := ReadInvestigationFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Title != "Failure across releases" || reopened.Notes != "Reviewer notes" || len(reopened.Sources) != 2 {
		t.Fatal("lost reviewed title, notes or origin separation")
	}
	if !reflect.DeepEqual(reopened.Sources[0].Session.Flows[0], loaded.sources[0].Session.Flows[0]) ||
		!reflect.DeepEqual(reopened.Sources[0].Session.Frames, loaded.sources[0].Session.Frames) {
		t.Fatal("round trip lost binary payloads, trailers, stream metadata or incomplete-body evidence")
	}
	if reopened.Sources[0].Label != "first.json" || reopened.Sources[1].Session.Flows[0].ID != 7 || len(reopened.Sources[0].Session.Flows) != 1 {
		t.Fatal("source labels, duplicate cross-session IDs or selected subset changed")
	}
	// Manual edits are honored exactly, not reconstituted from the raw source.
	edited := bytes.ReplaceAll(data, []byte("raw-secret"), []byte("REMOVED-BY-REVIEWER"))
	if err := WriteEvidenceBundle(out, edited, true); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(actual, edited) {
		t.Fatal("writer did not preserve the exact reviewed bytes")
	}
	if loaded.sources[0].Session.Flows[0].ReqHeader.Get("Authorization") != "Bearer raw-secret" {
		t.Fatal("review edits mutated opened historical data")
	}
}

func TestInvestigationLegacyMissingMetadataRemainsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	data := []byte(`{"version":1,"flows":[{"id":7,"method":"GET","reqBody":"head","resBody":"partial","reqFile":"/must/not/be/read"}],"frames":{"7":[{"direction":"in","kind":"text","data":"old message"}]},"pre":"throw new Error('must never execute')"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := ReadInvestigationFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := b.Sources[0].Session
	if s.Flows[0].ReqSize != 0 || s.Frames[7][0].Size != 0 || !s.Frames[7][0].At.IsZero() || !s.Flows[0].StreamEnd.At.IsZero() {
		t.Fatal("missing legacy metadata was invented")
	}
	f := investigationFlow(s.Flows[0])
	if f.reqFile != "" || f.resFile != "" || f.reqBody != "head" || f.reqSize != 4 {
		t.Fatal("reader restored a spill path or lost the captured head")
	}
	data, err = buildEvidence(b.Sources, []investigationPick{{0, 0}}, "Legacy", "")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("must/not/be/read")) || bytes.Contains(data, []byte("throw new Error")) {
		t.Fatal("unknown operational fields were carried into evidence")
	}
}

func TestInvestigationNativeSearchScopeAndCrossSessionSelection(t *testing.T) {
	f := sessionFlow{ID: 7, URL: "https://example.test/a.b", Status: 503, Note: "Regression suspected", ReqError: "upload interrupted", ResError: "disk missing", ConnectionError: "certificate rejected", ReqBody: "not-searchable-body", ReqHeaders: "X-Only-Header: secret"}
	s := investigationWorkbench{
		sources: []EvidenceSource{
			{Label: "one", Notes: "Bundle observation", Session: sessionFile{Version: 1, Flows: []sessionFlow{f}}},
			{Label: "two", Session: sessionFile{Version: 1, Flows: []sessionFlow{{ID: 7, Status: 200}}}},
		},
		panes: [2]int{0, 1},
	}
	s.refreshSearch()
	s.show()
	a := workspaceApp()
	tt := ui.NewTester(func(c *ui.Context) { s.modal(c, a) }, 960, 600)
	for _, label := range []string{"Select source 1 exchange 7", "Select source 2 exchange 7"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	search := func(query string) {
		t.Helper()
		if err := tt.Click("Search historical sources"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(query)
		if !tt.HasText("2 / 64 selected across all sources (including hidden results)") {
			t.Fatal("search erased cross-source selections")
		}
	}
	for _, q := range []string{"", " EXAMPLE.TEST ", "503", "REGRESSION", "interrupted", "disk missing", "certificate", "bundle observation", "a.b"} {
		search(q)
		if !tt.HasText("Select source 1 exchange 7") {
			t.Fatalf("missing search result for %q", q)
		}
	}
	for _, q := range []string{"not-searchable-body", "X-Only-Header", "a.*b", "200"} {
		search(q)
		if tt.HasText("Select source 1 exchange 7") {
			t.Fatalf("scope or literal-search violation for %q", q)
		}
	}
	if !tt.HasText("Select source 2 exchange 7") {
		t.Fatal("same exchange ID in the second source disappeared with the first")
	}
}

func TestInvestigationPublicFileContractRejectsInvalidAndOversizeInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	for name, data := range map[string]string{
		"invalid": "not JSON secret-value", "future session": `{"version":99,"flows":[{"id":1}]}`,
		"empty": `{"version":1,"flows":[]}`, "duplicate IDs": `{"version":1,"flows":[{"id":1},{"id":1}]}`,
		"orphan messages": `{"version":1,"flows":[{"id":1}],"frames":{"2":[{}]}}`,
		"future evidence": `{"format":"vscroxy-evidence","version":2}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadInvestigationFile(path); err == nil || strings.Contains(err.Error(), "secret-value") {
				t.Fatal("invalid file accepted or parser disclosed raw data")
			}
		})
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(investigationFileLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ReadInvestigationFile(path); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := ReadInvestigationFile(dir); err == nil {
		t.Fatal("directory accepted")
	}
	valid := sessionFile{Version: 1, Flows: []sessionFlow{{ID: 1}}}
	data, _ := json.Marshal(valid)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := loadInvestigationFiles([]string{path}, investigationSources, 0); r.err == nil {
		t.Fatal("retained source count exceeded")
	}
	if r := loadInvestigationFiles([]string{path}, 0, investigationKeepLimit); r.err == nil {
		t.Fatal("retained byte budget exceeded")
	}
	if _, err := buildEvidence([]EvidenceSource{{Label: "a", Session: valid}}, []investigationPick{{0, 3}}, "title", ""); err == nil {
		t.Fatal("stale row accepted")
	}
	out := filepath.Join(dir, "must-not-exist.json")
	if err := WriteEvidenceBundle(out, bytes.Repeat([]byte(" "), evidenceFileLimit+1), true); err == nil {
		t.Fatal("oversized reviewed evidence accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("invalid evidence created a file")
	}
}

func TestInvestigationNativeReviewGateInvalidatesOnEdit(t *testing.T) {
	a := workspaceApp()
	live := sessionFrom(a.flows, a.frames)
	s := investigationWorkbench{}
	s.show()
	if s.busy || s.result != nil {
		t.Fatal("opening the reader launched a file dialog")
	}
	s.reviewing, s.preview = true, `{"title":"editable review"}`
	tt := ui.NewTester(func(c *ui.Context) { s.modal(c, a) }, 1100, 850)
	if !tt.HasText("RAW EVIDENCE · NOT AUTOMATICALLY SANITIZED") {
		t.Fatal("raw-data warning is absent")
	}
	if err := tt.Click("Export reviewed evidence…"); err != nil {
		t.Fatal(err)
	}
	if s.busy {
		t.Fatal("unchecked review exported evidence")
	}
	if err := tt.Click("Confirm full evidence review"); err != nil {
		t.Fatal(err)
	}
	if !s.reviewed {
		t.Fatal("review checkbox did not confirm")
	}
	if err := tt.Click("Editable raw evidence JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type(`{"title":"changed after confirmation"}`)
	if s.reviewed {
		t.Fatal("editing the review retained stale consent")
	}
	if !reflect.DeepEqual(live, sessionFrom(a.flows, a.frames)) {
		t.Fatal("historical review mutated live traffic")
	}
	s.close()
	s.show()
	if s.open || s.preview != "" || s.sources != nil {
		t.Fatal("closed reader retained data or reopened after shutdown")
	}
}

func TestInvestigationNativeCrossSessionComparePreservesLiveTraffic(t *testing.T) {
	a := workspaceApp()
	live := sessionFrom(a.flows, a.frames)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")}
	for i, path := range paths {
		if err := writeSession(path, []flow{{id: 7, method: "GET", url: "https://example.test/", status: 200 + i, resBody: []string{"before", "after"}[i]}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	s := &a.investigations
	s.result = make(chan investigationResult, 1)
	s.result <- loadInvestigationFiles(paths, 0, 0)
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Historical sessions")
	tt.Key(0, ui.KeyEnter)
	if s.problem != "" {
		t.Fatal(s.problem)
	}
	for _, label := range []string{"Select source 1 exchange 7", "Select source 2 exchange 7", "Compare selected two"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	for label, want := range map[string]string{"Old text": "before", "New text": "after"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		if tt.Clipboard() != want {
			t.Fatalf("cross-session %s = %q, want %q", label, tt.Clipboard(), want)
		}
	}
	if err := tt.Click("Back to sources"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Search historical sources"); err != nil {
		t.Fatal(err)
	}
	tt.Type("201")
	if tt.HasText("Select source 1 exchange 7") || !tt.HasText("Select source 2 exchange 7") || !tt.HasText("2 / 64 selected across all sources (including hidden results)") {
		t.Fatal("search did not keep selected hidden evidence across sources")
	}
	if !reflect.DeepEqual(live, sessionFrom(a.flows, a.frames)) || a.analysis.diff != nil {
		t.Fatal("historical comparison replaced live data or its comparison state")
	}
}
