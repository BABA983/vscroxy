package app

// The Scenarios page of Tools: the groups of rules switched together, at
// most one at a time, with the rules in each.

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// scenarioUI is the scenario the page shows and its name as being edited.
type scenarioUI struct {
	choice, name, nameFor string
	list                  ui.ListState
	deleting              bool
}

// scenarioNames are the scenarios the rules name, in the order they first
// appear.
func (a *app) scenarioNames() []string {
	var names []string
	for _, r := range a.rules {
		if r.Scenario != "" && !slices.Contains(names, r.Scenario) {
			names = append(names, r.Scenario)
		}
	}
	return names
}

// changeScenario saves the rules with change applied to those in name.
func (a *app) changeScenario(name string, change func(r *capture.Rule) bool) bool {
	var next []capture.Rule
	for _, r := range a.rules {
		if r.Scenario != name || change(&r) {
			next = append(next, r)
		}
	}
	if !a.applyRuleChanges(next) {
		a.fail("Scenario", errors.New(a.ruleUI.problem))
		return false
	}
	// The rule being edited may have moved or gone; show it again from disk.
	a.ruleUI.open = false
	return true
}

func (a *app) renameScenario(old, name string) {
	if name == old {
		return
	}
	if name == "" || strings.ContainsFunc(name, unicode.IsControl) {
		a.warn("Enter a scenario name without control characters")
		return
	}
	if slices.Contains(a.scenarioNames(), name) {
		a.warn(fmt.Sprintf("A scenario named “%s” exists", name))
		return
	}
	if a.changeScenario(old, func(r *capture.Rule) bool { r.Scenario = name; return true }) {
		a.scenarioUI.choice, a.scenarioUI.nameFor = name, ""
	}
}

// deleteScenario removes its rules, and the mock files only they read.
func (a *app) deleteScenario(name string) {
	var gone []capture.Rule
	if !a.changeScenario(name, func(r *capture.Rule) bool { gone = append(gone, *r); return false }) {
		return
	}
	var stale []capture.Rule
	for _, r := range gone {
		if r.Type == capture.RuleMapLocal && filepath.Dir(r.Target) == a.mocksDir() &&
			!slices.ContainsFunc(a.rules, func(o capture.Rule) bool { return o.Target == r.Target }) {
			stale = append(stale, r)
		}
	}
	removeMocks(stale)
	a.done(fmt.Sprintf("Deleted scenario “%s” and its %d %s", name, len(gone), plural(len(gone), "rule", "rules")))
}

func (a *app) scenariosPage(c *ui.Context, t *ui.Theme, top float32, active bool) {
	s := &a.scenarioUI
	names := a.scenarioNames()
	if !slices.Contains(names, s.choice) {
		s.choice = ""
		if len(names) > 0 {
			s.choice = names[0]
		}
	}
	current := capture.ActiveScenario(a.rules)
	ui.Column(c).Width(268).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Text(c, "Scenarios").FontSize(uikit.TextTitle).FontWeight(600).TextColor(t.Text)
		uikit.Meta(c, "One at a time: its rules apply, with the rules in none.")
		choice := slices.Index(names, s.choice)
		s.list.Selected = &choice
		s.list.Key = func(i int) any { return names[i] }
		s.list.Label = func(i int) string { return names[i] }
		pick := *t
		pick.Accent, pick.AccentText = ui.Color{}, t.Text
		c.SetTheme(&pick)
		ui.List(c, &s.list, len(names)).Label("Scenarios").Grow(1).MinHeight(0).Gap(t.Space(0.5)).Rows(func(row ui.ListRow) {
			c := row.Context
			c.SetTheme(t)
			name := names[row.Index]
			r := ui.Row(c).Height(uikit.HeightLarge+4).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
			if row.Selected() {
				uikit.Selected(c, r, active)
			}
			r.Children(func() {
				on := name == current
				if ui.Switch(c, &on).Label("Scenario active: " + name).Changed() {
					a.selectRuleScenario(map[bool]string{true: name}[on])
				}
				ui.Text(c, name).FontSize(uikit.TextBody).TextColor(t.Text).Grow(1).SingleLine()
				n := 0
				for _, rule := range a.rules {
					if rule.Scenario == name {
						n++
					}
				}
				uikit.Meta(c, fmt.Sprint(n)).Font(uikit.FontMono).FontFeatures("tnum").Tooltip(fmt.Sprintf("%d %s", n, plural(n, "rule", "rules")))
			})
		})
		c.SetTheme(t)
		if choice >= 0 && choice < len(names) {
			s.choice = names[choice]
		}
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(3)).Children(func() {
		if s.choice == "" {
			uikit.EmptyState(c, "No scenarios", "A scenario is rules switched on and off together, such as an offline demo. Name one in a rule’s Scenario field, or make one from a collection’s examples with Mock from examples.", func() {
				if uikit.PrimaryButton(c, "Open Rules").Clicked() {
					a.openRules()
				}
			})
			return
		}
		a.scenarioDetail(c, t, s.choice, current)
	})
	if ui.AlertDialog(c, &s.deleting, "Delete scenario?", "Its rules are deleted too, with the mock files only they answer from.", "Cancel", "Delete") == 1 {
		a.deleteScenario(s.choice)
	}
}

// scenarioDetail is a scenario's name, switch, actions and rules.
func (a *app) scenarioDetail(c *ui.Context, t *ui.Theme, name, current string) {
	s := &a.scenarioUI
	if s.nameFor != name {
		s.name, s.nameFor = name, name
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.Input(c, &s.name).Label("Scenario name").Grow(1).MaxWidth(360).FontSize(uikit.TextTitle).Height(uikit.HeightLarge).Submitted() {
			a.renameScenario(name, strings.TrimSpace(s.name))
		}
		on := name == current
		if ui.Switch(c, &on).Label("Scenario active").Changed() {
			a.selectRuleScenario(map[bool]string{true: name}[on])
		}
		ui.Spacer(c)
		uikit.MoreMenu(c, "Scenario actions", func(m *ui.Menu) {
			actionMenu(m, []action{
				do("scenarios.rename", "Rename", func() { a.renameScenario(name, strings.TrimSpace(s.name)) }).
					when(strings.TrimSpace(s.name) != name, "Type the new name first").in("edit"),
				do("scenarios.rebuild", "Rebuild from examples", func() { a.scenarioFromExamples(name) }).
					when(a.examplesUnder(name), "Only a scenario named for a collection with examples").in("edit"),
				do("scenarios.ungroup", "Ungroup rules", func() {
					a.changeScenario(name, func(r *capture.Rule) bool { r.Scenario, r.ScenarioActive = "", false; return true })
				}).in("edit"),
				do("scenarios.delete", "Delete with its rules…", func() { s.deleting = true }).in("danger"),
			})
		})
	})
	status := "Off: its rules do nothing."
	if name == current {
		status = "On: its rules apply, with the rules in no scenario."
	}
	uikit.Meta(c, status)
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(0.5)).Children(func() {
		for i, r := range a.rules {
			if r.Scenario != name {
				continue
			}
			row := ui.Row(c.Key(fmt.Sprint("scenario-rule", i))).Height(uikit.HeightLarge+12).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
			if name != current || r.Disabled {
				row.Opacity(0.6)
			}
			row.Children(func() {
				on := !r.Disabled
				if ui.Switch(c, &on).Label(fmt.Sprintf("Enable rule %d", i+1)).Changed() {
					a.switchRule(i, on)
				}
				uikit.Glyph(c, ruleIcons[max(slices.Index(ruleTypes, r.Type), 0)]).Tooltip(ruleLabel(r.Type))
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, cmp.Or(r.Name, r.Match)).FontSize(uikit.TextBody).TextColor(t.Text).SingleLine()
					uikit.Meta(c, ruleSummary(r)).Font(uikit.FontMono).SingleLine()
				})
				if uikit.CompactButton(c, "Edit").Label(fmt.Sprintf("Edit rule %d", i+1)).Clicked() {
					a.editRule(i, r)
					a.openTools("rules")
				}
			})
		}
	})
	uikit.Meta(c, "Rules keep their place in the Rules order; turn one off to leave it out of the scenario.")
}
