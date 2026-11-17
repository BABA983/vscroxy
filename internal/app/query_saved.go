package app

import (
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
)

// savedQuery is one named JSONPath or jq expression.
type savedQuery struct {
	Name       string `json:"name"`
	Dialect    string `json:"dialect"`
	Expression string `json:"expression"`
}

const maxSavedQueries = 200

func validateSavedQueries(list []savedQuery) error {
	if len(list) > maxSavedQueries {
		return fmt.Errorf("keep at most %d saved queries", maxSavedQueries)
	}
	for i, q := range list {
		if q.Name == "" || q.Name != strings.TrimSpace(q.Name) || len(q.Name) > 64 ||
			!utf8.ValidString(q.Name) || strings.ContainsFunc(q.Name, unicode.IsControl) {
			return fmt.Errorf("query %d: names must be trimmed text of 1–64 bytes", i+1)
		}
		if q.Dialect != "JSONPath" && q.Dialect != "jq" {
			return fmt.Errorf("query %d: choose JSONPath or jq", i+1)
		}
		if q.Expression == "" || len(q.Expression) > 4096 || !utf8.ValidString(q.Expression) {
			return fmt.Errorf("query %d: enter an expression of at most 4096 bytes", i+1)
		}
		for _, other := range list[:i] {
			if strings.EqualFold(q.Name, other.Name) {
				return fmt.Errorf("a saved query named %q already exists", q.Name)
			}
		}
	}
	return nil
}

// loadQueries reads the saved queries; a damaged file leaves the last good
// list in place and is reported.
func (a *app) loadQueries() {
	if a.queriesPath == "" {
		return
	}
	data, err := a.file(a.queriesPath).Read()
	if err != nil || data == nil {
		if err != nil {
			a.queriesProblem = locales.SavedQueriesWith(err.Error())
		}
		return
	}
	var doc struct {
		Version int          `json:"version"`
		Queries []savedQuery `json:"queries"`
	}
	if err := json.Unmarshal(data, &doc, json.RejectUnknownMembers(true)); err != nil {
		a.queriesProblem = locales.SavedQueriesWith(err.Error())
		return
	}
	if err := validateSavedQueries(doc.Queries); err != nil {
		a.queriesProblem = locales.SavedQueriesWith(err.Error())
		return
	}
	a.queries, a.queriesProblem = doc.Queries, ""
}

// writeQueries saves the list, sorted by name, through the guarded file.
func (a *app) writeQueries(list []savedQuery) bool {
	if err := validateSavedQueries(list); err != nil {
		a.queriesProblem = err.Error()
		return false
	}
	if a.queriesPath == "" {
		a.queriesProblem = locales.SavedQueriesLocationIsUnavailable()
		return false
	}
	slices.SortFunc(list, func(x, y savedQuery) int { return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)) })
	data, err := json.Marshal(struct {
		Version int          `json:"version"`
		Queries []savedQuery `json:"queries"`
	}{store.Version, list})
	if err == nil {
		err = a.file(a.queriesPath).Write(append(data, '\n'))
	}
	if err != nil {
		a.queriesProblem = err.Error()
		return false
	}
	a.queries, a.queriesProblem = list, ""
	return true
}

// saveQuery adds or replaces a named query.
func (a *app) saveQuery(name, dialect, expression string) bool {
	name = strings.TrimSpace(name)
	list := slices.Clone(a.queries)
	query := savedQuery{Name: name, Dialect: dialect, Expression: expression}
	if i := slices.IndexFunc(list, func(q savedQuery) bool { return strings.EqualFold(q.Name, name) }); i >= 0 {
		list[i] = query
	} else {
		list = append(list, query)
	}
	if !a.writeQueries(list) {
		return false
	}
	a.done(locales.SavedQuery())
	return true
}

// deleteQuery removes a named query.
func (a *app) deleteQuery(name string) bool {
	list := slices.DeleteFunc(slices.Clone(a.queries), func(q savedQuery) bool { return q.Name == name })
	if len(list) == len(a.queries) {
		a.queriesProblem = locales.TheSavedQueryNoLonger()
		return false
	}
	return a.writeQueries(list)
}

// queryPath is the file saved queries live in under the app's directory.
func queryPath(home string) string { return filepath.Join(home, ".vscroxy", "queries.json") }
