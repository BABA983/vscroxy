package main

import (
	"maps"
	"slices"
	"strings"
)

// curlSkip are the headers curl sets itself, so a copied command does not
// send them twice or with a stale value.
var curlSkip = map[string]bool{
	"connection":       true,
	"proxy-connection": true,
	"content-length":   true,
	"host":             true,
}

// curlCommand writes the flow's request as a curl command line, the way it
// would be run from a terminal.
func curlCommand(f flow) string {
	var b strings.Builder
	b.WriteString("curl")
	if f.method != "" && f.method != "GET" {
		b.WriteString(" -X " + f.method)
	}
	for _, key := range slices.Sorted(maps.Keys(f.reqHeader)) {
		if curlSkip[strings.ToLower(key)] {
			continue
		}
		for _, value := range f.reqHeader[key] {
			b.WriteString(" -H " + shellQuote(key+": "+value))
		}
	}
	if f.reqBody != "" {
		b.WriteString(" --data-raw " + shellQuote(f.reqBody))
	}
	b.WriteString(" " + shellQuote(f.url))
	return b.String()
}

// shellQuote quotes s for a POSIX shell, so a body with quotes survives.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
