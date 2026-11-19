package app

import "strings"

// scriptVariableRef is a name passed to pm.<scope>.get("…") in a script.
// Start and End are byte offsets of the name itself, inside the quotes, so a
// hover underlines only the name and a completion replaces only what was
// typed there. The editor's normal word prefix is the typed part of the name.
type scriptVariableRef struct {
	Name       string
	Start, End int
}

// scriptVariableAt finds the quoted name at offset. The four scopes are the
// ones pm.js exposes as readable variable stores; pm.variables reads through
// the same scopes a {{name}} reference resolves.
func scriptVariableAt(text string, offset int) (scriptVariableRef, bool) {
	if offset < 0 || offset > len(text) {
		return scriptVariableRef{}, false
	}
	for at := 0; at < len(text); at++ {
		if !strings.HasPrefix(text[at:], "pm.") {
			continue
		}
		scope, after := identifierAt(text, at+3)
		switch scope {
		case "environment", "collectionVariables", "globals", "variables":
		default:
			continue
		}
		after = skipScriptSpace(text, after)
		if after >= len(text) || text[after] != '.' {
			continue
		}
		after = skipScriptSpace(text, after+1)
		method, afterMethod := identifierAt(text, after)
		if method != "get" {
			continue
		}
		after = skipScriptSpace(text, afterMethod)
		if after >= len(text) || text[after] != '(' {
			continue
		}
		after = skipScriptSpace(text, after+1)
		if after >= len(text) || (text[after] != '"' && text[after] != '\'' && text[after] != '`') {
			continue
		}
		quote := text[after]
		start := after + 1
		end, _ := quotedEnd(text, start, quote)
		// An empty literal is a valid completion site, even though there is
		// no name to hover over yet.
		if offset < start || offset > end {
			continue
		}
		return scriptVariableRef{Name: text[start:end], Start: start, End: end}, true
	}
	return scriptVariableRef{}, false
}

func identifierAt(text string, at int) (string, int) {
	end := at
	for end < len(text) {
		c := text[end]
		if c != '_' && !isASCIILetter(c) && !isASCIIDigit(c) {
			break
		}
		end++
	}
	return text[at:end], end
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c byte) bool  { return c >= '0' && c <= '9' }

func skipScriptSpace(text string, at int) int {
	for at < len(text) && (text[at] == ' ' || text[at] == '\t') {
		at++
	}
	return at
}

// quotedEnd stops at the matching quote or at the end of the line. Escape
// sequences are skipped as a pair so a name with an escaped quote stays whole.
func quotedEnd(text string, at int, quote byte) (int, bool) {
	for at < len(text) && text[at] != '\n' {
		if text[at] == '\\' {
			at += 2
			continue
		}
		if text[at] == quote {
			return at, true
		}
		at++
	}
	return min(at, len(text)), false
}
