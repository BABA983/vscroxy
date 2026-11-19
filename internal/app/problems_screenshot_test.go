package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestProblemsScreenshots captures the panel with every severity in both
// themes. Set VSCROXY_PROBLEMS_SCREENSHOTS to an output directory to export
// the 2x Tester images.
func TestProblemsScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_PROBLEMS_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_PROBLEMS_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := workspaceApp()
			a.page = pageRequest
			a.appearance = appearance
			for i, name := range []string{"List projects", "Create order"} {
				key := []string{"saved:problem-1", "saved:problem-2"}[i]
				a.addDocument(savedRequest{Name: name, Method: "GET", URL: "https://api.example.com/items"}, key)
				d := a.requestDocument
				d.sentSet, d.sent = true, d.draft()
			}
			first, second := a.documents[0], a.documents[1]
			a.markers.ChangeOne(problemOwnerAssertions, problemResource(first), []markers.Marker{
				{Severity: markers.Error, Message: locales.ProblemCheckFailed("status 200"), Source: "status 200", Code: problemFieldChecks},
				{Severity: markers.Hint, Message: "This check also accepts 201", Code: problemFieldChecks},
			})
			a.markers.ChangeOne(problemOwnerVariables, problemResource(first), []markers.Marker{
				{Severity: markers.Warning, Message: locales.ProblemUnresolvedVariable("tenant"), Code: problemFieldURL,
					Range: &markers.Range{StartLineNumber: 1, StartColumn: 9, EndLineNumber: 1, EndColumn: 17}},
			})
			a.markers.ChangeOne(problemOwnerScripts, problemResource(second), []markers.Marker{
				{Severity: markers.Info, Message: locales.ProblemScriptFailed("deprecated pm API"), Code: problemFieldPost,
					Range: &markers.Range{StartLineNumber: 2, StartColumn: 3, EndLineNumber: 2, EndColumn: 18}},
			})
			a.openProblems()

			tt := ui.NewTester(a.view, 1440, 900)
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText(locales.Problems()) || !tt.HasText(locales.ProblemError()) ||
				!tt.HasText(locales.ProblemWarning()) || !tt.HasText(locales.ProblemInfo()) ||
				!tt.HasText(locales.ProblemHint()) {
				t.Fatalf("panel is missing severities: %v", tt.Texts())
			}
			path := filepath.Join(dir, "problems-"+appearance+".png")
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
