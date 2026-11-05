package request

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPreferUsesCurrentUnlessItIsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		initial, current, want string
	}{
		{"current wins", "shared", "local", "local"},
		{"empty current falls back", "shared", "", "shared"},
		{"both empty", "", "", ""},
		{"current only", "", "local", "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Prefer(tc.initial, tc.current); got != tc.want {
				t.Errorf("Prefer(%q, %q) = %q, want %q", tc.initial, tc.current, got, tc.want)
			}
			if got := (Variable{Name: "v", Initial: tc.initial, Current: tc.current}).Value(); got != tc.want {
				t.Errorf("Variable.Value() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValuesKeepsCurrentOnlyNamesDefined(t *testing.T) {
	got := Values(
		map[string]string{"shared": "initial", "empty": "initial-empty"},
		map[string]string{"shared": "local", "empty": "", "only": "local-only", "emptyOnly": ""},
	)
	want := map[string]string{"shared": "local", "empty": "initial-empty", "only": "local-only", "emptyOnly": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Values = %v, want %v", got, want)
	}
}

// An unset in a script clears the Current override and falls back to Initial
// during the run; the saved Current is cleared afterwards, never Initial.
func TestUnsetFallsBackToInitial(t *testing.T) {
	in := Context{
		Globals:            map[string]string{"team": "team-current"},
		GlobalsInitial:     map[string]string{"team": "team-initial"},
		EnvironmentInitial: map[string]string{"env": "env-initial"},
		Folders: []Folder{{Path: "Shop", Variables: []Variable{
			{Name: "collection", Initial: "collection-initial", Current: "collection-current"},
			{Name: "local-only", Initial: "initial-only"},
		}}},
	}
	run := in.scriptRun(Request{Collection: "Shop"}, map[string]string{
		"env": "env-current", "only": "only-current", "collection": "collection-current",
	})
	if err := run.set("environment", "env", "", true); err != nil {
		t.Fatal(err)
	}
	if err := run.set("environment", "only", "", true); err != nil {
		t.Fatal(err)
	}
	if err := run.set("globals", "team", "", true); err != nil {
		t.Fatal(err)
	}
	if err := run.set("collectionVariables", "collection", "", true); err != nil {
		t.Fatal(err)
	}
	if run.vars["env"] != "env-initial" || run.globals["team"] != "team-initial" || run.collection["collection"] != "collection-initial" {
		t.Errorf("run values = %+v / %+v / %+v", run.vars, run.globals, run.collection)
	}
	if _, ok := run.vars["only"]; ok {
		t.Errorf("Current-only name stayed defined: %+v", run.vars)
	}
	if len(run.writes) != 4 {
		t.Fatalf("writes = %+v, want two environment, global and collection unsets", run.writes)
	}
	if err := run.set("environment", "reborn", "", true); err != nil {
		t.Fatal(err)
	}
	if err := run.set("environment", "reborn", "new", false); err != nil {
		t.Fatal(err)
	}
	if run.vars["reborn"] != "new" || slices.ContainsFunc(run.writes, func(w Write) bool {
		return w.Scope == "environment" && w.Name == "reborn" && w.Unset
	}) {
		t.Errorf("set after unset = %q, writes %+v", run.vars["reborn"], run.writes)
	}

	next := in.Apply([]Write{
		{Scope: "global", Name: "team", Unset: true},
		{Scope: "collection", Path: "Shop", Name: "collection", Unset: true},
	}, "Shop")
	if next.Globals["team"] != "team-initial" {
		t.Errorf("Apply global unset = %q, want team-initial", next.Globals["team"])
	}
	if got := next.Folders[0].Variables[0]; got.Initial != "collection-initial" || got.Current != "" {
		t.Errorf("Apply collection unset = %+v, want Initial kept and Current cleared", got)
	}
	vars := []Variable{
		{Name: "collection", Initial: "collection-initial", Current: "collection-current"},
		{Name: "local-only", Current: "local", Local: true},
	}
	got := Write{Scope: "collection", Name: "collection", Unset: true}.Variables(vars)
	if len(got) != 2 || got[0].Name != "collection" || got[0].Initial != "collection-initial" || got[0].Current != "" {
		t.Errorf("Write.Variables = %+v, want the Initial row and the unrelated local row", got)
	}
	got = Write{Scope: "collection", Name: "local-only", Unset: true}.Variables(got)
	if len(got) != 1 || got[0].Name != "collection" {
		t.Errorf("Write.Variables for a local-only row = %+v, want it removed", got)
	}
}

func TestVariableRefsCarryByteOffsets(t *testing.T) {
	text := "{{base}}/项目?q={{ query }}&x={{missing}}"
	refs := VariableRefs(text)
	want := []VariableRef{{Name: "base", Start: 0, End: 8}, {Name: "query", Start: 18, End: 29}, {Name: "missing", Start: 32, End: 43}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %+v, want %+v", refs, want)
	}
	for _, ref := range refs {
		if got := strings.TrimSpace(text[ref.Start+2 : ref.End-2]); got != ref.Name {
			t.Errorf("offset %d:%d holds %q, want %q", ref.Start, ref.End, got, ref.Name)
		}
		if got, ok := VariableAt(text, ref.Start); !ok || got != ref {
			t.Errorf("VariableAt(%d) = %+v, %v", ref.Start, got, ok)
		}
	}
	if _, ok := VariableAt(text, 8); ok {
		t.Error("a position between references must not match")
	}
	for _, text := range []string{"", "{{}}", "{{unterminated", "plain"} {
		if refs := VariableRefs(text); len(refs) != 0 {
			t.Errorf("VariableRefs(%q) = %+v", text, refs)
		}
	}
}

func TestDynamicNamesAndSamples(t *testing.T) {
	names := DynamicNames()
	if len(names) != 32 {
		t.Fatalf("DynamicNames has %d names, want 32: %v", len(names), names)
	}
	for _, name := range names {
		value, ok := DynamicValue(name)
		if !ok || value == "" {
			t.Errorf("DynamicValue(%q) = %q, %v", name, value, ok)
		}
	}
	if _, ok := DynamicValue("$notImplemented"); ok {
		t.Error("an unknown dynamic variable was accepted")
	}
}

func TestFolderVariablesResolveCurrentBeforeInitial(t *testing.T) {
	in := Context{Folders: []Folder{{Path: "Shop", Variables: []Variable{
		{Name: "current", Initial: "initial", Current: "local"},
		{Name: "fallback", Initial: "initial", Current: ""},
		{Name: "only", Current: "local-only"},
		{Name: "disabled", Initial: "initial", Current: "local", Disabled: true},
	}}}}
	values, sources := in.inherited()
	if values["current"] != "local" || values["fallback"] != "initial" || values["only"] != "local-only" {
		t.Errorf("values = %v", values)
	}
	if _, ok := values["disabled"]; ok {
		t.Error("a disabled row was defined")
	}
	if sources["current"] != "Collection: Shop" {
		t.Errorf("source = %q", sources["current"])
	}
	// A script writes Current, so the Initial value survives for an export.
	next := in.Apply([]Write{{Scope: "collection", Path: "Shop", Name: "current", Value: "script"}}, "Shop")
	if got := next.Folders[0].Variables[0]; got.Initial != "initial" || got.Current != "script" {
		t.Errorf("write changed the row to %+v", got)
	}
	if in.Folders[0].Variables[0].Current != "local" {
		t.Error("Apply mutated the original context")
	}
}
