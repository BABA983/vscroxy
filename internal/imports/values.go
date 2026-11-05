package imports

import (
	"encoding/json/v2"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var insomniaTag = regexp.MustCompile(`\{\{\s*_\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
var hoppTag = regexp.MustCompile(`<<\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*>>`)

// normalizeTags turns the source's variable spelling into the app's
// {{name}}: Insomnia writes {{ _.name }} and Hoppscotch <<name>>.
func normalizeTags(value string) string {
	value = insomniaTag.ReplaceAllString(value, "{{$1}}")
	return hoppTag.ReplaceAllString(value, "{{$1}}")
}

// valueText renders a JSON/YAML scalar as a variable or field value.
func valueText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return normalizeTags(v)
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	default:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
		return fmt.Sprint(v)
	}
}

// valueBool reads the booleans exports write as booleans or as strings.
func valueBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

func valueMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func valueList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

// pathVariables replaces :name path segments with a value or {{name}}.
func pathVariables(raw string, values map[string]string) string {
	start := 0
	if scheme, _, ok := strings.Cut(raw, "://"); ok && !strings.ContainsAny(scheme, "/?#") {
		start = len(scheme) + len("://")
	}
	slash := strings.IndexByte(raw[start:], '/')
	if slash < 0 {
		return raw
	}
	begin, end := start+slash, len(raw)
	if i := strings.IndexAny(raw[begin:], "?#"); i >= 0 {
		end = begin + i
	}
	segments := strings.Split(raw[begin:end], "/")
	for i, segment := range segments {
		if name, ok := strings.CutPrefix(segment, ":"); ok {
			if value, ok := values[name]; ok {
				segments[i] = value
				if segments[i] == "" {
					segments[i] = "{{" + name + "}}"
				}
			}
		}
	}
	return raw[:begin] + strings.Join(segments, "/") + raw[end:]
}

// appendQuery adds enabled parameters that the URL does not already carry.
func appendQuery(raw string, params []requestField) string {
	if len(params) == 0 {
		return raw
	}
	base, fragment, hasFragment := strings.Cut(raw, "#")
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	var pairs []string
	for _, param := range params {
		if param.Disabled || param.Name == "" {
			continue
		}
		name := urlQueryEscape(param.Name)
		if hasQueryKey(base, name) {
			continue
		}
		if param.Value == "" {
			pairs = append(pairs, name)
		} else {
			pairs = append(pairs, name+"="+urlQueryEscape(param.Value))
		}
	}
	if len(pairs) > 0 {
		base += separator + strings.Join(pairs, "&")
	}
	if hasFragment {
		base += "#" + fragment
	}
	return base
}

// requestField is a source parameter before it becomes a request.Field.
type requestField struct {
	Name, Value string
	Disabled    bool
	File        bool
}

func hasQueryKey(raw, escapedName string) bool {
	_, query, ok := strings.Cut(raw, "?")
	if !ok {
		return false
	}
	query, _, _ = strings.Cut(query, "#")
	for part := range strings.SplitSeq(query, "&") {
		name, _, _ := strings.Cut(part, "=")
		if name == escapedName || strings.EqualFold(name, escapedName) {
			return true
		}
	}
	return false
}

// urlQueryEscape escapes a query name or value the way the app's URL does.
func urlQueryEscape(value string) string {
	if strings.Contains(value, "{{") {
		return value
	}
	return url.QueryEscape(value)
}
