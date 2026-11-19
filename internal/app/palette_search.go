package app

import (
	"cmp"
	"hash/fnv"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/linkedmap"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// paletteEntry is one row of the command palette: a command or a search
// result. detail is a tooltip and the status line; it never holds a variable
// value or a cookie value. matches and detailMatches are highlighted runes of
// label and detail respectively, in fuzzy's rune offsets. Detail matches are
// only set for rows whose scored description is exactly this detail.
type paletteEntry struct {
	label         string
	detail        string
	meta          string // the shortcut of a command, or the kind of a result
	enabled       bool
	why           string
	action        bool
	matches       []fuzzy.Match
	detailMatches []fuzzy.Match
	run           func()
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
			// The collection leads, so typing it first highlights the name
			// rather than scattered letters earlier in the URL.
			detail = r.Collection + " · " + detail
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

// paletteAssetCacheLimit bounds how many distinct queries keep their sorted
// resource candidates around. Clearing the map is enough: every result is
// rebuilt from requestIndex and the current variables on the next query.
const paletteAssetCacheLimit = 16

// paletteAssetCacheKey includes everything that can change the scored and
// sorted resources. Variable names, not values, take part: the palette never
// reads values, and keeping them out of a key avoids retaining secrets.
type paletteAssetCacheKey struct {
	query      string
	scope      paletteScope
	requestGen int
	varsGen    uint64
	historyGen uint64
}

// paletteAssetResult is the cached ranking plus the overflow count. Storing
// overflow here lets a repeated frame stop at the visible rows instead of
// walking thousands of resource candidates again.
type paletteAssetResult struct {
	candidates []paletteCandidate
	overflow   int
}

type paletteSearch struct {
	entries []paletteEntry
	more    int
}

// paletteScope is the quick-access prefix typed at the start of the field.
// VS Code chooses a registered provider this way; unprefixed results stay
// mixed. The prefix is stripped before any scoring, so it never becomes part
// of the fuzzy query.
type paletteScope uint8

const (
	paletteMixed paletteScope = iota
	paletteCommandsOnly
	paletteRequestsOnly
	paletteConfigOnly
	paletteHelp
)

type paletteQuery struct {
	scope paletteScope
	text  string
}

func splitPaletteQuery(query string) paletteQuery {
	q := strings.TrimSpace(query)
	if q == "" {
		return paletteQuery{}
	}
	switch q[0] {
	case '>':
		return paletteQuery{paletteCommandsOnly, strings.TrimSpace(q[1:])}
	case '#':
		return paletteQuery{paletteRequestsOnly, strings.TrimSpace(q[1:])}
	case '$':
		return paletteQuery{paletteConfigOnly, strings.TrimSpace(q[1:])}
	case '?':
		return paletteQuery{paletteHelp, strings.TrimSpace(q[1:])}
	default:
		return paletteQuery{text: q}
	}
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
	kind          uint8
	key           string
	label         string
	detail        string
	meta          string
	why           string
	path          string
	request       string
	env           string
	action        action
	enabled       bool
	isAction      bool
	resource      bool
	global        bool
	score         int
	matches       []fuzzy.Match
	detailMatches []fuzzy.Match
	itemScore     *fuzzy.IItemScore
}

const (
	paletteOpenRequest uint8 = iota
	paletteRevealPath
	paletteOpenEnvironment
	paletteEditVariables
	paletteRunAction
)

// paletteCanMatch runs fuzzy's cheap subsequence check first. A no-match
// query is otherwise the slowest case: ScoreItemFuzzy would build a scoring
// matrix for every library row just to return zero.
//
// It must be a necessary condition, never a narrower one. ScoreItemFuzzy can
// score a label alone, a description followed by the label (VS Code reverses
// that order relative to this prefilter's old concatenation), or a full path
// identity. Each query piece gets its own choice of those alternatives.
func paletteCanMatch(item paletteScoreItem, query fuzzy.IPreparedQuery) bool {
	if item.path != "" && (query.PathNormalized == item.path || strings.EqualFold(query.PathNormalized, item.path)) {
		return true
	}

	label := strings.ToLower(item.label)
	description := strings.ToLower(item.detail)
	path := strings.ToLower(item.path)
	descriptionAndLabel := description + label
	slashAndLabel := description + "/" + label
	backslashAndLabel := description + `\` + label

	pieces := query.Values
	if len(pieces) == 0 {
		pieces = []fuzzy.IPreparedQueryPiece{query.IPreparedQueryPiece}
	}
	for _, piece := range pieces {
		pattern := piece.NormalizedLowercase
		if fuzzy.PatternInWord(pattern, label) ||
			fuzzy.PatternInWord(pattern, descriptionAndLabel) ||
			fuzzy.PatternInWord(pattern, slashAndLabel) ||
			fuzzy.PatternInWord(pattern, backslashAndLabel) ||
			fuzzy.PatternInWord(pattern, path) {
			continue
		}
		return false
	}
	return true
}

func scorePaletteItem(item paletteScoreItem, query fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) *fuzzy.IItemScore {
	if !paletteCanMatch(item, query) {
		return &fuzzy.IItemScore{}
	}
	return fuzzy.ScoreItemFuzzy(item, query, true, paletteAccessor{}, cache)
}

func paletteCandidateFor(label, detail, path, key string, prepared fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) paletteCandidate {
	// A bare prefix such as "#" lists its whole scope. An empty prepared
	// query scores zero in VS Code's scorer, so give these rows an equal
	// standing rank and let recency order them as it does a real match.
	if prepared.Normalized == "" {
		return paletteCandidate{
			key: key, label: label, detail: detail, path: path,
			score: 1, itemScore: &fuzzy.IItemScore{Score: 1}, enabled: true,
		}
	}
	score := scorePaletteItem(paletteScoreItem{label: label, detail: detail, path: path}, prepared, cache)
	return paletteCandidate{
		key: key, label: label, detail: detail, path: path,
		score: score.Score, matches: score.LabelMatch,
		detailMatches: score.DescriptionMatch, itemScore: score, enabled: true,
	}
}

// paletteQueryKey distinguishes queries that score differently even when
// their normalized text is the same: quoted contiguous pieces and path
// separators change how VS Code's scorer treats them.
func paletteQueryKey(query fuzzy.IPreparedQuery) string {
	var key strings.Builder
	appendPart := func(s string) {
		key.WriteString(strconv.Itoa(len(s)))
		key.WriteByte(':')
		key.WriteString(s)
		key.WriteByte('|')
	}
	appendPart(query.Normalized)
	appendPart(strconv.FormatBool(query.ExpectContiguousMatch))
	appendPart(strconv.FormatBool(query.ContainsPathSeparator))
	pieces := query.Values
	if len(pieces) == 0 {
		pieces = []fuzzy.IPreparedQueryPiece{query.IPreparedQueryPiece}
	}
	for _, piece := range pieces {
		appendPart(piece.Normalized)
		appendPart(strconv.FormatBool(piece.ExpectContiguousMatch))
	}
	return key.String()
}

// paletteVarsGen is the environment/globals generation for the asset cache.
// The palette only names environments and variables, so the signature hashes
// names rather than values. Direct map edits need no explicit bump.
func (a *app) paletteVarsGen() uint64 {
	hash := fnv.New64a()
	write := func(s string) {
		hash.Write([]byte(s))
		hash.Write([]byte{0})
	}
	// Map order is not stable, so hash each name and combine the results
	// commutatively. No sorted key slices, and no value ever enters the hash.
	names := func(values map[string]string) (uint64, int) {
		var digest uint64
		count := 0
		for name := range values {
			h := fnv.New64a()
			h.Write([]byte(name))
			digest ^= h.Sum64()
			count++
		}
		return digest, count
	}
	writeNumber := func(value uint64) {
		var bytes [8]byte
		for i := range 8 {
			bytes[i] = byte(value >> (i * 8))
		}
		hash.Write(bytes[:])
	}

	write(strconv.Itoa(len(a.envs)))
	for _, env := range a.envs {
		write(env.Name)
		for _, values := range []map[string]string{env.Vars, env.Current} {
			digest, count := names(values)
			writeNumber(uint64(count))
			writeNumber(digest)
		}
	}
	for _, values := range []map[string]string{a.globals, a.globalsCurrent} {
		digest, count := names(values)
		writeNumber(uint64(count))
		writeNumber(digest)
	}
	return hash.Sum64()
}

// paletteAssetsFor returns scored, zero-filtered and ranked resource
// candidates. An unchanged query on a frame costs a map lookup.
func (a *app) paletteAssetsFor(query fuzzy.IPreparedQuery, scope paletteScope) paletteAssetResult {
	key := paletteAssetCacheKey{
		query:      paletteQueryKey(query),
		scope:      scope,
		requestGen: a.requestGen,
		varsGen:    a.paletteVarsGen(),
		historyGen: a.workbench.paletteHistoryGen,
	}
	if cached, ok := a.paletteAssets[key]; ok {
		return cached
	}
	if a.paletteAssets == nil {
		a.paletteAssets = make(map[paletteAssetCacheKey]paletteAssetResult)
	}
	if len(a.paletteAssets) >= paletteAssetCacheLimit {
		clear(a.paletteAssets)
	}

	cache := fuzzy.NewFuzzyScorerCache()
	candidates := a.paletteAssetCandidates(query, cache, scope)
	candidates = slices.DeleteFunc(candidates, func(c paletteCandidate) bool { return c.score == 0 })
	sortPaletteCandidates(candidates, a.workbench.paletteHistoryRanks(), query)
	result := paletteAssetResult{
		candidates: candidates,
		overflow:   max(0, len(candidates)-paletteResultLimit),
	}
	a.paletteAssets[key] = result
	return result
}

// paletteAssetCandidates scores the collections, requests, environments and
// variables in scope. No rows or closures are created here, so a broad query
// does not allocate thousands of entries that would never be shown.
func (a *app) paletteAssetCandidates(prepared fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache, scope paletteScope) []paletteCandidate {
	idx := a.requestSearch()
	candidates := make([]paletteCandidate, 0, len(idx.paths)+len(idx.requests))
	if scope == paletteMixed || scope == paletteRequestsOnly {
		if scope == paletteMixed {
			for _, p := range idx.paths {
				detail := p.kind + " · " + p.path
				if p.description != "" {
					detail += " · " + p.description
				}
				c := paletteCandidateFor(p.label, detail, p.path, "path:"+p.path, prepared, cache)
				c.kind, c.resource, c.meta = paletteRevealPath, true, p.kind
				// ScoreItemFuzzy appends a separator to a description when a path is
				// present, so its detail offsets can run one rune past the displayed
				// detail. Skip description highlighting on path rows instead.
				c.detailMatches = nil
				candidates = append(candidates, c)
			}
		}
		for _, r := range idx.requests {
			c := paletteCandidateFor(r.label, r.detail, "", "request:"+r.key, prepared, cache)
			c.kind, c.resource, c.meta, c.request = paletteOpenRequest, true, locales.Request(), r.key
			candidates = append(candidates, c)
		}
	}
	if scope == paletteMixed || scope == paletteConfigOnly {
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
		score := scorePaletteItem(paletteScoreItem{label: item.label}, query, cache)
		if score.Score == 0 {
			continue
		}
		candidates = append(candidates, paletteCandidate{
			kind: paletteRunAction, key: "command:" + item.id, label: item.label,
			detail: item.why(), meta: item.shortcut.keycap(), why: item.why(), action: item,
			enabled: item.enabled, isAction: true,
			score: score.Score, matches: score.LabelMatch, itemScore: score,
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
	w.paletteHistoryGen++
}

func comparePaletteCandidates(x, y paletteCandidate, ranks map[string]int, query fuzzy.IPreparedQuery) int {
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
	// Below recency, quick open uses VS Code's full comparator: prefix and
	// compact label matches, then shorter labels, then the fuzzy score. The
	// scores were computed once while building the candidates.
	if x.itemScore == nil || y.itemScore == nil {
		return cmp.Compare(x.label, y.label)
	}
	return fuzzy.CompareItemScores(
		paletteScoreItem{label: x.label, detail: x.detail, path: x.path},
		paletteScoreItem{label: y.label, detail: y.detail, path: y.path},
		x.itemScore, y.itemScore, query, paletteAccessor{},
	)
}

func sortPaletteCandidates(candidates []paletteCandidate, ranks map[string]int, query fuzzy.IPreparedQuery) {
	slices.SortStableFunc(candidates, func(x, y paletteCandidate) int {
		return comparePaletteCandidates(x, y, ranks, query)
	})
}

func (c paletteCandidate) entry(a *app) paletteEntry {
	entry := paletteEntry{
		label: c.label, detail: c.detail, meta: c.meta, why: c.why,
		enabled: c.enabled, action: c.isAction, matches: c.matches,
		detailMatches: c.detailMatches,
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

// paletteMergeFinalize merges two already ranked candidate lists without
// allocating a row for every possible asset. Only rows that fit become
// paletteEntry values; extra resources are counted for the overflow caption.
func (a *app) paletteMergeFinalize(assets paletteAssetResult, commands []paletteCandidate, ranks map[string]int, query fuzzy.IPreparedQuery) paletteSearch {
	out := paletteSearch{more: assets.overflow}
	assetIndex, commandIndex, resources := 0, 0, 0
	for assetIndex < len(assets.candidates) || commandIndex < len(commands) {
		takeAsset := commandIndex >= len(commands) ||
			assetIndex < len(assets.candidates) &&
				comparePaletteCandidates(assets.candidates[assetIndex], commands[commandIndex], ranks, query) <= 0
		if takeAsset {
			candidate := assets.candidates[assetIndex]
			assetIndex++
			resources++
			if resources > paletteResultLimit {
				// The remaining resource candidates cannot be shown. Every
				// remaining command ranks below the last shown row, so append
				// them and stop instead of walking thousands of assets.
				for ; commandIndex < len(commands); commandIndex++ {
					if commands[commandIndex].score != 0 {
						out.entries = append(out.entries, commands[commandIndex].entry(a))
					}
				}
				return out
			}
			out.entries = append(out.entries, candidate.entry(a))
			continue
		}
		candidate := commands[commandIndex]
		commandIndex++
		if candidate.score != 0 {
			out.entries = append(out.entries, candidate.entry(a))
		}
	}
	return out
}

// paletteHelpItems are the quick-access prefixes, in the order VS Code lists
// its providers. Choosing one fills the field with the prefix rather than
// running a command, leaving the rest of the query to the reader. They are
// built on use, so the app's language is read when the palette renders.
func paletteHelpItems() []struct {
	prefix, label, detail string
} {
	return []struct {
		prefix, label, detail string
	}{
		{">", ">  " + locales.QuickAccessCommands(), locales.QuickAccessRunACommand()},
		{"#", "#  " + locales.QuickAccessRequests(), locales.QuickAccessOpenASavedRequest()},
		{"$", "$  " + locales.QuickAccessEnvironmentsAndVariables(), locales.QuickAccessEditAnEnvironmentOrVariable()},
		{"?", "?  " + locales.QuickAccessQuickAccess(), locales.QuickAccessShowThesePrefixes()},
	}
}

func (a *app) paletteHelp(q paletteQuery) paletteSearch {
	prepared := fuzzy.PrepareQuery(q.text)
	cache := fuzzy.NewFuzzyScorerCache()
	items := paletteHelpItems()
	ranks := a.workbench.paletteHistoryRanks()
	if prepared.Normalized == "" {
		// Equal standing scores would sort by label length; VS Code lists
		// providers in registration order until one has been used.
		if ranks == nil {
			ranks = make(map[string]int)
		}
		for i, item := range items {
			if _, used := ranks["help:"+item.prefix]; !used {
				ranks["help:"+item.prefix] = i
			}
		}
	}
	candidates := make([]paletteCandidate, 0, len(items))
	for _, item := range items {
		c := paletteCandidateFor(item.label, item.detail, "", "help:"+item.prefix, prepared, cache)
		c.kind, c.resource = paletteRunAction, false
		candidates = append(candidates, c)
	}
	sortPaletteCandidates(candidates, ranks, prepared)
	var result paletteSearch
	for _, c := range candidates {
		if c.score == 0 {
			continue
		}
		prefix, _ := strings.CutPrefix(c.key, "help:")
		entry := c.entry(a)
		remember := entry.run
		entry.run = func() {
			if remember != nil {
				remember()
			}
			w := &a.workbench
			w.query, w.command = prefix, 0
			w.commands.ScrollTo(0, ui.Start)
		}
		result.entries = append(result.entries, entry)
	}
	return result
}

// paletteSearch finds collections, folders, requests, environments and
// variable names with VS Code's fuzzy scorer. Variable values are never part
// of a label, detail or score, so typing a secret cannot reveal it exists.
func (a *app) paletteSearch(query string) paletteSearch {
	q := splitPaletteQuery(query)
	switch q.scope {
	case paletteMixed:
		return a.paletteSearchMixed(query)
	case paletteHelp:
		return a.paletteHelp(q)
	case paletteCommandsOnly:
		return paletteSearch{}
	}
	prepared := fuzzy.PrepareQuery(q.text)
	assets := a.paletteAssetsFor(prepared, q.scope)
	visible := assets.candidates
	if len(visible) > paletteResultLimit {
		visible = visible[:paletteResultLimit]
	}
	out := a.paletteFinalize(visible)
	out.more = assets.overflow
	return out
}

func (a *app) paletteSearchMixed(query string) paletteSearch {
	prepared := fuzzy.PrepareQuery(strings.TrimSpace(query))
	if prepared.Normalized == "" {
		return paletteSearch{}
	}
	assets := a.paletteAssetsFor(prepared, paletteMixed)
	visible := assets.candidates
	if len(visible) > paletteResultLimit {
		visible = visible[:paletteResultLimit]
	}
	out := a.paletteFinalize(visible)
	out.more = assets.overflow
	return out
}

// paletteRankedResults ranks commands and saved entries together. An
// unprefixed query keeps the mixed list; a quick-access prefix narrows it to
// one scope without letting the prefix take part in the fuzzy query.
func (a *app) paletteRankedResults(query string, actions []action) paletteSearch {
	q := splitPaletteQuery(query)
	switch q.scope {
	case paletteMixed:
		return a.paletteRankedMixed(query, actions)
	case paletteHelp:
		return a.paletteHelp(q)
	default:
		return a.paletteRankedScoped(q, actions)
	}
}

func paletteEnabledCommands(actions []action, ranks map[string]int, query fuzzy.IPreparedQuery) []paletteCandidate {
	var enabled []paletteCandidate
	for _, item := range actions {
		if item.id == "app.palette" || !item.enabled || len(item.items) > 0 {
			continue
		}
		enabled = append(enabled, paletteCandidate{
			kind: paletteRunAction, key: "command:" + item.id, label: item.label,
			meta: item.shortcut.keycap(), action: item, enabled: true, isAction: true, score: 1,
			itemScore: &fuzzy.IItemScore{Score: 1},
		})
	}
	sortPaletteCandidates(enabled, ranks, query)
	return enabled
}

// paletteRankedMixed is VS Code's unprefixed quick access: the commands that
// can run now for an empty query, otherwise commands and library entries
// ranked together. Unavailable commands stay last, to explain why.
func (a *app) paletteRankedMixed(query string, actions []action) paletteSearch {
	prepared := fuzzy.PrepareQuery(strings.TrimSpace(query))
	cache := fuzzy.NewFuzzyScorerCache()
	ranks := a.workbench.paletteHistoryRanks()
	if prepared.Normalized == "" {
		return a.paletteFinalize(paletteEnabledCommands(actions, ranks, prepared))
	}

	commands := paletteCommandCandidates(actions, true, prepared, cache)
	sortPaletteCandidates(commands, ranks, prepared)
	result := a.paletteMergeFinalize(a.paletteAssetsFor(prepared, paletteMixed), commands, ranks, prepared)

	unavailable := paletteCommandCandidates(actions, false, prepared, cache)
	sortPaletteCandidates(unavailable, ranks, prepared)
	result.entries = append(result.entries, a.paletteFinalize(unavailable).entries...)
	return result
}

// paletteRankedScoped applies one prefix. Assets and commands are generated
// only for the scope in play, so a "#" search cannot be crowded out by
// commands or have its overflow count inflated by variables.
func (a *app) paletteRankedScoped(q paletteQuery, actions []action) paletteSearch {
	prepared := fuzzy.PrepareQuery(q.text)
	cache := fuzzy.NewFuzzyScorerCache()
	ranks := a.workbench.paletteHistoryRanks()
	if q.scope == paletteCommandsOnly {
		var commands []paletteCandidate
		if prepared.Normalized == "" {
			commands = paletteEnabledCommands(actions, ranks, prepared)
		} else {
			commands = paletteCommandCandidates(actions, true, prepared, cache)
			sortPaletteCandidates(commands, ranks, prepared)
			unavailable := paletteCommandCandidates(actions, false, prepared, cache)
			sortPaletteCandidates(unavailable, ranks, prepared)
			commands = append(commands, unavailable...)
		}
		return a.paletteFinalize(commands)
	}
	assets := a.paletteAssetsFor(prepared, q.scope)
	visible := assets.candidates
	if len(visible) > paletteResultLimit {
		visible = visible[:paletteResultLimit]
	}
	result := a.paletteFinalize(visible)
	result.more = assets.overflow
	return result
}

// Geist's semibold step is 600; uikit only names the 500 medium role, so the
// highlight keeps its own weight constant. The theme supplies all colors.
const paletteMatchWeight = 600

func paletteHighlightInk(c *ui.Context, enabled bool) ui.Color {
	palette := uikit.Colors(c.Theme())
	if !enabled {
		return palette.Faint
	}
	return palette.Primary
}

// paletteSpans splits plain into spans that always concatenate back to plain.
// The matched runs are ink at semibold weight: no background chip, so the
// selected row's tint shows through.
func paletteSpans(plain string, matches []fuzzy.Match, ink ui.Color, size float32) []ui.Span {
	runes := []rune(plain)
	spans := make([]ui.Span, 0, len(matches)*2+1)
	at := 0
	for _, match := range matches {
		if match.Start < at || match.End <= match.Start || match.End > len(runes) {
			continue
		}
		if match.Start > at {
			spans = append(spans, ui.Span{Text: string(runes[at:match.Start]), Size: size})
		}
		spans = append(spans, ui.Span{
			Text: string(runes[match.Start:match.End]), Size: size,
			Color: ink, Weight: paletteMatchWeight,
		})
		at = match.End
	}
	if at < len(runes) {
		spans = append(spans, ui.Span{Text: string(runes[at:]), Size: size})
	}
	return spans
}

// paletteLabel draws the row label with fuzzy's rune-offset matches. RichText
// keeps one readable label for the tester and assistive technology.
func paletteLabel(c *ui.Context, item paletteEntry, color ui.Color) ui.Element {
	if len(item.matches) == 0 {
		return uikit.Body(c, item.label).TextColor(color)
	}
	spans := paletteSpans(item.label, item.matches, paletteHighlightInk(c, item.enabled), uikit.Scaled(uikit.TextBody))
	return ui.RichText(c, spans...).TextColor(color).Label(item.label)
}

// paletteDetail keeps the muted small style for unmatched text. Only rows
// whose scored description is exactly this detail carry detailMatches.
func paletteDetail(c *ui.Context, item paletteEntry) ui.Element {
	muted := c.Theme().TextMuted
	if len(item.detailMatches) == 0 {
		return uikit.Small(c, item.detail).TextColor(muted)
	}
	spans := paletteSpans(item.detail, item.detailMatches, paletteHighlightInk(c, item.enabled), uikit.Scaled(uikit.TextSmall))
	return ui.RichText(c, spans...).TextColor(muted).LineHeight(uikit.LineSmall).Label(item.detail)
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
