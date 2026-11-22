package app

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
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

// source names the scope and which of its two values applies, in the language
// the app shows.
func (h variableHint) source() string {
	if h.Origin == "" {
		return variableScopeName(h.Scope)
	}
	return variableScopeName(h.Scope) + " · " + variableOriginName(h.Origin)
}

// variableScopeName says a scope in the language the app shows. The scope
// itself stays an identifier, which variableScopeRank compares.
func variableScopeName(scope string) string {
	kind, path, _ := strings.Cut(scope, " · ")
	switch kind {
	case "Environment":
		return locales.KindPath(locales.Environment(), path)
	case "Collection":
		return locales.KindPath(locales.Collection(), path)
	case "Folder":
		return locales.KindPath(locales.FolderName(), path)
	case "Global":
		return locales.ScopeGlobal()
	case "Dynamic":
		return locales.ScopeDynamic()
	case "Vault":
		return locales.ScopeVault()
	}
	return scope
}

// variableOriginName says which of a variable's two values applies.
func variableOriginName(origin string) string {
	if origin == "initial" {
		return locales.OriginInitial()
	}
	return locales.OriginCurrent()
}

// display is the value a preview may show, masked when the name says it holds
// a credential or the value comes from the Vault.
func (h variableHint) display() string {
	switch {
	case h.Dynamic:
		return locales.GeneratedAtSend()
	case h.Vault:
		return locales.HiddenInTheVault()
	case h.Sensitive:
		return "••••••"
	case h.Value == "":
		return locales.EmptyPlaceholder()
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
	// Split camelCase before lowercasing, so accessToken and apiKey keep
	// their credential word; then split separators such as _ and -.
	var words []string
	runes, from := []rune(name), 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev) || unicode.IsUpper(prev) && nextLower) {
			words = append(words, string(runes[from:i]))
			from = i
		}
	}
	words = append(words, string(runes[from:]))
	for _, word := range words {
		for _, part := range strings.FieldsFunc(strings.ToLower(word), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			switch part {
			case "authorization", "cookie", "credential", "credentials", "key", "keys",
				"passwd", "password", "pass", "pwd", "secret", "secrets", "session",
				"token", "tokens", "bearer", "jwt", "auth", "apikey", "accesskey",
				"secretkey", "clientsecret", "authtoken", "accesstoken", "refreshtoken", "sessionid":
				return true
			}
		}
	}
	return false
}

// variableSet is one resolution of a request's scopes, with the list the
// completion shows and the lookup marks and hovers use.
type variableSet struct {
	list        []variableHint
	byName      map[string]variableHint
	vaultLocked bool
}

// hint resolves one name in this set. While the Vault is locked, a
// {{vault:name}} reference is not undefined: only opening the Vault can tell.
func (s variableSet) hint(name string) (variableHint, bool) {
	if h, ok := s.byName[name]; ok {
		return h, true
	}
	if s.vaultLocked {
		if _, ok := strings.CutPrefix(name, "vault:"); ok {
			return variableHint{Name: name, Scope: "Vault", Vault: true, Sensitive: true, Defined: true}, true
		}
	}
	return variableHint{}, false
}

// variableHintSet lists every variable an editor at collection can use,
// highest scope last, plus Dynamic and Vault names. A defined name shadows a
// Dynamic one, as a send does.
func (a *app) variableHintSet(collection string) variableSet {
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
		for name := range a.folderCredentials[path] {
			if h, ok := byName[name]; ok {
				h.Sensitive = true
				byName[name] = h
			}
		}
	}
	env := a.activeEnvironment()
	addValues(locales.EnvironmentDotName(env.Name), env.Vars, env.Current)
	for name := range env.Credentials {
		if h, ok := byName[name]; ok {
			h.Sensitive = true
			byName[name] = h
		}
	}

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
	list := slices.Collect(maps.Values(byName))
	slices.SortFunc(list, func(x, y variableHint) int {
		return cmp.Or(cmp.Compare(variableScopeRank(x), variableScopeRank(y)), strings.Compare(x.Name, y.Name))
	})
	return variableSet{list: list, byName: byName, vaultLocked: !a.vault.loaded}
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
	return a.variableHintSet(collection).hint(name)
}

// variableMark is one {{name}} with its rune offsets and resolution.
type variableMark struct {
	Ref     request.VariableRef
	Hint    variableHint
	Defined bool
	Start   int // runes into the text
	End     int
}

// variableMarksWith resolves references against a set already built for the
// frame, so a field does not rebuild and sort every scope per reference.
func variableMarksWith(set variableSet, text string) []variableMark {
	refs := request.VariableRefs(text)
	if len(refs) == 0 {
		return nil
	}
	marks := make([]variableMark, 0, len(refs))
	for _, ref := range refs {
		hint, ok := set.hint(ref.Name)
		marks = append(marks, variableMark{
			Ref: ref, Hint: hint, Defined: ok,
			Start: utf8.RuneCountInString(text[:ref.Start]), End: utf8.RuneCountInString(text[:ref.End]),
		})
	}
	return marks
}

// variablePreview is the text a hover shows for one reference. It reuses the
// same resolution the Problems panel and quick fixes use: a defined name gets
// its effective, masked value and its scope; an undefined one gets the fixes
// PR #39 offers in the panel, even when no marker has been produced yet.
func (a *app) variablePreview(collection, name string) string {
	set := a.variableHintSet(collection)
	hint, ok := set.hint(name)
	if !ok {
		text := fmt.Sprintf("%s\n%s\n%s", name, locales.ProblemUnresolvedVariable(name), locales.StaysLiteralWhenSent())
		if line := a.variableQuickFixLine(set, collection, name); line != "" {
			text += "\n" + line
		}
		return text
	}
	return fmt.Sprintf("%s\n%s\n%s", name, hint.source(), hint.display())
}

// variableQuickFixLine is the hover's text for the PR #39 actions. Both the
// editor card and the tooltip of a native field use it, so neither shows a
// different set of fixes from the Problems panel.
func (a *app) variableQuickFixLine(set variableSet, collection, name string) string {
	if _, defined := set.hint(name); defined {
		return ""
	}
	var labels []string
	for _, proposal := range a.variableFixProposals(set, collection, name) {
		labels = append(labels, proposal.label)
	}
	if len(labels) == 0 {
		return ""
	}
	return locales.VariableQuickFixes(strings.Join(labels, " · "))
}

// variableCompletionLimit matches the editor's own maximum list length; the
// "$" prefix lists every dynamic name, not just the first page.
const variableCompletionLimit = 200

// rankVariableHints orders the completion list by the same fuzzy scorer the
// app's palettes use, then by scope and name for ties. A lateral match such as
// "ten" still finds content_type, but the contiguous prefix of tenant wins;
// scope alone must not decide between a near and a far name.
func rankVariableHints(prefix string, hints []variableHint, limit int) []variableHint {
	if limit < 0 {
		limit = 0
	}
	if prefix == "" {
		return append([]variableHint(nil), hints[:min(limit, len(hints))]...)
	}
	lower := strings.ToLower(prefix)
	type ranked struct {
		hint  variableHint
		score int
	}
	var matches []ranked
	for _, hint := range hints {
		score := fuzzy.ScoreFuzzy(hint.Name, prefix, lower, true).Score
		if score > 0 {
			matches = append(matches, ranked{hint, score})
		}
	}
	slices.SortStableFunc(matches, func(x, y ranked) int {
		return cmp.Or(cmp.Compare(y.score, x.score),
			variableScopeRank(x.hint)-variableScopeRank(y.hint),
			strings.Compare(x.hint.Name, y.hint.Name))
	})
	out := make([]variableHint, 0, min(limit, len(matches)))
	for _, match := range matches[:min(limit, len(matches))] {
		out = append(out, match.hint)
	}
	return out
}

// variableCompletionItems are the editor's completions for the typed prefix.
// suffix is "}}" unless the text after the caret already closes the braces.
func (a *app) variableCompletionItems(collection, prefix, suffix string) []editor.Completion {
	set := a.variableHintSet(collection)
	var items []editor.Completion
	for _, hint := range rankVariableHints(prefix, set.list, variableCompletionLimit) {
		items = append(items, editor.Completion{
			Label: hint.Name, Detail: hint.source(), Insert: hint.Name + suffix,
			Kind: editor.CompletionVariable,
		})
	}
	return items
}
