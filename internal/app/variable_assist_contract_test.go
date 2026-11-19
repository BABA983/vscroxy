package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// variableAssistApp gives every scope one distinct name and one distinct
// value. Secrets, credentials and sensitive names carry values that must
// never appear in a hover.
func variableAssistApp() *app {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local", Vars: map[string]string{
		"env_var":      "from-environment",
		"tenant":       "acme",
		"content_type": "application/json",
		"api_token":    "top-secret-token",
		"auth_secret":  "plain-auth-secret",
		"credential":   "marked-credential-value",
	}, Credentials: map[string]bool{"credential": true}}}
	a.activeEnv = "Local"
	a.globals = map[string]string{"team": "from-global", "tenant_id": "acme-id"}
	a.folders = []request.Folder{
		{Path: "Shop", Variables: []request.Variable{{Name: "region", Initial: "from-collection"}}},
		{Path: "Shop/Orders", Variables: []request.Variable{{Name: "tier", Initial: "from-folder"}}},
	}
	a.vault.loaded = true
	a.vault.secrets = vault.Secrets{{Name: "db_password", Value: "vault-secret-value"}}
	return a
}

func TestVariableHoverScopesMaskEverySecret(t *testing.T) {
	a := variableAssistApp()
	for _, tc := range []struct {
		name, scope, value, hidden string
	}{
		{"env_var", locales.KindPath(locales.Environment(), "Local"), "from-environment", ""},
		{"region", locales.KindPath(locales.Collection(), "Shop"), "from-collection", ""},
		{"tier", locales.KindPath(locales.FolderName(), "Shop/Orders"), "from-folder", ""},
		{"team", locales.ScopeGlobal(), "from-global", ""},
		{"$guid", locales.ScopeDynamic(), locales.GeneratedAtSend(), ""},
		{"vault:db_password", locales.ScopeVault(), locales.HiddenInTheVault(), "vault-secret-value"},
		{"api_token", locales.KindPath(locales.Environment(), "Local"), "", "top-secret-token"},
		{"auth_secret", locales.KindPath(locales.Environment(), "Local"), "", "plain-auth-secret"},
		{"credential", locales.KindPath(locales.Environment(), "Local"), "", "marked-credential-value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := a.variablePreview("Shop/Orders", tc.name)
			if !strings.Contains(got, tc.name) || !strings.Contains(got, tc.scope) {
				t.Fatalf("hover = %q, want the name and scope %q", got, tc.scope)
			}
			if tc.value != "" && !strings.Contains(got, tc.value) {
				t.Errorf("hover = %q, want value %q", got, tc.value)
			}
			if tc.hidden != "" && strings.Contains(got, tc.hidden) {
				t.Errorf("hover revealed %q: %q", tc.hidden, got)
			}
		})
	}
}

func TestVariableHoverOffersQuickFixesForUndefinedName(t *testing.T) {
	a := variableAssistApp()
	got := a.variablePreview("Shop/Orders", "tenant_idd")
	for _, want := range []string{
		locales.ProblemUnresolvedVariable("tenant_idd"),
		locales.StaysLiteralWhenSent(),
		locales.CreateVariableIn("tenant_idd", locales.ScopeGlobal()),
		locales.ChangeToVariable("tenant_id"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hover = %q, want %q", got, want)
		}
	}
	set := a.variableHintSet("Shop/Orders")
	proposals := a.variableFixProposals(set, "Shop/Orders", "tenant_idd")
	fixes := a.quickFixes(quickFixTarget{doc: &requestDocument{composeCollection: "Shop/Orders", composeURL: "{{tenant_idd}}"},
		name: "tenant_idd", text: ptr("{{tenant_idd}}"), start: 2, end: 14})
	if len(proposals) == 0 || len(proposals) != len(fixes) {
		t.Fatalf("%d hover fix labels, %d actionable fixes", len(proposals), len(fixes))
	}
	for i := range proposals {
		if proposals[i].label != fixes[i].label {
			t.Errorf("fix %d: hover %q, action %q", i, proposals[i].label, fixes[i].label)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestVariableCompletionFuzzyRankAndInsertion(t *testing.T) {
	a := variableAssistApp()
	items := a.variableCompletionItems("Shop/Orders", "ten", "}}")
	if len(items) < 2 {
		t.Fatalf("completion = %+v", items)
	}
	if items[0].Label != "tenant" {
		t.Fatalf("first completion = %q, want tenant; all = %+v", items[0].Label, items)
	}
	at := map[string]int{}
	for i, item := range items {
		at[item.Label] = i
		if item.Detail == "" {
			t.Errorf("completion %q has no scope label", item.Label)
		}
	}
	if at["content_type"] <= at["tenant"] {
		t.Fatalf("fuzzy ranking put content_type before tenant: %+v", items)
	}
	// The suffix is chosen from the text after the caret: a closing brace pair
	// already in the text is kept rather than written twice.
	for _, tc := range []struct{ text, suffix, want string }{
		{"{{te", "}}", "{{tenant}}"},
		{"{{te}}", "", "{{tenant}}"},
	} {
		caret := strings.Index(tc.text, "te") + 2
		items := a.variableCompletionItems("Shop/Orders", "te", tc.suffix)
		if want := "tenant" + tc.suffix; items[0].Insert != want {
			t.Errorf("insert for %q = %q, want %q", tc.text, items[0].Insert, want)
		}
		edited, _, ok := replaceVariableToken(tc.text, "tenant", caret)
		if !ok || edited != tc.want {
			t.Errorf("replace in %q = %q, %v; want %q", tc.text, edited, ok, tc.want)
		}
	}
}

func TestVariableCompletionRequiresDoubleBraces(t *testing.T) {
	for _, text := range []string{"{te", "te", "plain value", "{{te}} tail"} {
		if start, _, ok := openVariableToken(text, len(text)); ok {
			t.Errorf("openVariableToken(%q) = %d, %v; no completion expected", text, start, ok)
		}
	}
	a := variableAssistApp()
	openDraft(a, a.flows[0])
	a.composeURL = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.urlInput.Focus()
	tt.Frame()
	tt.Type("{")
	tt.Frame()
	if a.varsComplete.open {
		t.Fatal("completion opened on a single brace")
	}
	tt.Type("te")
	tt.Frame()
	if a.varsComplete.open {
		t.Fatal("completion opened inside a single brace")
	}
	if !tt.HasText("URL") {
		t.Fatal("URL field did not render")
	}
}

func TestVariableCompletionEscapeAndInsertionInURL(t *testing.T) {
	a := variableAssistApp()
	openDraft(a, a.flows[0])
	a.composeURL = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.urlInput.Focus()
	tt.Frame()
	tt.Type("{{te")
	tt.Frame()
	if !a.varsComplete.open || !tt.HasText("tenant") {
		t.Fatalf("completion did not open: %+v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.varsComplete.open {
		t.Fatal("Escape did not close the completion")
	}
	tt.Type("n")
	tt.Frame()
	if a.varsComplete.open || tt.HasText(locales.InsertVariable("tenant")) {
		t.Fatal("dismissed completion reopened or stayed on screen while typing")
	}
}

func TestVariableHoverMasksCredentialInNativeInput(t *testing.T) {
	a := variableAssistApp()
	openDraft(a, a.flows[0])
	a.composeURL = "{{api_token}}"
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	rect, ok := tt.Find("URL")
	if !ok {
		t.Fatal("URL field missing")
	}
	tt.Move(rect.X+24, rect.Y+rect.H/2)
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("••••••") {
		t.Fatalf("masked hover missing: %v", tt.Texts())
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "top-secret-token") {
			t.Fatalf("native hover revealed the credential: %q", text)
		}
	}
}

func TestScriptVariableHoverAndCompletion(t *testing.T) {
	a := variableAssistApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeTab, d.scriptTab = composeTabScripts, 0
	script := `pm.environment.get("te"); pm.globals.get('auth_secret')`
	d.composePre = script
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if d.preEditor == nil || d.preEditor.OnHover == nil || d.preEditor.OnCompletion == nil {
		t.Fatal("script editor has no variable hover and completion")
	}
	at := strings.Index(script, `"te`) + 3
	ref, ok := scriptVariableAt(script, at)
	if !ok || ref.Name != "te" || ref.Start != at-2 || ref.End != at {
		t.Fatalf("script ref at %d = %+v, %v", at, ref, ok)
	}
	d.preEditor.OnCompletion(editor.CompletionRequest{Offset: at, Prefix: "te"})
	tt.Frame()
	if !tt.HasText("tenant") {
		t.Fatalf("no completion in pm.environment.get: %v", tt.Texts())
	}
	secretAt := strings.Index(script, `'auth_secret'`) + 3
	d.preEditor.OnHover(editor.HoverRequest{Offset: secretAt})
	tt.Frame()
	if !tt.HasText("••••••") {
		t.Fatalf("no masked script hover: %v", tt.Texts())
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "plain-auth-secret") {
			t.Fatalf("script hover revealed a secret: %q", text)
		}
	}
}

func TestScriptVariableRejectsOtherReceivers(t *testing.T) {
	for _, script := range []string{
		`pm.cookies.get("tenant")`,
		`pm.response.headers.get("tenant")`,
		`pm.iterationData.get("tenant")`,
	} {
		t.Run(script, func(t *testing.T) {
			at := strings.Index(script, "tenant") + 1
			if ref, ok := scriptVariableAt(script, at); ok {
				t.Fatalf("wrong receiver gave a variable ref: %+v", ref)
			}
			a := variableAssistApp()
			openDraft(a, a.flows[0])
			d := a.requestDocument
			d.composeTab, d.scriptTab = composeTabScripts, 0
			d.composePre = script
			tt := ui.NewTester(a.view, 1280, 800)
			tt.Frame()
			d.preEditor.OnHover(editor.HoverRequest{Offset: at})
			tt.Frame()
			if tt.HasText("acme-id") {
				t.Fatalf("wrong receiver showed a variable hover: %v", tt.Texts())
			}
		})
	}
}

func TestScriptVariableCompletionEdges(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		at                 int
	}{
		{"empty literal", `pm.environment.get("")`, "tenant", strings.Index(`pm.environment.get("")`, `""`) + 1},
		{"caret mid name", `pm.environment.get("ten")`, "team", strings.Index(`pm.environment.get("ten")`, `"ten`) + 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := variableAssistApp()
			openDraft(a, a.flows[0])
			d := a.requestDocument
			d.composeTab, d.scriptTab = composeTabScripts, 0
			d.composePre = tc.script
			tt := ui.NewTester(a.view, 1280, 800)
			tt.Frame()
			literalStart := strings.Index(tc.script, `"`) + 1
			d.preEditor.OnCompletion(editor.CompletionRequest{Offset: tc.at, Prefix: tc.script[literalStart:tc.at]})
			tt.Frame()
			if !tt.HasText(tc.want) {
				t.Fatalf("completion at %d did not offer %q: %v", tc.at, tc.want, tt.Texts())
			}
			d.preEditor.OnHover(editor.HoverRequest{Offset: tc.at})
			tt.Frame()
			if tc.name == "empty literal" && tt.HasText("acme-id") {
				t.Fatalf("an empty literal showed a hover: %v", tt.Texts())
			}
		})
	}
}

// TestVariableAssistScreenshots exports the two visible states at 2x in both
// themes. Set VSCROXY_VARIABLE_ASSIST_SCREENSHOTS to an output directory.
func TestVariableAssistScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_VARIABLE_ASSIST_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_VARIABLE_ASSIST_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		for _, state := range []string{"completion", "hover"} {
			t.Run(appearance+"-"+state, func(t *testing.T) {
				a := variableAssistApp()
				a.page, a.appearance = pageRequest, appearance
				openDraft(a, a.flows[0])
				if state == "hover" {
					a.composeURL = "{{env_var}}/p?token={{api_token}}&q={{tenant_idd}}"
				} else {
					a.composeURL = ""
				}
				tt := ui.NewTester(a.view, 1440, 900)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				if state == "completion" {
					a.urlInput.Focus()
					tt.Frame()
					tt.Type("{{te")
					tt.Frame()
					if !a.varsComplete.open {
						t.Fatalf("completion is not open: %v", tt.Texts())
					}
				} else {
					rect, ok := tt.Find("URL")
					if !ok {
						t.Fatal("URL field missing")
					}
					tt.Move(rect.X+24, rect.Y+rect.H/2)
					tt.Advance(700 * time.Millisecond)
					tt.Frame()
					if !tt.HasText("••••••") {
						t.Fatalf("hover preview is not open: %v", tt.Texts())
					}
				}
				path := filepath.Join(dir, "variable-"+state+"-"+appearance+".png")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err := png.Encode(file, tt.Image()); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", path)
			})
		}
	}
}

func TestVariableHoverQuickFixesAppearInEditorCard(t *testing.T) {
	a := variableAssistApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeTab, d.bodyMode, d.composeBody = composeTabBody, "Raw", "{{tenant_idd}}"
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Request body"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	d.bodyEditor.OnHover(editor.HoverRequest{Offset: 4})
	tt.Frame()
	if !tt.HasText(locales.ChangeToVariable("tenant_id")) {
		t.Fatalf("editor hover did not offer the quick fix: %v", tt.Texts())
	}
}
