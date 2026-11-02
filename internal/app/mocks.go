package app

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/rules"
)

// mockResponseName is the header that picks an example other than a
// request's first, as Postman's mock servers read it.
const mockResponseName = "X-Mock-Response-Name"

func (a *app) mocksDir() string { return filepath.Join(filepath.Dir(a.rulesPath), "mocks") }

// exampleMock is a Map Local rule answering with e, an example of r. Its URL
// is the example's as authored, with the variables r sees now filled in; any
// still unknown match whatever the request has there.
func (a *app) exampleMock(r request.Request, e request.Example) (capture.Rule, error) {
	raw := request.ExpandVars(cmp.Or(e.Request.URL, r.URL), a.inheritance(r.Collection).Values(a.activeVars()))
	rule := capture.Rule{
		Name: cmp.Or(r.Name, "Request") + " · " + e.Name, Type: capture.RuleMapLocal,
		MatchMode: rules.URLRegex, Match: capture.MockPattern(raw), Method: strings.ToUpper(cmp.Or(e.Request.Method, r.Method, "GET")),
		Status: e.Status, Headers: request.ParseHeaderBlock(e.Headers),
	}
	return capture.SaveMock(a.mocksDir(), rule, []byte(e.Body))
}

// mockExample opens a rule answering with the example in the rule editor;
// it applies once saved there.
func (a *app) mockExample(r request.Request, e request.Example) {
	if a.rulesPath == "" {
		a.warn("Rules storage is unavailable")
		return
	}
	rule, err := a.exampleMock(r, e)
	if err != nil {
		a.fail("Mock example", err)
		return
	}
	a.editRule(-1, rule)
	a.ruleUI.unsavedMock = rule.Target
	a.openTools("rules")
}

// scenarioFromExamples makes the examples of the requests filed under path
// the rules of a scenario named path, replacing the rules it had. Of the
// examples a request answers the same way, the first is the default and the
// others answer to X-Mock-Response-Name. The scenario keeps its on or off.
func (a *app) scenarioFromExamples(path string) {
	if a.rulesPath == "" {
		a.warn("Rules storage is unavailable")
		return
	}
	var picked, defaults []capture.Rule
	seen := map[string]bool{}
	created := func() []capture.Rule { return append(slices.Clone(picked), defaults...) }
	for _, r := range a.requests {
		if r.Collection != path && !strings.HasPrefix(r.Collection, path+"/") {
			continue
		}
		for _, e := range r.Examples {
			rule, err := a.exampleMock(r.Request, e)
			if err != nil {
				removeMocks(created())
				a.fail("Mock “"+r.Name+" · "+e.Name+"”", err)
				return
			}
			rule.Scenario = path
			if key := rule.Method + " " + rule.Match; seen[key] {
				rule.When = []rules.HeaderCondition{{Name: mockResponseName, Op: rules.HeaderEquals, Value: e.Name}}
				picked = append(picked, rule)
			} else {
				seen[key] = true
				defaults = append(defaults, rule)
			}
		}
	}
	made := created()
	if len(made) == 0 {
		a.inform("No examples under “" + path + "”; save a response as an example first")
		return
	}
	// Named examples come first: the default would answer them otherwise.
	at, active := len(a.rules), false
	var kept, replaced []capture.Rule
	for i, r := range a.rules {
		if r.Scenario == path {
			at, active = min(at, i-len(replaced)), r.ScenarioActive
			replaced = append(replaced, r)
		} else {
			kept = append(kept, r)
		}
	}
	at = min(at, len(kept))
	for i := range made {
		made[i].ScenarioActive = active
	}
	if !a.applyRuleChanges(slices.Insert(kept, at, made...)) {
		removeMocks(made)
		a.fail("Mock “"+path+"”", errors.New(a.ruleUI.problem))
		return
	}
	// The files the replaced rules answered from are the app's, unless a
	// rule still left reads one.
	var stale []capture.Rule
	for _, r := range replaced {
		if r.Type == capture.RuleMapLocal && filepath.Dir(r.Target) == a.mocksDir() &&
			!slices.ContainsFunc(a.rules, func(o capture.Rule) bool { return o.Target == r.Target }) {
			stale = append(stale, r)
		}
	}
	removeMocks(stale)
	a.openTools("rules")
	switch {
	case active:
		a.done(fmt.Sprintf("Scenario “%s” answers from %d examples", path, len(made)))
	default:
		a.done(fmt.Sprintf("Scenario “%s” has %d mocks; choose it in the scenario menu to answer from them", path, len(made)))
	}
}

func removeMocks(list []capture.Rule) {
	for _, r := range list {
		_ = os.Remove(r.Target)
	}
}

// examplesUnder reports whether a request filed under path has an example.
func (a *app) examplesUnder(path string) bool {
	return slices.ContainsFunc(a.requests, func(r savedRequest) bool {
		return len(r.Examples) > 0 && (r.Collection == path || strings.HasPrefix(r.Collection, path+"/"))
	})
}
