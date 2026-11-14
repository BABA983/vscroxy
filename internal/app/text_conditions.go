package app

// Text conditions for the capture filters.
//
// A text field holds conditions separated by spaces; a value with spaces is
// quoted. Each condition may start with "!" to negate it, then chooses how it
// matches:
//
//	value     the field's default: equals for host, app and IP; contains for note and query
//	=value    equals, ignoring case
//	^value    starts with
//	~value    contains
//	/regex/   regular expression, ignoring case unless (?-i) says otherwise
//
// Positive conditions on one field match when any of them does, and every
// negative one must hold. Across fields the view's AND/OR mode applies.

import (
	"regexp"
	"strings"

	"github.com/baba983/vscroxy/locales"
)

// maxTextPattern is the longest regular expression a condition may hold, as
// the search field's patterns are.
const maxTextPattern = 4096

// textCondition is one condition of a text field. op is '=', '^', '~' or '/'.
type textCondition struct {
	negate bool
	op     byte
	value  string
	lower  string
	re     *regexp.Regexp
}

// textExpr is a compiled text field. A field whose conditions do not all parse
// matches nothing, as a search with a malformed pattern does.
type textExpr struct {
	conds   []textCondition
	problem string
}

// parseTextExpr compiles a field's conditions. def is the operator a bare value
// takes: '=' for an exact field, '~' for a contains field. It returns the
// first problem as well, so the Filters dialog and the search field can show it.
func parseTextExpr(expr string, def byte) (textExpr, string) {
	var e textExpr
	for _, t := range tokenizeQuery(expr) {
		c, problem := parseTextCondition(t.raw, def)
		if problem != "" {
			return textExpr{problem: problem}, problem
		}
		e.conds = append(e.conds, c)
	}
	return e, ""
}

// parseTextCondition reads one condition as the syntax writes it.
func parseTextCondition(raw string, def byte) (textCondition, string) {
	var c textCondition
	rest := raw
	if strings.HasPrefix(rest, "!") {
		c.negate, rest = true, rest[1:]
	}
	if rest != "" && strings.IndexByte("=^~", rest[0]) >= 0 {
		c.op, rest = rest[0], rest[1:]
	}
	rest = unquoteText(rest)
	if c.op == 0 && len(rest) > 1 && rest[0] == '/' && rest[len(rest)-1] == '/' {
		c.op, rest = '/', rest[1:len(rest)-1]
	}
	if c.op == 0 {
		c.op = def
	}
	if rest == "" {
		return c, locales.TextConditionNeedsValue(raw)
	}
	c.value, c.lower = rest, strings.ToLower(rest)
	if c.op == '/' {
		if len(rest) > maxTextPattern {
			return c, locales.TextPatternTooLong(maxTextPattern)
		}
		re, err := regexp.Compile("(?i)" + rest)
		if err != nil {
			return c, locales.TextPatternInvalid(err.Error())
		}
		c.re = re
	}
	return c, ""
}

// unquoteText drops the quotes around a value that holds spaces.
func unquoteText(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// textToken writes a plain value as one condition, quoted when the syntax
// would otherwise read it as several conditions or as an operator.
func textToken(value string) string {
	if value == "" || strings.ContainsAny(value, " \t\n\"") || strings.IndexByte("!=^~/", value[0]) >= 0 {
		return `"` + value + `"`
	}
	return value
}

// addTextCondition appends one condition to a field's text, unless the field
// already holds the same condition.
func addTextCondition(field, condition string) string {
	for _, t := range tokenizeQuery(field) {
		if t.raw == condition {
			return field
		}
	}
	return strings.TrimSpace(field + " " + condition)
}

// removeTextCondition drops the first condition of a field that is raw.
func removeTextCondition(field, raw string) string {
	var kept []string
	removed := false
	for _, t := range tokenizeQuery(field) {
		if t.raw == raw && !removed {
			removed = true
			continue
		}
		kept = append(kept, t.raw)
	}
	return strings.Join(kept, " ")
}

// match reports whether the field accepts a row whose text is targets. A
// condition's targets are one or more values of the row, such as its client
// and upstream addresses; a positive condition matches when any of them does.
func (e textExpr) match(targets ...string) bool {
	if e.problem != "" {
		return false
	}
	hasPositive, found := false, false
	for _, c := range e.conds {
		hit := c.hit(targets)
		if c.negate {
			if hit {
				return false
			}
			continue
		}
		hasPositive = true
		found = found || hit
	}
	return found || !hasPositive
}

// hit reports whether the condition's own test holds for one of targets,
// before any negation.
func (c textCondition) hit(targets []string) bool {
	for _, target := range targets {
		switch c.op {
		case '/':
			if c.re.MatchString(target) {
				return true
			}
		case '=':
			if strings.EqualFold(target, c.value) {
				return true
			}
		case '^':
			if strings.HasPrefix(strings.ToLower(target), c.lower) {
				return true
			}
		default:
			if strings.Contains(strings.ToLower(target), c.lower) {
				return true
			}
		}
	}
	return false
}

// textFor compiles a field's text once for each distinct text, so a pass over
// the rows parses each condition once. It runs on the UI thread only.
func (a *app) textFor(expr string, def byte) textExpr {
	key := string(def) + expr
	if e, ok := a.textExprs[key]; ok {
		return e
	}
	if len(a.textExprs) >= 256 {
		a.textExprs = nil
	}
	if a.textExprs == nil {
		a.textExprs = make(map[string]textExpr)
	}
	e, _ := parseTextExpr(expr, def)
	a.textExprs[key] = e
	return e
}
