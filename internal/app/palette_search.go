package app

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/linkedmap"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// paletteEntry is one row of the command palette: a command or a search
// result. detail is a tooltip and the status line; it never holds a variable
// value or a cookie value. matches are the highlighted runes of label, in
// fuzzy's rune offsets.
type paletteEntry struct {
	label   string
	detail  string
	meta    string // the shortcut of a command, or the kind of a result
	enabled bool
	why     string
	action  bool
	matches []fuzzy.Match
	run     func()
}

type requestSearchEntry struct {
	key    string
	label  string
	detail string
	path   string
}

type pathSearchEntry struct {
	path        string
	label       string
	kind        string
	description string
}

// requestSearchIndex caches the labels and details the palette scores. It is
// rebuilt when requestGen changes, not on every keystroke or frame.
type requestSearchIndex struct {
	gen      int
	built    bool
	requests []requestSearchEntry
	paths    []pathSearchEntry
}

func (a *app) requestSearch() requestSearchIndex {
	if a.requestIndex.built && a.requestIndex.gen == a.requestGen {
		return a.requestIndex
	}
	idx := requestSearchIndex{gen: a.requestGen, built: true}
	// A collection or folder is a path. Folders carry a description; a
	// collection implied only by its requests still gets a result.
	paths := map[string]string{}
	for _, f := range a.folders {
		paths[f.Path] = f.Description
	}
	for _, r := range a.requests {
		for _, prefix := range pathPrefixes(r.Collection) {
			if _, ok := paths[prefix]; !ok {
				paths[prefix] = ""
			}
		}
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		name := path
		if _, last, ok := strings.CutLast(path, "/"); ok {
			name = last
		}
		kind := "Collection"
		if strings.Contains(path, "/") {
			kind = "Folder"
		}
		idx.paths = append(idx.paths, pathSearchEntry{
			path: path, label: name, kind: kind, description: paths[path],
		})
	}
	for _, r := range a.requests {
		method := cmp.Or(r.Protocol, r.Method)
		detail := strings.TrimSpace(method + " " + r.URL)
		if r.Collection != "" {
			detail += " · " + r.Collection
		}
		// Request descriptions are not part of the model yet; add them here
		// when they are. Values of {{variables}} stay authored, as in the URL.
		idx.requests = append(idx.requests, requestSearchEntry{
			key: r.key(), label: cmp.Or(r.Name, locales.UntitledRequest()), detail: detail,
		})
	}
	a.requestIndex = idx
	return idx
}

// paletteResultLimit bounds resource rows in one palette frame. A broad
// query can match every saved request; building thousands of rows and their
// closures every frame is wasted work when only a few can be seen.
const paletteResultLimit = 50

// paletteHistoryLimit keeps a short, in-memory quick-open history. There is
// no palette field in the persisted settings, so it does not survive restart.
const paletteHistoryLimit = 100

type paletteSearch struct {
	entries []paletteEntry
	more    int
}

// paletteSearchEntries is paletteSearch without the overflow count.
func (a *app) paletteSearchEntries(query string) []paletteEntry {
	return a.paletteSearch(query).entries
}

// paletteScoreItem is the fuzzy accessor for one candidate's label and
// description. Empty strings stand in for VS Code's undefined; a request's
// detail holds authored URL and collection text, never a variable value.
type paletteScoreItem struct {
	label  string
	detail string
	path   string
}

type paletteAccessor struct{}

func (paletteAccessor) GetItemLabel(item paletteScoreItem) string { return item.label }

func (paletteAccessor) GetItemDescription(item paletteScoreItem) (string, bool) {
	return item.detail, item.detail != ""
}

func (paletteAccessor) GetItemPath(item paletteScoreItem) (string, bool) {
	return item.path, item.path != ""
}

// paletteCandidate holds what scoring needs, without building a row closure
// until the candidate is known to fit in the palette.
type paletteCandidate struct {
	kind     uint8
	key      string
	label    string
	detail   string
	meta     string
	why      string
	path     string
	request  string
	env      string
	action   action
	enabled  bool
	isAction bool
	resource bool
	global   bool
	score    int
	matches  []fuzzy.Match
}

const (
	paletteOpenRequest uint8 = iota
	paletteRevealPath
	paletteOpenEnvironment
	paletteEditVariables
	paletteRunAction
)

func scorePaletteItem(item paletteScoreItem, query fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) (int, []fuzzy.Match) {
	result := fuzzy.ScoreItemFuzzy(item, query, true, paletteAccessor{}, cache)
	return result.Score, result.LabelMatch
}

func paletteCandidateFor(label, detail, path, key string, prepared fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) paletteCandidate {
	score, matches := scorePaletteItem(paletteScoreItem{label: label, detail: detail, path: path}, prepared, cache)
	return paletteCandidate{key: key, label: label, detail: detail, path: path, score: score, matches: matches, enabled: true}
}

// paletteAssetCandidates scores every collection, request, environment and
// variable. No rows or closures are created here, so a broad query does not
// allocate thousands of entries that would never be shown.
func (a *app) paletteAssetCandidates(prepared fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) []paletteCandidate {
	idx := a.requestSearch()
	candidates := make([]paletteCandidate, 0, len(idx.paths)+len(idx.requests))
	for _, p := range idx.paths {
		detail := p.kind + " · " + p.path
		if p.description != "" {
			detail += " · " + p.description
		}
		c := paletteCandidateFor(p.label, detail, p.path, "path:"+p.path, prepared, cache)
		c.kind, c.resource, c.meta = paletteRevealPath, true, p.kind
		candidates = append(candidates, c)
	}
	for _, r := range idx.requests {
		c := paletteCandidateFor(r.label, r.detail, "", "request:"+r.key, prepared, cache)
		c.kind, c.resource, c.meta, c.request = paletteOpenRequest, true, locales.Request(), r.key
		candidates = append(candidates, c)
	}

	envs := slices.Clone(a.envs)
	slices.SortFunc(envs, func(x, y environment) int { return strings.Compare(x.Name, y.Name) })
	for _, env := range envs {
		c := paletteCandidateFor(env.Name, locales.EnvironmentDotName(env.Name), "", "env:"+env.Name, prepared, cache)
		c.kind, c.resource, c.meta, c.env = paletteOpenEnvironment, true, "Environment", env.Name
		candidates = append(candidates, c)

		names := make(map[string]bool)
		for name := range maps.Keys(env.Vars) {
			names[name] = true
		}
		for name := range maps.Keys(env.Current) {
			names[name] = true
		}
		for _, name := range slices.Sorted(maps.Keys(names)) {
			c := paletteCandidateFor(name, locales.VariableDotName(env.Name), "", "var:"+env.Name+"\x00"+name, prepared, cache)
			c.kind, c.resource, c.meta, c.env = paletteEditVariables, true, "Variable", env.Name
			candidates = append(candidates, c)
		}
	}

	globals := make(map[string]bool)
	for name := range maps.Keys(a.globals) {
		globals[name] = true
	}
	for name := range maps.Keys(a.globalsCurrent) {
		globals[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(globals)) {
		c := paletteCandidateFor(name, locales.GlobalVariable(), "", "global:"+name, prepared, cache)
		c.kind, c.resource, c.meta, c.global = paletteEditVariables, true, "Variable", true
		candidates = append(candidates, c)
	}
	return candidates
}

// paletteCommandCandidates scores the commands separately: unavailable
// commands are sorted after every available row so their explanation reads
// last. The palette action itself is never offered to itself.
func paletteCommandCandidates(actions []action, available bool, query fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) []paletteCandidate {
	candidates := make([]paletteCandidate, 0, len(actions))
	for _, item := range actions {
		if item.id == "app.palette" || item.enabled != available || len(item.items) > 0 {
			continue
		}
		score, matches := scorePaletteItem(paletteScoreItem{label: item.label}, query, cache)
		if score == 0 {
			continue
		}
		candidates = append(candidates, paletteCandidate{
			kind: paletteRunAction, key: "command:" + item.id, label: item.label,
			meta: item.shortcut.keycap(), why: item.why(), action: item,
			enabled: item.enabled, isAction: true, score: score, matches: matches,
		})
	}
	return candidates
}

// paletteHistoryRanks maps a recently used key to its recency: 0 is the most
// recent. LRUCache iterates oldest to newest, as VS Code's LinkedMap does.
func (w *desktopWorkbench) paletteHistoryRanks() map[string]int {
	if w == nil || w.paletteHistory == nil {
		return nil
	}
	keys := slices.Collect(w.paletteHistory.Keys())
	ranks := make(map[string]int, len(keys))
	for i, key := range keys {
		ranks[key] = len(keys) - 1 - i
	}
	return ranks
}

// rememberPalette is the palette's in-memory command history: selecting a
// row moves it to the most recent end, and the oldest rows fall off the LRU.
func (w *desktopWorkbench) rememberPalette(key string) {
	if w == nil || key == "" {
		return
	}
	if w.paletteHistory == nil {
		w.paletteHistory = linkedmap.NewLRUCache[string, struct{}](paletteHistoryLimit)
	}
	w.paletteHistory.Set(key, struct{}{})
}

func sortPaletteCandidates(candidates []paletteCandidate, ranks map[string]int) {
	slices.SortStableFunc(candidates, func(x, y paletteCandidate) int {
		xr, xok := ranks[x.key]
		yr, yok := ranks[y.key]
		switch {
		case xok && !yok:
			return -1
		case !xok && yok:
			return 1
		case xok && xr != yr:
			return cmp.Compare(xr, yr)
		}
		if x.score != y.score {
			return cmp.Compare(y.score, x.score)
		}
		return cmp.Compare(x.label, y.label)
	})
}

func (c paletteCandidate) entry(a *app) paletteEntry {
	entry := paletteEntry{
		label: c.label, detail: c.detail, meta: c.meta, why: c.why,
		enabled: c.enabled, action: c.isAction, matches: c.matches,
	}
	switch c.kind {
	case paletteRunAction:
		entry.run = c.action.run
	case paletteOpenRequest:
		key := c.request
		entry.run = func() { a.openSaved(key) }
	case paletteRevealPath:
		path := c.path
		entry.run = func() { a.revealCollection(path) }
	case paletteOpenEnvironment:
		name := c.env
		entry.run = func() { a.openEnvironment(name) }
	case paletteEditVariables:
		if c.global {
			entry.run = func() {
				a.editGlobals()
				a.varsOpen = true
			}
		} else {
			name := c.env
			entry.run = func() { a.openEnvironment(name) }
		}
	}
	if c.key != "" {
		key := c.key
		wrapped := entry.run
		entry.run = func() {
			a.workbench.rememberPalette(key)
			if wrapped != nil {
				wrapped()
			}
		}
	}
	return entry
}

// paletteFinalize builds only the rows that fit. Resource rows are capped
// separately from commands, and the rest are counted for the overflow label.
func (a *app) paletteFinalize(candidates []paletteCandidate) paletteSearch {
	var out paletteSearch
	resources := 0
	for _, c := range candidates {
		if c.resource {
			if resources >= paletteResultLimit {
				out.more++
				continue
			}
			resources++
		}
		if c.score == 0 {
			continue
		}
		out.entries = append(out.entries, c.entry(a))
	}
	return out
}

// paletteSearch finds collections, folders, requests, environments and
// variable names with VS Code's fuzzy scorer. Variable values are never part
// of a label, detail or score, so typing a secret cannot reveal it exists.
func (a *app) paletteSearch(query string) paletteSearch {
	prepared := fuzzy.PrepareQuery(strings.TrimSpace(query))
	if prepared.Normalized == "" {
		return paletteSearch{}
	}
	cache := fuzzy.NewFuzzyScorerCache()
	candidates := a.paletteAssetCandidates(prepared, cache)
	candidates = slices.DeleteFunc(candidates, func(c paletteCandidate) bool { return c.score == 0 })
	sortPaletteCandidates(candidates, a.workbench.paletteHistoryRanks())
	return a.paletteFinalize(candidates)
}

// paletteRankedResults ranks commands and saved entries together. Empty
// queries list the commands that can run now; a search also finds unavailable
// commands, after the available ones, so asking for one explains why it
// cannot run instead of hiding it.
func (a *app) paletteRankedResults(query string, actions []action) paletteSearch {
	prepared := fuzzy.PrepareQuery(strings.TrimSpace(query))
	cache := fuzzy.NewFuzzyScorerCache()
	ranks := a.workbench.paletteHistoryRanks()
	if prepared.Normalized == "" {
		var enabled []paletteCandidate
		for _, item := range actions {
			if item.id == "app.palette" || !item.enabled || len(item.items) > 0 {
				continue
			}
			enabled = append(enabled, paletteCandidate{
				kind: paletteRunAction, key: "command:" + item.id, label: item.label,
				meta: item.shortcut.keycap(), action: item, enabled: true, isAction: true, score: 1,
			})
		}
		sortPaletteCandidates(enabled, ranks)
		return a.paletteFinalize(enabled)
	}

	candidates := a.paletteAssetCandidates(prepared, cache)
	candidates = append(candidates, paletteCommandCandidates(actions, true, prepared, cache)...)
	candidates = slices.DeleteFunc(candidates, func(c paletteCandidate) bool { return c.score == 0 })
	sortPaletteCandidates(candidates, ranks)
	result := a.paletteFinalize(candidates)

	unavailable := paletteCommandCandidates(actions, false, prepared, cache)
	sortPaletteCandidates(unavailable, ranks)
	result.entries = append(result.entries, a.paletteFinalize(unavailable).entries...)
	return result
}

// paletteLabel draws the row label with fuzzy's rune-offset matches. RichText
// keeps one readable label for the tester and assistive technology while the
// matched spans carry the theme's quiet accent wash.
func paletteLabel(c *ui.Context, item paletteEntry, color ui.Color) ui.Element {
	if len(item.matches) == 0 {
		return uikit.Body(c, item.label).TextColor(color)
	}
	runes := []rune(item.label)
	size := uikit.Scaled(uikit.TextBody)
	palette := uikit.Colors(c.Theme())
	spans := make([]ui.Span, 0, len(item.matches)*2+1)
	at := 0
	for _, match := range item.matches {
		if match.Start < at || match.End <= match.Start || match.End > len(runes) {
			continue
		}
		if match.Start > at {
			spans = append(spans, ui.Span{Text: string(runes[at:match.Start]), Size: size})
		}
		spans = append(spans, ui.Span{
			Text: string(runes[match.Start:match.End]), Size: size,
			Weight: uikit.WeightMedium, Background: palette.Accent, Radius: uikit.RadiusInner,
		})
		at = match.End
	}
	if at < len(runes) {
		spans = append(spans, ui.Span{Text: string(runes[at:]), Size: size})
	}
	return ui.RichText(c, spans...).TextColor(color)
}

// revealCollection shows a collection or folder in the sidebar without
// filtering the tree: its ancestors and itself open, and any sidebar search
// that would hide it is cleared.
func (a *app) revealCollection(path string) {
	a.page = pageRequest
	*a.regionOpen("collections") = true
	a.workbench.sidebarHidden = false
	a.requestQuery = ""
	for _, prefix := range pathPrefixes(path) {
		*a.collectionShown(prefix) = true
	}
}

func (a *app) openEnvironment(name string) {
	a.editEnvironment(name)
	a.varsOpen = true
}
