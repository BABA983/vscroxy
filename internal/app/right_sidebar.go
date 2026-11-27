package app

import (
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// rightSidebarState is the request workspace's third column: whether it is
// open, its width, the mode chosen in it and the code view's own state.
type rightSidebarState struct {
	open  bool
	width float32
	// tab is Variables, Code or Info; it is remembered while the app runs.
	tab int
	// codeFormat is the snippet language chosen in Code, and codeKeep leaves
	// {{variables}} as written rather than resolving them for display.
	codeFormat string
	codeKeep   bool
	// codeView is rebuilt when the snippet text changes.
	codeView  *editor.State
	codeCopy  string
	copyFlash uikit.CopyFlash
	// varsExpanded opens the All variables groups by scope; a group starts
	// folded, as the mockup shows them.
	varsExpanded map[string]bool
	// split is the room the request pane gets while the sidebar is open. It
	// is derived from width when the sidebar opens and after a drag.
	split      float32
	splitReady bool
	splitWidth float32
}

// rightSidebarShown: the third column belongs to a request tab, as VS Code's
// secondary side bar belongs to its editor.
func (a *app) rightSidebarShown() bool {
	return a.rightSidebar.open && a.page == pageRequest && a.requestDocument != nil
}

// clampRightSidebar keeps the column readable and leaves the request most of
// the window.
func clampRightSidebar(w float32) float32 {
	return min(max(w, 220), 520)
}

// rightSidebarSplit lays the request beside the third column. The split's
// first pane is the request, so its size is derived from the sidebar width;
// a drag updates the width that settings carries.
func (a *app) rightSidebarSplit(c *ui.Context, main func()) {
	s := &a.rightSidebar
	available := a.workspaceWidth(c)
	width := clampRightSidebar(cmp.Or(s.width, 280))
	// The sidebar keeps its width: the request pane takes or gives the room
	// when the window changes. A drag is what moves the sidebar itself.
	if !s.splitReady || s.splitWidth != available {
		s.split, s.splitReady = available-width-1, true
	}
	before := s.split
	uikit.Split(c, &s.split, main, func() { a.rightSidebarView(c) }).Grow(1).FillHeight()
	if s.splitWidth == available && s.split != before {
		s.width = clampRightSidebar(available - s.split - 1)
		s.split = available - s.width - 1
	}
	s.splitWidth = available
}

// rightSidebarView is the column's own surface and its icon tabs.
func (a *app) rightSidebarView(c *ui.Context) {
	t, p := c.Theme(), uikit.Colors(c.Theme())
	s := &a.rightSidebar
	items := []uikit.ModeItem{
		{Icon: lucide.Braces(), Label: locales.Variables()},
		{Icon: lucide.Code(), Label: locales.Code()},
		{Icon: lucide.Info(), Label: locales.Info()},
	}
	ui.Column(c).FillHeight().Clip().Background(p.Window).BorderWidth(0, 0, 0, 1).BorderColor(p.Border).Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.ModeSwitch(c, &s.tab, items...)
		})
		if s.tab == 1 {
			a.rightSidebarCode(c, t)
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Padding(t.Space(uikit.SpaceS), t.Space(uikit.SpaceM)).Children(func() {
			ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceM)).Children(func() {
				if s.tab == 2 {
					a.rightSidebarInfo(c, t)
				} else {
					a.rightSidebarVariables(c, t)
				}
			})
		})
	})
}

// scopeBadge is a scope's one-letter mark: E environment, C collection,
// F folder, G globals, V vault, D dynamic.
func scopeBadge(c *ui.Context, letter, label string) {
	t, p := c.Theme(), uikit.Colors(c.Theme())
	box := ui.Box(c).Size(14, 14).Radius(3).Background(p.Muted).Shrink(0).Center()
	box.Children(func() {
		uikit.Meta(c, letter).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.TextMuted)
	})
	uikit.Tip(c, box, label)
}

// scopeOfSource maps a resolved variable's source onto the badge and the
// scope's own name. The environment's values come back as Run input from
// request.ResolveVariables, as the request panel already renames them.
func (a *app) scopeOfSource(source string) (letter, label string) {
	switch {
	case source == "Run input":
		name := cmp.Or(a.activeEnv, locales.NoEnvironment())
		return "E", locales.KindPath(locales.Environment(), name)
	case strings.HasPrefix(source, "Collection:"):
		path := strings.TrimSpace(strings.TrimPrefix(source, "Collection:"))
		if strings.Contains(path, "/") {
			return "F", locales.KindPath(locales.FolderName(), path)
		}
		return "C", locales.KindPath(locales.Collection(), path)
	case source == "Global":
		return "G", locales.ScopeGlobal()
	case source == "Vault":
		return "V", locales.ScopeVault()
	case source == "Dynamic":
		return "D", locales.ScopeDynamic()
	case source == "Data file", source == "Pre-request script":
		return "D", source
	}
	return "?", source
}

// rightSidebarVariables lists what the request uses and every variable the
// request's scopes offer. A missing name can be added where a send would
// look for it, then its editor opens on that value.
func (a *app) rightSidebarVariables(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil {
		return
	}
	r := d.value()
	in := a.inheritance(r.Collection)
	set := a.variableHintSet(r.Collection)
	rows := r.ResolveVariables(in, a.activeVars(), nil)
	uikit.BodyStrong(c, locales.VariablesInRequest())
	ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceXS)).Children(func() {
		if len(rows) == 0 {
			uikit.Hint(c, locales.NoReferencedVariablesInThis())
			return
		}
		for _, row := range rows {
			letter, label := a.scopeOfSource(row.Source)
			undefined := row.Missing || row.Source == "Missing"
			ui.Row(c).FillWidth().Padding(0, t.Space(uikit.SpaceXS)).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
				if undefined {
					warn := ui.Box(c).Size(14, 14).Shrink(0).Center()
					warn.Children(func() {
						uikit.Meta(c, "!").TextColor(t.Warning)
					})
				} else {
					scopeBadge(c, letter, label)
				}
				uikit.DataMeta(c, row.Name).Shrink(0).MaxWidth(120).SingleLine()
				if undefined {
					uikit.Tip(c, uikit.Meta(c, locales.NotDefinedInAnyScope()).TextColor(t.Warning).SingleLine().Grow(1).MinWidth(0), locales.NotDefinedInAnyScope())
					if uikit.LinkButton(c, locales.Add()).Clicked() {
						a.addVariableFromSidebar(row.Name)
					}
					return
				}
				value, ink := a.hintValueText(c, set, row.Name, row.Value)
				uikit.Tip(c, uikit.DataMeta(c, value).TextColor(ink).SingleLine().Grow(1).MinWidth(0), value)
			})
		}
	})

	uikit.BodyStrong(c, locales.AllVariables())
	groups := a.rightSidebarVariableGroups(set)
	ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceXS)).Children(func() {
		if len(groups) == 0 {
			uikit.Hint(c, locales.NoReferencedVariablesInThis())
			return
		}
		for _, group := range groups {
			open := a.rightSidebar.varsExpanded[group.key]
			arrow := statusMoreIcon
			if open {
				arrow = chevronIcon
			}
			row := ui.Row(c).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceXS)).Gap(t.Space(uikit.SpaceS)).
				Radius(uikit.RadiusControl).AlignItems(ui.Center)
			if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
					ui.Icon(c, arrow).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
				})
				scopeBadge(c, group.letter, group.label)
				uikit.Caption(c, group.label).Grow(1).MinWidth(0).SingleLine()
				uikit.Meta(c, fmt.Sprint(len(group.hints))).FontFeatures("tnum").Shrink(0)
				if group.edit != nil {
					if uikit.LinkButton(c, locales.EditScope()).Clicked() {
						group.edit()
					}
				}
			})
			if row.Clicked() {
				a.toggleVarsGroup(group.key)
			}
			if open {
				ui.Column(c).FillWidth().PaddingStart(t.Space(uikit.SpaceL)).BorderWidth(0, 0, 0, 1).
					BorderColor(t.Border).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					for _, hint := range group.hints {
						ui.Row(c).FillWidth().Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
							uikit.DataMeta(c, hint.Name).Shrink(0).MaxWidth(110).SingleLine()
							value, ink := a.hintValueText(c, set, hint.Name, hint.Value)
							uikit.Tip(c, uikit.DataMeta(c, value).TextColor(ink).SingleLine().Grow(1).MinWidth(0), value)
						})
					}
				})
			}
		}
	})
}

func (a *app) toggleVarsGroup(key string) {
	if a.rightSidebar.varsExpanded == nil {
		a.rightSidebar.varsExpanded = map[string]bool{}
	}
	a.rightSidebar.varsExpanded[key] = !a.rightSidebar.varsExpanded[key]
}

// hintValueText is what a variable's value reads as in the sidebar: masked
// for a credential or the vault, empty as a dash, and otherwise the value.
func (a *app) hintValueText(c *ui.Context, set variableSet, name, fallback string) (string, ui.Color) {
	t := c.Theme()
	hint, ok := set.hint(name)
	if ok {
		if hint.Vault || hint.Sensitive {
			return "••••••", t.TextMuted
		}
		if hint.Value != "" {
			return oneLine(hint.Value), t.TextMuted
		}
	}
	if fallback != "" {
		return oneLine(fallback), t.TextMuted
	}
	return locales.EmptyValue(), t.TextMuted
}

type rightSidebarGroup struct {
	key    string
	letter string
	label  string
	hints  []variableHint
	edit   func()
}

// rightSidebarVariableGroups groups every defined variable by its scope, in
// the completion list's nearest-first order.
func (a *app) rightSidebarVariableGroups(set variableSet) []rightSidebarGroup {
	var groups []rightSidebarGroup
	index := map[string]int{}
	for _, hint := range set.list {
		if !hint.Defined || hint.Dynamic {
			continue
		}
		key := hint.Scope
		at, ok := index[key]
		if !ok {
			letter, label := a.scopeBadgeForScope(hint.Scope)
			group := rightSidebarGroup{key: key, letter: letter, label: label, edit: a.scopeEditor(hint.Scope)}
			groups = append(groups, group)
			at = len(groups) - 1
			index[key] = at
		}
		groups[at].hints = append(groups[at].hints, hint)
	}
	for i := range groups {
		slices.SortFunc(groups[i].hints, func(x, y variableHint) int { return strings.Compare(x.Name, y.Name) })
	}
	return groups
}

func (a *app) scopeBadgeForScope(scope string) (letter, label string) {
	kind, path, _ := strings.Cut(scope, " · ")
	switch kind {
	case "Environment":
		return "E", locales.KindPath(locales.Environment(), path)
	case "Collection":
		return "C", locales.KindPath(locales.Collection(), path)
	case "Folder":
		return "F", locales.KindPath(locales.FolderName(), path)
	case "Global":
		return "G", locales.ScopeGlobal()
	case "Vault":
		return "V", locales.ScopeVault()
	case "Dynamic":
		return "D", locales.ScopeDynamic()
	}
	return "?", scope
}

// scopeEditor opens the editor the scope already has: the environment
// dialog, the folder's settings, the globals or the vault.
func (a *app) scopeEditor(scope string) func() {
	kind, path, _ := strings.Cut(scope, " · ")
	switch kind {
	case "Environment":
		return func() {
			a.editEnvironment(path)
			a.varsOpen = true
		}
	case "Collection", "Folder":
		return func() { a.openFolder(path) }
	case "Global":
		return func() {
			a.editGlobals()
			a.varsOpen = true
		}
	case "Vault":
		return func() { a.openVault() }
	}
	return nil
}

// addVariableFromSidebar adds an undefined name where a send would look:
// the active environment, otherwise the request's collection, otherwise
// globals. It reuses the quick fix's writers and opens that editor.
func (a *app) addVariableFromSidebar(name string) {
	d := a.requestDocument
	if d == nil || name == "" {
		return
	}
	if a.envsProblem != "" {
		a.warn(locales.VariablesNotSaved(a.envsProblem))
		return
	}
	collection := d.composeCollection
	if a.activeEnv != "" {
		if err := a.addQuickFixEnvVariable(a.activeEnv, name); err != nil {
			a.fail(locales.Variables(), err)
			return
		}
		a.editEnvironment(a.activeEnv)
		a.varsOpen, a.varsFocus = true, name
		return
	}
	root := collectionRoot(collection)
	if root != "" {
		if err := a.addQuickFixFolderVariable(root, name); err != nil {
			a.fail(locales.Variables(), err)
			return
		}
		a.openFolder(root)
		a.folderUI.tab, a.folderUI.focus = 2, name
		return
	}
	globals := maps.Clone(a.globals)
	if globals == nil {
		globals = map[string]string{}
	}
	globals[name] = ""
	f := a.localEnvs()
	f.Globals = globals
	if err := a.saveEnvFile(f); err != nil {
		a.fail(locales.Variables(), err)
		return
	}
	a.globals = globals
	a.editGlobals()
	a.varsOpen, a.varsFocus = true, name
}

// rightSidebarCode is the generated snippet: its format, a copy action and
// the request rendered in the editor's syntax colors.
func (a *app) rightSidebarCode(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil {
		return
	}
	s := &a.rightSidebar
	if s.codeFormat == "" {
		s.codeFormat = requestCopyFormats[0].format
	}
	short := strings.TrimPrefix(formatLabel(s.codeFormat), "Copy as ")
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(t.Space(uikit.SpaceS), t.Space(uikit.SpaceM)).
		Gap(t.Space(uikit.SpaceS)).Children(func() {
		ui.Row(c).FillWidth().Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
			uikit.MenuSelect(c, short, func(m *ui.Menu) {
				for _, format := range requestCopyFormats {
					item := m.Item(formatLabel(format.format))
					if format.format == s.codeFormat {
						item.Checked(true)
					}
					if item.Chosen() {
						s.codeFormat, s.codeView = format.format, nil
					}
				}
			}).Width(180).Grow(0)
			ui.Spacer(c)
			copy := uikit.ToolbarButton(c, locales.Copy(), locales.Copy(), false).
				Size(uikit.HeightControl, uikit.HeightControl).Padding(0).Justify(ui.Center)
			copy.Children(func() {
				if s.copyFlash.Showing(c) {
					ui.Icon(c, lucide.Check()).Size(uikit.IconInline, uikit.IconInline).TextColor(uikit.Colors(t).Success)
					return
				}
				ui.Icon(c, lucide.Copy()).Size(uikit.IconInline, uikit.IconInline)
			})
			if copy.Clicked() {
				if s.codeCopy != "" {
					a.copyText(c, s.codeFormat, func() (string, error) { return s.codeCopy, nil })
					s.copyFlash.Copied(c)
				}
			}
		})

		display, copyText, problem := a.rightSidebarSnippet(d)
		s.codeCopy = copyText
		if problem != "" {
			uikit.ErrorText(c, problem)
			return
		}
		if s.codeView == nil || s.codeView.Label != locales.CodeNamed(s.codeFormat) || s.codeView.Buffer.String() != display {
			buf := editor.NewBuffer(display)
			if language := codeLanguage(s.codeFormat); language != "" {
				buf.SetLanguage(editor.LanguageNamed(language))
			}
			s.codeView = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: false,
				LineNumbers: editor.LineNumbersOff, NoFolding: true, NoActiveLine: true, NoIndentGuides: true, Label: locales.CodeNamed(s.codeFormat)}
		}
		requestCodeEditor(c, s.codeView).Grow(1).MinHeight(0)
		ui.Row(c).FillWidth().Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			if uikit.Switch(c, &s.codeKeep).Label(locales.KeepVariables()).Changed() {
				s.codeView = nil
			}
			uikit.Small(c, locales.KeepVariables()).TextColor(t.Text).Grow(1).MinWidth(0)
		})
		caption := locales.CodeKeepCaption()
		if !s.codeKeep {
			caption = locales.CodeCopyResolvedCaption(cmp.Or(a.activeEnv, locales.NoEnvironment()))
		}
		uikit.Hint(c, caption)
	})
}

func formatLabel(format string) string {
	for _, candidate := range requestCopyFormats {
		if candidate.format == format {
			return candidate.label
		}
	}
	return format
}

// rightSidebarSnippet renders the current format for the panel. Curl is
// written the way Postman writes it: one argument per line, without the
// exact-replay flags or header suppression the Copy as path uses. Other
// languages use their own snippet writers.
func (a *app) rightSidebarSnippet(d *requestDocument) (display, copyText, problem string) {
	s := &a.rightSidebar
	if s.codeKeep {
		req, err := a.draftPrepared(d)
		if err != nil {
			return "", "", err.Error()
		}
		text, err := sidebarSnippetText(req, s.codeFormat)
		if err != nil {
			return "", "", err.Error()
		}
		return text, text, ""
	}
	vars := a.inheritance(d.composeCollection).Values(a.activeVars())
	req, err := a.draftPreparedWith(d, vars)
	if err != nil {
		return "", "", err.Error()
	}
	text, err := sidebarSnippetText(req, s.codeFormat)
	if err != nil {
		return "", "", err.Error()
	}
	masked := a.maskPreparedSecrets(req)
	shown, err := sidebarSnippetText(masked, s.codeFormat)
	if err != nil {
		return "", "", err.Error()
	}
	return shown, text, ""
}

// sidebarSnippetText writes the panel's copy of a request. A curl command is
// clean and readable rather than an exact replay: empty headers are left out,
// the hop-by-hop headers are removed, and each argument has its own line.
func sidebarSnippetText(req *request.Prepared, format string) (string, error) {
	clean := cleanSidebarPrepared(req)
	switch format {
	case "cURL":
		return sidebarCurl(clean, false), nil
	case "cURL (CMD)":
		return sidebarCurl(clean, true), nil
	}
	return request.Snippet(format, clean)
}

// cleanSidebarPrepared drops the header entries an authored request carries
// with no value. An empty value is curl's suppression syntax, not something a
// person writes in a snippet.
func cleanSidebarPrepared(req *request.Prepared) *request.Prepared {
	out := *req
	out.Header = http.Header{}
	for name, values := range req.Header {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				out.Header.Add(name, value)
			}
		}
	}
	return &out
}

// sidebarCurl writes curl --location with one argument per line. It is the
// authored snippet, not a capture replay: no -q/--path-as-is/--globoff, and no
// empty -H suppressions.
func sidebarCurl(req *request.Prepared, cmd bool) string {
	method := strings.ToUpper(strings.TrimSpace(cmp.Or(req.Method, "GET")))
	quote, binary, cont := shellQuote, "curl", " \\"
	if cmd {
		quote, binary, cont = cmdQuote, "curl.exe", " ^"
	}
	lines := []string{binary + " --location " + quote(req.URL)}
	if method != "GET" || req.Body != "" || req.BodyFile != "" {
		lines = append(lines, "--request "+quote(method))
	}
	for _, name := range slices.Sorted(maps.Keys(req.Header)) {
		if curlSkip[strings.ToLower(name)] {
			continue
		}
		for _, value := range req.Header[name] {
			if strings.TrimSpace(value) == "" {
				continue
			}
			lines = append(lines, "--header "+quote(name+": "+value))
		}
	}
	if req.Body != "" {
		lines = append(lines, "--data "+quote(req.Body))
	} else if req.BodyFile != "" {
		lines = append(lines, "--data-binary "+quote("@"+req.BodyFile))
	}
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString(line)
		} else {
			b.WriteString("\n  ")
			b.WriteString(line)
		}
		if i < len(lines)-1 {
			b.WriteString(cont)
		}
	}
	return b.String()
}

// draftPreparedWith is draftPrepared with a variable map: nil keeps the
// references as written, as Copy as does, and a map resolves them.
func (a *app) draftPreparedWith(d *requestDocument, vars map[string]string) (*request.Prepared, error) {
	r := d.draft()
	r.Auth, _ = a.inheritance(r.Collection).InheritAuth(r.Auth)
	if r.Auth.Type == request.AuthOAuth2 {
		if in, cfg, err := a.oauthConfig(r.Auth, r.Collection); err == nil {
			tok, _ := in.Tokens.Peek(cfg)
			r.Auth.Token, r.Auth.OAuth2.HeaderPrefix = tok.Access, cfg.HeaderPrefix
		}
	}
	return r.Prepare(vars)
}

// maskPreparedSecrets replaces the values a credential or the vault supplies
// in a prepared request. Display only; the copy path keeps the real bytes.
func (a *app) maskPreparedSecrets(req *request.Prepared) *request.Prepared {
	out := *req
	out.URL, out.Body = req.URL, req.Body
	out.Header = req.Header.Clone()
	values := a.secretValues(a.requestDocument)
	for _, value := range values {
		if len([]rune(value)) < 3 {
			continue
		}
		out.URL = strings.ReplaceAll(out.URL, value, "••••••")
		out.Body = strings.ReplaceAll(out.Body, value, "••••••")
		for name, list := range out.Header {
			for i, item := range list {
				out.Header[name][i] = strings.ReplaceAll(item, value, "••••••")
			}
		}
	}
	for name, list := range out.Header {
		if sensitiveVariable(name) {
			for i := range list {
				out.Header[name][i] = "••••••"
			}
		}
	}
	return &out
}

// secretValues are the values the request's scopes mark as credentials, plus
// the vault's loaded secrets. Longest first so a value that contains another
// is not partly replaced.
func (a *app) secretValues(d *requestDocument) []string {
	if d == nil {
		return nil
	}
	set := a.variableHintSet(d.composeCollection)
	var values []string
	for _, hint := range set.list {
		if !hint.Defined || hint.Value == "" {
			continue
		}
		if hint.Vault || hint.Sensitive {
			values = append(values, hint.Value)
		}
	}
	for _, secret := range a.vault.secrets {
		if secret.Value != "" {
			values = append(values, secret.Value)
		}
	}
	slices.SortFunc(values, func(x, y string) int { return len(y) - len(x) })
	return values
}

// rightSidebarInfo is what the app knows about the open request: its place,
// its file, its last send and its identity.
func (a *app) rightSidebarInfo(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil {
		return
	}
	r := d.value()
	uikit.BodyStrong(c, locales.Details())
	ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceS)).Children(func() {
		infoRow(c, locales.Name(), d.title(), false)
		if r.Collection != "" {
			infoRow(c, locales.Collection(), strings.ReplaceAll(r.Collection, "/", " / "), false)
		}
		if d.savedKey != "" {
			file := path.Join(dirOf(r.Collection), escapeName(r.Name)+requestFileSuffix)
			infoRowMiddle(c, locales.File(), file, file, true)
		}
		// The draft's target is what the URL resolves to before scripts can
		// change it; the request panel's line above the editor used to say
		// it, and the sidebar keeps it out of the editor's way.
		target, tip := a.draftTarget(d)
		infoRowMiddle(c, locales.Target(), target, tip, true)
		if d.sentSet {
			sent := d.sentAt.Local().Format("15:04:05")
			if res := d.response; res != nil && res.Status != 0 {
				sent += fmt.Sprintf(" · %d", res.Status)
			}
			if res := d.response; res != nil && res.Millis > 0 {
				sent += " · " + (time.Duration(res.Millis) * time.Millisecond).Round(time.Millisecond).String()
			}
			infoRow(c, locales.LastSent(), sent, false)
		}
		if r.ID != "" {
			ui.Row(c).FillWidth().Height(uikit.HeightRow).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, locales.RequestId()).Width(infoLabelWidth).Shrink(0)
				uikit.Tip(c, uikit.DataMeta(c, r.ID).Grow(1).MinWidth(0).SingleLine(), r.ID)
				copy := uikit.RowIconButton(c, locales.Copy(), locales.Copy(), lucide.Copy())
				if copy.Clicked() {
					c.Services().WriteClipboard(r.ID)
					a.done(locales.CopiedRequestId())
				}
			})
		}
	})
}

// draftTarget is where the draft would go before its scripts run, in the form
// the request's own line used to read: the host only, never its query or
// userinfo, or the unresolved name that stops a target from being known.
func (a *app) draftTarget(d *requestDocument) (string, string) {
	r := d.value()
	in := a.inheritance(r.Collection)
	target := locales.UnresolvedReviewVariables()
	tip := locales.TargetBeforeScripts(target)
	if r.Protocol == "MCP" && r.MCP.Transport == request.MCPTransportStdio {
		target = locales.StdioProcess()
		tip = locales.TargetBeforeScripts(target)
	} else if u, err := url.Parse(request.ExpandVars(d.composeURL, in.Values(a.activeVars()))); err == nil && u.Hostname() != "" {
		target = u.Scheme + "://" + u.Host // Do not reveal query tokens or userinfo.
		tip = locales.TargetBeforeScripts(target)
	}
	if r.Protocol != "" {
		tip = locales.TargetAfterVariables(target)
	}
	return target, tip
}

// infoLabelWidth is the one column every Details label starts in, so the
// values line up down the panel.
const infoLabelWidth float32 = 72

func infoRow(c *ui.Context, label, value string, mono bool) {
	infoRowTip(c, label, value, value, mono)
}

// infoRowTip is infoRow whose tip says more than the value it shows, as a
// target's sentence explains when it is taken. Each row is one fixed-height
// line, its label and value centered on the same baseline.
func infoRowTip(c *ui.Context, label, value, tip string, mono bool) {
	ui.Row(c).FillWidth().Height(uikit.HeightRow).AlignItems(ui.Center).Gap(0).Children(func() {
		uikit.Meta(c, label).Width(infoLabelWidth).Shrink(0)
		box := ui.Row(c).Grow(1).MinWidth(0).Height(uikit.HeightRow).AlignItems(ui.Center).Clip()
		box.Children(func() {
			var element ui.Element
			if mono {
				element = uikit.DataMeta(c, value)
			} else {
				element = uikit.Body(c, value)
			}
			element.SingleLine()
		})
		uikit.Tip(c, box, tip)
	})
}

// infoRowMiddle is infoRowTip for a value that lives at both of its ends, as
// a file path and a URL do: the middle is cut instead of the end, and the tip
// still carries the whole value.
func infoRowMiddle(c *ui.Context, label, value, tip string, mono bool) {
	infoRowTip(c, label, middleEllipsis(value, 31), tip, mono)
}

// middleEllipsis cuts s in the middle to max runes, keeping both the start
// and the end: a path's root and file name, or a URL's scheme and host.
func middleEllipsis(s string, max int) string {
	r := []rune(s)
	if len(r) <= max || max < 3 {
		return s
	}
	keep := max - 1
	head := keep / 2
	tail := keep - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
