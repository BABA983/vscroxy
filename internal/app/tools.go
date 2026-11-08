package app

// Tools is the window for what changes traffic and is switched often: rules,
// scenarios, rule scripts, Host profiles and mocks. It shares the main
// window's state, as Settings does.

import (
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"

	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// openTools shows the Tools window on page ("rules", "scenarios", "scripts",
// "hosts" or "mocks"), focused if
// already open. Without a main window (headless tests) it is a dialog instead.
func (a *app) openTools(page string) {
	a.profiles.menuOpen = false
	a.toolsOpen, a.toolsPage = true, page
	if a.win == nil {
		return
	}
	if a.toolsWin != nil {
		a.toolsWin.Invalidate()
		a.toolsWin.Focus()
		return
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:           locales.Tools(),
		Width:           1080,
		Height:          680,
		MinWidth:        760,
		MinHeight:       460,
		StateKey:        "tools",
		AutoHideMenuBar: true,
		Content:         ui.View(a.toolsView),
	})
	a.toolsWin, a.toolsFocused = w, true
	w.OnFocus(func() { w.Update(func() { a.toolsFocused = true }) })
	w.OnBlur(func() { w.Update(func() { a.toolsFocused = false }) })
	w.OnClosed(func() {
		a.win.Update(func() {
			if a.toolsWin == w {
				a.toolsWin, a.toolsFocused = nil, false
				a.closeTools()
			}
		})
	})
}

// closeTools forgets what Tools had not saved: a new rule's response file.
func (a *app) closeTools() {
	a.toolsOpen = false
	a.discardRuleDraft()
}

func (a *app) invalidateTools() {
	if w := a.toolsWin; w != nil {
		w.Invalidate()
	}
}

// toolsView is the Tools window, on the main window's state, which it
// redraws as it changes.
func (a *app) toolsView(c *ui.Context) {
	dark := a.appearance == "dark" || a.appearance == "" && c.Theme().Dark
	c.SetTheme(uikit.Theme(dark))
	c.SetMenuPresentation(ui.PopoverMenus)
	t := c.Theme()
	a.drainHosts()
	if a.toolsMenuAction == "request.save" {
		switch a.toolsPage {
		case "rules", "mocks":
			if a.ruleUI.open {
				a.saveRule()
			}
		case "scripts":
			if name, ok := a.scriptToSave(); ok {
				a.saveScript(name)
			}
		case "hosts":
			if i, ok := a.profileToSave(); ok {
				a.saveProfile(i)
			}
		}
	}
	a.toolsMenuAction = ""
	if a.toolsFocused {
		a.showNotices(c)
	}
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.toolsContent(c, t, a.toolsFocused, true)
	})
	uikit.Toasts(c)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// toolsModal is Tools without a main window to open its own beside, as in
// headless tests: the same content, in a dialog.
func (a *app) toolsModal(c *ui.Context, t *ui.Theme) {
	if a.win != nil || !a.toolsOpen {
		return
	}
	width, height := c.Size()
	if uikit.Modal(c, &a.toolsOpen, func() {
		ui.Column(c).Width(min(1040, width-80)).Height(min(640, height-80)).Gap(t.Space(3)).Children(func() {
			uikit.DialogTitle(c, locales.Tools())
			a.toolsContent(c, t, true, false)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Done()).Clicked() {
					a.closeTools()
				}
			})
		})
	}).Dismissed() {
		a.closeTools()
	}
}

// toolsContent is the categories on the left and the chosen one beside.
// active draws the chosen category as in a window in front.
func (a *app) toolsContent(c *ui.Context, t *ui.Theme, active, window bool) {
	pal := uikit.Colors(t)
	ui.Row(c).Grow(1).FillWidth().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		top := t.Space(2)
		if window {
			top = max(c.TitleBar().Height+t.Space(1), t.Space(4))
		}
		ui.Sidebar(c, &a.toolsPage, func() {
			for _, page := range []struct {
				id    string
				icon  *ui.SVG
				label string
			}{{"rules", rulesIcon, locales.Rules()}, {"scenarios", layersIcon, locales.Scenarios()}, {"scripts", scriptsIcon, locales.Scripts()},
				{"hosts", hostsIcon, locales.HostProfiles()}, {"mocks", mocksIcon, locales.Mocks()}} {
				uikit.SidebarItem(c, page.id, page.icon, page.label, a.toolsPage == page.id, active).Label(locales.ToolsCategory(page.label))
			}
		}).Width(180).Padding(top, t.Space(2), t.Space(2), t.Space(2)).Gap(t.Space(0.5)).Background(pal.Panel)
		ui.Divider(c)
		switch a.toolsPage {
		case "rules", "mocks":
			a.rulesPage(c, t, top, active)
		case "scenarios":
			a.scenariosPage(c, t, top, active)
		case "scripts":
			a.scriptsPage(c, t, top, active)
		default:
			a.hostProfilesPage(c, t, window)
		}
	})
}

var mocksIcon = lucide.Server()

// hostProfilesPage is the profiles in priority order beside the chosen one's
// hosts file.
func (a *app) hostProfilesPage(c *ui.Context, t *ui.Theme, window bool) {
	h := &a.profiles
	top := t.Space(2)
	if window {
		top = max(c.TitleBar().Height+t.Space(1), t.Space(4))
	}
	ui.Column(c).Width(268).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.HostProfiles()).FontSize(uikit.TextTitle).FontWeight(600).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, locales.NewProfile(), locales.NewProfile(), plusIcon).Clicked() {
				if a.newProfile("Untitled", "", "") {
					h.nameFor = "" // the name field takes the new one
				}
			}
		})
		ui.Row(c).Children(func() { a.hostsMode(c) })
		caption := "Higher profiles win when two map a host."
		if h.set.Exclusive {
			caption = "Turning one on turns the others off."
		}
		uikit.Meta(c, caption)
		if h.problem != nil {
			ui.Text(c, locales.ReadOnlyReason(h.problem.Error())).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3)
		}
		a.profileList(c, t)
		ui.Row(c).Gap(t.Space(1)).Wrap().Children(func() {
			if uikit.CompactButton(c, locales.ImportSystemHosts()).Tooltip(locales.CopySystemHosts(hosts.SystemFile())).Clicked() {
				a.importSystemHosts()
			}
			if uikit.CompactButton(c, locales.SubscribeToUrl()).Tooltip(locales.AProfileFetchedFromA()).Clicked() {
				a.newProfile("Subscription", "", "https://")
				h.nameFor, h.urlFor = "", ""
			}
		})
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(3)).Children(func() {
		i, ok := h.chosen()
		if !ok {
			uikit.EmptyState(c, locales.NoHostProfiles(), locales.PointHostsAtOtherMachines(), func() {
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					if uikit.PrimaryButton(c, locales.NewProfile()).Clicked() {
						a.newProfile("Default", "# address host…, or host = address; add @ App to limit a line to an app\n", "")
					}
					if uikit.SecondaryButton(c, locales.ImportSystemHosts()).Clicked() {
						a.importSystemHosts()
					}
				})
			})
			return
		}
		a.profileEditor(c, t, i)
	})
}

// profileList is the profiles with their switches; dragging a row reorders
// them, which decides which wins.
func (a *app) profileList(c *ui.Context, t *ui.Theme) {
	h := &a.profiles
	conflicts := a.profileConflicts()
	h.list.Selected = &h.choice
	h.list.Key = func(i int) any { return h.set.Profiles[i].Name }
	h.list.Label = func(i int) string { return h.set.Profiles[i].Name }
	h.list.Reorder = a.moveProfiles
	// A pick list: the rows draw the rounded selection themselves, as in a
	// source list, so the list's own fill is clear.
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	c.SetTheme(&pick)
	list := ui.List(c, &h.list, len(h.set.Profiles)).Label(locales.HostProfiles()).Grow(1).MinHeight(0).Gap(t.Space(0.5))
	list.Rows(func(row ui.ListRow) {
		c := row.Context
		c.SetTheme(t)
		p := h.set.Profiles[row.Index]
		r := ui.Row(c).Height(uikit.HeightLarge+4).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
		if row.Selected() {
			uikit.Selected(c, r, true)
		}
		r.Children(func() {
			on := p.On
			if ui.Switch(c, &on).Label(locales.HostProfileNamed(p.Name)).Changed() {
				a.switchProfile(row.Index, on)
			}
			if p.URL != "" {
				uikit.Glyph(c, subscribeIcon).Tooltip(locales.SubscribedUrl(p.URL))
			}
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, p.Name).FontSize(uikit.TextBody).TextColor(t.Text).SingleLine()
				if p.URL != "" {
					uikit.Meta(c, profileFreshness(p, h.fetching[p.Name])).SingleLine()
				}
			})
			if n := conflicts[p.Name]; n > 0 && p.On {
				ui.Box(c).Size(7, 7).Radius(uikit.RadiusControl).Background(t.Warning).
					Label(fmt.Sprintf("%d %s", n, plural(n, "conflict", "conflicts"))).Tooltip(locales.AHigherProfileMapsThe())
			}
			uikit.Meta(c, fmt.Sprint(profileHosts(p))).Font(uikit.FontMono).FontFeatures("tnum").Tooltip(locales.HostsMapped())
		})
	})
	c.SetTheme(t)
}

// profileFreshness says when a subscription was last fetched.
func profileFreshness(p hosts.Profile, fetching bool) string {
	switch {
	case fetching:
		return "Updating…"
	case p.Updated.IsZero():
		return "Not fetched yet"
	}
	return "Updated " + sinceLabel(time.Since(p.Updated))
}

func sinceLabel(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

// profileEditor is the chosen profile: its name, switch and menu, the hosts
// file with its problems and conflicts, and Save.
func (a *app) profileEditor(c *ui.Context, t *ui.Theme, i int) {
	h := &a.profiles
	p := h.set.Profiles[i]
	if h.nameFor != p.Name {
		h.name, h.nameFor = p.Name, p.Name
	}
	if h.urlFor != p.Name {
		h.url, h.urlFor = p.URL, p.Name
	}
	st := h.editors[p.Name]
	if st == nil {
		buf := editor.NewBuffer(p.Text)
		buf.SetLanguage(hostsLanguage)
		st = &editor.State{Buffer: buf, NoScrollPastEnd: true, Label: locales.HostsOf(p.Name),
			Placeholder: "10.0.3.21  api.example.com auth.example.com"}
		if h.editors == nil {
			h.editors = map[string]*editor.State{}
		}
		h.editors[p.Name] = st
	}
	st.ReadOnly = p.URL != ""
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		name := uikit.Input(c, &h.name).Label(locales.ProfileName()).Grow(1).MaxWidth(320).FontSize(uikit.TextTitle).Height(uikit.HeightLarge)
		if name.Submitted() {
			a.renameProfile(i, strings.TrimSpace(h.name))
		}
		on := p.On
		if ui.Switch(c, &on).Label(locales.ProfileOn()).Changed() {
			a.switchProfile(i, on)
		}
		ui.Spacer(c)
		a.hostTest(c, t)
		uikit.MoreMenu(c, locales.ProfileActions(), func(m *ui.Menu) {
			actions := []action{
				do("hosts.rename", locales.Rename(), func() { a.renameProfile(i, strings.TrimSpace(h.name)) }).
					when(strings.TrimSpace(h.name) != p.Name, "Type the new name first").in("edit"),
				do("hosts.duplicate", locales.Duplicate(), func() { a.duplicateProfile(i) }).in("edit"),
				do("hosts.refresh", locales.RefreshNow(), func() { a.refreshProfile(i) }).
					when(p.URL != "" && !h.fetching[p.Name], "Only subscribed profiles refresh").in("edit"),
				do("hosts.delete", locales.Delete(), func() { a.deleteProfile(i) }).in("danger"),
			}
			actionMenu(m, actions)
		})
	})
	if p.URL != "" {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Glyph(c, subscribeIcon)
			in := uikit.Input(c, &h.url).Label(locales.SubscriptionUrl()).Placeholder("https://example.com/hosts").Font(uikit.FontMono).Grow(1)
			refresh := uikit.SecondaryButton(c, locales.Refresh()).Disabled(h.fetching[p.Name])
			if in.Submitted() || refresh.Clicked() {
				url := strings.TrimSpace(h.url)
				if url != p.URL && a.changeHosts(func(s *hosts.Set) { s.Profiles[i].URL, s.Profiles[i].Updated = url, time.Time{} }) {
					p.URL = url
				}
				a.refreshProfile(i)
			}
			uikit.Meta(c, profileFreshness(p, h.fetching[p.Name])).SingleLine()
		})
	}
	a.profileDiagnostics(st, i)
	theme := uikit.EditorTheme(t)
	pal := uikit.Colors(t)
	theme.Tokens[editor.TokenNumber] = editor.TokenStyle{Color: pal.Info}
	theme.Tokens[editor.TokenKeyword] = editor.TokenStyle{Color: pal.MethodPatch}
	st.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	st.Theme = theme
	ui.Box(c).Grow(1).MinHeight(0).FillWidth().Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		editor.View(c, st).Grow(1).FillWidth().FocusRing(true)
	})
	_, modified := a.profileToSave()
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		note := "Changes apply to new connections."
		if p.URL != "" {
			note = "A subscription is read-only; it refreshes every hour."
		}
		uikit.Meta(c, note).Grow(1).SingleLine()
		ui.Row(c).Disabled(a.srv == nil).Children(func() {
			if uikit.SecondaryButton(c, locales.DropOpenConnections()).Tooltip(locales.CloseTheClientsOpenConnections()).Clicked() {
				a.dropConnections()
			}
		})
		if p.URL == "" {
			ui.Row(c).Disabled(!modified).Children(func() {
				save := uikit.PrimaryButton(c, locales.Save()).Tooltip(locales.SaveCtrlCmdS())
				save.OnClick(func() { a.saveProfile(i) })
			})
		}
	})
	if modified {
		// In the Tools window the menu's Save takes the key first.
		c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveProfile(i) })
	}
}

// profileToSave is the profile Save writes: the chosen one, when it is local
// and its draft has changed.
func (a *app) profileToSave() (int, bool) {
	h := &a.profiles
	i, ok := h.chosen()
	if !ok {
		return 0, false
	}
	p := h.set.Profiles[i]
	st := h.editors[p.Name]
	return i, p.URL == "" && st != nil && st.Buffer.String() != p.Text
}

// hostTest asks the profiles that are on where a host goes.
func (a *app) hostTest(c *ui.Context, t *ui.Theme) {
	h := &a.profiles
	ui.Column(c).Width(240).Gap(t.Space(0.5)).Children(func() {
		uikit.Input(c, &h.test).Label(locales.TestAHost()).Placeholder(locales.TestAHostApiExample()).Font(uikit.FontMono).FillWidth()
		host := strings.TrimSpace(h.test)
		if host == "" {
			return
		}
		if r, ok := hosts.Lookup(h.set.Rules(), host, ""); ok {
			uikit.Meta(c, locales.MappedRule(r.IP.String(), r.Profile, r.Line)).Font(uikit.FontMono).SingleLine()
			return
		}
		uikit.Meta(c, locales.NotMappedResolvedAsUsual()).SingleLine()
	})
}

// profileDiagnostics underlines the lines that map nothing and marks those
// another profile that is on hides or is hidden by. They are worked out
// again only when the draft or the profiles change.
func (a *app) profileDiagnostics(st *editor.State, i int) {
	h := &a.profiles
	p := h.set.Profiles[i]
	version := st.Buffer.Version()
	if h.diagFor.name == p.Name && h.diagFor.version == version && h.diagFor.rev == h.rev {
		return
	}
	h.diagFor.name, h.diagFor.version, h.diagFor.rev = p.Name, version, h.rev
	text := st.Buffer.String()
	lineRange := func(n int) (int, int) {
		start := st.Buffer.Offset(editor.Pos{Line: n - 1})
		return start, start + len(st.Buffer.Line(n-1))
	}
	var ds []editor.Diagnostic
	var inlays []editor.Inlay
	said := map[int]bool{} // one note at the end of a line
	note := func(line, end int, message string) {
		if !said[line] {
			said[line] = true
			inlays = append(inlays, editor.Inlay{Offset: end, Text: "   " + message})
		}
	}
	_, problems := hosts.Parse(text)
	for _, problem := range problems {
		start, end := lineRange(problem.Line)
		ds = append(ds, editor.Diagnostic{Start: start, End: end, Severity: editor.SeverityError, Message: problem.Message})
		note(problem.Line, end, problem.Message)
	}
	if p.On {
		// The draft as it would apply, against the others as saved.
		set := h.set
		set.Profiles = append([]hosts.Profile(nil), h.set.Profiles...)
		set.Profiles[i].Text = text
		for _, conflict := range set.Conflicts() {
			var mine, other hosts.Rule
			var message string
			switch p.Name {
			case conflict.Loser.Profile:
				mine, other = conflict.Loser, conflict.Winner
				message = fmt.Sprintf("%s wins (%s)", where(other, p.Name), other.IP)
			case conflict.Winner.Profile:
				mine, other = conflict.Winner, conflict.Loser
				message = fmt.Sprintf("overrides %s (%s)", where(other, p.Name), other.IP)
			default:
				continue
			}
			start, end := lineRange(mine.Line)
			ds = append(ds, editor.Diagnostic{Start: start, End: end, Severity: editor.SeverityWarning, Message: message})
			note(mine.Line, end, message)
		}
	}
	st.SetDiagnostics(version, ds)
	st.SetInlays(inlays)
	h.diag = ds
}

// where names a conflicting line: its profile, or its line in this one.
func where(r hosts.Rule, current string) string {
	if r.Profile == current {
		return fmt.Sprintf("line %d", r.Line)
	}
	return fmt.Sprintf("%s line %d", r.Profile, r.Line)
}

// hostsLanguage highlights hosts files: comments, addresses, = and the apps
// after @.
var hostsLanguage = &editor.Language{
	Name:        "Hosts",
	Extensions:  []string{".hosts"},
	LineComment: "#",
	Tokenizer: editor.TokenizerFunc(func(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
		code, _, commented := strings.Cut(line, "#")
		spec, _, scoped := strings.Cut(code, "@")
		at := 0
		emit := func(end int, kind editor.TokenKind) {
			if end > at {
				tokens = append(tokens, editor.Token{End: end, Kind: kind})
				at = end
			}
		}
		for at < len(spec) {
			start := at
			switch r := rune(spec[at]); {
			case r == '=':
				emit(at+1, editor.TokenOperator)
			case unicode.IsSpace(r):
				end := start
				for end < len(spec) && unicode.IsSpace(rune(spec[end])) {
					end++
				}
				emit(end, editor.TokenText)
			default:
				end := start
				for end < len(spec) && spec[end] != '=' && !unicode.IsSpace(rune(spec[end])) {
					end++
				}
				kind := editor.TokenText
				if net.ParseIP(spec[start:end]) != nil {
					kind = editor.TokenNumber
				}
				emit(end, kind)
			}
		}
		if scoped {
			emit(len(code), editor.TokenKeyword)
		}
		if commented {
			emit(len(line), editor.TokenComment)
		}
		return tokens, 0
	}),
}
