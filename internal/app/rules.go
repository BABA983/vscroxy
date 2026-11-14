package app

import (
	"cmp"
	"crypto/rand"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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
	statusValue    float64
	headers        string
	problem        string
	unsavedMock    string
	test           string // see ruleTest
	testBody       string
	testFor        [2]string
	testOperations []string
	testProblem    string
	apps           string // the draft's Apps, comma-separated
	network        networkDraft
	networkPreset  string // copies values, never links a saved rule to a preset
	// body edits a Map Local file's response, read from bodyFor as bodyText;
	// nil for a folder or a file it cannot edit, as bodyNote says.
	body               *editor.State
	bodyFor, bodyText  string
	bodyNote           string
	bodyDir, bodyFresh bool // bodyFresh: the file does not exist yet
	list               ui.ListState
}

var ruleTypes = []capture.RuleType{capture.RuleMapLocal, capture.RuleMapRemote, capture.RuleBlock, capture.RuleScript, capture.RuleCodeMock, capture.RuleBreak, capture.RuleBreakResponse, capture.RuleBreakMessage, capture.RuleNetwork}
var ruleLabels = []string{"Map Local", "Map Remote", "Block", "Script", "Code Mock", "Request breakpoint", "Response breakpoint", "WebSocket message breakpoint", "Network conditions"}
var ruleIcons = []*ui.SVG{fileInputIcon, lucide.ArrowRightLeft(), lucide.Ban(), scriptsIcon, scriptsIcon, breakIcon, breakIcon, breakIcon, networkIcon}
var hideIcon = lucide.EyeOff()
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
	e.statusValue, _ = strconv.ParseFloat(e.status, 64)
	// The draft must not edit the saved rule's slices in place.
	e.draft.Apps, e.draft.When = nil, slices.Clone(r.When)
	e.apps = strings.Join(r.Apps, ", ")
	e.network, e.networkPreset = newNetworkDraft(capture.Throttle{}), ""
	if r.Network != nil {
		e.network = newNetworkDraft(*r.Network)
		e.draft.Network = new(*r.Network)
	}
	e.body, e.bodyFor = nil, ""
}

// loadRuleBody reads the draft's Map Local file into the body editor when
// the target changed, unless the body has edits the new target would lose.
func (a *app) loadRuleBody() {
	e := &a.ruleUI
	target := strings.TrimSpace(e.draft.Target)
	if e.body != nil && (e.bodyFor == target || e.body.Buffer.String() != e.bodyText) {
		return
	}
	if e.body == nil && e.bodyFor == target && (e.bodyDir || e.bodyNote != "") {
		return
	}
	e.body, e.bodyFor, e.bodyText, e.bodyNote, e.bodyDir, e.bodyFresh = nil, target, "", "", false, false
	var data []byte
	if target == "" {
		e.bodyFresh = true
		e.bodyNote = locales.SavingCreatesMock()
	} else if info, err := os.Stat(target); err == nil && info.IsDir() {
		e.bodyDir = true
		return
	} else if errors.Is(err, os.ErrNotExist) {
		e.bodyFresh = true
		e.bodyNote = locales.SavingCreatesFile()
	} else if err != nil {
		e.bodyNote = locales.CannotReadFile(err.Error())
		return
	} else if info.Size() > 8<<20 {
		e.bodyNote = locales.LargerThan8Mib()
		return
	} else if data, err = os.ReadFile(target); err != nil {
		e.bodyNote = locales.CannotReadFile(err.Error())
		return
	} else if !utf8.Valid(data) {
		e.bodyNote = "Binary content; edit it outside vscroxy."
		return
	}
	e.bodyText = string(data)
	buf := editor.NewBuffer(e.bodyText)
	if strings.EqualFold(filepath.Ext(target), ".json") || jsontext.Value(data).IsValid() {
		buf.SetLanguage(editor.LanguageNamed("json"))
	} else {
		buf.SetLanguage(editor.LanguageNamed(strings.TrimPrefix(filepath.Ext(target), ".")))
	}
	e.body = &editor.State{Buffer: buf, Label: locales.ResponseBody(), Placeholder: `{"ok": true}`}
}

// ruleBodyEdited reports whether the body editor has changes to write.
func (e *ruleEditor) ruleBodyEdited() bool {
	return e.body != nil && e.body.Buffer.String() != e.bodyText
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
			a.warn(locales.RulesStorageIsUnavailable())
			return
		}
		var err error
		r, err = capture.NewMockRule(filepath.Join(filepath.Dir(a.rulesPath), "mocks"), capture.Flow{
			Method: f.method, URL: f.url, Path: f.path, Status: f.status, Kind: f.kind,
			ResponseBody: f.resBody, ResponseBodyFile: f.resFile, ResponseBodySize: f.resSize, ResponseHeader: f.respHeader,
		})
		if err != nil {
			a.fail(locales.CreateMock(), err)
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
	a.done(locales.RulesSavedApplyToNew())
	return true
}

func (a *app) saveRule() {
	e := &a.ruleUI
	r := e.draft
	r.Type = ruleTypes[slices.Index(ruleLabels, e.kind)]
	r.Match, r.Method = strings.TrimSpace(r.Match), strings.ToUpper(strings.TrimSpace(r.Method))
	r.Apps = nil
	for app := range strings.SplitSeq(e.apps, ",") {
		if app = strings.TrimSpace(app); app != "" {
			r.Apps = append(r.Apps, app)
		}
	}
	r.When = nil
	for _, h := range e.draft.When {
		if h.Name = strings.TrimSpace(h.Name); h.Name != "" {
			if h.Op == rules.HeaderPresent {
				h.Value = ""
			}
			r.When = append(r.When, h)
		}
	}
	r.Target = strings.TrimSpace(r.Target)
	r.Scenario = strings.TrimSpace(r.Scenario)
	if r.Type == capture.RuleBreak || r.Type == capture.RuleBreakResponse || r.Type == capture.RuleBreakMessage {
		r.Scenario = ""
	}
	if r.Type != capture.RuleBreakMessage {
		r.Direction = ""
	}
	r.Network = nil
	if r.Type == capture.RuleNetwork {
		conditions, err := e.network.value()
		if err != nil {
			e.problem = err.Error()
			return
		}
		conditions.Name = "Rule"
		r.Network = &conditions
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
		if r.Target == "" && e.ruleBodyEdited() {
			if a.rulesPath == "" {
				e.problem = "Rules storage is unavailable"
				return
			}
			ext := ".txt"
			if jsontext.Value(e.body.Buffer.String()).IsValid() {
				ext = ".json"
			}
			r.Target = filepath.Join(filepath.Dir(a.rulesPath), "mocks", rand.Text()+ext)
		}
	} else if r.Type != capture.RuleMapRemote && r.Type != capture.RuleScript && r.Type != capture.RuleCodeMock {
		r.Target = ""
	}
	r.PathFrom, r.PathTo = strings.TrimSpace(r.PathFrom), strings.TrimSpace(r.PathTo)
	if r.Type != capture.RuleMapRemote {
		r.PathTo, r.KeepHost = "", false
		if r.Type != capture.RuleMapLocal || !e.bodyDir {
			r.PathFrom = ""
		}
	}
	if r.Type != capture.RuleBlock {
		r.Block = ""
	}
	next := slices.Clone(a.rules)
	index := e.index
	if index < 0 {
		index = len(next)
		next = append(next, r)
	} else {
		next[index] = r
	}
	if r.Type == capture.RuleMapLocal && e.ruleBodyEdited() {
		// The rule must be valid before its file changes.
		if err := capture.ValidateRules(next); err != nil {
			e.problem = err.Error()
			return
		}
		if err := os.MkdirAll(filepath.Dir(r.Target), 0o700); err != nil {
			e.problem = "Write response file: " + err.Error()
			return
		}
		if err := store.WriteFile(r.Target, []byte(e.body.Buffer.String())); err != nil {
			e.problem = "Write response file: " + err.Error()
			return
		}
		if r.Target != strings.TrimSpace(e.draft.Target) { // a new file in mocks
			// The draft owns it from now on, and loses it unless saved.
			e.draft.Target, e.unsavedMock = r.Target, r.Target
		}
		e.bodyText, e.bodyFresh = e.body.Buffer.String(), false
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

func (a *app) switchRule(i int, on bool) bool {
	next := slices.Clone(a.rules)
	next[i].Disabled = !on
	if !a.applyRuleChanges(next) {
		return false
	}
	if a.ruleUI.index == i {
		a.ruleUI.draft.Disabled = !on
	}
	return true
}

func (a *app) browseRuleFile(folder bool) {
	title := "Choose local response file"
	if folder {
		title = "Choose a folder to answer from"
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Directory: folder})
		if err != nil {
			a.fail(locales.ChooseResponse(), err)
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

// ruleTest is the request the rules are tested against, written as
//
//	[METHOD] URL [@ App]; Header: value; …
//
// The method is GET and the scheme https when left out. ok is false while it
// is not one.
func ruleTest(s string) (req rules.Request, ok bool) {
	parts := strings.Split(s, ";")
	first := strings.TrimSpace(parts[0])
	if at := strings.Index(first, " @"); at >= 0 {
		req.App = strings.TrimSpace(first[at+2:])
		first = strings.TrimSpace(first[:at])
	}
	req.Method = "GET"
	if m, rest, found := strings.Cut(first, " "); found && !strings.Contains(m, "/") {
		req.Method, first = m, strings.TrimSpace(rest)
	}
	if !strings.Contains(first, "://") {
		first = "https://" + first
	}
	u, err := url.Parse(first)
	if err != nil || u.Hostname() == "" || !slices.Contains([]string{"http", "https", "ws", "wss"}, u.Scheme) || u.User != nil || u.Fragment != "" || !httpguts.ValidHeaderFieldName(req.Method) {
		return rules.Request{}, false
	}
	req.URL, req.Host = u.String(), u.Hostname()
	req.Header = http.Header{}
	for _, part := range parts[1:] {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, value, found := strings.Cut(part, ":")
		if name = strings.TrimSpace(name); !found || !httpguts.ValidHeaderFieldName(name) {
			return rules.Request{}, false
		}
		req.Header.Add(name, strings.TrimSpace(value))
	}
	return req, true
}

// ruleRows are the rules the page lists, by index: on Mocks, only the
// Map Local and Code Mock rules.
func (a *app) ruleRows() []int {
	var rows []int
	for i, r := range a.rules {
		if a.toolsPage != "mocks" || r.Type == capture.RuleMapLocal || r.Type == capture.RuleCodeMock {
			rows = append(rows, i)
		}
	}
	return rows
}

// rulesPage is the rules in the order they are tried beside the one being
// edited. Testing a request numbers the rules that act on it in the order
// they act, and fades the others. Mocks is the same page for the Map Local
// rules alone.
func (a *app) rulesPage(c *ui.Context, t *ui.Theme, top float32, active bool) {
	e := &a.ruleUI
	mocks := a.toolsPage == "mocks"
	if e.index >= len(a.rules) { // the rules were reloaded from disk
		a.discardRuleDraft()
		e.open, e.index = false, -1
	}
	rows := a.ruleRows()
	// Mocks shows a local response rule, including a new unsaved one.
	if mocks && e.open && (e.index >= 0 && !slices.Contains(rows, e.index) || e.index < 0 && e.kind != "Map Local" && e.kind != "Code Mock") {
		e.open = false
	}
	if !e.open && len(rows) > 0 {
		a.editRule(rows[0], a.rules[rows[0]])
	}
	title, kind := locales.Rules(), capture.RuleMapRemote
	if mocks {
		title, kind = "Mocks", capture.RuleMapLocal
	}
	var preview []capture.RulePreview
	req, testing := ruleTest(e.test)
	if testing {
		source := [2]string{e.test, e.testBody}
		if source != e.testFor {
			e.testFor, e.testOperations, e.testProblem = source, nil, ""
			ops, err := graphql.RequestOperations(req.Method, req.URL, cmp.Or(req.Header.Get("Content-Type"), "application/json"), e.testBody)
			if err != nil {
				e.testProblem = err.Error()
			} else {
				for _, op := range ops {
					if op.Name != "" {
						e.testOperations = append(e.testOperations, op.Name)
					}
				}
			}
		}
		req.Operations = e.testOperations
	}
	if testing {
		preview = capture.PreviewRules(a.rules, req, a.breakOn)
	}
	ui.Column(c).Width(300).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(uikit.Scaled(uikit.TextTitle)).FontWeight(500).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, locales.NewRule(), locales.NewRule(), plusIcon).Clicked() {
				a.editRule(-1, capture.Rule{Type: kind})
			}
		})
		uikit.Tip(c, uikit.Input(c, &e.test).Label(locales.TestARequest()).Placeholder(locales.TestGetHttpsApiExample()), locales.MethodUrlAppHeaderValue()).Font(uikit.FontMono).FillWidth()
		if e.draft.Operation != "" || slices.ContainsFunc(a.rules, func(r capture.Rule) bool { return r.Operation != "" }) {
			uikit.TextArea(c, &e.testBody).Label(locales.TestGraphqlBody()).Placeholder(`{"query":"query Search { items { id } }"}`).Height(80).FillWidth().Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextMeta)).
				Tooltip(locales.PostUsesJsonByDefault())
			if testing && e.testProblem != "" {
				uikit.Hint(c, locales.GraphqlNotVerified(e.testProblem))
			}
		}
		switch {
		case strings.TrimSpace(e.test) == "" && mocks:
			uikit.Meta(c, locales.LocalFilesAndCodeMocks())
		case strings.TrimSpace(e.test) == "":
			uikit.Meta(c, locales.RulesAreTriedFromThe())
		case !testing:
			uikit.Meta(c, locales.EnterAUrlThenApp())
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
			for _, p := range preview {
				if p.Step > 0 && p.Reason != "" {
					uikit.Meta(c, p.Reason)
				}
			}
		}
		a.ruleScenario(c)
		a.ruleList(c, t, rows, preview, active)
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(3)).Children(func() {
		if !e.open {
			empty, hint := "No rules", "Map, redirect, block, script or pause matching requests. Create one here, or from a request’s Tools menu."
			if mocks {
				empty, hint = "No mocks", "Answer requests from local files or JavaScript. Create a Map Local or Code Mock rule here; saved examples can also become mocks."
			}
			uikit.EmptyState(c, empty, hint, func() {
				if uikit.PrimaryButton(c, locales.NewRule()).Clicked() {
					a.editRule(-1, capture.Rule{Type: kind})
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
			if rows := a.ruleRows(); len(rows) > 0 {
				at, _ := slices.BinarySearch(rows, e.index)
				i := rows[min(at, len(rows)-1)]
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
		current = locales.ScenarioLabel(active)
	}
	uikit.Tip(c, uikit.MenuSelect(c, current, func(m *ui.Menu) {
		if m.Item("No scenario").Radio(active == "").Chosen() {
			a.selectRuleScenario("")
		}
		m.Separator()
		for _, name := range names {
			if m.Item(name).Radio(active == name).Chosen() {
				a.selectRuleScenario(name)
			}
		}
	}).Label(locales.ActiveScenario()), locales.OnlyTheActiveScenarioS()).FillWidth()
}

// ruleList is the rules with their switches, in the order they are tried;
// dragging a row moves it.
func (a *app) ruleList(c *ui.Context, t *ui.Theme, rows []int, preview []capture.RulePreview, active bool) {
	e := &a.ruleUI
	choice := slices.Index(rows, e.index)
	e.list.Selected = &choice
	e.list.Key = func(row int) any { return rows[row] }
	e.list.Label = func(row int) string {
		i := rows[row]
		return fmt.Sprintf("Rule %d: %s", i+1, cmp.Or(a.rules[i].Name, a.rules[i].Match))
	}
	e.list.Reorder = func(moved []int, to int) {
		at := len(a.rules)
		if to < len(rows) {
			at = rows[to]
		}
		for k, row := range moved {
			moved[k] = rows[row]
		}
		a.moveRules(moved, at)
	}
	// A pick list, as the Host profiles: the rows draw the rounded selection.
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	c.SetTheme(&pick)
	ui.List(c, &e.list, len(rows)).Label(locales.Rules()).Grow(1).MinHeight(0).Gap(t.Space(0.5)).Rows(func(row ui.ListRow) {
		c := row.Context
		c.SetTheme(t)
		i := rows[row.Index]
		r := a.rules[i]
		line := ui.Row(c).MinHeight(uikit.HeightLarge+12).Padding(t.Space(1), t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
		if row.Selected() {
			uikit.Selected(c, line, active)
		}
		var p capture.RulePreview
		if preview != nil {
			p = preview[i]
			if p.Reason != "" {
				uikit.Tip(c, line, p.Reason)
			}
			if p.Step == 0 {
				line.Opacity(0.45)
			}
		}
		line.Children(func() {
			on := !r.Disabled
			if ui.Switch(c, &on).Label(locales.EnableRule(i + 1)).Changed() {
				a.switchRule(i, on)
			}
			icon, kind := ruleIcons[max(slices.Index(ruleTypes, r.Type), 0)], ruleLabel(r.Type)
			switch r.Block {
			case capture.BlockHide:
				icon, kind = hideIcon, "Block · hide"
			case capture.BlockDrop:
				kind = "Block · drop connection"
			}
			uikit.Tip(c, uikit.Glyph(c, icon), kind)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, cmp.Or(r.Name, r.Match)).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Text).Grow(1).SingleLine()
					if r.Scenario != "" {
						uikit.Tip(c, uikit.Meta(c, r.Scenario).MaxWidth(96).SingleLine(), "In scenario "+r.Scenario)
					}
					switch {
					case p.Step > 0:
						pal := uikit.Colors(t)
						ui.Box(c).Size(16, 16).Radius(8).Background(pal.Accent).AlignItems(ui.Center).Justify(ui.Center).
							Label(fmt.Sprintf("Acts %s", ordinal(p.Step))).Children(func() {
							ui.Text(c, strconv.Itoa(p.Step)).FontSize(uikit.Scaled(uikit.TextMeta)).FontWeight(500).TextColor(pal.AccentText).FontFeatures("tnum")
						})
					case preview == nil && a.srv != nil:
						if n := a.srv.RuleHits(r); n > 0 {
							uikit.Meta(c, strconv.Itoa(n)).Font(uikit.FontMono).FontFeatures("tnum").
								Tooltip(fmt.Sprintf("Changed %d %s since launch", n, plural(n, "request", "requests")))
						}
					}
				})
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					summary := ruleSummary(r)
					uikit.Tip(c, uikit.Meta(c, summary).Font(uikit.FontMono).Grow(1).MaxLines(2), summary)
				})
			})
		})
	})
	c.SetTheme(t)
	if choice >= 0 && choice < len(rows) && rows[choice] != e.index {
		a.editRule(rows[choice], a.rules[rows[choice]])
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
	breakpoint := e.kind == "Request breakpoint" || e.kind == "Response breakpoint" || e.kind == "WebSocket message breakpoint"
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Input(c, &e.draft.Name).Label(locales.RuleName()).Placeholder(cmp.Or(e.draft.Match, "Untitled rule")).Grow(1).MaxWidth(360).FontSize(uikit.Scaled(uikit.TextTitle)).Height(uikit.HeightLarge)
		on := !e.draft.Disabled
		if ui.Switch(c, &on).Label(locales.RuleEnabled()).Changed() {
			e.draft.Disabled = !on
		}
		ui.Spacer(c)
		uikit.MoreMenu(c, locales.RuleActions(), func(m *ui.Menu) {
			i := e.index
			actionMenu(m, []action{
				do("rules.up", locales.MoveUp(), func() { a.moveRules([]int{i}, i-1) }).when(i > 0, "Already tried first").in("order"),
				do("rules.down", locales.MoveDown(), func() { a.moveRules([]int{i}, i+2) }).when(i >= 0 && i < len(a.rules)-1, "Already tried last").in("order"),
				do("rules.delete", locales.DeleteRule(), func() { e.deleting = true }).when(i >= 0, "Not saved yet").in("danger"),
			})
		})
	})
	// Each rule's form starts at its top, not where the last one was scrolled.
	ui.Scroll(c.Key(fmt.Sprint("rule-form", e.index))).Grow(1).MinHeight(0).Children(func() {
		ui.Form(c, func() {
			uikit.FormRow(c, locales.TypeText(), "", "", func() {
				labels := ruleLabels
				if a.toolsPage == "mocks" {
					labels = []string{"Map Local", "Code Mock"}
				}
				uikit.Select(c, &e.kind, labels).Label(locales.RuleType()).Width(200)
			})
			uikit.FormRow(c, locales.Method(), "", "", func() {
				uikit.Input(c, &e.draft.Method).Width(95).Label(locales.MatchMethod()).Placeholder(locales.Any()).Font(uikit.FontMono)
			})
			uikit.FormRow(c, locales.Url(), urlHelp()[max(slices.Index([]string{rules.URLContains, rules.URLWildcard, rules.URLRegex}, e.draft.MatchMode), 0)], "", func() {
				ui.Row(c).Gap(t.Space(2)).FillWidth().Children(func() {
					modes := []string{rules.URLContains, rules.URLWildcard, rules.URLRegex}
					mode := max(slices.Index(modes, e.draft.MatchMode), 0)
					uikit.Segmented(c, &mode, locales.Contains(), locales.Wildcard(), locales.Regex()).Label(locales.UrlMatch())
					e.draft.MatchMode = modes[mode]
					uikit.Input(c, &e.draft.Match).Label(locales.MatchUrl()).Placeholder(urlPlaceholders[mode]).Font(uikit.FontMono).Grow(1).MinWidth(0)
				})
			})
			uikit.FormRow(c, locales.Apps(), locales.CommaSeparatedAppNamesMatches(), "", func() {
				uikit.Input(c, &e.apps).Label(locales.MatchApps()).Placeholder(locales.Any()).FillWidth()
			})
			uikit.FormRow(c, locales.RequestHeadersLabel(), "", "", func() { a.ruleHeaderConditions(c, t) })
			switch e.kind {
			case "Network conditions":
				uikit.FormRow(c, locales.CopyPreset(), locales.CopiesValuesLaterPresetEdits(), "", func() {
					presets := a.networkPresets()
					names := make([]string, 0, len(presets))
					for _, p := range presets {
						names = append(names, p.Name)
					}
					uikit.Select(c, &e.networkPreset, names).Label(locales.NetworkRulePreset()).Placeholder(locales.ChooseAPreset()).Width(240).OnChange(func() {
						for _, p := range a.networkPresets() {
							if p.Name == e.networkPreset {
								e.network = newNetworkDraft(p)
								break
							}
						}
					})
				})
				uikit.FormRow(c, locales.Conditions(), locales.FirstMatchingNetworkRuleReplaces(), "", func() { e.network.fields(c) })
				uikit.FormRow(c, locales.Scope(), "", "", func() {
					uikit.Meta(c, locales.HttpInspectedHttpsAndSse2())
				})
			case "Map Local":
				a.loadRuleBody()
				uikit.FormRow(c, locales.ResponseFrom(), locales.AFileUpTo8(), "", func() {
					ui.Column(c).Gap(t.Space(1)).FillWidth().Children(func() {
						ui.Row(c).Gap(t.Space(2)).FillWidth().Children(func() {
							uikit.Input(c, &e.draft.Target).Label(locales.ResponseFile()).Placeholder(locales.AFileOrAFolder()).Font(uikit.FontMono).Grow(1)
							if uikit.SecondaryButton(c, locales.File2()).Clicked() {
								a.browseRuleFile(false)
							}
							if uikit.SecondaryButton(c, locales.Folder()).Clicked() {
								a.browseRuleFile(true)
							}
						})
						if e.draft.Target != "" {
							uikit.Tip(c, uikit.Meta(c, e.draft.Target).Font(uikit.FontMono).SingleLine().TruncateMiddle().MinWidth(0), e.draft.Target)
						}
					})
				})
				if !e.bodyDir {
					uikit.FormRowTall(c, locales.Body(), cmp.Or(e.bodyNote, "Save rule writes it to the file."), "", func() ui.Element {
						if e.body == nil {
							return uikit.Hint(c, e.bodyNote)
						}
						e.body.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
						e.body.Theme = uikit.EditorTheme(t)
						return ui.Box(c).Height(180).FillWidth().Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
							editor.View(c, e.body).Grow(1).FillWidth().FocusRing(true)
						})
					})
				}
				if e.bodyDir {
					uikit.FormRow(c, locales.StripPath(), locales.ApiUsers42JsonReads(), "", func() {
						uikit.Input(c, &e.draft.PathFrom).Label(locales.StripPathPrefix()).Placeholder("/api").Font(uikit.FontMono).Width(200)
					})
				}
				uikit.FormRow(c, locales.Status(), "", "", func() {
					uikit.NumberField(c, &e.status, &e.statusValue, 200, 599, "", locales.ResponseStatus(), "", nil)
				})
				uikit.FormRow(c, locales.RequestHeadersLabel(), locales.ConnectionHeadersAreRemovedThe(), "", func() {
					uikit.TextArea(c, &e.headers).Height(76).FillWidth().Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Label(locales.ResponseHeaders()).Placeholder("Content-Type: application/json")
				})
			case "Map Remote":
				uikit.FormRow(c, locales.SendTo(), locales.SchemeHostAndPortA(), "", func() {
					uikit.Input(c, &e.draft.Target).Label(locales.TargetBaseUrl()).Placeholder("http://127.0.0.1:8080").Font(uikit.FontMono).FillWidth()
				})
				uikit.FormRow(c, locales.RewritePath(), locales.APathStartingWithThe(), "", func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
						uikit.Input(c, &e.draft.PathFrom).Label(locales.RewritePathFrom()).Placeholder("/v1/").Font(uikit.FontMono).Grow(1)
						uikit.Meta(c, "→")
						uikit.Input(c, &e.draft.PathTo).Label(locales.RewritePathTo()).Placeholder("/api/v1/").Font(uikit.FontMono).Grow(1)
					})
				})
				uikit.FormRow(c, locales.HostHeader(), locales.ForAServerThatServes(), "", func() {
					ui.Checkbox(c, &e.draft.KeepHost, "Keep the original Host header")
				})
			case "Block":
				uikit.FormRow(c, locales.Action(), blockHelp[max(slices.Index(capture.BlockActions, e.draft.Block), 0)], "", func() {
					ui.Row(c).Children(func() {
						choice := max(slices.Index(capture.BlockActions, e.draft.Block), 0)
						uikit.Segmented(c, &choice, locales.Refuse(), locales.DropConnection(), locales.Hide()).Label(locales.BlockAction())
						e.draft.Block = capture.BlockActions[choice]
					})
				})
			case "Script":
				uikit.FormRow(c, locales.Script(), locales.OnrequestOnresponseEditHttpOnmessage(), "", func() { a.ruleScriptField(c, t) })
			case "Code Mock":
				uikit.FormRow(c, locales.Script(), locales.OnmockReadsPmRequestAnd(), "", func() { a.ruleScriptField(c, t) })
			case "WebSocket message breakpoint":
				uikit.FormRow(c, locales.Direction(), locales.WhileBreakpointsIsOnEdit(), "", func() {
					labels := []string{"Both", "Server → client", "Client → server"}
					values := []string{"", "in", "out"}
					choice := max(0, slices.Index(values, e.draft.Direction))
					uikit.Segmented(c, &choice, labels...).Label(locales.WebsocketBreakpointDirection())
					e.draft.Direction = values[choice]
				})
			default:
				uikit.FormRow(c, locales.Pauses(), locales.ContinueOrAbortInThe(), "", func() { uikit.Body(c, locales.WhileBreakpointsIsOn()) })
			}
			if !breakpoint {
				uikit.FormRow(c, locales.Scenario(), locales.ARuleInAScenario(), "", func() {
					uikit.Input(c, &e.draft.Scenario).Label(locales.RuleScenario()).Placeholder(locales.None()).Width(200)
				})
			}
			uikit.FormRow(c, locales.GraphqlOperation(), locales.ExactSelectedOperationNameCase(), "", func() {
				uikit.Input(c, &e.draft.Operation).Label(locales.MatchGraphqlOperation()).Placeholder(locales.Any()).Font(uikit.FontMono).FillWidth()
			})
		})
	})
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if e.problem != "" {
			ui.Text(c, e.problem).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger).Grow(1).MaxLines(2)
		} else {
			uikit.Meta(c, locales.ChangesApplyToNewRequests()).Grow(1).SingleLine()
		}
		if uikit.Tip(c, uikit.PrimaryButton(c, locales.SaveRule()), locales.SaveCtrlCmdS()).Clicked() {
			a.saveRule()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyS, a.saveRule) // the Tools window's menu takes it first
}

var urlPlaceholders = []string{"api.example.com/v1/", "*.example.com/v1/users/*", `^https://api\.example\.com/v1/users/\d+$`}

func urlHelp() []string {
	return []string{locales.UrlLiteralHelp(), locales.UrlWildcardHelp(), locales.UrlRegexHelp()}
}

// ruleSummary is what a rule matches, in one line.
func ruleSummary(r capture.Rule) string {
	match := r.Match
	if r.MatchMode == rules.URLRegex {
		match = "/" + match + "/"
	}
	parts := []string{r.Method, match}
	if r.Operation != "" {
		parts = append(parts, "operation:"+r.Operation)
	}
	if len(r.Apps) > 0 {
		parts = append(parts, "@"+strings.Join(r.Apps, ","))
	}
	if n := len(r.When); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d %s", n, plural(n, "header", "headers")))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ruleScriptField chooses the script a Script rule runs.
func (a *app) ruleScriptField(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
		current := strings.TrimSuffix(e.draft.Target, ".js")
		label := cmp.Or(current, "Choose a script")
		if current != "" && !slices.Contains(a.scripts.names, current) {
			label = current + " (missing)"
		}
		uikit.MenuSelect(c, label, func(m *ui.Menu) {
			for _, name := range a.scripts.names {
				if m.Item(name).Radio(name == current).Chosen() {
					e.draft.Target = scriptFile(name)
				}
			}
			if len(a.scripts.names) > 0 {
				m.Separator()
			}
			if m.Item("New script").Chosen() {
				text := newScriptText
				if e.kind == "Code Mock" {
					text = newMockScriptText
				}
				if name, ok := a.newScript(&a.scripts, cmp.Or(strings.TrimSpace(e.draft.Name), "Untitled"), text); ok {
					e.draft.Target = scriptFile(name)
				}
			}
		}).Label(locales.RuleScript()).Width(240)
		if current != "" && slices.Contains(a.scripts.names, current) {
			if uikit.Tip(c, uikit.SecondaryButton(c, locales.EditScript()), "Defines "+scriptHooks(&a.scripts, current)).Clicked() {
				a.openScript(current)
			}
		}
	})
}

// ruleHeaderConditions edits the request headers the draft needs.
func (a *app) ruleHeaderConditions(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	ui.Column(c).Gap(t.Space(2)).FillWidth().Children(func() {
		remove := -1
		for i := range e.draft.When {
			h := &e.draft.When[i]
			ui.Row(c.Key(fmt.Sprint("when", i))).Gap(t.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
				uikit.Input(c, &h.Name).Label(locales.HeaderName(i + 1)).Placeholder(locales.Name()).Font(uikit.FontMono).Width(150)
				op := cmp.Or(h.Op, rules.HeaderEquals)
				uikit.SelectLabels(c, &op, rules.HeaderOps, optionLabel).Label(locales.HeaderComparison(i + 1)).Width(100)
				h.Op = op
				if op != rules.HeaderPresent {
					uikit.Input(c, &h.Value).Label(locales.HeaderValue(i + 1)).Placeholder(locales.Value()).Font(uikit.FontMono).Grow(1)
				} else {
					ui.Spacer(c)
				}
				if uikit.IconButton(c, locales.RemoveHeader(i+1), locales.RemoveCondition(), closeIcon).Clicked() {
					remove = i
				}
			})
		}
		if remove >= 0 {
			e.draft.When = slices.Delete(e.draft.When, remove, remove+1)
		}
		ui.Row(c).Children(func() {
			if uikit.CompactButton(c, locales.AddHeaderCondition()).Clicked() {
				e.draft.When = append(e.draft.When, rules.HeaderCondition{Op: rules.HeaderEquals})
			}
		})
	})
}

var blockHelp = []string{
	"Answers 403 without contacting the server.",
	"Closes the client’s connection without an answer, as a network failure would. Clients may retry GET.",
	"Proxies it as usual but keeps it out of the table. Rules below it do not act on it.",
}
