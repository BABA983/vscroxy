package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func quickFixTestApp(t *testing.T, saved savedRequest) (*app, *requestDocument) {
	t.Helper()
	a := workspaceApp()
	a.globals = map[string]string{"tenant": "acme"}
	a.addDocument(saved, "saved:quickfix")
	d := a.requestDocument
	a.syncProblems()
	return a, d
}

func markerFor(t *testing.T, a *app, d *requestDocument, name string) markers.Marker {
	t.Helper()
	list := a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: problemResource(d)})
	for _, m := range list {
		if m.Source == name {
			return m
		}
	}
	t.Fatalf("no marker for %q in %+v", name, list)
	return markers.Marker{}
}

func applyFix(t *testing.T, a *app, m markers.Marker, label string) {
	t.Helper()
	for _, fix := range a.quickFixesForMarker(m) {
		if strings.Contains(fix.label, label) {
			fix.apply()
			a.syncProblems()
			return
		}
	}
	t.Fatalf("no fix %q among %+v", label, a.quickFixesForMarker(m))
}

func TestRenameRewritesOnlyTheMarkedOccurrence(t *testing.T) {
	a, d := quickFixTestApp(t, savedRequest{Name: "Twice", Method: "GET",
		URL: "https://api.example.com/{{tenat}}/{{tenat}}"})
	applyFix(t, a, markerFor(t, a, d, "tenat"), locales.ChangeToVariable("tenant"))
	want := "https://api.example.com/{{tenant}}/{{tenat}}"
	if d.composeURL != want {
		t.Fatalf("URL after fix = %q, want %q", d.composeURL, want)
	}
	if got := a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: problemResource(d)}); len(got) != 1 {
		t.Fatalf("%d markers remain, want the second occurrence: %+v", len(got), got)
	}
}

func TestRenameRewritesHeaderAuthAndFormFields(t *testing.T) {
	t.Run("header", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Header", Method: "GET", URL: "https://api.example.com",
			Headers: "X-Tenant: {{tenat}}"})
		d.headerRows = parseHeaderRows(d.composeHeaders)
		a.syncProblems()
		m := markerFor(t, a, d, "tenat")
		if m.Code != "header-value-1" {
			t.Fatalf("header marker code = %q", m.Code)
		}
		applyFix(t, a, m, locales.ChangeToVariable("tenant"))
		if d.composeHeaders != "X-Tenant: {{tenant}}" {
			t.Fatalf("headers after fix = %q", d.composeHeaders)
		}
	})
	t.Run("auth", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Auth", Method: "GET", URL: "https://api.example.com"})
		d.auth = request.Auth{Type: "Bearer", Token: "Bearer {{tenat}}"}
		a.syncProblems()
		m := markerFor(t, a, d, "tenat")
		if m.Code != problemFieldAuthPrefix+"Token" {
			t.Fatalf("auth marker code = %q", m.Code)
		}
		applyFix(t, a, m, locales.ChangeToVariable("tenant"))
		if d.auth.Token != "Bearer {{tenant}}" {
			t.Fatalf("token after fix = %q", d.auth.Token)
		}
	})
	t.Run("form", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Form", Method: "POST", URL: "https://api.example.com"})
		d.bodyMode = "Form"
		d.formFields = []request.Field{{Name: "tenant", Value: "{{tenat}}"}}
		a.syncProblems()
		m := markerFor(t, a, d, "tenat")
		if m.Code != problemFieldFormPrefix+"value-1" {
			t.Fatalf("form marker code = %q", m.Code)
		}
		applyFix(t, a, m, locales.ChangeToVariable("tenant"))
		if d.formFields[0].Value != "{{tenant}}" {
			t.Fatalf("form value after fix = %q", d.formFields[0].Value)
		}
	})
	t.Run("body", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Body", Method: "POST", URL: "https://api.example.com",
			Body: `{"tenant":"{{tenat}}","again":"{{tenat}}"}`})
		m := markerFor(t, a, d, "tenat")
		if m.Code != problemFieldBody {
			t.Fatalf("body marker code = %q", m.Code)
		}
		applyFix(t, a, m, locales.ChangeToVariable("tenant"))
		want := `{"tenant":"{{tenant}}","again":"{{tenat}}"}`
		if d.composeBody != want {
			t.Fatalf("body after fix = %q, want %q", d.composeBody, want)
		}
	})
}

func TestRenameInBodyEditorKeepsUndo(t *testing.T) {
	a, d := quickFixTestApp(t, savedRequest{Name: "Body", Method: "POST", URL: "https://api.example.com",
		Body: `{"tenant":"{{tenat}}"}`})
	m := markerFor(t, a, d, "tenat")
	fixes := a.quickFixesForMarker(m)
	if len(fixes) == 0 {
		t.Fatal("no fixes for the body marker")
	}
	d.bodyEditor = nil
	d.bodyFor = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if d.bodyEditor == nil {
		t.Fatal("body editor was not built")
	}
	// Work against the editor's own buffer, as a hover or a menu does.
	d.bodyEditor.GoTo(editor.Pos{Col: strings.Index(d.composeBody, "{{tenat}}") + 1})
	for _, fix := range a.quickFixesForMarker(markerFor(t, a, d, "tenat")) {
		if strings.Contains(fix.label, locales.ChangeToVariable("tenant")) {
			fix.apply()
			break
		}
	}
	if got := d.bodyEditor.Buffer.String(); got != `{"tenant":"{{tenant}}"}` {
		t.Fatalf("editor buffer after fix = %q", got)
	}
	if !d.bodyEditor.Run(editor.Undo) {
		t.Fatal("the editor had no undo step for the rename")
	}
	if got := d.bodyEditor.Buffer.String(); got != `{"tenant":"{{tenat}}"}` {
		t.Fatalf("undo restored %q", got)
	}
}

func TestCreateVariableInChosenScopeClearsMarker(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Create env", Method: "GET", URL: "{{env_only}}"})
		a.activeEnv = "Dev"
		a.envs = []environment{{Name: "Dev", Vars: map[string]string{"host": "dev"}}}
		a.syncProblems()
		m := markerFor(t, a, d, "env_only")
		label := locales.CreateVariableIn("env_only", locales.KindPath(locales.Environment(), "Dev"))
		applyFix(t, a, m, label)
		if value, ok := a.envs[0].Vars["env_only"]; !ok || value != "" {
			t.Fatalf("created env variable = %q, %v", value, ok)
		}
		if got := a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: problemResource(d)}); len(got) != 0 {
			t.Fatalf("%d markers remain after creation: %+v", len(got), got)
		}
	})
	t.Run("globals", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Create global", Method: "GET", URL: "{{global_only}}"})
		m := markerFor(t, a, d, "global_only")
		applyFix(t, a, m, locales.ScopeGlobal())
		if value, ok := a.globals["global_only"]; !ok || value != "" {
			t.Fatalf("created global = %q, %v", value, ok)
		}
		if got := a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: problemResource(d)}); len(got) != 0 {
			t.Fatalf("%d markers remain after creation", len(got))
		}
	})
	t.Run("collection", func(t *testing.T) {
		a, d := quickFixTestApp(t, savedRequest{Name: "Create collection", Method: "GET",
			Collection: "Shop", URL: "{{collection_only}}"})
		a.folders = []request.Folder{{Path: "Shop"}}
		a.syncProblems()
		m := markerFor(t, a, d, "collection_only")
		applyFix(t, a, m, locales.KindPath(locales.Collection(), "Shop"))
		if len(a.folders) != 1 || len(a.folders[0].Variables) != 1 || a.folders[0].Variables[0].Name != "collection_only" {
			t.Fatalf("collection variables after fix = %+v", a.folders)
		}
		if got := a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: problemResource(d)}); len(got) != 0 {
			t.Fatalf("%d markers remain after creation", len(got))
		}
	})
}

func TestSimilarVariableNamesRankAsymmetricCases(t *testing.T) {
	hints := []variableHint{
		{Name: "unrelated", Scope: "Global"},
		{Name: "tenant_id", Scope: "Global"},
		{Name: "tenant", Scope: "Global"},
		{Name: "tenants", Scope: "Global"},
	}
	got := similarVariableNames("tenat", hints)
	names := make([]string, 0, len(got))
	for _, hint := range got {
		names = append(names, hint.Name)
	}
	if !slices.Equal(names, []string{"tenant", "tenants", "tenant_id"}) {
		t.Fatalf("similar names = %v, want tenant, tenants, tenant_id", names)
	}
	if got := similarVariableNames("zzzzzz", hints); len(got) != 0 {
		t.Fatalf("unrelated name suggested %v", got)
	}
}

// TestQuickFixShortcutOnAndOffAVariable exercises the scoped Cmd/Ctrl+.
// binding on the URL field. Off a reference the same key does nothing.
func TestQuickFixShortcutOnAndOffAVariable(t *testing.T) {
	a := workspaceApp()
	a.globals = map[string]string{"tenant": "acme"}
	openDraft(a, a.flows[0])
	a.composeURL = "{{tenat}}"
	d := a.requestDocument

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.urlInput.Focus()
	tt.Frame()
	// The caret is just past the closing braces: no variable under it.
	tt.Key(ui.Cmd, ui.KeyPeriod)
	tt.Frame()
	if a.problems.fixMenuOpen {
		t.Fatal("Cmd+. opened fixes with the caret off the variable")
	}
	for range 3 {
		tt.Key(0, ui.KeyLeft)
		tt.Frame()
	}
	tt.Key(ui.Cmd, ui.KeyPeriod)
	tt.Frame()
	if !a.problems.fixMenuOpen {
		t.Fatal("Cmd+. did not open fixes on the variable")
	}
	if !tt.HasText(locales.ChangeToVariable("tenant")) {
		t.Fatalf("quick fix menu lacks the rename: %v", tt.Texts())
	}
	if d.composeURL != "{{tenat}}" {
		t.Fatalf("opening the menu changed the URL to %q", d.composeURL)
	}
	for _, fix := range a.problems.caretFixes {
		if strings.Contains(fix.label, locales.ChangeToVariable("tenant")) {
			fix.apply()
			break
		}
	}
	if d.composeURL != "{{tenant}}" {
		t.Fatalf("applying from the menu left %q", d.composeURL)
	}
}
