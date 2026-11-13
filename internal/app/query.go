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
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/rules"
)

// queryKey is one key of the syntax and the condition it sets. check returns
// what is wrong with a value, or "" when the value is valid.
type queryKey struct {
	name, hint string
	field      func(a *app) *string
	check      func(string) string
}

// queryKeys are in the order their chips show.
var queryKeys = []queryKey{
	{"app", "Client app", func(a *app) *string { return &a.filterTriage.Client }, nil},
	{"host", "Exact host", func(a *app) *string { return &a.filterTriage.Host }, nil},
	{"status", "Code, class or range", func(a *app) *string { return &a.filterStatus }, checkStatus},
	{"method", "HTTP method", func(a *app) *string { return &a.filterTriage.Method }, nil},
	{"operation", "Exact GraphQL operation", func(a *app) *string { return &a.filterTriage.Operation }, checkOperation},
	{"duration", "Milliseconds", func(a *app) *string { return &a.filterTriage.Duration }, checkNumber("duration", "ms")},
	{"size", "Response bytes", func(a *app) *string { return &a.filterTriage.Size }, checkNumber("size", "response bytes")},
	{"header", "Name, or name: value", func(a *app) *string { return &a.filterHeader }, nil},
	{"body", "Text in either body", func(a *app) *string {
		if view := a.views.activeView(); view != nil {
			return &view.body
		}
		return nil
	}, nil},
	{"color", "Row color", func(a *app) *string { return &a.filterTriage.Color }, nil},
	{"note", "Text in the note", func(a *app) *string { return &a.filterTriage.Note }, nil},
}

// slowQuery is what the bare word "slow" sets: the same 1 s threshold as the
// Duration column's highlight and the sidebar's Slow.
var slowQuery = fmt.Sprintf(">=%d", slowMillis)

// captureSearch compiles once per filter pass, never per row or into the
// rules' process-wide regexp cache. The field's free text is a substring
// match, unless it is written as a pattern: a text wrapped in slashes is a
// regular expression and one with * or ? is a wildcard, both over the whole
// host and path (including query) as the field's old pattern modes were.
// Chips are conditions, so they never end up in the pattern.
func (a *app) captureSearch() func(string) bool {
	a.searchProblem = ""
	text := strings.TrimSpace(queryText(a.query))
	regex, wildcard := false, strings.ContainsAny(text, "*?")
	if len(text) > 1 && strings.HasPrefix(text, "/") && strings.HasSuffix(text, "/") {
		regex, wildcard, text = true, false, text[1:len(text)-1]
	}
	if !regex && !wildcard {
		needle := strings.ToLower(text)
		return func(target string) bool { return strings.Contains(strings.ToLower(target), needle) }
	}
	var expr string
	switch {
	case len(text) > 4096:
		a.searchProblem = "Search patterns are limited to 4096 bytes"
	case wildcard:
		expr = "(?is)^" + strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(text)) + "$"
	default:
		expr = "(?i)" + text
	}
	if a.searchProblem == "" {
		if text == "" {
			return func(string) bool { return true }
		}
		re, err := regexp.Compile(expr)
		if err == nil {
			return re.MatchString
		}
		a.searchProblem = "Invalid search pattern: " + err.Error()
	}
	return func(string) bool { return false }
}

func checkOperation(value string) string {
	if err := (rules.Matcher{Operation: value}).Validate(); err != nil {
		return err.Error()
	}
	return ""
}

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

// queryToken is one whitespace-separated part of the field, q[start:end]
// in bytes. key is "" for free text; done means whitespace follows it, so
// the user has finished it.
type queryToken struct {
	raw, key, value string
	start, end      int
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
			t := parseQueryToken(q[start:i], i < len(q))
			t.start, t.end = start, i
			tokens = append(tokens, t)
			start = -1
		}
	}
	if start >= 0 {
		// An unclosed quote runs to the end of the field.
		t := parseQueryToken(q[start:], false)
		t.start, t.end = start, len(q)
		tokens = append(tokens, t)
	}
	return tokens
}

func parseQueryToken(raw string, done bool) queryToken {
	t := queryToken{raw: raw, done: done}
	if len(raw) > 1 && strings.HasPrefix(raw, "/") && strings.HasSuffix(raw, "/") {
		return t // a regular expression, whatever it holds
	}
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
// changed. A key that is already set is replaced: one key holds one value,
// and content fields use the Filters dialog's AND/OR combination.
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

// querySuggestion is one line of the query bar's autocomplete: the token it
// writes, and what it means.
type querySuggestion struct{ text, hint string }

// maxQuerySuggestions keeps the popup within the activity strip and the top
// rows of the table it covers.
const maxQuerySuggestions = 8

// queryTokenAt is where the token holding caret, in runes, starts and ends
// in q, in bytes, and how much of it is before the caret. Between tokens it
// is an empty token at the caret.
func queryTokenAt(q string, caret int) (start, end int, prefix string) {
	at := len(q)
	for i := range q {
		if caret == 0 {
			at = i
			break
		}
		caret--
	}
	for _, t := range tokenizeQuery(q) {
		if t.start <= at && at <= t.end {
			return t.start, t.end, q[t.start:at]
		}
	}
	return at, at, ""
}

// querySuggestions completes the token being typed: a key while it has no
// colon, then the values this capture holds for it. An empty token lists
// the keys only when the user asked for the list, by a click or Down
// (asked), not after each space typed.
func (a *app) querySuggestions(prefix string, asked bool) []querySuggestion {
	var out []querySuggestion
	key, typed, hasValue := strings.Cut(prefix, ":")
	if !hasValue {
		if prefix == "" && !asked {
			return nil
		}
		lower := strings.ToLower(prefix)
		for _, k := range queryKeys {
			if strings.HasPrefix(k.name, lower) {
				out = append(out, querySuggestion{k.name + ":", k.hint})
			}
		}
		if strings.HasPrefix("slow", lower) && lower != "slow" {
			out = append(out, querySuggestion{"slow", "1 s or longer"})
		}
		return out
	}
	key = strings.ToLower(key)
	var values []querySuggestion
	switch key {
	case "status":
		values = []querySuggestion{{"2xx", "Success"}, {"3xx", "Redirect"}, {"4xx", "Client error"}, {"5xx", "Server error"}, {">=400", "Any error"}}
	case "host":
		for _, d := range a.domains {
			values = append(values, querySuggestion{d.name, fmt.Sprint(d.n)})
		}
	case "app":
		for _, d := range a.clients {
			values = append(values, querySuggestion{d.name, fmt.Sprint(d.n)})
		}
	case "method":
		seen := map[string]int{}
		for _, f := range a.flows {
			if f.method != "" {
				seen[f.method]++
			}
		}
		for _, m := range slices.Sorted(maps.Keys(seen)) {
			values = append(values, querySuggestion{m, fmt.Sprint(seen[m])})
		}
	}
	typed = strings.ToLower(strings.Trim(typed, `"`))
	var later []querySuggestion
	for _, v := range values {
		switch l := strings.ToLower(v.text); {
		case l == typed:
			// Already written: a space or Enter takes it.
		case strings.HasPrefix(l, typed):
			out = append(out, querySuggestion{queryChip(key, v.text), v.hint})
		case strings.Contains(l, typed):
			later = append(later, querySuggestion{queryChip(key, v.text), v.hint})
		}
	}
	return append(out, later...)
}

// completeQuery writes text in place of the token holding caret, in runes,
// and returns the field and where the caret goes, after it. A key keeps the
// caret on it for the value; anything else is a whole token, so a space
// follows it and commitQuery takes it.
func completeQuery(q string, caret int, text string) (string, int) {
	start, end, _ := queryTokenAt(q, caret)
	rest := q[end:]
	if !strings.HasSuffix(text, ":") {
		text += " "
		rest = strings.TrimLeft(rest, " \t")
	}
	q = q[:start] + text + rest
	return q, utf8.RuneCountInString(q[:start] + text)
}
