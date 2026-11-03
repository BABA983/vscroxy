package app

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestCollectionRecordingUIReviewsOnlyStoppedEvidence(t *testing.T) {
	for _, size := range [][2]int{{960, 600}, {1280, 800}} {
		a, client := recordingApp(t)
		startRecording(t, client, nil)
		flows := tokenPreviewFlows()
		for i := range flows {
			flows[i].at = a.collectionRecording.StartedAt.Add(time.Duration(i+1) * time.Second)
		}
		a.recordCollection(flows)
		if len(a.collectionRecording.evidence) != 2 {
			t.Fatal("saved evidence missing")
		}
		before := slices.Clone(a.requests)
		a.openCollectionRecording("", "", "")
		tt := ui.NewTester(a.view, size[0], size[1])
		if tt.HasText("Review recorded token links") {
			t.Fatal("review offered while recording is active")
		}
		a.collectionRecording.Busy = true
		a.stopCollectionRecording("")
		tt.Frame()
		if tt.HasText("Review recorded token links") {
			t.Fatal("review offered before the last worker finished")
		}
		a.collectionRecording.Busy = false
		a.flows = nil // entry must use recorder evidence, not LIVE selection
		a.applyFilter()
		tt.Frame()
		button, ok := tt.Find("Review recorded token links")
		if !ok || button.Y < 0 || button.Y+button.H > float32(size[1]) {
			t.Fatalf("review entry outside window: %+v", button)
		}
		if !strings.Contains(strings.Join(tt.Texts(), "\n"), "lost on restart") {
			t.Fatal("in-memory evidence lifetime not explained")
		}
		if err := tt.Click("Review recorded token links"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		w := a.tokenLinks
		if a.recordCollectionUI.open || w == nil || !w.open || w.config.Collection != "Recorded" || w.config.Environment != "Local" || len(w.flows) != 2 || !tt.HasText("Response #1 → 1 Bearer requests") {
			t.Fatal("review did not open retained evidence with recording scope")
		}
		if tt.HasText("preview-secret-token-123") || !reflect.DeepEqual(before, a.requests) || w.plan != nil || w.applied {
			t.Fatal("review disclosed a token or mutated authored requests")
		}
		if err := tt.Click("Close token links"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if err := tt.Click("Dismiss recording status"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		a.openCollectionRecording("", "", "")
		tt.Frame()
		if a.collectionRecording != nil || tt.HasText("Review recorded token links") {
			t.Fatal("dismissed evidence returned")
		}
	}
}
