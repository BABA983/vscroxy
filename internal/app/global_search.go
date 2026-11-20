package app

import (
	"cmp"
	"context"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/locales"
)

// One query searches the app's own assets, not a file system. The palette
// (palette_search.go) ranks names with VS Code's fuzzy scorer; this view
// searches the text of a field line by line with the body find widget's
// options. They share the find toggles and the highlighted-run drawing, but
// not a matcher: quick open's subsequence score would miss text inside a
// body or a script.
type searchSource uint8

const (
	searchCollections searchSource = iota
	searchEnvironments
	searchFlows
)

func (source searchSource) label() string {
	switch source {
	case searchCollections:
		return locales.Collections()
	case searchEnvironments:
		return locales.QuickAccessEnvironmentsAndVariables()
	default:
		return locales.CapturedRequests()
	}
}

// Values whose replacement is a whole new string rather than a snippet at a
// byte range: a variable's Initial/Current cell, a collection row's name and
// a folder's local Current value. They live in maps; fieldRefs own text.
const (
	searchValueEnvInitial    = "env-initial"
	searchValueEnvCurrent    = "env-current"
	searchValueGlobalInitial = "global-initial"
	searchValueGlobalCurrent = "global-current"
	searchValueFolderDef     = "folder-def"
	searchValueFolderCurrent = "folder-current"
	folderDefinitionOwner    = searchValueFolderDef
	searchFieldName          = "name"
)

// searchTarget says where a match may be written. Flows and read-only
// manifestations carry no target: they can be opened, never replaced.
type searchTarget struct {
	owner, key, code, collection string
	// valueKind is empty for a text field, or one of searchValue* above.
	valueKind string
	// base is the display line's byte offset in the authored field text.
	base int
	// start/end are byte offsets in the full field or value, not in the line.
	start, end int
	index      int // folder variable row
}

// searchFieldKind routes a result to the editor that reveals it. The label a
// result shows is localized; the kind is not, so changing the app's language
// between searching and clicking cannot misdirect a reveal.
type searchFieldKind uint8

const (
	searchFieldOther searchFieldKind = iota
	searchFieldRequestBody
	searchFieldPreScript
	searchFieldPostScript
	searchFieldHeaders
	searchFieldDescription
	searchFieldFlowBody
)

// searchResult is one matching line. start and end are byte offsets in text,
// so a result can be highlighted without keeping the whole source string.
type searchResult struct {
	source   searchSource
	key      string // saved request key, environment name or flow id
	resource string
	field    string // catalog label for display
	kind     searchFieldKind
	text     string
	start    int
	end      int
	line     int // one-based
	env      string
	variable string
	flowID   int
	side     int // 0 request, 1 response
	// fieldText is the whole authored value the match came from, kept for
	// stale-range checks when a change is planned.
	fieldText string
	target    searchTarget
}

func (r searchResult) match() string { return r.text[r.start:r.end] }

// replaceable says whether a result can reach an authored, editable source.
// Captured traffic, run history and other read-only results have no target.
func (r searchResult) replaceable() bool {
	return r.target.owner != ""
}

// searchBatch carries one streamed page of results. Its generation is checked
// against the current query before anything is shown: a worker from an older
// query may finish after a newer one and must not add stale rows.
type searchBatch struct {
	gen     uint64
	results []searchResult
	done    bool
	limited int
}

// searchQuery is the compiled form of the Search view's query. Regexp syntax
// and case behaviour follow MyGo's editor find bar (RE2, (?i), (?m)).
type searchQuery struct {
	text      string
	regexp    *regexp.Regexp
	lower     string
	caseSens  bool
	wholeWord bool
}

func compileSearchQuery(input string, target findTarget) (searchQuery, error) {
	q := searchQuery{
		text:      input,
		caseSens:  target != nil && *target.caseOption(),
		wholeWord: target != nil && *target.wordOption(),
	}
	if input == "" {
		return q, nil
	}
	if target != nil && *target.regexpOption() {
		pattern := input
		if !q.caseSens {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile("(?m)" + pattern)
		if err != nil {
			return searchQuery{}, err
		}
		q.regexp = re
		return q, nil
	}
	if !q.caseSens {
		q.lower = asciiLower(input)
	}
	return q, nil
}

// asciiLower keeps byte offsets stable for case-insensitive literal search,
// as the editor's finder does. Non-ASCII text still matches through RE2's
// case-insensitive literal below.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// find returns the first match of the query in one line, as byte offsets.
func (q searchQuery) find(line string) (int, int, bool) {
	if q.text == "" {
		return 0, 0, false
	}
	var found [2]int
	switch {
	case q.regexp != nil:
		m := q.regexp.FindStringIndex(line)
		if m == nil || m[1] == m[0] {
			return 0, 0, false
		}
		found = [2]int{m[0], m[1]}
	case q.caseSens || q.lower != "":
		needle, text := q.text, line
		if q.lower != "" {
			needle, text = q.lower, asciiLower(line)
		}
		at := strings.Index(text, needle)
		if at < 0 {
			return 0, 0, false
		}
		found = [2]int{at, at + len(needle)}
	default:
		re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(q.text))
		m := re.FindStringIndex(line)
		if m == nil || m[1] == m[0] {
			return 0, 0, false
		}
		found = [2]int{m[0], m[1]}
	}
	if q.wholeWord && !isWholeSearchWord(line, found[0], found[1]) {
		return 0, 0, false
	}
	return found[0], found[1], true
}

// expand follows Go's regexp.Expand semantics for regexp searches: $1 and
// ${name} name capture groups, $$ is a literal dollar and an unknown name
// expands to nothing. Literal searches insert the replacement as written.
func (q searchQuery) expand(line string, from, to int, template string) string {
	if q.regexp == nil {
		return template
	}
	for _, m := range q.regexp.FindAllStringSubmatchIndex(line, -1) {
		if m[0] == from && m[1] == to {
			return string(q.regexp.ExpandString(nil, template, line, m))
		}
	}
	return template
}

func isWholeSearchWord(line string, from, to int) bool {
	return !searchWordAt(line, from-1) && !searchWordAt(line, to)
}

func searchWordAt(s string, at int) bool {
	if at < 0 || at >= len(s) {
		return false
	}
	start := at
	for start > 0 && !utf8.RuneStart(s[start]) {
		start--
	}
	r, _ := utf8.DecodeRuneInString(s[start:])
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// searchSecrets keeps the values sealed by internal/vault out of search. A
// line that contains one is neither searched nor shown, so neither the line
// nor a match range can leak it into the results.
type searchSecrets struct {
	lower []string
}

func newSearchSecrets(values []string) searchSecrets {
	var f searchSecrets
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			f.lower = append(f.lower, strings.ToLower(trimmed))
		}
	}
	return f
}

func (f searchSecrets) safe(line string) bool {
	if len(f.lower) == 0 {
		return true
	}
	lower := strings.ToLower(line)
	for _, secret := range f.lower {
		if strings.Contains(lower, secret) {
			return false
		}
	}
	return true
}

// resource masks a label built from the same fields we search: a result's
// heading must not carry a secret that its matching line had to skip.
func (f searchSecrets) resource(label string) string {
	if f.safe(label) {
		return label
	}
	return locales.SearchHiddenResource()
}

func (f searchSecrets) key(value string) string {
	if f.safe(value) {
		return value
	}
	return ""
}

// searchSnapshot is the data a worker reads, copied on the UI thread. Bodies
// held in spill files are read by the worker through a copied flow, never
// while a frame is being built.
type searchSnapshot struct {
	query          searchQuery
	secrets        searchSecrets
	sources        []searchSourceSnapshot
	envs           []environment
	globals        map[string]string
	globalsCurrent map[string]string
	folderCurrent  map[string]map[string]string
	flows          []flow
	limit          int
	batch          int
}

// searchSourceSnapshot is an authored resource copied on the UI thread. The
// same field walkers that back the variable editor supply the targets: an
// open draft replaces its saved copy, and a save conflict cannot move the
// ranges the worker is reading.
type searchSourceSnapshot struct {
	owner, key, resource, collection string
	fields                           []searchField
}

type searchField struct {
	label string
	kind  searchFieldKind
	code  string
	text  string
}

func (a *app) searchSnapshot(query searchQuery) searchSnapshot {
	flows := slices.Clone(a.flows)
	for i := range flows {
		flows[i].columnData = nil
	}
	values := make([]string, 0, len(a.vault.secrets))
	for _, secret := range a.vault.secrets {
		values = append(values, secret.Value)
	}
	sources := make([]searchSourceSnapshot, 0, len(a.documents)+len(a.requests)+len(a.folders))
	for _, source := range a.variableSources() {
		fields := []searchField{{locales.Name(), searchFieldOther, searchFieldName, source.value.Name}}
		for _, f := range source.fields() {
			fields = append(fields, searchField{f.field, searchCodeKind(f.code), f.code, f.text})
		}
		sources = append(sources, searchSourceSnapshot{owner: source.owner, key: source.key,
			resource: source.resource, collection: source.collection, fields: fields})
	}
	for _, folder := range a.folders {
		fields := make([]searchField, 0, len(folder.Variables)+3)
		for _, f := range variableFolderFields(folder) {
			fields = append(fields, searchField{f.field, searchCodeKind(f.code), f.code, f.text})
		}
		for i, v := range folder.Variables {
			name := strings.TrimSpace(v.Name)
			if name != "" {
				fields = append(fields, searchField{locales.Definition(), searchFieldOther,
					folderDefinitionOwner + ":" + strconv.Itoa(i), name})
			}
		}
		sources = append(sources, searchSourceSnapshot{owner: "folder", key: folder.Path,
			resource: folder.Path, collection: folder.Path, fields: fields})
	}
	current := map[string]map[string]string{}
	for path, values := range a.folderCurrent {
		current[path] = maps.Clone(values)
	}
	return searchSnapshot{
		query:          query,
		secrets:        newSearchSecrets(values),
		sources:        sources,
		envs:           slices.Clone(a.envs),
		globals:        maps.Clone(a.globals),
		globalsCurrent: maps.Clone(a.globalsCurrent),
		folderCurrent:  current,
		flows:          flows,
		limit:          globalSearchLimit,
		batch:          globalSearchBatchSize,
	}
}

// searchCodeKind maps a variable field code onto the reveal target the
// Search view already uses; an unmapped authored field still opens its tab.
func searchCodeKind(code string) searchFieldKind {
	switch {
	case code == problemFieldBody, strings.HasPrefix(code, "body-"):
		return searchFieldRequestBody
	case code == problemFieldPre:
		return searchFieldPreScript
	case code == problemFieldPost:
		return searchFieldPostScript
	case code == problemFieldHeaderRaw, strings.HasPrefix(code, "header-"):
		return searchFieldHeaders
	case code == problemFieldDescription:
		return searchFieldDescription
	}
	return searchFieldOther
}

// scanSearch streams matching lines source by source. It never touches app
// state: results reach the UI through batches that carry the query's
// generation. Returning early on cancellation drops the rest of the scan.
func scanSearch(ctx context.Context, s searchSnapshot, gen uint64, sink chan<- searchBatch, notify func()) {
	if s.limit <= 0 {
		s.limit = globalSearchLimit
	}
	if s.batch <= 0 {
		s.batch = globalSearchBatchSize
	}
	total, limited := 0, false
	pending := make([]searchResult, 0, s.batch)
	send := func(done bool) bool {
		if len(pending) > 0 {
			batch := searchBatch{gen: gen, results: pending}
			pending = make([]searchResult, 0, s.batch)
			select {
			case <-ctx.Done():
				return false
			case sink <- batch:
				if !done && notify != nil {
					notify()
				}
			}
		}
		if done {
			select {
			case <-ctx.Done():
				return false
			case sink <- searchBatch{gen: gen, done: true, limited: boolInt(limited)}:
				if notify != nil {
					notify()
				}
			}
		}
		return true
	}
	add := func(r searchResult) bool {
		pending = append(pending, r)
		total++
		if len(pending) >= s.batch {
			if !send(false) {
				return false
			}
		}
		if total >= s.limit {
			limited = true
			send(false) // no rows past the cap
			return false
		}
		return true
	}

	for _, scan := range []func(context.Context, searchSnapshot, func(searchResult) bool) bool{
		scanAuthoredSources, scanEnvironments, scanFlows,
	} {
		if ctx.Err() != nil {
			return
		}
		if limited || !scan(ctx, s, add) {
			break
		}
	}
	send(true)
}

func boolInt(on bool) int {
	if on {
		return 1
	}
	return 0
}

// scanAuthoredSources walks saved requests, open drafts and folders in the
// order the library shows them, so the UI can group adjacent results under
// one resource. Every result carries the fieldRef target the bulk edit needs.
func scanAuthoredSources(ctx context.Context, s searchSnapshot, add func(searchResult) bool) bool {
	for _, source := range s.sources {
		if ctx.Err() != nil {
			return false
		}
		key, resource := s.secrets.key(source.key), s.secrets.resource(source.resource)
		for _, field := range source.fields {
			label, kind, code := field.label, field.kind, field.code
			if !scanField(ctx, s, func(line string, number, base, from, to int) searchResult {
				r := searchResult{source: searchCollections, key: key, resource: resource,
					field: label, kind: kind, text: line, start: from, end: to, line: number,
					fieldText: field.text}
				r.target = searchTarget{owner: source.owner, key: source.key, code: code,
					collection: source.collection, base: base, start: base + from, end: base + to}
				if at, ok := strings.CutPrefix(code, folderDefinitionOwner+":"); ok {
					index, _ := strconv.Atoi(at)
					r.target.owner, r.target.code, r.target.index = folderDefinitionOwner, "row", index
					r.target.valueKind, r.target.start, r.target.end = folderDefinitionOwner, from, to
				}
				return r
			}, add, field.text) {
				return false
			}
		}
	}
	return true
}

// scanEnvironments includes environment names, variable values and globals.
// A credential or conventionally sensitive variable keeps its name searchable
// but its value is excluded, as it was before Search Replace: a match must
// never become an oracle for guessing a secret.
func scanEnvironments(ctx context.Context, s searchSnapshot, add func(searchResult) bool) bool {
	envs := slices.Clone(s.envs)
	slices.SortFunc(envs, func(a, b environment) int { return strings.Compare(a.Name, b.Name) })
	for _, env := range envs {
		if ctx.Err() != nil {
			return false
		}
		name, key := s.secrets.resource(env.Name), s.secrets.key(env.Name)
		if !scanField(ctx, s, func(line string, number, base, from, to int) searchResult {
			return searchResult{source: searchEnvironments, key: key, resource: name,
				field: locales.Name(), text: line, start: from, end: to, line: number, env: env.Name}
		}, add, env.Name) {
			return false
		}
		for _, variable := range slices.Sorted(maps.Keys(env.Vars)) {
			sensitive := env.Credentials[variable] || sensitiveVariable(variable)
			if !scanVariable(ctx, s, add, env.Name, variable, locales.Initial(),
				env.Vars[variable], searchValueEnvInitial, sensitive) {
				return false
			}
		}
		for _, variable := range slices.Sorted(maps.Keys(env.Current)) {
			sensitive := env.Credentials[variable] || sensitiveVariable(variable)
			if !scanVariable(ctx, s, add, env.Name, variable, locales.Current(),
				env.Current[variable], searchValueEnvCurrent, sensitive) {
				return false
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.globals)) {
		if !scanVariable(ctx, s, add, "", name, locales.GlobalVariable(),
			s.globals[name], searchValueGlobalInitial, sensitiveVariable(name)) {
			return false
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.globalsCurrent)) {
		if !scanVariable(ctx, s, add, "", name, locales.GlobalVariable(),
			s.globalsCurrent[name], searchValueGlobalCurrent, sensitiveVariable(name)) {
			return false
		}
	}
	for _, path := range slices.Sorted(maps.Keys(s.folderCurrent)) {
		for _, name := range slices.Sorted(maps.Keys(s.folderCurrent[path])) {
			if !scanVariable(ctx, s, add, path, name, locales.Current(),
				s.folderCurrent[path][name], searchValueFolderCurrent, sensitiveVariable(name)) {
				return false
			}
		}
	}
	return true
}

// scanVariable searches "name = value" as one line. A match in the name is a
// name result; a match in the value targets the map cell or folder row. For a
// credential or conventionally sensitive variable only the name is searched,
// so neither the value's bytes nor its length can be inferred from Search.
func scanVariable(ctx context.Context, s searchSnapshot, add func(searchResult) bool, env, name, field, value, owner string, sensitive bool) bool {
	text := name
	valueAt := len(name)
	if !sensitive && value != "" {
		text += " = " + value
		valueAt += 3
	}
	key, resource := s.secrets.key(env), s.secrets.resource(cmp.Or(env, locales.GlobalVariable()))
	return scanField(ctx, s, func(line string, number, base, from, to int) searchResult {
		r := searchResult{source: searchEnvironments, key: key, resource: resource,
			field: field, text: line, start: from, end: to, line: number, env: env, variable: name}
		if from >= valueAt {
			r.target = searchTarget{owner: owner, key: env, code: name, valueKind: owner,
				start: from - valueAt, end: to - valueAt, index: 0}
			r.fieldText = value
		} else {
			// A name match points at no editable cell; the key is not a
			// value text field and is left to the variable editor's rename.
			r.text = name
			r.end = min(to, len(name))
		}
		return r
	}, add, text)
}

// scanFlows searches captured requests read-only: no result from here carries
// an edit target, so neither Replace nor Replace All can offer a change.
func scanFlows(ctx context.Context, s searchSnapshot, add func(searchResult) bool) bool {
	for i := range s.flows {
		f := s.flows[i]
		if ctx.Err() != nil {
			return false
		}
		resource := s.secrets.resource(cmp.Or(f.method, "GET") + " " + f.url)
		key := s.secrets.key(strconv.Itoa(f.id))
		fields := []struct {
			label string
			kind  searchFieldKind
			text  string
			side  int
		}{
			{locales.Url(), searchFieldOther, f.url, -1},
			{locales.RequestHeaders(), searchFieldOther, f.reqHeaders, -1},
			{locales.ResponseHeaders(), searchFieldOther, f.resHeaders, -1},
		}
		for side := range 2 {
			body, _ := f.bodyPreview(side == 1)
			label := locales.RequestBody()
			if side == 1 {
				label = locales.ResponseBody()
			}
			fields = append(fields, struct {
				label string
				kind  searchFieldKind
				text  string
				side  int
			}{label, searchFieldFlowBody, body, side})
		}
		for _, field := range fields {
			side, label, kind := field.side, field.label, field.kind
			if !scanField(ctx, s, func(line string, number, base, from, to int) searchResult {
				return searchResult{source: searchFlows, key: key, resource: resource,
					field: label, kind: kind, text: line, start: from, end: to, line: number,
					flowID: f.id, side: side}
			}, add, field.text) {
				return false
			}
		}
	}
	return true
}

// scanField keeps one match per line, as the Search view always has. A field
// containing a vault value is skipped whole: a multi-line secret has no line
// that holds all of it, so checking lines alone would leak every line of a
// pasted key or JSON credential.
func scanField(ctx context.Context, s searchSnapshot, result func(string, int, int, int, int) searchResult, add func(searchResult) bool, text string) bool {
	if text == "" || !s.secrets.safe(text) {
		return true
	}
	number, base := 0, 0
	for line := range strings.Lines(text) {
		number++
		if ctx.Err() != nil {
			return false
		}
		raw := line
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		lineBase := base
		// strings.Lines yields the terminator with the line. Advancing by
		// the trimmed length plus one would miss CRLF's extra byte and
		// shift every later match one byte earlier per preceding line.
		base += len(raw)
		from, to, ok := s.query.find(line)
		if !ok {
			continue
		}
		if !add(result(line, number, lineBase, from, to)) {
			return false
		}
	}
	return true
}
