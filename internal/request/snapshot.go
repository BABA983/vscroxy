package request

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// MaxSnapshotBody bounds a stored response snapshot, as examples are bounded.
const MaxSnapshotBody = 5 << 20

// Snapshot is a whole response body kept for comparison. Vary lists RFC 6901
// pointers learned to differ between captures; VaryMode says whether they are
// skipped ("ignore", the default) or compared by JSON type only ("type").
type Snapshot struct {
	Body     string   `json:"body"`
	Vary     []string `json:"vary,omitempty"`
	VaryMode string   `json:"varyMode,omitempty"`
}

const (
	SnapshotVaryIgnore = "ignore"
	SnapshotVaryType   = "type"
)

// Validate checks the stored snapshot before a send uses it.
func (s Snapshot) Validate() error {
	if len(s.Body) > MaxSnapshotBody {
		return fmt.Errorf("response snapshots are limited to %s", byteCount(MaxSnapshotBody))
	}
	switch s.VaryMode {
	case "", SnapshotVaryIgnore, SnapshotVaryType:
	default:
		return fmt.Errorf("snapshot vary mode must be %q or %q", SnapshotVaryIgnore, SnapshotVaryType)
	}
	for _, path := range s.Vary {
		if !jsontext.Pointer(path).IsValid() {
			return fmt.Errorf("snapshot varying path %q is not an RFC 6901 pointer", path)
		}
	}
	return nil
}

// Evaluate compares actual with the snapshot. A mismatch is one line per
// differing field, with the pointer and a short description; values are not
// repeated beyond a short excerpt.
func (s Snapshot) Evaluate(actual string, complete bool) []string {
	if !complete {
		return []string{"response body is incomplete"}
	}
	if len(s.Body) > MaxSnapshotBody {
		return []string{"snapshot is larger than the supported limit"}
	}
	if err := s.Validate(); err != nil {
		return []string{err.Error()}
	}
	want, wantJSON := decodeSnapshotJSON(s.Body)
	got, gotJSON := decodeSnapshotJSON(actual)
	if !wantJSON || !gotJSON {
		if s.Body == actual {
			return nil
		}
		return []string{"body differs from the snapshot"}
	}
	mode := s.VaryMode
	if mode == "" {
		mode = SnapshotVaryIgnore
	}
	vary := make(map[string]string, len(s.Vary))
	for _, path := range s.Vary {
		vary[path] = mode
	}
	return snapshotDiff(want, got, "", vary)
}

func decodeSnapshotJSON(text string) (any, bool) {
	var value any
	if json.Unmarshal([]byte(text), &value) != nil {
		return nil, false
	}
	return value, true
}

func snapshotDiff(want, got any, path string, vary map[string]string) []string {
	if mode, ok := vary[path]; ok {
		if mode == SnapshotVaryType && snapshotKind(want) != snapshotKind(got) {
			return []string{path + ": expected " + snapshotKind(want) + ", got " + snapshotKind(got)}
		}
		return nil
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{path + ": expected object, got " + snapshotKind(got)}
		}
		var out []string
		for _, key := range slices.Sorted(maps.Keys(w)) {
			child := path + "/" + pointerToken(key)
			gv, present := g[key]
			if !present {
				out = append(out, child+": missing from the response")
				continue
			}
			out = append(out, snapshotDiff(w[key], gv, child, vary)...)
		}
		for _, key := range slices.Sorted(maps.Keys(g)) {
			if _, present := w[key]; !present {
				out = append(out, path+"/"+pointerToken(key)+": unexpected field")
			}
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok {
			return []string{path + ": expected array, got " + snapshotKind(got)}
		}
		var out []string
		if len(w) != len(g) {
			out = append(out, fmt.Sprintf("%s: expected %d items, got %d", path, len(w), len(g)))
		}
		for i := range min(len(w), len(g)) {
			out = append(out, snapshotDiff(w[i], g[i], path+"/"+strconv.Itoa(i), vary)...)
		}
		return out
	default:
		if snapshotKind(want) != snapshotKind(got) {
			return []string{path + ": expected " + snapshotKind(want) + ", got " + snapshotKind(got)}
		}
		if !snapshotScalarEqual(want, got) {
			return []string{path + ": value differs (expected " + snapshotExcerpt(want) + ", got " + snapshotExcerpt(got) + ")"}
		}
		return nil
	}
}

func snapshotKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func snapshotScalarEqual(want, got any) bool {
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		return ok && w == g
	case bool:
		g, ok := got.(bool)
		return ok && w == g
	case float64:
		g, ok := got.(float64)
		return ok && w == g
	case nil:
		return got == nil
	}
	return false
}

func snapshotExcerpt(value any) string {
	text := fmt.Sprint(value)
	if len(text) > 80 {
		text = text[:80] + "…"
	}
	return text
}

func pointerToken(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// LearnVaryingFields compares expected with capture bodies of the same
// endpoint and returns the JSON pointers whose value or presence differs. An
// array whose length differs is reported whole, so its elements are not each
// learned.
func LearnVaryingFields(expected string, captures []string) []string {
	want, ok := decodeSnapshotJSON(expected)
	if !ok {
		return nil
	}
	var bodies []any
	for _, capture := range captures {
		if value, ok := decodeSnapshotJSON(capture); ok {
			bodies = append(bodies, value)
		}
	}
	if len(bodies) == 0 {
		return nil
	}
	varying := map[string]bool{}
	learnVarying(want, bodies, "", varying)
	paths := slices.Sorted(maps.Keys(varying))
	return paths
}

func learnVarying(want any, bodies []any, path string, varying map[string]bool) {
	if len(bodies) == 0 {
		return
	}
	if w, ok := want.(map[string]any); ok {
		for key, value := range w {
			child := path + "/" + pointerToken(key)
			var values []any
			missing := false
			for _, body := range bodies {
				object, ok := body.(map[string]any)
				if !ok {
					missing = true
					break
				}
				got, present := object[key]
				if !present {
					missing = true
					break
				}
				values = append(values, got)
			}
			if missing || anyDiffers(value, values) {
				varying[child] = true
				continue
			}
			learnVarying(value, values, child, varying)
		}
		return
	}
	if w, ok := want.([]any); ok {
		lengths := []int{len(w)}
		allArrays := true
		for _, body := range bodies {
			array, ok := body.([]any)
			if !ok {
				allArrays = false
				break
			}
			lengths = append(lengths, len(array))
		}
		if !allArrays || len(slices.Compact(slices.Clone(lengths))) > 1 {
			varying[path] = true
			return
		}
		for i, value := range w {
			var values []any
			for _, body := range bodies {
				array := body.([]any)
				if i >= len(array) {
					varying[path+"/"+strconv.Itoa(i)] = true
					continue
				}
				values = append(values, array[i])
			}
			learnVarying(value, values, path+"/"+strconv.Itoa(i), varying)
		}
	}
}

// anyDiffers reports whether any value has a different type or scalar value
// from the expected one.
func anyDiffers(want any, values []any) bool {
	for _, value := range values {
		if snapshotKind(want) != snapshotKind(value) {
			return true
		}
		switch w := want.(type) {
		case map[string]any:
			// Objects are compared field by field by the caller.
			continue
		case []any:
			continue
		default:
			if !snapshotScalarEqual(w, value) {
				return true
			}
		}
	}
	return false
}

// byteCount is a compact byte size for validation messages.
func byteCount(n int) string {
	if n >= 1<<20 {
		return strconv.Itoa(n>>20) + " MiB"
	}
	return strconv.Itoa(n) + " bytes"
}
