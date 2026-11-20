package app

import (
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func inspectorRestoreApp(t *testing.T, flows ...flow) *app {
	t.Helper()
	a := workspaceApp()
	a.inspectorWindowsPath = inspectorWindowsPath(t.TempDir())
	if len(flows) > 0 {
		a.flows = flows
		for i := range a.flows {
			if a.flows[i].debugKey == "" {
				a.flows[i].debugKey = "key-1"
			}
		}
		a.recount()
		a.applyFilter()
	}
	return a
}

func TestInspectorWindowsStateRoundTrips(t *testing.T) {
	path := inspectorWindowsPath(t.TempDir())
	want := savedInspectorWindows{Windows: []savedInspectorWindow{{
		Target: inspectorTarget{Kind: inspectorTargetFlow, Key: "stable-flow-key"},
		Bounds: savedWindowBounds{X: 120, Y: 40, Width: 900, Height: 700},
	}}}
	if err := saveInspectorWindows(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadInspectorWindows(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("saved inspector windows = %+v, want %+v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version":1`) {
		t.Fatalf("saved file has no format version: %s", data)
	}
}

func TestInspectorWindowsStateHasNoExchangeContents(t *testing.T) {
	a := inspectorRestoreApp(t)
	secret := flow{
		id: 17, debugKey: "stable-secret-key", method: "POST",
		url:        "https://api.example.com/login?token=SECRET-URL-TOKEN",
		reqHeader:  http.Header{"Authorization": {"Bearer SECRET-REQUEST-HEADER"}},
		respHeader: http.Header{"Set-Cookie": {"session=SECRET-RESPONSE-HEADER"}},
		reqBody:    "SECRET-REQUEST-BODY",
		resBody:    "SECRET-RESPONSE-BODY",
		note:       "SECRET-NOTE",
	}
	a.flows = []flow{secret}
	a.visible, a.picked, a.pickedID = []int{0}, 0, secret.id
	a.inspectorDetached = true
	saved := a.snapshotInspectorWindows(mygo.Rectangle{X: 1300, Y: -400, Width: 900, Height: 700})
	if len(saved.Windows) != 1 || saved.Windows[0].Target.Key != secret.debugKey {
		t.Fatalf("saved target = %+v, want %q", saved.Windows, secret.debugKey)
	}
	if err := saveInspectorWindows(a.inspectorWindowsPath, saved); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.inspectorWindowsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"SECRET-URL-TOKEN", "SECRET-REQUEST-HEADER", "SECRET-RESPONSE-HEADER",
		"SECRET-REQUEST-BODY", "SECRET-RESPONSE-BODY", "SECRET-NOTE",
		secret.url, secret.method,
	} {
		if strings.Contains(string(data), needle) {
			t.Errorf("saved inspector window contains %q: %s", needle, data)
		}
	}
}

func TestInspectorWindowRestoreSkipsMissingTarget(t *testing.T) {
	f := flow{id: 9, debugKey: "present-key"}
	a := inspectorRestoreApp(t, f)
	if err := saveInspectorWindows(a.inspectorWindowsPath, savedInspectorWindows{
		Windows: []savedInspectorWindow{{
			Target: inspectorTarget{Kind: inspectorTargetFlow, Key: "gone-key"},
			Bounds: savedWindowBounds{Width: 900, Height: 700},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	a.restoreInspectorWindows()
	if a.inspectorDetached || a.inspectorWin != nil {
		t.Fatal("a missing target still reopened the inspector window")
	}
	if a.pickedID != f.id || a.message != "" {
		t.Fatalf("skip changed the selection or showed a notice: picked=%d message=%q", a.pickedID, a.message)
	}
}

func TestInspectorWindowRestoreReopensFlowKinds(t *testing.T) {
	for _, kind := range []string{"", "connect", "ws", "sse"} {
		t.Run("kind="+kind, func(t *testing.T) {
			f := flow{id: 23, debugKey: "surviving-key", kind: kind}
			a := inspectorRestoreApp(t, f)
			if err := saveInspectorWindows(a.inspectorWindowsPath, savedInspectorWindows{
				Windows: []savedInspectorWindow{{
					Target: inspectorTarget{Kind: inspectorTargetFlow, Key: f.debugKey},
					Bounds: savedWindowBounds{X: 100, Y: 120, Width: 900, Height: 700},
				}},
			}); err != nil {
				t.Fatal(err)
			}
			a.restoreInspectorWindows()
			if !a.inspectorDetached || a.page != pageLive || a.pickedID != f.id {
				t.Fatalf("restored inspector = detached %v, page %v, picked %d", a.inspectorDetached, a.page, a.pickedID)
			}
		})
	}
}

func TestClampWindowBoundsToVisibleDisplay(t *testing.T) {
	primary := mygo.Display{
		Bounds:   mygo.Rectangle{Width: 1440, Height: 900},
		WorkArea: mygo.Rectangle{Width: 1440, Height: 860},
		Primary:  true,
	}
	second := mygo.Display{
		Bounds:   mygo.Rectangle{X: 1440, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 1440, Y: 30, Width: 1920, Height: 1050},
	}
	tests := []struct {
		name  string
		saved mygo.Rectangle
		disps []mygo.Display
		want  mygo.Rectangle
	}{
		{
			name:  "fits where it was",
			saved: mygo.Rectangle{X: 100, Y: 60, Width: 900, Height: 700},
			disps: []mygo.Display{primary, second},
			want:  mygo.Rectangle{X: 100, Y: 60, Width: 900, Height: 700},
		},
		{
			name:  "off the right edge",
			saved: mygo.Rectangle{X: 3000, Y: 340, Width: 900, Height: 700},
			disps: []mygo.Display{primary},
			want:  mygo.Rectangle{X: 540, Y: 160, Width: 900, Height: 700},
		},
		{
			name:  "unplugged monitor to the left",
			saved: mygo.Rectangle{X: -2000, Y: 1000, Width: 900, Height: 700},
			disps: []mygo.Display{primary},
			want:  mygo.Rectangle{X: 0, Y: 160, Width: 900, Height: 700},
		},
		{
			name:  "still on its second display",
			saved: mygo.Rectangle{X: 1500, Y: 50, Width: 900, Height: 700},
			disps: []mygo.Display{primary, second},
			want:  mygo.Rectangle{X: 1500, Y: 50, Width: 900, Height: 700},
		},
		{
			name:  "larger than the work area",
			saved: mygo.Rectangle{X: 100, Y: 100, Width: 2000, Height: 1500},
			disps: []mygo.Display{primary},
			want:  mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 860},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampWindowBounds(tt.saved, tt.disps); got != tt.want {
				t.Fatalf("clampWindowBounds(%+v) = %+v, want %+v", tt.saved, got, tt.want)
			}
		})
	}
}
