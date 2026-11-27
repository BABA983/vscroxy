package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// screens must say what they mean through uikit, so a future toolkit means
// reimplementing the design system rather than every screen. Each entry is a
// MyGo widget uikit wraps, with what to use instead.
var guardedWidgets = map[string]string{
	"AlertDialog":   "uikit.ConfirmDialog",
	"Modal":         "uikit.Modal",
	"Button":        "uikit.PrimaryButton, uikit.SecondaryButton, uikit.ToolbarButton or uikit.LinkButton",
	"PrimaryButton": "uikit.PrimaryButton or uikit.PrimaryButtonBusy",
	"ButtonBase":    "uikit.ToolbarButton, uikit.RowButton, uikit.SecondaryButton or uikit.CompactButton",
	"Checkbox":      "uikit.Checkbox or uikit.OutlineCheckbox",
	"Switch":        "uikit.Switch",
	"Select":        "uikit.Select or uikit.SelectLabels",
	"TextInput":     "uikit.Input or uikit.SearchField",
	"TooltipBase":   "uikit.Tip, uikit.CardTip or uikit.TipSurface",
}

// TestScreensCallUikitNotMyGoWidgets fails when a screen calls a MyGo widget
// uikit already wraps. A line preceded by a "// Bare: <reason>" comment is
// allowed for what uikit cannot express; the reason must say why.
func TestScreensCallUikitNotMyGoWidgets(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// A Bare comment allows the call on the line under it. Comments are
		// matched by their last line so a wrapped comment still works.
		bare := map[int]bool{}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(strings.TrimSpace(comment.Text), "// Bare:") {
					bare[fset.Position(comment.End()).Line] = true
				}
			}
		}
		allowed := func(pos token.Pos) bool {
			return bare[fset.Position(pos).Line-1]
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			at := fset.Position(call.Pos())
			if root, ok := sel.X.(*ast.Ident); ok && root.Name == "ui" {
				if use, guarded := guardedWidgets[sel.Sel.Name]; guarded && !allowed(call.Pos()) {
					t.Errorf("%s:%d: ui.%s bypasses uikit; use %s, or put // Bare: <reason> on the line above",
						at.Filename, at.Line, sel.Sel.Name, use)
				}
				return true
			}
			// An element's own Tooltip is MyGo's, except on menu items, which
			// have no uikit tip wrapper: action.go is the one place they are
			// built.
			if sel.Sel.Name == "Tooltip" && at.Filename != "action.go" && !allowed(call.Pos()) {
				t.Errorf("%s:%d: .Tooltip() bypasses uikit; use uikit.Tip, or put // Bare: <reason> on the line above",
					at.Filename, at.Line)
			}
			// A drawn menu takes its highlight from the theme active at the
			// PopoverMenu call, so screens go through uikit.ContextMenu,
			// which applies the menu face around it.
			if sel.Sel.Name == "PopoverMenu" && !allowed(call.Pos()) {
				t.Errorf("%s:%d: .PopoverMenu() bypasses uikit; use uikit.ContextMenu, or put // Bare: <reason> on the line above",
					at.Filename, at.Line)
			}
			return true
		})
	}
}
