package request

import (
	"maps"
	"slices"
	"strings"
)

// Variable is one row of a collection's or folder's variables. Initial is
// saved with the collection directory, so it can be committed to Git. Current
// is kept on this machine only and never written to the library or an export.
// A send uses Current when it is not empty, otherwise Initial.
type Variable struct {
	Name    string `json:"name"`
	Initial string `json:"initial,omitempty"`
	// Current is local: json:"-" keeps it out of every collection file.
	Current string `json:"-"`
	// Local marks a row that exists only as a Current value. It is never
	// written to the library; clearing its Current removes the row.
	Local bool `json:"-"`
	// Credential marks a local value that came from a credential slot. It is
	// never written to the library and Persist current leaves it local.
	Credential bool `json:"-"`
	Disabled   bool `json:"disabled,omitempty"`
	// Revealed is the table's own state, never saved.
	Revealed bool `json:"-"`
}

// Value is the value a send reads: a nonempty Current overrides Initial.
func (v Variable) Value() string {
	return Prefer(v.Initial, v.Current)
}

// Prefer is the value a send reads from one scope: a nonempty Current wins,
// an empty one falls back to Initial.
func Prefer(initial, current string) string {
	if current != "" {
		return current
	}
	return initial
}

// Values resolves a scope's Initial and Current values. A nonempty Current
// wins; an empty Current falls back to Initial. A Current name with no
// Initial still defines an empty value, as a reserved local slot does.
func Values(initial, current map[string]string) map[string]string {
	values := maps.Clone(initial)
	if values == nil {
		values = map[string]string{}
	}
	for name, value := range current {
		if value != "" {
			values[name] = value
			continue
		}
		if _, ok := values[name]; !ok {
			values[name] = ""
		}
	}
	return values
}

// VariableRef is one {{name}} in a text, with byte offsets of the whole
// reference, braces included.
type VariableRef struct {
	Name  string
	Start int
	End   int
}

// VariableRefs lists the {{name}} references in s, in order. An unterminated
// "{{" is not a reference; an empty name is skipped.
func VariableRefs(s string) []VariableRef {
	var refs []VariableRef
	for at := 0; at < len(s); {
		start := strings.Index(s[at:], "{{")
		if start < 0 {
			break
		}
		start += at
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		if name := strings.TrimSpace(s[start+2 : end]); name != "" {
			refs = append(refs, VariableRef{Name: name, Start: start, End: end + 2})
		}
		at = end + 2
	}
	return refs
}

// VariableAt returns the reference holding byte offset, if any.
func VariableAt(s string, offset int) (VariableRef, bool) {
	for _, ref := range VariableRefs(s) {
		if offset >= ref.Start && offset < ref.End {
			return ref, true
		}
	}
	return VariableRef{}, false
}

// DynamicNames lists the {{$name}} variables a send generates, sorted.
func DynamicNames() []string {
	return slices.Sorted(maps.Keys(dynamic))
}

// DynamicValue generates a sample of the dynamic variable named, as a send
// would for one occurrence.
func DynamicValue(name string) (string, bool) {
	generate := dynamic[name]
	if generate == nil {
		return "", false
	}
	return generate(), true
}
