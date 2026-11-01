package app

// Query syntax for the capture search field.
//
// The field holds free text, which matches a request's host and path as it
// always has, and key:value tokens that set the same conditions as the
// Filters dialog and the context menus. A token becomes a chip once
// whitespace follows it, or on Enter. Until then it is left out of the free
// text, so a half-typed token never empties the table by acting as a URL
// search. Only the keys below are syntax: "localhost:8080" is still text.

import (
	"cmp"
	"fmt"
	"strings"
)

// queryKey is one key of the syntax and the condition it sets. check returns
// what is wrong with a value, or "" when the value is valid.
type queryKey struct {
	name  string
	field func(a *app) *string
	check func(string) string
}

// queryKeys are in the order their chips show.
var queryKeys = []queryKey{
	{"app", func(a *app) *string { return &a.filterTriage.Client }, nil},
	{"host", func(a *app) *string { return &a.filterTriage.Host }, nil},
	{"status", func(a *app) *string { return &a.filterStatus }, checkStatus},
	{"method", func(a *app) *string { return &a.filterTriage.Method }, nil},
	{"duration", func(a *app) *string { return &a.filterTriage.Duration }, checkNumber("duration", "ms")},
	{"size", func(a *app) *string { return &a.filterTriage.Size }, checkNumber("size", "response bytes")},
	{"header", func(a *app) *string { return &a.filterHeader }, nil},
	{"body", func(a *app) *string {
		if view := a.views.activeView(); view != nil {
			return &view.body
		}
		return nil
	}, nil},
	{"color", func(a *app) *string { return &a.filterTriage.Color }, nil},
	{"note", func(a *app) *string { return &a.filterTriage.Note }, nil},
}

// slowQuery is what the bare word "slow" sets: the same 1 s threshold as the
// Duration column's highlight and the sidebar's Slow.
var slowQuery = fmt.Sprintf(">=%d", slowMillis)

func checkStatus(value string) string {
	for part := range strings.SplitSeq(value, ",") {
		if _, _, ok := statusBounds(part); !ok {
			return "status takes a code, class or range, such as 404, >=400, 4xx or 200-299"
		}
	}
	return ""
}

func checkNumber(name, unit string) func(string) string {
	return func(value string) string {
		if _, ok := numericCondition(0, value); !ok {
			return fmt.Sprintf("%s takes a number of %s, such as >=500", name, unit)
		}
		return ""
	}
}

// queryToken is one whitespace-separated part of the field. key is "" for
// free text; done means whitespace follows it, so the user has finished it.
type queryToken struct {
	raw, key, value string
	done            bool
}

// tokenizeQuery splits the field at whitespace outside double quotes, so
// app:"Google Chrome" is one token.
func tokenizeQuery(q string) []queryToken {
	var tokens []queryToken
	start, quoted := -1, false
	for i, r := range q + " " {
		space := r == ' ' || r == '\t' || r == '\n'
		switch {
		case start < 0 && !space:
			start, quoted = i, r == '"'
		case start >= 0 && r == '"' && i > start:
			quoted = !quoted
		case start >= 0 && space && !quoted:
			tokens = append(tokens, parseQueryToken(q[start:i], i < len(q)))
			start = -1
		}
	}
	if start >= 0 {
		// An unclosed quote runs to the end of the field.
		tokens = append(tokens, parseQueryToken(q[start:], false))
	}
	return tokens
}

func parseQueryToken(raw string, done bool) queryToken {
	t := queryToken{raw: raw, done: done}
	if strings.EqualFold(raw, "slow") {
		t.key, t.value = "slow", slowQuery
		return t
	}
	key, value, ok := strings.Cut(raw, ":")
	if !ok || queryKeyNamed(strings.ToLower(key)) == nil {
		return t
	}
	t.key = strings.ToLower(key)
	t.value = strings.TrimSpace(strings.Trim(value, `"`))
	if t.key == "method" {
		t.value = strings.ToUpper(t.value)
	}
	return t
}

func queryKeyNamed(name string) *queryKey {
	if name == "slow" {
		name = "duration"
	}
	for i := range queryKeys {
		if queryKeys[i].name == name {
			return &queryKeys[i]
		}
	}
	return nil
}

// queryText is the field's free text, the part that searches host and path.
func queryText(q string) string {
	var words []string
	for _, t := range tokenizeQuery(q) {
		if t.key == "" {
			words = append(words, t.raw)
		}
	}
	return strings.Join(words, " ")
}

// commitQuery turns the finished tokens of the field into conditions, or
// every token when all is set (Enter). A token with an invalid value stays
// in the field and queryProblem says why. It reports whether a condition
// changed. A key that is already set is replaced: conditions always combine
// with AND, and one key holds one value, as in the Filters dialog.
func (a *app) commitQuery(all bool) bool {
	a.queryProblem = ""
	tokens := tokenizeQuery(a.query)
	var kept []string
	committed := false
	for _, t := range tokens {
		key := queryKeyNamed(t.key)
		if t.key == "" || !(t.done || all) || key == nil {
			kept = append(kept, t.raw)
			continue
		}
		problem := ""
		if t.value == "" {
			problem = t.key + " needs a value, such as " + t.key + ":…"
		} else if key.check != nil {
			problem = key.check(t.value)
		}
		field := key.field(a)
		if problem != "" || field == nil {
			a.queryProblem = cmp.Or(a.queryProblem, problem)
			kept = append(kept, t.raw)
			continue
		}
		*field = t.value
		committed = true
	}
	if !committed {
		return false
	}
	// Keep the space the user typed after the last token, so the next word
	// does not run into the free text before it.
	a.query = strings.Join(kept, " ")
	if a.query != "" && !all && tokens[len(tokens)-1].done {
		a.query += " "
	}
	a.views.capture(a)
	a.applyFilter()
	return true
}

// queryChip is a condition as the syntax writes it, for a chip a click turns
// back into text. It is "" for a condition the syntax has no key for.
func queryChip(key, value string) string {
	if key == "duration" && value == slowQuery {
		return "slow"
	}
	if strings.ContainsAny(value, " \t") {
		value = `"` + value + `"`
	}
	return key + ":" + value
}
