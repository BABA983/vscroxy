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
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"golang.org/x/net/http/httpguts"
)

// ruleEditor is the Rules page of Tools: the rule being edited and the
// request the rules are tested against.
type ruleEditor struct {
	open, deleting bool
	index          int // -1 is a new, unsaved rule
	draft          capture.Rule
	kind           string
	status         string
	headers        string
	problem        string
	unsavedMock    string
	test           string // "METHOD URL" or a URL, to see which rules act on it
	list           ui.ListState
}

var ruleTypes = []capture.RuleType{capture.RuleMapLocal, capture.RuleMapRemote, capture.RuleBlock, capture.RuleBreak, capture.RuleBreakResponse}
var ruleLabels = []string{"Map Local", "Map Remote", "Block", "Request breakpoint", "Response breakpoint"}
var ruleIcons = []*ui.SVG{fileInputIcon, lucide.ArrowRightLeft(), lucide.Ban(), breakIcon, breakIcon}
var rulesIcon = lucide.SlidersHorizontal()

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

// editRule shows r, the rule at index or a new one for -1, in the editor.
func (a *app) editRule(index int, r capture.Rule) {
	a.discardRuleDraft()
	e := &a.ruleUI
	e.open, e.deleting, e.index, e.draft, e.kind = true, false, index, r, ruleLabel(r.Type)
	e.status, e.headers, e.problem = strconv.Itoa(cmp.Or(r.Status, 200)), request.HeaderBlock(r.Headers), ""
}

// openRules shows Tools on Rules, keeping the rule being edited.
func (a *app) openRules() {
	if !a.ruleUI.open && len(a.rules) > 0 {
		a.editRule(0, a.rules[0])
	}
	a.openTools("rules")
}

func (a *app) ruleFromFlow(f flow, kind capture.RuleType) {
	r := capture.Rule{Name: ruleLabel(kind) + " " + f.path, Type: kind, Match: f.url, Method: f.method}
	if kind == capture.RuleMapLocal {
		if a.rulesPath == "" {
			a.warn("Rules storage is unavailable")
			return
		}
		var err error
		r, err = capture.NewMockRule(filepath.Join(filepath.Dir(a.rulesPath), "mocks"), capture.Flow{
			Method: f.method, URL: f.url, Path: f.path, Status: f.status, Kind: f.kind,
			ResponseBody: f.resBody, ResponseBodyFile: f.resFile, ResponseBodySize: f.resSize, ResponseHeader: f.respHeader,
		})
		if err != nil {
			a.fail("Create mock", err)
			return
		}
	}
	a.editRule(-1, r)
	if kind == capture.RuleMapLocal {
		a.ruleUI.unsavedMock = r.Target
	}
	a.openTools("rules")
}

func (a *app) applyRuleChanges(next []capture.Rule) bool {
	if a.rulesPath == "" {
		a.ruleUI.problem = "Rules storage is unavailable"
		return false
	}
	if err := a.writeRules(next); err != nil {
		a.ruleUI.problem = err.Error()
		return false
	}
	a.rules = next
	if a.srv != nil {
		a.srv.SetRules(next)
	}
	a.ruleUI.problem = ""
	a.done("Rules saved · apply to new requests")
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

// moveRules puts the rules at rows before the one at to, which changes which
// rule wins. The rule being edited stays in the editor, unsaved edits too.
func (a *app) moveRules(rows []int, to int) {
	var order, moved []int // old indexes in their new order
	at := to
	for i := range a.rules {
		if slices.Contains(rows, i) {
			moved = append(moved, i)
			if i < to {
				at--
			}
		} else {
			order = append(order, i)
		}
	}
	order = slices.Insert(order, at, moved...)
	next := make([]capture.Rule, len(order))
	for i, old := range order {
		next[i] = a.rules[old]
	}
	if a.applyRuleChanges(next) && a.ruleUI.index >= 0 {
		a.ruleUI.index = slices.Index(order, a.ruleUI.index)
	}
}

func (a *app) switchRule(i int, on bool) {
	next := slices.Clone(a.rules)
	next[i].Disabled = !on
	if a.applyRuleChanges(next) && a.ruleUI.index == i {
		a.ruleUI.draft.Disabled = !on
	}
}

func (a *app) browseRuleFile() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Choose local response file"})
		if err != nil {
			a.fail("Choose response", err)
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

// ruleTest is the request the rules are tested against: a URL, after a
// method or not. ok is false while it is not one.
func ruleTest(s string) (method, raw string, ok bool) {
	s = strings.TrimSpace(s)
	method = "GET"
	if m, rest, found := strings.Cut(s, " "); found && !strings.Contains(m, "/") {
		method, s = m, strings.TrimSpace(rest)
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || !httpguts.ValidHeaderFieldName(method) {
		return "", "", false
	}
	return method, u.String(), true
}

// rulesPage is the rules in the order they are tried beside the one being
// edited. Testing a request numbers the rules that act on it in the order
// they act, and fades the others.
func (a *app) rulesPage(c *ui.Context, t *ui.Theme, top float32, active bool) {
	e := &a.ruleUI
	if e.index >= len(a.rules) { // the rules were reloaded from disk
		a.discardRuleDraft()
		e.open, e.index = false, -1
	}
	var preview []capture.RulePreview
	method, raw, testing := ruleTest(e.test)
	if testing {
		preview = capture.PreviewRules(a.rules, raw, method, a.breakOn)
	}
	ui.Column(c).Width(300).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Rules").FontSize(uikit.TextTitle).FontWeight(600).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, "New rule", "New rule", plusIcon).Clicked() {
				a.editRule(-1, capture.Rule{Type: capture.RuleMapRemote})
			}
		})
		uikit.Input(c, &e.test).Label("Test a request").Placeholder("Test: GET https://api.example.com").Font(uikit.FontMono).FillWidth()
		switch {
		case strings.TrimSpace(e.test) == "":
			uikit.Meta(c, "Rules are tried from the top.")
		case !testing:
			uikit.Meta(c, "Enter a URL, after a method or not.")
		default:
			acting := 0
			for _, p := range preview {
				acting += min(p.Step, 1)
			}
			summary := "No rule acts · it goes upstream unchanged"
			if acting > 0 {
				summary = fmt.Sprintf("%d %s, in the order numbered", acting, plural(acting, "rule acts", "rules act"))
			}
			uikit.Meta(c, summary).SingleLine()
		}
		a.ruleScenario(c)
		a.ruleList(c, t, preview, active)
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(3)).Children(func() {
		if !e.open {
			uikit.EmptyState(c, "No rules", "Map, redirect, block or pause matching requests. Create one here, or from a request’s Tools menu.", func() {
				if uikit.PrimaryButton(c, "New rule").Clicked() {
					a.editRule(-1, capture.Rule{Type: capture.RuleMapRemote})
				}
			})
			return
		}
		a.ruleDetail(c, t)
	})
	if ui.AlertDialog(c, &e.deleting, "Delete rule?", "New requests will no longer use this rule. Local response files are kept for reuse.", "Cancel", "Delete") == 1 {
		next := slices.Delete(slices.Clone(a.rules), e.index, e.index+1)
		if a.applyRuleChanges(next) {
			e.open = false
			if len(next) > 0 {
				i := min(e.index, len(next)-1)
				a.editRule(i, next[i])
			}
		}
	}
}

// ruleScenario chooses the one scenario whose rules apply, if any.
func (a *app) ruleScenario(c *ui.Context) {
	var names []string
	for _, r := range a.rules {
		if r.Scenario != "" && !slices.Contains(names, r.Scenario) {
			names = append(names, r.Scenario)
		}
	}
	if len(names) == 0 {
		return
	}
	active := capture.ActiveScenario(a.rules)
	current := "No scenario"
	if active != "" {
		current = "Scenario: " + active
	}
	uikit.MenuSelect(c, current, func(m *ui.Menu) {
		if m.Item("No scenario").Radio(active == "").Chosen() {
			a.selectRuleScenario("")
		}
		m.Separator()
		for _, name := range names {
			if m.Item(name).Radio(active == name).Chosen() {
				a.selectRuleScenario(name)
			}
		}
	}).Label("Active scenario").Tooltip("Only the active scenario’s rules apply, with the rules in none").FillWidth()
}

// ruleList is the rules with their switches, in the order they are tried;
// dragging a row moves it.
func (a *app) ruleList(c *ui.Context, t *ui.Theme, preview []capture.RulePreview, active bool) {
	e := &a.ruleUI
	choice := e.index
	e.list.Selected = &choice
	e.list.Key = func(i int) any { return i }
	e.list.Label = func(i int) string { return fmt.Sprintf("Rule %d: %s", i+1, cmp.Or(a.rules[i].Name, a.rules[i].Match)) }
	e.list.Reorder = a.moveRules
	// A pick list, as the Host profiles: the rows draw the rounded selection.
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	c.SetTheme(&pick)
	ui.List(c, &e.list, len(a.rules)).Label("Rules").Grow(1).MinHeight(0).Gap(t.Space(0.5)).Rows(func(row ui.ListRow) {
		c := row.Context
		c.SetTheme(t)
		i := row.Index
		r := a.rules[i]
		line := ui.Row(c).Height(uikit.HeightLarge+12).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
		if row.Selected() {
			uikit.Selected(c, line, active)
		}
		var p capture.RulePreview
		if preview != nil {
			p = preview[i]
			if p.Step == 0 {
				line.Opacity(0.45).Tooltip(p.Reason)
			}
		}
		line.Children(func() {
			on := !r.Disabled
			if ui.Switch(c, &on).Label(fmt.Sprintf("Enable rule %d", i+1)).Changed() {
				a.switchRule(i, on)
			}
			uikit.Glyph(c, ruleIcons[max(slices.Index(ruleTypes, r.Type), 0)]).Tooltip(ruleLabel(r.Type))
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, cmp.Or(r.Name, r.Match)).FontSize(uikit.TextBody).TextColor(t.Text).Grow(1).SingleLine()
					switch {
					case p.Step > 0:
						pal := uikit.Colors(t)
						ui.Box(c).Size(16, 16).Radius(8).Background(pal.Accent).AlignItems(ui.Center).Justify(ui.Center).
							Label(fmt.Sprintf("Acts %s", ordinal(p.Step))).Children(func() {
							ui.Text(c, strconv.Itoa(p.Step)).FontSize(uikit.TextMeta).FontWeight(600).TextColor(pal.AccentText).FontFeatures("tnum")
						})
					case preview == nil && a.srv != nil:
						if n := a.srv.RuleHits(r); n > 0 {
							uikit.Meta(c, strconv.Itoa(n)).Font(uikit.FontMono).FontFeatures("tnum").
								Tooltip(fmt.Sprintf("Changed %d %s since launch", n, plural(n, "request", "requests")))
						}
					}
				})
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Meta(c, strings.TrimSpace(r.Method+" "+r.Match)).Font(uikit.FontMono).Grow(1).SingleLine()
					if r.Scenario != "" {
						uikit.Meta(c, r.Scenario).MaxWidth(96).SingleLine().Tooltip("In scenario " + r.Scenario)
					}
				})
			})
		})
	})
	c.SetTheme(t)
	if choice != e.index && choice >= 0 && choice < len(a.rules) {
		a.editRule(choice, a.rules[choice])
	}
}

func ordinal(n int) string {
	switch n {
	case 1:
		return "first"
	case 2:
		return "second"
	case 3:
		return "third"
	}
	return fmt.Sprintf("%dth", n)
}

// ruleDetail edits the chosen rule: what it matches, what it does, Save.
func (a *app) ruleDetail(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	breakpoint := e.kind == "Request breakpoint" || e.kind == "Response breakpoint"
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Input(c, &e.draft.Name).Label("Rule name").Placeholder(cmp.Or(e.draft.Match, "Untitled rule")).Grow(1).MaxWidth(360).FontSize(uikit.TextTitle).Height(uikit.HeightLarge)
		on := !e.draft.Disabled
		if ui.Switch(c, &on).Label("Rule enabled").Changed() {
			e.draft.Disabled = !on
		}
		ui.Spacer(c)
		uikit.MoreMenu(c, "Rule actions", func(m *ui.Menu) {
			i := e.index
			actionMenu(m, []action{
				do("rules.up", "Move up", func() { a.moveRules([]int{i}, i-1) }).when(i > 0, "Already tried first").in("order"),
				do("rules.down", "Move down", func() { a.moveRules([]int{i}, i+2) }).when(i >= 0 && i < len(a.rules)-1, "Already tried last").in("order"),
				do("rules.delete", "Delete rule…", func() { e.deleting = true }).when(i >= 0, "Not saved yet").in("danger"),
			})
		})
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Form(c, func() {
			ui.Field(c, "Type", func() {
				uikit.Select(c, &e.kind, ruleLabels).Label("Rule type").Width(200)
			})
			ui.Field(c, "Method", func() {
				uikit.Input(c, &e.draft.Method).Width(95).Label("Match method").Placeholder("Any").Font(uikit.FontMono)
			})
			ui.Field(c, "URL contains", func() {
				uikit.Input(c, &e.draft.Match).Label("URL contains").Placeholder("https://api.example.com/v1/").Font(uikit.FontMono).FillWidth()
			}).Description("A literal part of the full URL, not a wildcard or regex.")
			if !breakpoint {
				ui.Field(c, "Scenario", func() {
					uikit.Input(c, &e.draft.Scenario).Label("Rule scenario").Placeholder("None").Width(200)
				}).Description("A rule in a scenario applies only while that scenario is active.")
			}
			switch e.kind {
			case "Map Local":
				ui.Field(c, "Response file", func() {
					ui.Row(c).Gap(t.Space(2)).FillWidth().Children(func() {
						uikit.Input(c, &e.draft.Target).Label("Response file").Font(uikit.FontMono).Grow(1)
						if uikit.SecondaryButton(c, "Browse…").Clicked() {
							a.browseRuleFile()
						}
					})
				}).Description("Up to 8 MiB.")
				ui.Field(c, "Status", func() {
					uikit.Input(c, &e.status).Label("Response status").Width(95).Font(uikit.FontMono)
				})
				ui.Field(c, "Headers", func() {
					ui.TextArea(c, &e.headers).Height(76).FillWidth().Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Label("Response headers").Placeholder("Content-Type: application/json")
				}).Description("Connection headers are removed; the length is recalculated.")
			case "Map Remote":
				ui.Field(c, "Send to", func() {
					uikit.Input(c, &e.draft.Target).Label("Target base URL").Placeholder("http://127.0.0.1:8080").Font(uikit.FontMono).FillWidth()
				}).Description("Keeps the original path and query. A target path prefixes the path; a target query replaces the query.")
			case "Block":
				ui.Field(c, "Response", func() { uikit.Body(c, "403, without contacting the server") })
			default:
				ui.Field(c, "Pauses", func() { uikit.Body(c, "While Breakpoints is on") }).
					Description("Continue or abort in the breakpoint dialog.")
			}
		})
	})
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if e.problem != "" {
			ui.Text(c, e.problem).FontSize(uikit.TextMeta).TextColor(t.Danger).Grow(1).MaxLines(2)
		} else {
			uikit.Meta(c, "Changes apply to new requests.").Grow(1).SingleLine()
		}
		if uikit.PrimaryButton(c, "Save rule").Tooltip("Save · Ctrl/Cmd+S").Clicked() {
			a.saveRule()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyS, a.saveRule) // the Tools window's menu takes it first
}
