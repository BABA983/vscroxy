package app

// Settings' search: one index over every registered setting, built when the
// language or a saved value changes, and a small scored result list on each
// keystroke. The index holds only the registry's title, description and
// stable id; no setting value is stored in it or drawn in a result, so a
// password or token cannot be echoed into a match.

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

type settingsSearchEntry struct {
	spec        settingSpec
	title       string
	description string
}

// settingsSearchIndex is a snapshot for one language and one settings
// generation. builds counts snapshots for the contract tests; nothing else
// needs to know how often a frame calls search().
type settingsSearchIndex struct {
	locale  string
	gen     int
	built   bool
	builds  int
	entries []settingsSearchEntry
}

func (a *app) settingsSearchKey() (string, int) { return i18n.Locale(), a.settingsGen }

// settingsSearch builds at most one index per language/settings change. The
// call is cheap and safe in a drawing callback: every repeated frame with an
// unchanged key returns the existing snapshot.
func (a *app) settingsSearch() settingsSearchIndex {
	locale, gen := a.settingsSearchKey()
	idx := a.settingsIndex
	if idx.built && idx.locale == locale && idx.gen == gen {
		return idx
	}
	idx = settingsSearchIndex{locale: locale, gen: gen, built: true, builds: a.settingsIndex.builds + 1,
		entries: make([]settingsSearchEntry, 0, len(settingSpecs()))}
	for _, spec := range settingSpecs() {
		idx.entries = append(idx.entries, settingsSearchEntry{
			spec: spec, title: spec.title(), description: spec.description(),
		})
	}
	a.settingsIndex = idx
	return idx
}

// settingsSearchQuery splits VS Code's @modified filter from the fuzzy text.
// The filter word is a command, not translated text, as in VS Code.
func settingsSearchQuery(input string) (text string, modifiedOnly bool) {
	words := strings.Fields(input)
	if len(words) > 0 && strings.EqualFold(words[0], "@modified") {
		modifiedOnly = true
		words = words[1:]
	}
	return strings.Join(words, " "), modifiedOnly
}

// settingsSearchHit keeps one match per rendered field, in that field's own
// rune offsets, so the highlighter never has to search the visible text.
type settingsSearchHit struct {
	entry              settingsSearchEntry
	score              int
	titleMatches       []fuzzy.Match
	descriptionMatches []fuzzy.Match
	idMatches          []fuzzy.Match
	modified           bool
}

// settingsScoreField returns the score and match ranges for one indexed
// field. It uses the same scorer and cache-less accessor as the command
// palette, so a query that ranks a command ranks a setting the same way.
func settingsScoreField(target string, query fuzzy.IPreparedQuery) (int, []fuzzy.Match) {
	if target == "" {
		return 0, nil
	}
	score := fuzzy.ScoreItemFuzzy(paletteScoreItem{label: target}, query, true, paletteAccessor{}, nil)
	return score.Score, score.LabelMatch
}

// settingsDescriptionMatch follows VS Code's settings search: titles and ids
// are fuzzy, but prose is a contiguous case-insensitive substring, with a
// word start outranking a match inside a word. Scattered letters in a long
// description must not pull an unrelated row into the results.
func settingsDescriptionMatch(description, query string) (int, []fuzzy.Match) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return 0, nil
	}
	lower := strings.ToLower(description)
	at := strings.Index(lower, needle)
	if at < 0 {
		return 0, nil
	}
	start := utf8.RuneCountInString(lower[:at])
	match := fuzzy.Match{Start: start, End: start + utf8.RuneCountInString(needle)}
	score := 1000
	runes := []rune(description)
	if start == 0 || unicode.IsSpace(runes[start-1]) || unicode.IsPunct(runes[start-1]) {
		score += 200
	}
	return score, []fuzzy.Match{match}
}

// settingsFind ranks the snapshot for one input, including the @modified
// filter. A title match outranks an id match, which outranks a description
// match; for "@modified" alone the registry order is kept.
func settingsFind(a *app, input string) []settingsSearchHit {
	query, modifiedOnly := settingsSearchQuery(input)
	idx := a.settingsSearch()
	prepared := fuzzy.PrepareQuery(query)
	hits := make([]settingsSearchHit, 0, len(idx.entries))
	for _, entry := range idx.entries {
		hit := settingsSearchHit{entry: entry, modified: entry.spec.modified(a)}
		if modifiedOnly && !hit.modified {
			continue
		}
		if prepared.Normalized == "" {
			hit.score = 1
			hits = append(hits, hit)
			continue
		}
		titleScore, titleMatches := settingsScoreField(entry.title, prepared)
		idScore, idMatches := settingsScoreField(entry.spec.id, prepared)
		descScore, descMatches := settingsDescriptionMatch(entry.description, query)
		hit.titleMatches, hit.idMatches, hit.descriptionMatches = titleMatches, idMatches, descMatches
		best := max(titleScore*4, idScore*3, descScore*2)
		if best == 0 {
			continue
		}
		hit.score = best
		hits = append(hits, hit)
	}
	if prepared.Normalized != "" {
		slices.SortStableFunc(hits, func(x, y settingsSearchHit) int {
			if x.score != y.score {
				return y.score - x.score
			}
			if x.entry.spec.section != y.entry.spec.section {
				return int(x.entry.spec.section) - int(y.entry.spec.section)
			}
			return strings.Compare(x.entry.spec.id, y.entry.spec.id)
		})
	}
	return hits
}

// settingsSearchBar sits above both Settings shells, so Cmd/Ctrl+, can focus
// the same field whether Settings is a window or a dialog.
func (a *app) settingsSearchBar(c *ui.Context, t *ui.Theme) {
	// The field is the app's own search field, not a text field with a
	// magnifier beside it: Settings is one more list to find something in.
	// The bar keeps a margin under the field, so it does not sit on the
	// categories it opens.
	ui.Row(c.Key("settings-search")).FillWidth().Height(uikit.HeightBar+t.Space(uikit.SpaceM)).AlignItems(ui.Center).
		Padding(0, t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceS)).Children(func() {
		field, input := uikit.SearchFieldInput(c, &a.settingsQuery, locales.SettingsSearchLabel(), locales.SettingsSearchPlaceholder(), false)
		field.Grow(1).MinWidth(240)
		input.Bind(&a.settingsSearchHandle)
		input.FocusBind(&a.settingsFocusSearch, a.settingsFocusSearch)
		// Escape clears a query; on an empty field it is left to the
		// window, so it closes Settings as it closed Tools.
		if a.settingsQuery != "" {
			input.OnShortcut(0, ui.KeyEscape, func() {
				a.settingsQuery = ""
				a.settingsFocusSearch = 0
			})
		}
		if a.settingsQuery != "" && uikit.SecondaryButton(c, locales.Clear()).KeepFocus().Clicked() {
			a.settingsQuery = ""
		}
		// A window narrow enough for Settings keeps the field's room and
		// shortens the hint, whose tooltip still says the whole sentence.
		uikit.Tip(c, uikit.Meta(c, locales.SettingsSearchHint()).SingleLine().Shrink(1), locales.SettingsSearchHint())
	})
}

// settingsSearchResults draws ranked hits grouped by Settings section. The
// row's own title and description carry the matched spans, and a modified
// row gets the same edge marker and menu as a row on a Settings page.
func (a *app) settingsSearchResults(c *ui.Context, t *ui.Theme) {
	_, modifiedOnly := settingsSearchQuery(a.settingsQuery)
	hits := settingsFind(a, a.settingsQuery)
	var tools []toolSearchHit
	if !modifiedOnly {
		tools = a.toolSearch(a.settingsQuery)
	}
	if modifiedOnly {
		uikit.Hint(c, locales.SettingsModifiedCount(len(hits))).Padding(0, t.Space(uikit.SpaceXL))
		if len(hits) == 0 {
			uikit.Hint(c, locales.SettingsNoModified()).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceXL))
			return
		}
	} else if len(hits) == 0 && len(tools) == 0 {
		uikit.Hint(c, locales.SettingsSearchNoMatches(a.settingsQuery)).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceXL))
		return
	}
	defer a.toolSearchResults(c, t, tools)
	var section settingsSection = -1
	for _, hit := range hits {
		if hit.entry.spec.section != section {
			section = hit.entry.spec.section
			uikit.Label(c, section.title()).Padding(t.Space(uikit.SpaceL), t.Space(uikit.SpaceXL))
		}
		a.settingsSearchResultRow(c, t, hit)
	}
}

func (a *app) settingsSearchResultRow(c *ui.Context, t *ui.Theme, hit settingsSearchHit) {
	id := hit.entry.spec.id
	row := ui.Row(c.Key("settings-result:"+id)).FillWidth().MinHeight(uikit.SettingsRowHeight).
		Padding(t.Space(uikit.SpaceS), t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).
		AlignItems(ui.Center)
	reveal := func() { a.revealSetting(id) }
	row.Children(func() {
		if hit.modified {
			ui.Box(c).Width(3).Height(uikit.SettingsRowHeight - t.Space(uikit.SpaceL)).Shrink(0).
				Radius(2).Background(uikit.Colors(t).Info)
		} else {
			ui.Box(c).Width(3).Shrink(0)
		}
		// Only the text acts as a link. An ⋯ press outside this column must
		// not reveal the setting and rebuild the row under the open menu.
		head := ui.Column(c).Grow(1).MinWidth(0).Gap(0).Role(ui.RoleButton)
		head.OnClick(reveal)
		head.Children(func() {
			settingsHighlighted(c, hit.entry.title, hit.titleMatches, uikit.Scaled(uikit.TextBody), false, t.Text)
			settingsHighlighted(c, hit.entry.description, hit.descriptionMatches, uikit.Scaled(uikit.TextSmall), false, t.TextMuted)
			ui.Row(c).Gap(0).Children(func() {
				uikit.Meta(c, locales.SettingsSettingID(""))
				settingsHighlighted(c, id, hit.idMatches, uikit.Scaled(uikit.TextMeta), true, t.TextMuted).
					Label(locales.SettingsSettingID(id))
			})
		})
		if hit.modified {
			uikit.MoreMenu(c, locales.SettingsSettingActions(hit.entry.title), func(m *ui.Menu) {
				if m.Item(locales.SettingsResetToDefault()).Chosen() {
					a.resetSetting(id)
				}
			})
		}
	})
}

// settingsHighlighted adds the fuzzy spans to a title, description or id and
// keeps the plain text as the accessible label, as the palette does.
func settingsHighlighted(c *ui.Context, text string, matches []fuzzy.Match, size float32, mono bool, ink ui.Color) ui.Element {
	if text == "" {
		return ui.Box(c)
	}
	if len(matches) == 0 {
		if mono {
			return uikit.Mono(c, text).FontSize(size).TextColor(ink)
		}
		return uikit.Body(c, text).FontSize(size).TextColor(ink)
	}
	element := ui.RichText(c, paletteSpans(text, matches, uikit.Colors(c.Theme()).Info, size)...).TextColor(ink)
	if mono {
		element = element.Font(uikit.FontMono)
	}
	return element.Label(text)
}

// revealSetting opens the section that owns an id and leaves the search, so
// the row is visible on the page with its control.
func (a *app) revealSetting(id string) {
	spec, ok := settingByID(id)
	if !ok {
		return
	}
	a.chooseSettingsCategory(spec.section.id())
	a.settingsQuery = ""
}

// toolSearchHit is a debugging tool, or a rule, script or Host profile in
// one, found by its name. Only names are searched: a rule's match or a
// script's code is the editor's own Find.
type toolSearchHit struct {
	key, name, kind string
	matches         []fuzzy.Match
	score           int
	open            func()
}

// toolSearch ranks the tools' pages and the named things on them for the
// Settings search, best first.
func (a *app) toolSearch(input string) []toolSearchHit {
	query, _ := settingsSearchQuery(input)
	prepared := fuzzy.PrepareQuery(query)
	if prepared.Normalized == "" {
		return nil
	}
	var hits []toolSearchHit
	add := func(key, name, kind string, open func()) {
		score, matches := settingsScoreField(name, prepared)
		if score == 0 {
			return
		}
		hits = append(hits, toolSearchHit{key: key, name: name, kind: kind, matches: matches, score: score, open: open})
	}
	for _, page := range toolPages() {
		add("page:"+page.id, page.label, locales.DebuggingTools(), func() { a.chooseSettingsCategory(page.id) })
	}
	for i, r := range a.rules {
		add("rule:"+strconv.Itoa(i), cmp.Or(r.Name, r.Match), locales.Rules(), func() {
			a.chooseSettingsCategory("rules")
			a.editRule(i, r)
		})
	}
	a.scripts.mu.RLock()
	names := slices.Sorted(maps.Keys(a.scripts.texts))
	a.scripts.mu.RUnlock()
	for _, name := range names {
		add("script:"+name, name, locales.Scripts(), func() {
			a.chooseSettingsCategory("scripts")
			a.openScript(name)
		})
	}
	for i, p := range a.profiles.set.Profiles {
		add("hosts:"+strconv.Itoa(i), p.Name, locales.HostProfiles(), func() {
			a.chooseSettingsCategory("hosts")
			a.profiles.choice = i
		})
	}
	slices.SortStableFunc(hits, func(x, y toolSearchHit) int { return y.score - x.score })
	return hits
}

// toolSearchResults lists the tools' hits under the settings: a row names
// what it found and where, and opens it there.
func (a *app) toolSearchResults(c *ui.Context, t *ui.Theme, hits []toolSearchHit) {
	if len(hits) == 0 {
		return
	}
	uikit.Label(c, locales.DebuggingTools()).Padding(t.Space(uikit.SpaceL), t.Space(uikit.SpaceXL))
	for _, hit := range hits {
		row := ui.Row(c.Key("tool-result:"+hit.key)).FillWidth().MinHeight(uikit.HeightRow).
			Padding(t.Space(uikit.SpaceS), t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).
			Role(ui.RoleButton).Label(hit.kind + ": " + hit.name)
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		row.OnClick(func() {
			a.settingsQuery = ""
			hit.open()
		})
		row.Children(func() {
			ui.Box(c).Width(3).Shrink(0)
			settingsHighlighted(c, hit.name, hit.matches, uikit.Scaled(uikit.TextBody), false, t.Text).SingleLine().Grow(1).MinWidth(0)
			uikit.Meta(c, hit.kind).SingleLine()
		})
	}
}
