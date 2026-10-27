package request

import "strings"

// ExpandVars replaces {{name}} with a variable's value, leaving a name it does
// not know as it is, so the mistake shows in the request.
func ExpandVars(s string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(s, "{{") {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		name := strings.TrimSpace(s[start+2 : start+end])
		if value, ok := vars[name]; ok {
			b.WriteString(value)
		} else {
			b.WriteString(s[start : start+end+2])
		}
		s = s[start+end+2:]
	}
}
