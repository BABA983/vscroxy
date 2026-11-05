package request

import (
	"reflect"
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
