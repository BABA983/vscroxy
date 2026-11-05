package app

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/egoist/mygo/ui/editor"
)

// variableHint is one name an editor can offer or explain, resolved as a send
// would see it. Value is the effective value; Sensitive values are masked when
// shown. Dynamic values are generated per occurrence and carry no value here.
type variableHint struct {
	Name      string
	Value     string
	Scope     string // Environment · Local, Collection · Shop, Global, Dynamic, Vault
	Origin    string // current or initial; empty for Dynamic and Vault
	Defined   bool
	Sensitive bool
	Dynamic   bool
	Vault     bool
}

// source names the scope and which of its two values applies.
func (h variableHint) source() string {
	if h.Origin == "" {
		return h.Scope
	}
	return h.Scope + " · " + h.Origin
}

// display is the value a preview may show, masked when the name says it holds
// a credential or the value comes from the Vault.
func (h variableHint) display() string {
	switch {
	case h.Dynamic:
		return "generated at send"
	case h.Vault:
		return "hidden in the Vault"
	case h.Sensitive:
		return "••••••"
	case h.Value == "":
		return "(empty)"
	default:
		return oneLine(h.Value)
	}
}

func oneLine(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "⏎")
	if utf8.RuneCountInString(value) > 80 {
		value = string([]rune(value)[:79]) + "…"
	}
	return value
}

// sensitiveVariable reports whether a variable name usually holds a credential,
// so previews mask its value even outside the Vault.
func sensitiveVariable(name string) bool {
	lower := strings.ToLower(name)
	for _, part := range strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		switch part {
		case "authorization", "cookie", "credential", "credentials", "key", "keys", "passwd", "password", "secret", "secrets", "session", "token", "tokens":
			return true
		}
	}
	return false
}

// variableHints lists every variable an editor at collection can use, highest
// scope last, plus Dynamic and Vault names. A defined name shadows a Dynamic
// one, as a send does.
func (a *app) variableHints(collection string) []variableHint {
	byName := map[string]variableHint{}
	add := func(h variableHint) {
		if h.Name == "" {
			return
		}
		h.Defined = true
		h.Sensitive = h.Sensitive || sensitiveVariable(h.Name)
		byName[h.Name] = h
	}
	addValues := func(scope string, initial, current map[string]string) {
		for name, value := range initial {
			add(variableHint{Name: name, Value: value, Scope: scope, Origin: "initial"})
		}
		for name, value := range current {
			old, hadInitial := initial[name]
			switch {
			case value != "":
				add(variableHint{Name: name, Value: value, Scope: scope, Origin: "current"})
			case hadInitial:
				add(variableHint{Name: name, Value: old, Scope: scope, Origin: "initial"})
			default:
				add(variableHint{Name: name, Value: "", Scope: scope, Origin: "current"})
			}
		}
	}

	addValues("Global", a.globals, a.globalsCurrent)
	for _, path := range variablePathChain(collection) {
		var initial []request.Variable
		for _, f := range a.folders {
			if f.Path == path {
				initial = f.Variables
				break
			}
		}
		scope := "Collection · " + path
		if strings.Contains(path, "/") {
			scope = "Folder · " + path
		}
		values, disabled := map[string]string{}, map[string]bool{}
		for _, v := range initial {
			if name := strings.TrimSpace(v.Name); name != "" {
				if v.Disabled {
					disabled[name] = true
				} else {
					values[name] = v.Initial
				}
			}
		}
		current := map[string]string{}
		for name, value := range a.folderCurrent[path] {
			if !disabled[name] {
				current[name] = value
			}
		}
		addValues(scope, values, current)
	}
	env := a.activeEnvironment()
	addValues("Environment · "+env.Name, env.Vars, env.Current)

	if a.vault.loaded {
		for _, secret := range a.vault.secrets {
			add(variableHint{Name: "vault:" + secret.Name, Scope: "Vault", Vault: true, Sensitive: true})
		}
	}
	for _, name := range request.DynamicNames() {
		if _, defined := byName[name]; !defined {
			byName[name] = variableHint{Name: name, Scope: "Dynamic", Dynamic: true, Defined: true}
		}
	}
	hints := slices.Collect(maps.Values(byName))
	slices.SortFunc(hints, func(x, y variableHint) int {
		return cmp.Or(cmp.Compare(variableScopeRank(x), variableScopeRank(y)), strings.Compare(x.Name, y.Name))
	})
	return hints
}

// variableScopeRank orders the completion list from the nearest scope out.
func variableScopeRank(h variableHint) int {
	switch {
	case strings.HasPrefix(h.Scope, "Environment"):
		return 0
	case strings.HasPrefix(h.Scope, "Folder"):
		return 1
	case strings.HasPrefix(h.Scope, "Collection"):
		return 2
	case h.Scope == "Global":
		return 3
	case h.Dynamic:
		return 4
	default:
		return 5
	}
}

// variablePathChain is "Shop", "Shop/Orders" for "Shop/Orders".
func variablePathChain(collection string) []string {
	var paths []string
	for part := range strings.SplitSeq(strings.Trim(collection, "/"), "/") {
		if part == "" {
			continue
		}
		if len(paths) == 0 {
			paths = append(paths, part)
		} else {
			paths = append(paths, paths[len(paths)-1]+"/"+part)
		}
	}
	return paths
}

// variableHint finds one name as variableHints resolves it. A Vault reference
// is not undefined while the Vault is locked: only opening it can tell.
func (a *app) variableHint(collection, name string) (variableHint, bool) {
	for _, h := range a.variableHints(collection) {
		if h.Name == name {
			return h, true
		}
	}
	if secret, ok := strings.CutPrefix(name, "vault:"); ok {
		if !a.vault.loaded {
			return variableHint{Name: name, Scope: "Vault", Vault: true, Sensitive: true, Defined: true}, true
		}
		if slices.ContainsFunc(a.vault.secrets, func(s vault.Secret) bool { return s.Name == secret }) {
			return variableHint{Name: name, Scope: "Vault", Vault: true, Sensitive: true, Defined: true}, true
		}
	}
	return variableHint{}, false
}

// variableMark is one {{name}} with its rune offsets and resolution.
type variableMark struct {
	Ref     request.VariableRef
	Hint    variableHint
	Defined bool
	Start   int // runes into the text
	End     int
}

// variableMarks resolves the references in text for inline styling.
func (a *app) variableMarks(text, collection string) []variableMark {
	refs := request.VariableRefs(text)
	if len(refs) == 0 {
		return nil
	}
	marks := make([]variableMark, 0, len(refs))
	for _, ref := range refs {
		hint, ok := a.variableHint(collection, ref.Name)
		marks = append(marks, variableMark{
			Ref: ref, Hint: hint, Defined: ok,
			Start: utf8.RuneCountInString(text[:ref.Start]), End: utf8.RuneCountInString(text[:ref.End]),
		})
	}
	return marks
}

// variablePreview is the text an editor hover shows for one reference.
func (a *app) variablePreview(collection, name string) string {
	hint, ok := a.variableHint(collection, name)
	if !ok {
		return fmt.Sprintf("%s\nUndefined variable · it stays literal when sent", name)
	}
	return fmt.Sprintf("%s\n%s\n%s", name, hint.source(), hint.display())
}

// variableCompletionItems are the editor's completions for the typed prefix.
// suffix is "}}" unless the text after the caret already closes the braces.
func (a *app) variableCompletionItems(collection, prefix, suffix string) []editor.Completion {
	var items []editor.Completion
	for _, hint := range a.variableHints(collection) {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(hint.Name), strings.ToLower(prefix)) {
			continue
		}
		items = append(items, editor.Completion{Label: hint.Name, Detail: hint.source(), Insert: hint.Name + suffix, Kind: editor.CompletionVariable})
	}
	return items
}
