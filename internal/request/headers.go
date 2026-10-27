package request

import (
	"maps"
	"net/http"
	"slices"
	"strings"
)

// HeaderBlock writes headers the way the editor takes them back.
func HeaderBlock(h http.Header) string {
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(h)) {
		for _, value := range h[key] {
			b.WriteString(key + ": " + value + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// ParseHeaderBlock reads the editor's headers, one "Key: Value" per line.
func ParseHeaderBlock(text string) http.Header {
	h := http.Header{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		h.Add(key, strings.TrimSpace(value))
	}
	return h
}
