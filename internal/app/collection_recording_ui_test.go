package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCollectionRecordingUIStartStopAndDismiss(t *testing.T) {
	a, _ := recordingApp(t)
	a.activeEnv = "Local"
	a.openCollectionRecording("Recorded", "api.example.com", "Browser")
	tt := ui.NewTester(a.view, 960, 600)
	footer, ok := tt.Find("Start collection recording")
	if !ok || footer.Y < 0 || footer.Y+footer.H > 600 {
		t.Fatalf("start button outside minimum window: %+v", footer)
	}
	if err := tt.Click("Start collection recording"); err != nil {
		t.Fatal(err)
	}
	if a.collectionRecording != nil {
		t.Fatal("recording started without acknowledgment")
	}
	// Scroll the form, not the nested multi-line text editor near its footer.
	tt.Scroll(480, 400, 0, 2000)
	if err := tt.Click("I understand captured content will be saved locally"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Start collection recording"); err != nil {
		t.Fatal(err)
	}
	if a.recordCollectionUI.open || a.collectionRecording == nil || !a.collectionRecording.Active || a.collectionRecording.Environment != "Local" {
		t.Fatalf("start failed: %s", a.recordCollectionUI.problem)
	}
	a.recordCollection([]flow{recordingFlow("/users/42")})
	tt.Frame()
	if err := tt.Click("Collection recording status"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("1 added · 0 updated · 0 skipped · 0 discarded · 0 queued") {
		t.Fatal("running counters not visible")
	}
	if err := tt.Click("Stop collection recording"); err != nil {
		t.Fatal(err)
	}
	if a.collectionRecording.Active || len(a.requests) != 1 {
		t.Fatal("stop did not preserve saved request")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click("Dismiss recording status"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.collectionRecording != nil || len(a.requests) != 1 || tt.HasText("Collection recording status") {
		t.Fatal("dismiss deleted assets or left a stale status control")
	}
}

func TestCollectionRecordingUIReflectsRemoteScopeAndGuardsRunningForm(t *testing.T) {
	a, client := recordingApp(t)
	a.openCollectionRecording("Local draft", "draft.example.com", "Draft")
	tt := ui.NewTester(a.view, 1280, 800)
	startRecording(t, client, map[string]any{"collection": "Remote scope", "hosts": []string{"api.example.com"}, "apps": []string{"Browser"}})
	tt.Frame()
	w := a.recordCollectionUI
	if w.collection != "Remote scope" || w.environment != "Local" || w.hosts != "api.example.com" || w.apps != "Browser" {
		t.Fatalf("disabled form shows stale local scope: %+v", w)
	}
	// Opening another sidebar entry cannot silently widen a running scope.
	a.openCollectionRecording("Other", "other.example.com", "Other")
	tt.Frame()
	if a.recordCollectionUI.collection != "Remote scope" || a.recordCollectionUI.hosts != "api.example.com" {
		t.Fatal("opening another entry changed a running scope")
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if !a.collectionRecording.Active {
		t.Fatal("closing the status dialog stopped recording")
	}
	if err := tt.Click("Stop collection recorder"); err != nil {
		t.Fatal(err)
	}
	if a.collectionRecording.Active {
		t.Fatal("persistent status control did not stop remote session")
	}
	tt.Frame()
}
