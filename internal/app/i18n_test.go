package app

// The catalogs and the screens stay in step: every message has a translation,
// and every string a screen shows is either a catalog message or listed in
// locales/untranslated.txt as data that must not be translated.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestCatalogsMatch(t *testing.T) {
	en := readCatalog(t, filepath.Join("..", "..", "locales", "en.json"))
	zh := readCatalog(t, filepath.Join("..", "..", "locales", "zh-Hans.json"))
	for key := range en {
		if _, ok := zh[key]; !ok {
			t.Errorf("zh-Hans is missing %q", key)
		}
	}
	for key := range zh {
		if _, ok := en[key]; !ok {
			t.Errorf("zh-Hans has %q, which en does not", key)
		}
	}
}

func readCatalog(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	messages := make(map[string]string, len(raw))
	for key, value := range raw {
		if strings.HasPrefix(key, "@") {
			continue
		}
		var message string
		if err := json.Unmarshal(value, &message); err != nil {
			t.Fatalf("%s: %q is not a message", path, key)
		}
		messages[key] = message
	}
	return messages
}

// TestUIScreensUseTheCatalog finds string literals in the places a screen
// shows text, and fails on each one that is not a catalog message.
func TestUIScreensUseTheCatalog(t *testing.T) {
	allowed := readAllowed(t, filepath.Join("..", "..", "locales", "untranslated.txt"))
	var problems []string
	for _, dir := range []string{".", filepath.Join("..", "uikit")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, text := range screenStrings(t, path) {
				if allowed[text] {
					continue
				}
				problems = append(problems, path+": "+strconv.Quote(text))
			}
		}
	}
	for _, problem := range problems {
		t.Errorf("not a catalog message: %s", problem)
	}
}

// screenStrings are the literals a file passes where a screen shows text.
func screenStrings(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// args are the argument positions of UI constructors; variadic labels
	// start at first and run to the last argument.
	args := map[string]int{
		"Text": 1, "Button": 1, "Field": 1, "Alert": 1,
		"Heading": 1, "Body": 1, "Prose": 1, "Hint": 1, "Meta": 1, "Mono": 1,
		"PrimaryButton": 1, "SecondaryButton": 1, "CompactButton": 1, "DangerButton": 1,
		"RowButton": 1, "ToolbarButton": 1, "IconButton": 1, "StatusPill": 1,
		"DialogTitle": 1, "SettingsPage": 1, "SettingsGroup": 1, "MoreMenu": 1,
		"SidebarItem": 3, "SidebarSection": 1,
		"Description": 0, "Error": 0, "Tooltip": 0, "Placeholder": 0,
		"do": 1, "note": 0, "submenu": 0, "command": 1, "MenuSelect": 1,
		"inform": 0, "done": 0, "warn": 0, "fail": 0,
	}
	variadic := map[string]int{"Segmented": 2, "Tabs": 3, "SubTabs": 3}
	fields := map[string]bool{
		"Label": true, "Title": true, "Description": true, "Placeholder": true,
		"Tooltip": true, "Empty": true, "Action": true, "Detail": true,
		"Prompt": true, "OKLabel": true, "CancelLabel": true,
		"SearchPlaceholder": true, "EmptyText": true, "Heading": true,
		"Subtitle": true, "Caption": true, "Hint": true,
	}
	var texts []string
	add := func(lit *ast.BasicLit) {
		if lit.Kind != token.STRING {
			return
		}
		text, err := strconv.Unquote(lit.Value)
		if err == nil && hasLetter(text) {
			texts = append(texts, text)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			name := ""
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if name == "Label" {
				// uikit.Label(c, text) and Element.Label(text).
				i := 0
				if len(x.Args) >= 2 {
					if id, ok := x.Args[0].(*ast.Ident); ok && id.Name == "c" {
						i = 1
					}
				}
				if i < len(x.Args) {
					if lit, ok := x.Args[i].(*ast.BasicLit); ok {
						add(lit)
					}
				}
				return true
			}
			if i, ok := args[name]; ok && i < len(x.Args) {
				if lit, ok := x.Args[i].(*ast.BasicLit); ok {
					add(lit)
				}
			}
			if start, ok := variadic[name]; ok {
				for i := start; i < len(x.Args); i++ {
					if lit, ok := x.Args[i].(*ast.BasicLit); ok {
						add(lit)
					}
				}
			}
		case *ast.CompositeLit:
			if id, ok := x.Type.(*ast.Ident); ok && id.Name == "notice" {
				for _, e := range x.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || (key.Name != "text" && key.Name != "detail" && key.Name != "action") {
						continue
					}
					if lit, ok := kv.Value.(*ast.BasicLit); ok {
						add(lit)
					}
				}
				return true
			}
			for _, e := range x.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || !fields[key.Name] {
					continue
				}
				if lit, ok := kv.Value.(*ast.BasicLit); ok {
					add(lit)
				}
			}
		}
		return true
	})
	return texts
}

func readAllowed(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		allowed[line] = true
		allowed[strings.ReplaceAll(line, `\n`, "\n")] = true
	}
	return allowed
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// TestOptionLabels keeps every fixed option set translatable: a value either
// has a message or is a protocol or format identifier that stays as it is.
func TestOptionLabels(t *testing.T) {
	zh := locales.In("zh-Hans")
	for set, values := range optionSets {
		for _, value := range values {
			if optionLabelFor(zh, value) == value && !isOptionIdentifier(value) {
				t.Errorf("%s: %q has no message and is not a known identifier", set, value)
			}
		}
	}
}

// TestOptionsUseTheOptionCatalog walks Select and SelectLabels calls and
// checks every option, including one named by a package-level slice, so a new
// option cannot be added in English alone.
func TestOptionsUseTheOptionCatalog(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	lists := packageStringLists(t, files)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if (name != "SelectLabels" && name != "Select") || len(call.Args) < 3 {
				return true
			}
			zh := locales.In("zh-Hans")
			for _, value := range stringListValues(call.Args[2], lists) {
				if optionLabelFor(zh, value) == value && !isOptionIdentifier(value) {
					t.Errorf("%s: %s option %q has no message and is not a known identifier", path, name, value)
				}
			}
			return true
		})
	}
}

// packageStringLists maps each package-level []string variable to its
// literal values, so a Select given one can be checked.
// isOptionIdentifier reports an option that is not prose: a protocol or
// format name, a number, or a date such as a protocol version.
func isOptionIdentifier(value string) bool {
	if optionIdentifiers[value] {
		return true
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return true
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func packageStringLists(t *testing.T, files []string) map[string][]string {
	t.Helper()
	lists := map[string][]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) {
						continue
					}
					if values := literalStrings(value.Values[i]); values != nil {
						lists[name.Name] = values
					}
				}
			}
		}
	}
	return lists
}

// stringListValues reads a []string literal, a package-level variable, or an
// append of the two, and returns the literal values it can see.
func stringListValues(expr ast.Expr, lists map[string][]string) []string {
	switch x := expr.(type) {
	case *ast.CompositeLit:
		return literalStrings(x)
	case *ast.Ident:
		return lists[x.Name]
	case *ast.CallExpr:
		if len(x.Args) > 0 {
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "append" {
				return stringListValues(x.Args[0], lists)
			}
		}
	}
	return nil
}

func literalStrings(expr ast.Expr) []string {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var values []string
	for _, elt := range lit.Elts {
		basic, ok := elt.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			continue
		}
		text, err := strconv.Unquote(basic.Value)
		if err == nil {
			values = append(values, text)
		}
	}
	return values
}

// TestScriptSnippetsUseTheCatalog keeps Insert snippet labels translatable:
// a snippet is a pair of a label and its code, and the label must come from
// the catalog, not a literal.
func TestScriptSnippetsUseTheCatalog(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "scripts.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || len(lit.Elts) != 2 {
			return true
		}
		arr, ok := lit.Type.(*ast.ArrayType)
		if !ok {
			return true
		}
		length, ok := arr.Len.(*ast.BasicLit)
		element, ok := arr.Elt.(*ast.Ident)
		if !ok || length.Value != "2" || element.Name != "string" {
			return true
		}
		if label, ok := lit.Elts[0].(*ast.BasicLit); ok && label.Kind == token.STRING {
			t.Errorf("snippet label %s is not a catalog message", label.Value)
		}
		return true
	})
}

// TestSnippetMenuChinese checks the sandbox library snippets say their labels
// in Chinese, including the ones added with the bundled libraries.
func TestSnippetMenuChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	st := &editor.State{Buffer: editor.NewBuffer("")}
	tt := ui.NewTester(func(c *ui.Context) { snippetMenu(c, st, "request") }, 420, 700)
	tt.SetLocale("zh-Hans")
	tt.Frame()
	if err := tt.Click("插入代码片段"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"用 cheerio 解析 HTML", "用 xml2js 解析 XML", "解析 CSV", "用 Ajv 验证 schema", "生成 UUID", "解码 Base64"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}
