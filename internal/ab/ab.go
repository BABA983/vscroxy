// Package ab compares the same requests replayed against two environments.
// It reports structural differences: status, content type and the JSON shape
// of the response, not scalar values that legitimately differ between them.
package ab

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"encoding/xml"
	"fmt"
	"maps"
	"mime"
	"slices"
	"strings"
)

// Response is one side's response for an endpoint.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
	Error       string
}

// Endpoint is one request's comparison. Differences are empty when both sides
// have the same status, content type and body shape.
type Endpoint struct {
	Name        string   `json:"name"`
	Method      string   `json:"method"`
	URL         string   `json:"url"`
	Differences []string `json:"differences,omitempty"`
}

// Report is a complete A/B replay.
type Report struct {
	Version   int        `json:"version"`
	Input     string     `json:"input"`
	A         string     `json:"a"`
	B         string     `json:"b"`
	Same      int        `json:"same"`
	Different int        `json:"different"`
	Endpoints []Endpoint `json:"endpoints"`
}

// Compare returns one endpoint's structural differences.
func Compare(name, method, url string, a, b Response) Endpoint {
	endpoint := Endpoint{Name: name, Method: method, URL: url}
	add := func(format string, args ...any) {
		endpoint.Differences = append(endpoint.Differences, fmt.Sprintf(format, args...))
	}
	if a.Error != "" || b.Error != "" {
		switch {
		case a.Error != "" && b.Error != "" && a.Error != b.Error:
			add("request failed: A %s; B %s", oneLine(a.Error), oneLine(b.Error))
		case a.Error != "":
			add("request failed only on A: %s", oneLine(a.Error))
		case b.Error != "":
			add("request failed only on B: %s", oneLine(b.Error))
		}
		return endpoint
	}
	if a.Status != b.Status {
		add("status %d vs %d", a.Status, b.Status)
	}
	if mediaType(a.ContentType) != mediaType(b.ContentType) {
		add("content type %q vs %q", mediaType(a.ContentType), mediaType(b.ContentType))
	}
	endpoint.Differences = append(endpoint.Differences, bodyShapeDiff(a.Body, b.Body)...)
	return endpoint
}

func bodyShapeDiff(a, b []byte) []string {
	aValue, aJSON := decode(a)
	bValue, bJSON := decode(b)
	switch {
	case len(bytes.TrimSpace(a)) == 0 && len(bytes.TrimSpace(b)) == 0:
		return nil
	case len(bytes.TrimSpace(a)) == 0:
		return []string{"body empty on A, not on B"}
	case len(bytes.TrimSpace(b)) == 0:
		return []string{"body empty on B, not on A"}
	case aJSON && bJSON:
		return shapeDiff(aValue, bValue, "")
	case aJSON != bJSON:
		if aJSON {
			return []string{"body is JSON on A, not on B"}
		}
		return []string{"body is JSON on B, not on A"}
	case bytes.Equal(a, b):
		return nil
	default:
		return []string{"body text differs"}
	}
}

func decode(data []byte) (any, bool) {
	if !jsontext.Value(data).IsValid() {
		return nil, false
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil, false
	}
	return value, true
}

func shapeDiff(a, b any, path string) []string {
	ka, kb := kind(a), kind(b)
	if ka != kb {
		return []string{path + ": type " + ka + " vs " + kb}
	}
	switch av := a.(type) {
	case map[string]any:
		bv := b.(map[string]any)
		var out []string
		for _, key := range slices.Sorted(maps.Keys(mapKeys(av, bv))) {
			child := path + "/" + pointerToken(key)
			left, inA := av[key]
			right, inB := bv[key]
			switch {
			case !inA:
				out = append(out, child+": only on B")
			case !inB:
				out = append(out, child+": only on A")
			default:
				out = append(out, shapeDiff(left, right, child)...)
			}
		}
		return out
	case []any:
		bv := b.([]any)
		if len(av) != len(bv) {
			return []string{fmt.Sprintf("%s: array length %d vs %d", path, len(av), len(bv))}
		}
		var out []string
		for i := range av {
			out = append(out, shapeDiff(av[i], bv[i], path+"/"+fmt.Sprint(i))...)
		}
		return out
	default:
		return nil
	}
}

func mapKeys(a, b map[string]any) map[string]struct{} {
	keys := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	return keys
}

func kind(value any) string {
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

func pointerToken(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func mediaType(value string) string {
	if value == "" {
		return ""
	}
	if media, _, err := mime.ParseMediaType(value); err == nil {
		return media
	}
	return value
}

func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

// Add records one endpoint and updates the counts.
func (r *Report) Add(endpoint Endpoint) {
	r.Endpoints = append(r.Endpoints, endpoint)
	if len(endpoint.Differences) == 0 {
		r.Same++
	} else {
		r.Different++
	}
}

// JSON renders the report for a file.
func (r Report) JSON() ([]byte, error) {
	r.Version = 1
	if r.Endpoints == nil {
		r.Endpoints = []Endpoint{}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// JUnit renders one test case per endpoint, with differences as its failure.
func (r Report) JUnit() ([]byte, error) {
	type failure struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	}
	type testcase struct {
		Name    string   `xml:"name,attr"`
		Class   string   `xml:"classname,attr"`
		Failure *failure `xml:"failure,omitempty"`
	}
	suite := struct {
		XMLName  xml.Name   `xml:"testsuite"`
		Name     string     `xml:"name,attr"`
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Cases    []testcase `xml:"testcase"`
	}{Name: "A/B replay", Tests: len(r.Endpoints), Failures: r.Different}
	for _, endpoint := range r.Endpoints {
		item := testcase{Name: endpoint.Name, Class: endpoint.Method + " " + endpoint.URL}
		if len(endpoint.Differences) > 0 {
			item.Failure = &failure{Message: endpoint.Differences[0], Text: strings.Join(endpoint.Differences, "\n")}
		}
		suite.Cases = append(suite.Cases, item)
	}
	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(data, '\n')...), nil
}
