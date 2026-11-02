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
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
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
	headers        string
	problem        string
	unsavedMock    string
	test           string // see ruleTest
	apps           string // the draft's Apps, comma-separated
	// body edits a Map Local file's response, read from bodyFor as bodyText;
	// nil for a folder or a file it cannot edit, as bodyNote says.
	body               *editor.State
	bodyFor, bodyText  string
	bodyNote           string
	bodyDir, bodyFresh bool // bodyFresh: the file does not exist yet
	list               ui.ListState
}

var ruleTypes = []capture.RuleType{capture.RuleMapLocal, capture.RuleMapRemote, capture.RuleBlock, capture.RuleBreak, capture.RuleBreakResponse}
var ruleLabels = []string{"Map Local", "Map Remote", "Block", "Request breakpoint", "Response breakpoint"}
var ruleIcons = []*ui.SVG{fileInputIcon, lucide.ArrowRightLeft(), lucide.Ban(), breakIcon, breakIcon}
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
	// The draft must not edit the saved rule's slices in place.
	e.draft.Apps, e.draft.When = nil, slices.Clone(r.When)
	e.apps = strings.Join(r.Apps, ", ")
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
		e.bodyNote = "Saving creates a response file in vscroxy’s mocks folder."
	} else if info, err := os.Stat(target); err == nil && info.IsDir() {
		e.bodyDir = true
		return
	} else if errors.Is(err, os.ErrNotExist) {
		e.bodyFresh = true
		e.bodyNote = "Saving creates this file."
	} else if err != nil {
		e.bodyNote = "Cannot read the file: " + err.Error()
		return
	} else if info.Size() > 8<<20 {
		e.bodyNote = "Larger than 8 MiB; edit it outside vscroxy."
		return
	} else if data, err = os.ReadFile(target); err != nil {
		e.bodyNote = "Cannot read the file: " + err.Error()
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
	e.body = &editor.State{Buffer: buf, Label: "Response body", Placeholder: `{"ok": true}`}
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
	} else {
		if r.Type != capture.RuleMapRemote {
			r.Target = ""
		}
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

func (a *app) switchRule(i int, on bool) {
	next := slices.Clone(a.rules)
	next[i].Disabled = !on
	if a.applyRuleChanges(next) && a.ruleUI.index == i {
		a.ruleUI.draft.Disabled = !on
	}
}

func (a *app) browseRuleFile(folder bool) {
	title := "Choose local response file"
	if folder {
		title = "Choose a folder to answer from"
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Directory: folder})
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
	req, testing := ruleTest(e.test)
	if testing {
		preview = capture.PreviewRules(a.rules, req, a.breakOn)
	}
	ui.Column(c).Width(300).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Rules").FontSize(uikit.TextTitle).FontWeight(600).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, "New rule", "New rule", plusIcon).Clicked() {
				a.editRule(-1, capture.Rule{Type: capture.RuleMapRemote})
			}
		})
		uikit.Input(c, &e.test).Label("Test a request").Placeholder("Test: GET https://api.example.com @ App; Header: value").Tooltip("[METHOD] URL [@ App]; Header: value; …").Font(uikit.FontMono).FillWidth()
		switch {
		case strings.TrimSpace(e.test) == "":
			uikit.Meta(c, "Rules are tried from the top.")
		case !testing:
			uikit.Meta(c, "Enter a URL, then @ App and ; Header: value if needed.")
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
			icon, kind := ruleIcons[max(slices.Index(ruleTypes, r.Type), 0)], ruleLabel(r.Type)
			switch r.Block {
			case capture.BlockHide:
				icon, kind = hideIcon, "Block · hide"
			case capture.BlockDrop:
				kind = "Block · drop connection"
			}
			uikit.Glyph(c, icon).Tooltip(kind)
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
					uikit.Meta(c, ruleSummary(r)).Font(uikit.FontMono).Grow(1).SingleLine()
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
	// Each rule's form starts at its top, not where the last one was scrolled.
	ui.Scroll(c.Key(fmt.Sprint("rule-form", e.index))).Grow(1).MinHeight(0).Children(func() {
		ui.Form(c, func() {
			ui.Field(c, "Type", func() {
				uikit.Select(c, &e.kind, ruleLabels).Label("Rule type").Width(200)
			})
			ui.Field(c, "Method", func() {
				uikit.Input(c, &e.draft.Method).Width(95).Label("Match method").Placeholder("Any").Font(uikit.FontMono)
			})
			ui.Field(c, "URL", func() {
				ui.Row(c).Gap(t.Space(2)).FillWidth().Children(func() {
					modes := []string{rules.URLContains, rules.URLWildcard, rules.URLRegex}
					mode := max(slices.Index(modes, e.draft.MatchMode), 0)
					uikit.Segmented(c, &mode, "Contains", "Wildcard", "Regex").Label("URL match")
					e.draft.MatchMode = modes[mode]
					uikit.Input(c, &e.draft.Match).Label("Match URL").Placeholder(urlPlaceholders[mode]).Font(uikit.FontMono).Grow(1)
				})
			}).Description(urlHelp[max(slices.Index([]string{rules.URLContains, rules.URLWildcard, rules.URLRegex}, e.draft.MatchMode), 0)])
			ui.Field(c, "Apps", func() {
				uikit.Input(c, &e.apps).Label("Match apps").Placeholder("Any").FillWidth()
			}).Description("Comma-separated app names; * matches any text, as in Google Chrome*.")
			ui.Field(c, "Headers", func() { a.ruleHeaderConditions(c, t) })
			switch e.kind {
			case "Map Local":
				a.loadRuleBody()
				ui.Field(c, "Response from", func() {
					ui.Row(c).Gap(t.Space(2)).FillWidth().Children(func() {
						uikit.Input(c, &e.draft.Target).Label("Response file").Placeholder("A file or a folder").Font(uikit.FontMono).Grow(1)
						if uikit.SecondaryButton(c, "File…").Clicked() {
							a.browseRuleFile(false)
						}
						if uikit.SecondaryButton(c, "Folder…").Clicked() {
							a.browseRuleFile(true)
						}
					})
				}).Description("A file up to 8 MiB, or a folder that answers with the file at the request’s path; a missing file is left to the server.")
				if !e.bodyDir {
					body := ui.Field(c, "Body", func() {
						if e.body == nil {
							uikit.Hint(c, e.bodyNote)
							return
						}
						e.body.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
						e.body.Theme = uikit.EditorTheme(t)
						ui.Box(c).Height(180).FillWidth().Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
							editor.View(c, e.body).Grow(1).FillWidth().FocusRing(true)
						})
					})
					if e.body != nil {
						body.Description(cmp.Or(e.bodyNote, "Save rule writes it to the file."))
					}
				}
				if e.bodyDir {
					ui.Field(c, "Strip path", func() {
						uikit.Input(c, &e.draft.PathFrom).Label("Strip path prefix").Placeholder("/api").Font(uikit.FontMono).Width(200)
					}).Description("/api/users/42.json reads users/42.json in the folder; a path ending in / reads index.html.")
				}
				ui.Field(c, "Status", func() {
					uikit.Input(c, &e.status).Label("Response status").Width(95).Font(uikit.FontMono)
				})
				ui.Field(c, "Headers", func() {
					ui.TextArea(c, &e.headers).Height(76).FillWidth().Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Label("Response headers").Placeholder("Content-Type: application/json")
				}).Description("Connection headers are removed; the length is recalculated.")
			case "Map Remote":
				ui.Field(c, "Send to", func() {
					uikit.Input(c, &e.draft.Target).Label("Target base URL").Placeholder("http://127.0.0.1:8080").Font(uikit.FontMono).FillWidth()
				}).Description("Scheme, host and port. A path here goes before the request’s path; a query replaces its query. WebSockets follow, as ws or wss.")
				ui.Field(c, "Rewrite path", func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
						uikit.Input(c, &e.draft.PathFrom).Label("Rewrite path from").Placeholder("/v1/").Font(uikit.FontMono).Grow(1)
						uikit.Meta(c, "→")
						uikit.Input(c, &e.draft.PathTo).Label("Rewrite path to").Placeholder("/api/v1/").Font(uikit.FontMono).Grow(1)
					})
				}).Description("A path starting with the first part starts with the second instead.")
				ui.Field(c, "Host header", func() {
					ui.Checkbox(c, &e.draft.KeepHost, "Keep the original Host header")
				}).Description("For a server that serves several sites by name. Otherwise it gets the target’s host.")
			case "Block":
				ui.Field(c, "Action", func() {
					ui.Row(c).Children(func() {
						choice := max(slices.Index(capture.BlockActions, e.draft.Block), 0)
						uikit.Segmented(c, &choice, "Refuse", "Drop connection", "Hide").Label("Block action")
						e.draft.Block = capture.BlockActions[choice]
					})
				}).Description(blockHelp[max(slices.Index(capture.BlockActions, e.draft.Block), 0)])
			default:
				ui.Field(c, "Pauses", func() { uikit.Body(c, "While Breakpoints is on") }).
					Description("Continue or abort in the breakpoint dialog.")
			}
			if !breakpoint {
				ui.Field(c, "Scenario", func() {
					uikit.Input(c, &e.draft.Scenario).Label("Rule scenario").Placeholder("None").Width(200)
				}).Description("A rule in a scenario applies only while that scenario is active.")
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

var urlPlaceholders = []string{"api.example.com/v1/", "*.example.com/v1/users/*", `^https://api\.example\.com/v1/users/\d+$`}
var urlHelp = []string{
	"A literal part of the full URL.",
	"The whole URL: * is any text, ? one character; the scheme may be left out.",
	"A Go regular expression found anywhere in the full URL.",
}

// ruleSummary is what a rule matches, in one line.
func ruleSummary(r capture.Rule) string {
	match := r.Match
	if r.MatchMode == rules.URLRegex {
		match = "/" + match + "/"
	}
	parts := []string{r.Method, match}
	if len(r.Apps) > 0 {
		parts = append(parts, "@"+strings.Join(r.Apps, ","))
	}
	if n := len(r.When); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d %s", n, plural(n, "header", "headers")))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ruleHeaderConditions edits the request headers the draft needs.
func (a *app) ruleHeaderConditions(c *ui.Context, t *ui.Theme) {
	e := &a.ruleUI
	ui.Column(c).Gap(t.Space(2)).FillWidth().Children(func() {
		remove := -1
		for i := range e.draft.When {
			h := &e.draft.When[i]
			ui.Row(c.Key(fmt.Sprint("when", i))).Gap(t.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
				uikit.Input(c, &h.Name).Label(fmt.Sprintf("Header %d name", i+1)).Placeholder("Name").Font(uikit.FontMono).Width(150)
				op := cmp.Or(h.Op, rules.HeaderEquals)
				uikit.Select(c, &op, rules.HeaderOps).Label(fmt.Sprintf("Header %d comparison", i+1)).Width(100)
				h.Op = op
				if op != rules.HeaderPresent {
					uikit.Input(c, &h.Value).Label(fmt.Sprintf("Header %d value", i+1)).Placeholder("Value").Font(uikit.FontMono).Grow(1)
				} else {
					ui.Spacer(c)
				}
				if uikit.IconButton(c, fmt.Sprintf("Remove header %d", i+1), "Remove condition", closeIcon).Clicked() {
					remove = i
				}
			})
		}
		if remove >= 0 {
			e.draft.When = slices.Delete(e.draft.When, remove, remove+1)
		}
		ui.Row(c).Children(func() {
			if uikit.CompactButton(c, "Add header condition").Clicked() {
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
