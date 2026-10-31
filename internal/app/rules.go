package app

import (
	"cmp"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/net/http/httpguts"
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
	scenarioChoice string
	preview        bool
	previewURL     string
	previewMethod  string
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
	previous := a.ruleUI
	a.ruleUI = ruleEditor{open: true, index: index, draft: r, kind: ruleLabel(r.Type), status: strconv.Itoa(cmp.Or(r.Status, 200)), headers: request.HeaderBlock(r.Headers),
		scenarioChoice: previous.scenarioChoice, previewURL: previous.previewURL, previewMethod: cmp.Or(previous.previewMethod, "GET")}
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
	r.Scenario = strings.TrimSpace(r.Scenario)
	if r.Type == capture.RuleBreak || r.Type == capture.RuleBreakResponse {
		r.Scenario = ""
	}
	// Joining an active group inherits its group state, never its Disabled flags.
	r.ScenarioActive = r.Scenario != "" && r.Scenario == capture.ActiveScenario(a.rules)
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
		r.Headers = request.ParseHeaderBlock(e.headers)
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

func (a *app) selectRuleScenario(name string) {
	next, err := capture.SelectScenario(a.rules, name)
	if err != nil {
		a.ruleUI.problem = err.Error()
		return
	}
	if a.applyRuleChanges(next) {
		// Keep unsaved editor content, but do not retain stale selection metadata.
		r := &a.ruleUI.draft
		r.ScenarioActive = r.Scenario != "" && r.Scenario == name
	}
}

func (a *app) ruleScenarios(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	active := capture.ActiveScenario(a.rules)
	options := []string{}
	for _, r := range a.rules {
		if r.Scenario != "" && !slices.Contains(options, r.Scenario) {
			options = append(options, r.Scenario)
		}
	}
	if !slices.Contains(options, e.scenarioChoice) {
		e.scenarioChoice = active
		if e.scenarioChoice == "" && len(options) > 0 {
			e.scenarioChoice = options[0]
		}
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Active: "+cmp.Or(active, "none · global rules only")).FontSize(textBody).Bold().Grow(1)
		if len(options) > 0 {
			ui.Select(c, &e.scenarioChoice, options).Label("Scenario to activate").Width(180).Height(heightControl).Radius(radiusControl)
			if secondaryButton(c, "Activate scenario").Disabled(e.scenarioChoice == active).Clicked() {
				a.selectRuleScenario(e.scenarioChoice)
			}
		}
		if secondaryButton(c, "Deactivate scenario").Disabled(active == "").Clicked() {
			a.selectRuleScenario("")
		}
	})
}

func (a *app) previewRuleMatches(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	ui.Text(c, "Test saved rules · no request is sent").Bold()
	ui.Row(c).Gap(t.Space(2)).Children(func() {
		ui.TextInput(c, &e.previewMethod).Label("Test method").Width(85).Height(heightControl).Radius(radiusControl)
		ui.TextInput(c, &e.previewURL).Label("Test URL").Placeholder("https://api.example.com/v1/").Grow(1).Height(heightControl).Radius(radiusControl)
	})
	u, err := url.Parse(strings.TrimSpace(e.previewURL))
	method := strings.TrimSpace(e.previewMethod)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || !httpguts.ValidHeaderFieldName(method) {
		ui.Text(c, "Enter an http(s) URL without credentials or fragment and an HTTP method.").FontSize(textBody).TextColor(t.TextMuted)
		return
	}
	winner := false
	for _, p := range capture.PreviewRules(a.rules, u.String(), method) {
		r := a.rules[p.Index]
		color := t.TextMuted
		if p.Reason == "winner" {
			winner = true
			color = t.Accent
		}
		ui.Text(c, fmt.Sprintf("%d · %s · %s — %s", p.Index+1, cmp.Or(r.Name, r.Match), ruleLabel(r.Type), p.Reason)).FontSize(textBody).TextColor(color)
	}
	if !winner {
		ui.Text(c, "No response rule wins · request goes upstream.").FontSize(textBody)
	}
}

func (a *app) rulesModal(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	if !e.open {
		return
	}
	_, height := c.Size()
	if appModal(c, &e.open, func() {
		dialogColumn(c, dialogLarge).Height(min(560, height-80)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				dialogTitle(c, "Rules")
				ui.Spacer(c)
				ui.Checkbox(c, &e.preview, "Test URL + method")
				ui.Badge(c, fmt.Sprintf("%d rules", len(a.rules)))
			})
			ui.Text(c, "The first matching Map or Block rule wins. Breakpoints pause only while Breakpoints is on.").FontSize(textMeta).TextColor(t.TextMuted)
			a.ruleScenarios(c, t)
			ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(4)).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(260).Gap(t.Space(2)).Children(func() {
					if secondaryButton(c, "+ New rule").Clicked() {
						a.editRule(-1, capture.Rule{Type: capture.RuleMapRemote})
					}
					ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Children(func() {
						for i, r := range a.rules {
							ui.Row(c).Key(fmt.Sprint(i)).Gap(t.Space(2)).AlignItems(ui.Center).Padding(t.Space(2)).Radius(radiusControl).Background(t.Surface).Children(func() {
								enabled := !r.Disabled
								if ui.Switch(c, &enabled).Label(fmt.Sprintf("Enable rule %d", i+1)).Changed() {
									next := slices.Clone(a.rules)
									next[i].Disabled = !enabled
									if a.applyRuleChanges(next) && e.index == i {
										e.draft.Disabled = !enabled
									}
								}
								button := ui.Button(c, "").Label(fmt.Sprintf("Edit rule %d", i+1)).Grow(1).Border(0, t.Border).Radius(radiusControl)
								if e.index == i {
									button.Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
								}
								button.Children(func() {
									ui.Column(c).Gap(t.Space(1)).Children(func() {
										ui.Text(c, cmp.Or(r.Name, r.Match)).FontSize(textBody).Bold().SingleLine()
										ui.Text(c, fmt.Sprintf("%d · %s · %s", i+1, ruleLabel(r.Type), cmp.Or(r.Scenario, "global"))).FontSize(textMeta).TextColor(t.TextMuted).SingleLine()
									})
								})
								if button.Clicked() {
									a.editRule(i, r)
								}
							})
						}
						if len(a.rules) == 0 {
							ui.Text(c, "No rules yet.\nCreate one here, or from a captured flow’s context menu.").FontSize(textBody).TextColor(t.TextMuted)
						}
					})
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						if secondaryButton(c, "Move up").Disabled(e.index <= 0).Clicked() {
							a.moveRule(-1)
						}
						if secondaryButton(c, "Move down").Disabled(e.index < 0 || e.index >= len(a.rules)-1).Clicked() {
							a.moveRule(1)
						}
					})
				})
				ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Children(func() {
					if e.preview {
						a.previewRuleMatches(c, t)
						return
					}
					ui.Text(c, "Name / scenario (blank means global)").FontSize(textMeta).TextColor(t.TextMuted)
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						ui.TextInput(c, &e.draft.Name).Label("Rule name").Placeholder("A descriptive name").Grow(1).Height(heightControl).Radius(radiusControl)
						if e.kind != "Request breakpoint" && e.kind != "Response breakpoint" {
							ui.TextInput(c, &e.draft.Scenario).Label("Rule scenario").Placeholder("Named scenario").Width(180).Height(heightControl).Radius(radiusControl)
						}
					})
					ui.Text(c, "Type / HTTP method").FontSize(textMeta).TextColor(t.TextMuted)
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						ui.Select(c, &e.kind, ruleLabels).Label("Rule type").Grow(1).Height(heightControl).Radius(radiusControl)
						ui.TextInput(c, &e.draft.Method).Width(95).Label("Match method").Placeholder("Any").Height(heightControl).Radius(radiusControl)
					})
					ui.Text(c, "Match URL").FontSize(textMeta).TextColor(t.TextMuted)
					ui.TextInput(c, &e.draft.Match).Label("URL contains").Placeholder("https://api.example.com/v1/").Height(heightControl).Radius(radiusControl)
					ui.Text(c, "Literal substring of the full URL, not a regex or wildcard.").FontSize(textMeta).TextColor(t.TextMuted)
					switch e.kind {
					case "Map Local":
						ui.Text(c, "Local response").FontSize(textMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Children(func() {
							ui.TextInput(c, &e.draft.Target).Label("Response file").Grow(1).Height(heightControl).Radius(radiusControl)
							if secondaryButton(c, "Browse…").Clicked() {
								a.browseRuleFile()
							}
						})
						ui.Text(c, "HTTP status / response headers").FontSize(textMeta).TextColor(t.TextMuted)
						ui.TextInput(c, &e.status).Label("Response status").Width(130).Height(heightControl).Radius(radiusControl)
						ui.TextArea(c, &e.headers).Height(76).Font(fontMono).FontSize(textBody).Radius(radiusControl).Label("Response headers").Placeholder("Content-Type: application/json")
						ui.Text(c, "Up to 8 MiB. Connection headers are removed; length is recalculated.").FontSize(textMeta).TextColor(t.TextMuted)
					case "Map Remote":
						ui.Text(c, "Upstream destination").FontSize(textMeta).TextColor(t.TextMuted)
						ui.TextInput(c, &e.draft.Target).Label("Target base URL").Placeholder("http://127.0.0.1:8080").Height(heightControl).Radius(radiusControl)
						ui.Text(c, "Keeps the original path and query. A target path prefixes the path; a target query replaces the query.").FontSize(textMeta).TextColor(t.TextMuted)
					case "Block":
						ui.Text(c, "Returns 403 without contacting the upstream server.").FontSize(textBody).TextColor(t.TextMuted)
					default:
						ui.Text(c, "Pauses matching traffic while Break is on. Use Continue or Abort in the breakpoint dialog.").FontSize(textBody).TextColor(t.TextMuted)
					}
					enabled := !e.draft.Disabled
					if ui.Checkbox(c, &enabled, "Rule enabled").Changed() {
						e.draft.Disabled = !enabled
					}
				})
			})
			if e.problem != "" {
				ui.Text(c, e.problem).FontSize(textBody).TextColor(t.Danger)
			}
			dialogFooter(c).Children(func() {
				if secondaryButton(c, "Delete rule…").Disabled(e.index < 0).Clicked() {
					e.deleting = true
				}
				ui.Spacer(c)
				if secondaryButton(c, "Close").Clicked() {
					a.discardRuleDraft()
					e.open = false
				}
				if primaryButton(c, "Save rule").Disabled(e.preview).Clicked() {
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
