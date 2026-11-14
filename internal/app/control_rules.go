package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/control"
)

// controlRules creates, changes and deletes rules one at a time. Existing rules
// are addressed by index and the fingerprint from rules.list, so a stale list
// cannot change a different rule. Like rules.import, nothing is saved unless the
// whole resulting list validates.
func (a *app) controlRules(command control.Command) (any, error) {
	if command.Action == "rules.create" {
		p, err := controlParams[struct {
			Rule    capture.Rule `json:"rule"`
			Confirm bool         `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("rule and confirm:true are required")
		}
		rule, err := controlRuleDraft(p.Rule, a.rules)
		if err != nil {
			return nil, err
		}
		next := append(slices.Clone(a.rules), rule)
		if err := a.controlSaveRules(next); err != nil {
			return nil, err
		}
		return map[string]any{"index": len(next) - 1, "key": controlRuleKey(rule)}, nil
	}
	p, err := controlParams[struct {
		Index   *int         `json:"index"`
		Key     string       `json:"key"`
		Rule    capture.Rule `json:"rule"`
		Confirm bool         `json:"confirm"`
	}](command)
	if err != nil || !p.Confirm || p.Index == nil || *p.Index < 0 || *p.Index >= len(a.rules) {
		return nil, errors.New("valid index, key and confirm:true are required")
	}
	if p.Key != controlRuleKey(a.rules[*p.Index]) {
		return nil, errors.New("rule changed; list rules again")
	}
	// An open editor holds its own copy of an index; changing rules under it
	// would make its next save overwrite the wrong rule.
	if a.ruleUI.open {
		return nil, errors.New("close the rule editor before changing rules")
	}
	next := slices.Clone(a.rules)
	if command.Action == "rules.delete" {
		next = slices.Delete(next, *p.Index, *p.Index+1)
		if err := a.controlSaveRules(next); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "total": len(next)}, nil
	}
	rule, err := controlRuleDraft(p.Rule, a.rules)
	if err != nil {
		return nil, err
	}
	next[*p.Index] = rule
	if err := a.controlSaveRules(next); err != nil {
		return nil, err
	}
	return map[string]any{"index": *p.Index, "key": controlRuleKey(rule)}, nil
}

// controlRuleDraft applies the same normalization the rule editor does to a
// draft, so a client cannot store fields that only one rule type uses.
func controlRuleDraft(r capture.Rule, existing []capture.Rule) (capture.Rule, error) {
	if !slices.Contains(ruleTypes, r.Type) {
		return r, errors.New("unknown rule type")
	}
	r.Match, r.Method = strings.TrimSpace(r.Match), strings.ToUpper(strings.TrimSpace(r.Method))
	r.Target, r.Scenario = strings.TrimSpace(r.Target), strings.TrimSpace(r.Scenario)
	switch r.Type {
	case capture.RuleBreak, capture.RuleBreakResponse, capture.RuleBreakMessage:
		r.Scenario = ""
	}
	if r.Type != capture.RuleBreakMessage {
		r.Direction = ""
	}
	if r.Type != capture.RuleNetwork {
		r.Network = nil
	}
	if r.Type != capture.RuleMapLocal {
		r.Status, r.Headers = 0, nil
	}
	// Joining an active group inherits its state, never a client's flag.
	r.ScenarioActive = r.Scenario != "" && r.Scenario == capture.ActiveScenario(existing)
	return r, nil
}

// controlSaveRules validates and persists the whole list before applying it.
// Validation errors may quote authored content, so they are not returned.
func (a *app) controlSaveRules(next []capture.Rule) error {
	if capture.ValidateRules(next) != nil {
		return errors.New("invalid rules or conflicting scenario state; nothing saved")
	}
	if !a.applyRuleChanges(next) {
		return errors.New("could not save rules; nothing saved")
	}
	return nil
}
