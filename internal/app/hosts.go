package app

// Host profiles: named hosts files the proxy resolves itself
// (internal/hosts). The title bar switches them, the Tools window edits
// them, and a request's Remote IP and connection details say which profile
// mapped it. Each profile is a file under ~/.vscroxy/tools/hosts, which a
// person may edit by hand or keep in Git; profiles.json keeps their order,
// switches, subscriptions and mode.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

var (
	hostsIcon     = lucide.Globe()
	subscribeIcon = lucide.Cloud()
)

// hostsState is the profiles, and the title bar's menu and the Tools page
// that show them.
type hostsState struct {
	dir string
	set hosts.Set
	// problem is why the profiles could not be read; nothing is saved over
	// them until they read cleanly.
	problem error
	// rev counts changes to the set, so what is derived from it is
	// computed again only when it changes.
	rev int

	menuOpen bool // the title bar's popover

	choice  int // the profile Tools shows
	list    ui.ListState
	editors map[string]*editor.State // each profile's draft, by name
	name    string                   // the chosen profile's name, as being edited
	nameFor string                   // the profile name holds
	url     string                   // a subscription's URL, as being edited
	urlFor  string
	test    string // Test a host

	diagFor struct {
		name    string
		version uint64
		rev     int
	}
	diag []editor.Diagnostic // what the editor underlines

	fetching map[string]bool
	results  chan hostsFetch
	ticking  bool
}

type hostsFetch struct {
	name, url, text string
	at              time.Time
	err             error
}

func (h *hostsState) path(name string) string { return filepath.Join(h.dir, hosts.FileName(name)) }

// loadHosts reads the profiles and hands those that are on to the proxy. A
// hosts file in the folder that profiles.json does not list yet joins the
// end, off.
func (a *app) loadHosts() {
	h := &a.profiles
	if h.dir == "" {
		return
	}
	h.rev++
	index := filepath.Join(h.dir, hosts.IndexFile)
	data, err := a.file(index).Read()
	set, parseErr := hosts.UnmarshalIndex(data)
	if err == nil && parseErr != nil {
		err = parseErr
		a.file(index).Fail(err)
	}
	if err != nil {
		h.problem = err
		a.fail(index+" could not be read and is left untouched", err)
		return
	}
	for i := range set.Profiles {
		text, err := a.file(h.path(set.Profiles[i].Name)).Read()
		if err != nil {
			a.fail(locales.HostProfileUnreadable(set.Profiles[i].Name), err)
		}
		set.Profiles[i].Text = string(text)
	}
	files, _ := filepath.Glob(filepath.Join(h.dir, "*.hosts"))
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".hosts")
		if set.Index(name) >= 0 || set.CheckName(name, -1) != nil {
			continue
		}
		text, err := a.file(file).Read()
		if err != nil {
			continue
		}
		set.Profiles = append(set.Profiles, hosts.Profile{Name: name, Text: string(text)})
	}
	h.set, h.problem = set, nil
	h.choice = min(h.choice, max(len(set.Profiles)-1, 0))
	// Drafts of profiles still there survive a reload; their saved text
	// may have changed under them, which Save then reports.
	drafts := h.editors
	h.editors = map[string]*editor.State{}
	for name, st := range drafts {
		if set.Index(name) >= 0 {
			h.editors[name] = st
		}
	}
	a.applyHosts()
	a.refreshStaleProfiles()
	if a.win != nil && !h.ticking {
		h.ticking = true
		go func() {
			for range time.Tick(10 * time.Minute) {
				a.win.Update(a.refreshStaleProfiles)
			}
		}()
	}
}

// applyHosts hands what the profiles that are on map to the proxy.
func (a *app) applyHosts() {
	if a.srv == nil {
		return
	}
	if err := a.srv.SetHosts(a.profiles.set.Rules()); err != nil {
		a.fail(locales.HostProfiles(), err)
	}
}

// saveHostsIndex keeps the order, switches and mode; false when it could
// not, after saying so.
func (a *app) saveHostsIndex() bool {
	h := &a.profiles
	if h.problem != nil {
		a.fail(locales.HostProfilesAreReadOnly(), h.problem)
		return false
	}
	data, err := h.set.MarshalIndex()
	if err == nil {
		err = a.file(filepath.Join(h.dir, hosts.IndexFile)).Write(data)
	}
	if err != nil {
		a.fail(locales.CouldNotSaveHostProfiles(), err)
		return false
	}
	return true
}

// changeHosts runs a change to the profiles, saves it and applies it, or
// puts them back as they were when it cannot be saved.
func (a *app) changeHosts(change func(*hosts.Set)) bool {
	h := &a.profiles
	before := h.set
	before.Profiles = slices.Clone(h.set.Profiles)
	change(&h.set)
	h.rev++
	if !a.saveHostsIndex() {
		h.set = before
		return false
	}
	a.applyHosts()
	return true
}

// switchProfile turns a profile on or off; one at a time, turning one on
// turns the others off.
func (a *app) switchProfile(i int, on bool) {
	a.changeHosts(func(s *hosts.Set) { s.Switch(i, on) })
}

func (a *app) setHostsExclusive(exclusive bool) {
	a.changeHosts(func(s *hosts.Set) { s.SetExclusive(exclusive) })
}

// hostsOff turns every profile off, as the enabled tools strip's × does.
func (a *app) hostsOff() {
	a.changeHosts(func(s *hosts.Set) {
		for i := range s.Profiles {
			s.Profiles[i].On = false
		}
	})
}

// dropConnections closes the clients' open connections, so the next
// requests dial the addresses the profiles map now.
func (a *app) dropConnections() {
	if a.srv == nil {
		return
	}
	n := a.srv.DropConnections()
	a.inform(locales.DroppedConnections(n))
}

// hostsLabel is what the title bar says the profiles map: "Off", one
// profile's name, or the highest one's and how many more.
// hostsLabel names the profiles that are on, or "" when none is.
func (a *app) hostsLabel() string {
	on := a.profiles.set.On()
	switch len(on) {
	case 0:
		return ""
	case 1:
		return on[0]
	}
	return fmt.Sprintf("%s +%d", on[0], len(on)-1)
}

// hostsMenu lists the profiles with their switches, the mode, and the way to
// Tools.
func (a *app) hostsMenu(c *ui.Context, t *ui.Theme) {
	h := &a.profiles
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Padding(t.Space(1), t.Space(1), t.Space(1), t.Space(2)).Children(func() {
		ui.Text(c, locales.HostProfiles()).FontSize(uikit.Scaled(uikit.TextBody)).FontWeight(500).TextColor(t.Text).Grow(1)
		a.hostsMode(c)
	})
	if len(h.set.Profiles) == 0 {
		uikit.Meta(c, locales.NoProfilesYetMapHosts()).Padding(t.Space(1), t.Space(2))
	}
	conflicts := a.profileConflicts()
	for i, p := range h.set.Profiles {
		ui.Row(c.Key("hosts-menu-"+p.Name)).Height(uikit.HeightLarge).Gap(t.Space(2)).AlignItems(ui.Center).Padding(0, t.Space(2)).Children(func() {
			on := p.On
			if ui.Switch(c, &on).Label("Host profile: " + p.Name).Changed() {
				a.switchProfile(i, on)
			}
			ui.Text(c, p.Name).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Text).SingleLine().MinWidth(0)
			if p.URL != "" {
				uikit.Tip(c, uikit.Glyph(c, subscribeIcon), "Subscribed: "+p.URL)
			}
			if n := conflicts[p.Name]; n > 0 && p.On {
				ui.Text(c, locales.ConflictsCount(n)).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).SingleLine().
					Tooltip(locales.AHigherProfileMapsThe())
			}
			ui.Spacer(c)
			n := profileHosts(p)
			uikit.Meta(c, locales.HostsCount(n)).Font(uikit.FontMono).FontFeatures("tnum").SingleLine()
		})
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Padding(t.Space(1), t.Space(2)).Children(func() {
		uikit.Meta(c, locales.AppliesToNewConnections()).Grow(1).SingleLine()
		ui.Row(c).Disabled(a.srv == nil).Children(func() {
			if uikit.Tip(c, uikit.CompactButton(c, locales.DropOpenConnections()), locales.CloseTheClientsOpenConnections()).Clicked() {
				a.dropConnections()
			}
		})
	})
	ui.Divider(c)
	manage := pick(a.hostsActions(), "hosts.manage")
	if uikit.RowButton(c, locales.ManageProfiles()).Children(func() {
		ui.Text(c, locales.ManageProfiles()).Grow(1)
		uikit.Meta(c, manage.shortcut.String())
	}).Clicked() {
		h.menuOpen = false
		a.openTools("hosts")
	}
}

// hostsMode chooses whether profiles combine or one is on at a time.
func (a *app) hostsMode(c *ui.Context) {
	mode := 0
	if a.profiles.set.Exclusive {
		mode = 1
	}
	if uikit.Segmented(c, &mode, locales.Combine(), locales.OneAtATime()).Label(locales.HostProfileMode()).Changed() {
		a.setHostsExclusive(mode == 1)
	}
}

// hostsActions are the profiles' commands for the palette, menus and keys.
func (a *app) hostsActions() []action {
	actions := []action{
		do("hosts.manage", locales.ManageHostProfiles(), func() { a.openTools("hosts") }).keys(ui.Cmd|ui.Shift, ui.KeyH).in("hosts"),
		do("hosts.drop-connections", locales.DropOpenConnections(), a.dropConnections).
			when(a.srv != nil, "Start capture first").in("hosts"),
		do("hosts.off", locales.TurnOffHostProfiles(), a.hostsOff).
			when(len(a.profiles.set.On()) > 0, "No Host profile is on").in("hosts"),
	}
	for i, p := range a.profiles.set.Profiles {
		actions = append(actions, do("hosts.switch", "Host profile: "+p.Name, func() { a.switchProfile(i, !p.On) }).
			checked(checkbox(p.On)).in("hosts"))
	}
	return actions
}

// profileHosts counts the hosts a profile maps.
func profileHosts(p hosts.Profile) int {
	rules, _ := hosts.Parse(p.Text)
	return len(rules)
}

// profileConflicts counts, by profile, the lines a higher profile hides.
func (a *app) profileConflicts() map[string]int {
	counts := map[string]int{}
	for _, conflict := range a.profiles.set.Conflicts() {
		counts[conflict.Loser.Profile]++
	}
	return counts
}

// hostProfileLabel spells where a flow's mapping came from, "Staging, line
// 2", from the engine's "Staging:2".
func hostProfileLabel(source string) string {
	name, line, ok := strings.CutLast(source, ":")
	if !ok {
		return source
	}
	return name + ", line " + line
}

// newProfile adds a profile below the others, saves its file, and shows it
// in Tools. The name is made unique.
func (a *app) newProfile(name, text, url string) bool {
	h := &a.profiles
	base := name
	for n := 2; h.set.Index(name) >= 0; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	if err := a.file(h.path(name)).Write([]byte(text)); err != nil {
		a.fail(locales.CouldNotCreateHostProfile(name), err)
		return false
	}
	if !a.changeHosts(func(s *hosts.Set) {
		s.Profiles = append(s.Profiles, hosts.Profile{Name: name, Text: text, URL: url})
	}) {
		return false
	}
	h.choose(len(h.set.Profiles) - 1)
	return true
}

// importSystemHosts copies this system's hosts file into a new profile, off.
func (a *app) importSystemHosts() {
	path := hosts.SystemFile()
	data, err := os.ReadFile(path)
	if err != nil {
		a.fail(locales.CouldNotRead(path), err)
		return
	}
	text := "# Imported from " + path + " on " + time.Now().Format("2006-01-02") + "\n" + string(data)
	if a.newProfile("System hosts", text, "") {
		a.done(locales.ImportedProfile(path))
	}
}

// subscribe adds a profile fetched from source, kept up to date hourly.
func (a *app) subscribe(source string) {
	source = strings.TrimSpace(source)
	name := "Subscription"
	if u, err := urlHost(source); err == nil {
		name = u
	}
	if a.newProfile(name, "", source) {
		a.refreshProfile(a.profiles.choice)
	}
}

func urlHost(source string) (string, error) {
	_, rest, ok := strings.Cut(source, "://")
	if !ok {
		return "", fs.ErrInvalid
	}
	host, _, _ := strings.Cut(rest, "/")
	if host == "" {
		return "", fs.ErrInvalid
	}
	return host, nil
}

// refreshProfile fetches a subscribed profile in the background.
func (a *app) refreshProfile(i int) {
	h := &a.profiles
	p := h.set.Profiles[i]
	if p.URL == "" || h.fetching[p.Name] {
		return
	}
	if h.fetching == nil {
		h.fetching = map[string]bool{}
	}
	if h.results == nil {
		h.results = make(chan hostsFetch, 16)
	}
	h.fetching[p.Name] = true
	results := h.results
	go func() {
		text, err := hosts.Fetch(context.Background(), p.URL)
		results <- hostsFetch{name: p.Name, url: p.URL, text: text, at: time.Now(), err: err}
		a.invalidate()
		a.invalidateTools()
	}()
}

// refreshStaleProfiles fetches the subscriptions older than an hour.
func (a *app) refreshStaleProfiles() {
	for i, p := range a.profiles.set.Profiles {
		if p.URL != "" && time.Since(p.Updated) > hosts.RefreshEvery {
			a.refreshProfile(i)
		}
	}
}

// drainHosts keeps what fetches brought, for the profiles still subscribed
// to the same URL.
func (a *app) drainHosts() {
	h := &a.profiles
	for {
		select {
		case r := <-h.results:
			delete(h.fetching, r.name)
			i := h.set.Index(r.name)
			if i < 0 || h.set.Profiles[i].URL != r.url {
				continue
			}
			if r.err != nil {
				a.fail(locales.CouldNotRefreshHostProfile(r.name), r.err)
				continue
			}
			if err := a.file(h.path(r.name)).Write([]byte(r.text)); err != nil {
				a.fail(locales.CouldNotSaveHostProfile(r.name), err)
				continue
			}
			a.changeHosts(func(s *hosts.Set) {
				s.Profiles[i].Text, s.Profiles[i].Updated = r.text, r.at
			})
			if st := h.editors[r.name]; st != nil {
				st.Buffer.Reset(r.text)
			}
		default:
			return
		}
	}
}

// saveProfile writes the chosen profile's draft, and applies it when the
// profile is on.
func (a *app) saveProfile(i int) {
	h := &a.profiles
	p := h.set.Profiles[i]
	st := h.editors[p.Name]
	if st == nil {
		return
	}
	text := st.Buffer.String()
	if err := a.file(h.path(p.Name)).Write([]byte(text)); err != nil {
		a.fail(locales.CouldNotSaveHostProfile(p.Name), err)
		return
	}
	h.set.Profiles[i].Text = text
	h.rev++
	a.applyHosts()
	if _, problems := hosts.Parse(text); len(problems) > 0 {
		a.warn(locales.SavedProfileWarning(p.Name, len(problems)))
		return
	}
	a.done(locales.SavedName(p.Name))
}

// renameProfile renames a profile and its file.
func (a *app) renameProfile(i int, name string) {
	h := &a.profiles
	old := h.set.Profiles[i]
	if name == old.Name {
		return
	}
	if err := h.set.CheckName(name, i); err != nil {
		a.warn(locales.RenameError(err.Error()))
		h.name = old.Name
		return
	}
	from, to := h.path(old.Name), h.path(name)
	if !strings.EqualFold(old.Name, name) {
		if _, err := os.Stat(to); err == nil {
			a.warn(locales.RenameExists(to))
			h.name = old.Name
			return
		}
	}
	if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
		a.fail(locales.CouldNotRenameHostProfile(old.Name), err)
		h.name = old.Name
		return
	}
	delete(a.files, from)
	a.file(to).Read()
	if !a.changeHosts(func(s *hosts.Set) { s.Profiles[i].Name = name }) {
		_ = os.Rename(to, from)
		h.name = old.Name
		return
	}
	if st := h.editors[old.Name]; st != nil {
		delete(h.editors, old.Name)
		h.editors[name] = st
	}
	h.name, h.nameFor = name, name
}

// deleteProfile removes a profile and its file, offering Undo.
func (a *app) deleteProfile(i int) {
	h := &a.profiles
	p := h.set.Profiles[i]
	draft := h.editors[p.Name]
	path := h.path(p.Name)
	if !a.changeHosts(func(s *hosts.Set) { s.Profiles = slices.Delete(s.Profiles, i, i+1) }) {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		a.fail(locales.CouldNotDelete(path), err)
	}
	delete(a.files, path)
	delete(h.editors, p.Name)
	h.choice = min(h.choice, max(len(h.set.Profiles)-1, 0))
	a.notify(notice{level: noticeSuccess, text: "Deleted Host profile " + p.Name, action: locales.Undo(), run: func() {
		if h.set.Index(p.Name) >= 0 {
			return
		}
		if err := a.file(path).Write([]byte(p.Text)); err != nil {
			a.fail(locales.CouldNotRestoreHostProfile(p.Name), err)
			return
		}
		a.changeHosts(func(s *hosts.Set) {
			s.Profiles = slices.Insert(s.Profiles, min(i, len(s.Profiles)), p)
			if p.On && s.Exclusive {
				s.Switch(min(i, len(s.Profiles)-1), true)
			}
		})
		if draft != nil {
			h.editors[p.Name] = draft
		}
		h.choose(h.set.Index(p.Name))
	}})
}

// duplicateProfile copies a profile's draft as a new local profile, off.
func (a *app) duplicateProfile(i int) {
	p := a.profiles.set.Profiles[i]
	text := p.Text
	if st := a.profiles.editors[p.Name]; st != nil {
		text = st.Buffer.String()
	}
	a.newProfile(p.Name+" copy", text, "")
}

// choose points the list at a profile. A keyed list puts the selection back on
// the row whose key it remembers, so setting the index alone would hand the
// selection to the profile that was chosen before this one.
func (h *hostsState) choose(i int) {
	h.choice = i
	h.list.Choose(i)
}

// moveProfiles reorders the profiles, which changes which wins.
func (a *app) moveProfiles(rows []int, to int) {
	h := &a.profiles
	chosen := ""
	if h.choice < len(h.set.Profiles) {
		chosen = h.set.Profiles[h.choice].Name
	}
	a.changeHosts(func(s *hosts.Set) {
		var moved, rest []hosts.Profile
		at := to
		for i, p := range s.Profiles {
			if slices.Contains(rows, i) {
				moved = append(moved, p)
				if i < to {
					at--
				}
			} else {
				rest = append(rest, p)
			}
		}
		s.Profiles = slices.Insert(rest, at, moved...)
	})
	h.choose(max(h.set.Index(chosen), 0))
}

// mapHostLine adds one mapping, as the flow's "Map this host…" makes it, at
// the end of a profile, creating it first if needed, and turns it on.
func (a *app) mapHostLine(profile, line string) bool {
	h := &a.profiles
	i := h.set.Index(profile)
	if i < 0 {
		if !a.newProfile(profile, "", "") {
			return false
		}
		i = h.set.Index(profile)
	}
	p := h.set.Profiles[i]
	if p.URL != "" {
		a.warn(p.Name + " is a subscription; add the mapping to another profile")
		return false
	}
	text := p.Text
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += line + "\n"
	if err := a.file(h.path(p.Name)).Write([]byte(text)); err != nil {
		a.fail(locales.CouldNotSaveHostProfile(p.Name), err)
		return false
	}
	if st := h.editors[p.Name]; st != nil && st.Buffer.String() == p.Text {
		st.Buffer.Reset(text)
	}
	return a.changeHosts(func(s *hosts.Set) {
		s.Profiles[i].Text = text
		s.Switch(i, true)
	})
}

// chosen is the profile Tools shows, kept in range; false without any.
func (h *hostsState) chosen() (int, bool) {
	if len(h.set.Profiles) == 0 {
		return 0, false
	}
	h.choice = min(max(h.choice, 0), len(h.set.Profiles)-1)
	return h.choice, true
}
