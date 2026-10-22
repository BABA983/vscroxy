package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type ruleEditor struct {
	open, deleting bool
	index          int // -1 is a new, unsaved rule
	draft          capture.Rule
	kind           string
	status         string
	headers        string
	problem        string
	unsavedMock    string
}

var ruleTypes = []capture.RuleType{capture.RuleMapLocal, capture.RuleMapRemote, capture.RuleBlock, capture.RuleBreak, capture.RuleBreakResponse}
var ruleLabels = []string{"Map Local", "Map Remote", "Block", "Request breakpoint", "Response breakpoint"}

func ruleLabel(kind capture.RuleType) string {
	if i := slices.Index(ruleTypes, kind); i >= 0 {
		return ruleLabels[i]
	}
	return string(kind)
}

func (a *app) discardRuleDraft() {
	if a.ruleUI.unsavedMock != "" {
		_ = os.Remove(a.ruleUI.unsavedMock)
		a.ruleUI.unsavedMock = ""
	}
}

func (a *app) editRule(index int, r capture.Rule) {
	a.discardRuleDraft()
	a.ruleUI = ruleEditor{open: true, index: index, draft: r, kind: ruleLabel(r.Type), status: strconv.Itoa(cmp.Or(r.Status, 200)), headers: headerBlock(r.Headers)}
}

func (a *app) openRules() {
	if len(a.rules) > 0 {
		a.editRule(0, a.rules[0])
	} else {
		a.editRule(-1, capture.Rule{Type: capture.RuleMapRemote})
	}
}

func (a *app) ruleFromFlow(f flow, kind capture.RuleType) {
	r := capture.Rule{Name: ruleLabel(kind) + " " + f.path, Type: kind, Match: f.url, Method: f.method}
	if kind == capture.RuleMapLocal {
		if a.rulesPath == "" {
			a.setMessage("Rules storage is unavailable")
			return
		}
		var err error
		r, err = capture.NewMockRule(filepath.Join(filepath.Dir(a.rulesPath), "mocks"), capture.Flow{
			Method: f.method, URL: f.url, Path: f.path, Status: f.status, Kind: f.kind,
			ResponseBody: f.resBody, ResponseBodyFile: f.resFile, ResponseBodySize: f.resSize, ResponseHeader: f.respHeader,
		})
		if err != nil {
			a.setMessage("Create mock: " + err.Error())
			return
		}
	}
	a.editRule(-1, r)
	if kind == capture.RuleMapLocal {
		a.ruleUI.unsavedMock = r.Target
	}
}

func (a *app) applyRuleChanges(next []capture.Rule) bool {
	if a.rulesPath == "" {
		a.ruleUI.problem = "Rules storage is unavailable"
		return false
	}
	if err := capture.SaveRules(a.rulesPath, next); err != nil {
		a.ruleUI.problem = err.Error()
		return false
	}
	a.rules = next
	if a.srv != nil {
		a.srv.SetRules(next)
	}
	a.ruleUI.problem = ""
	a.setMessage("Rules saved · apply to new requests")
	return true
}

func (a *app) saveRule() {
	e := &a.ruleUI
	r := e.draft
	r.Type = ruleTypes[slices.Index(ruleLabels, e.kind)]
	r.Match, r.Method = strings.TrimSpace(r.Match), strings.ToUpper(strings.TrimSpace(r.Method))
	r.Target = strings.TrimSpace(r.Target)
	r.Status, r.Headers = 0, nil
	if r.Type == capture.RuleMapLocal {
		status, err := strconv.Atoi(e.status)
		if err != nil {
			e.problem = "Response status must be a number between 200 and 599"
			return
		}
		r.Status = status
		// Unlike the permissive request editor, a saved rule must not drop
		// a malformed header line silently.
		for line := range strings.SplitSeq(e.headers, "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.Contains(line, ":") {
				e.problem = "Response headers must use Name: Value, one per line"
				return
			}
		}
		r.Headers = parseHeaderBlock(e.headers)
	} else {
		if r.Type != capture.RuleMapRemote {
			r.Target = ""
		}
	}
	next := slices.Clone(a.rules)
	index := e.index
	if index < 0 {
		index = len(next)
		next = append(next, r)
	} else {
		next[index] = r
	}
	if a.applyRuleChanges(next) {
		if e.unsavedMock == r.Target {
			e.unsavedMock = "" // the rule now owns this file
		}
		a.editRule(index, r)
	}
}

func (a *app) moveRule(delta int) {
	i := a.ruleUI.index
	next := slices.Clone(a.rules)
	next[i], next[i+delta] = next[i+delta], next[i]
	if a.applyRuleChanges(next) {
		a.editRule(i+delta, next[i+delta])
	}
}

func (a *app) browseRuleFile() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Choose local response file"})
		if err != nil {
			a.setMessage("Choose response: " + err.Error())
			return
		}
		if len(paths) > 0 {
			a.win.Update(func() { a.ruleUI.draft.Target = paths[0] })
		}
	}()
}

func (a *app) rulesModal(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	if !e.open {
		return
	}
	if ui.Modal(c, &e.open, func() {
		ui.Column(c).Width(840).Height(510).Gap(12).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Rules").FontSize(18).Bold()
				ui.Spacer(c)
				ui.Badge(c, fmt.Sprintf("%d rules", len(a.rules)))
			})
			ui.Text(c, "Map / Block: first enabled match wins. Breakpoints match independently; turn on Break in the toolbar.").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Grow(1).Gap(18).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(260).Gap(8).Children(func() {
					if ui.Button(c, "+ New rule").Clicked() {
						a.editRule(-1, capture.Rule{Type: capture.RuleMapRemote})
					}
					ui.Scroll(c).Grow(1).Gap(6).Children(func() {
						for i, r := range a.rules {
							ui.Row(c).Key(fmt.Sprint(i)).Gap(8).AlignItems(ui.Center).Padding(8).Radius(6).Background(t.Surface).Children(func() {
								enabled := !r.Disabled
								if ui.Switch(c, &enabled).Label(fmt.Sprintf("Enable rule %d", i+1)).Changed() {
									next := slices.Clone(a.rules)
									next[i].Disabled = !enabled
									if a.applyRuleChanges(next) && e.index == i {
										e.draft.Disabled = !enabled
									}
								}
								button := ui.Button(c, "").Label(fmt.Sprintf("Edit rule %d", i+1)).Grow(1).Border(0, t.Border)
								if e.index == i {
									button.Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
								}
								button.Children(func() {
									ui.Column(c).Gap(3).Children(func() {
										ui.Text(c, cmp.Or(r.Name, r.Match)).FontSize(12).Bold().SingleLine()
										ui.Text(c, fmt.Sprintf("%d · %s", i+1, ruleLabel(r.Type))).FontSize(11).TextColor(t.TextMuted).SingleLine()
									})
								})
								if button.Clicked() {
									a.editRule(i, r)
								}
							})
						}
						if len(a.rules) == 0 {
							ui.Text(c, "No rules yet.\nCreate one here, or from a captured flow’s context menu.").FontSize(12).TextColor(t.TextMuted)
						}
					})
					ui.Row(c).Gap(6).Children(func() {
						if ui.Button(c, "Move up").Disabled(e.index <= 0).Clicked() {
							a.moveRule(-1)
						}
						if ui.Button(c, "Move down").Disabled(e.index < 0 || e.index >= len(a.rules)-1).Clicked() {
							a.moveRule(1)
						}
					})
				})
				ui.Scroll(c).Grow(1).Gap(8).Children(func() {
					ui.Text(c, "Name").FontSize(11).TextColor(t.TextMuted)
					ui.TextInput(c, &e.draft.Name).Label("Rule name").Placeholder("A descriptive name")
					ui.Text(c, "Type / HTTP method").FontSize(11).TextColor(t.TextMuted)
					ui.Row(c).Gap(10).Children(func() {
						ui.Select(c, &e.kind, ruleLabels).Label("Rule type").Grow(1)
						ui.TextInput(c, &e.draft.Method).Width(95).Label("Match method").Placeholder("Any")
					})
					ui.Text(c, "Match URL").FontSize(11).TextColor(t.TextMuted)
					ui.TextInput(c, &e.draft.Match).Label("URL contains").Placeholder("https://api.example.com/v1/")
					ui.Text(c, "Literal substring of the full URL, not a regex or wildcard.").FontSize(11).TextColor(t.TextMuted)
					switch e.kind {
					case "Map Local":
						ui.Text(c, "Local response").FontSize(11).TextColor(t.TextMuted)
						ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
							ui.TextInput(c, &e.draft.Target).Label("Response file").Grow(1)
							if ui.Button(c, "Browse…").Clicked() {
								a.browseRuleFile()
							}
						})
						ui.Text(c, "HTTP status / response headers").FontSize(11).TextColor(t.TextMuted)
						ui.TextInput(c, &e.status).Label("Response status").Width(130)
						ui.TextArea(c, &e.headers).Height(76).Label("Response headers").Placeholder("Content-Type: application/json")
						ui.Text(c, "Up to 8 MiB. Connection headers are removed; length is recalculated.").FontSize(11).TextColor(t.TextMuted)
					case "Map Remote":
						ui.Text(c, "Upstream destination").FontSize(11).TextColor(t.TextMuted)
						ui.TextInput(c, &e.draft.Target).Label("Target base URL").Placeholder("http://127.0.0.1:8080")
						ui.Text(c, "Keeps the original path and query. A target path prefixes the path; a target query replaces the query.").FontSize(11).TextColor(t.TextMuted)
					case "Block":
						ui.Text(c, "Returns 403 without contacting the upstream server.").FontSize(12).TextColor(t.TextMuted)
					default:
						ui.Text(c, "Pauses matching traffic while Break is on. Use Continue or Abort in the breakpoint dialog.").FontSize(12).TextColor(t.TextMuted)
					}
					enabled := !e.draft.Disabled
					if ui.Checkbox(c, &enabled, "Rule enabled").Changed() {
						e.draft.Disabled = !enabled
					}
				})
			})
			if e.problem != "" {
				ui.Text(c, e.problem).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "Delete rule…").Disabled(e.index < 0).Clicked() {
					e.deleting = true
				}
				ui.Spacer(c)
				if ui.Button(c, "Close").Clicked() {
					a.discardRuleDraft()
					e.open = false
				}
				if ui.PrimaryButton(c, "Save rule").Clicked() {
					a.saveRule()
				}
			})
		})
	}).Dismissed() {
		a.discardRuleDraft()
	}
	if ui.AlertDialog(c, &e.deleting, "Delete rule?", "New requests will no longer use this rule. Local response files are kept for reuse.", "Cancel", "Delete") == 1 {
		next := slices.Delete(slices.Clone(a.rules), e.index, e.index+1)
		if a.applyRuleChanges(next) {
			a.openRules()
		}
	}
}
