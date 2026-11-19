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
}

func (r searchResult) match() string { return r.text[r.start:r.end] }

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
	requests       []savedRequest
	envs           []environment
	globals        map[string]string
	globalsCurrent map[string]string
	flows          []flow
	limit          int
	batch          int
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
	return searchSnapshot{
		query:          query,
		secrets:        newSearchSecrets(values),
		requests:       slices.Clone(a.requests),
		envs:           slices.Clone(a.envs),
		globals:        maps.Clone(a.globals),
		globalsCurrent: maps.Clone(a.globalsCurrent),
		flows:          flows,
		limit:          globalSearchLimit,
		batch:          globalSearchBatchSize,
	}
}

type searchField struct {
	label string
	kind  searchFieldKind
	text  string
}

// searchFieldsOf are the authored text parts of one saved request. A field is
// labelled with a catalog message and carries a stable kind for revealing;
// its value is kept as written, including {{variables}}, which is what the
// collection stores.
func searchFieldsOf(r savedRequest) []searchField {
	return []searchField{
		{locales.Name(), searchFieldOther, r.Name},
		{locales.Url(), searchFieldOther, r.URL},
		{locales.Headers(), searchFieldHeaders, r.Headers},
		{locales.RequestBody(), searchFieldRequestBody, r.Body},
		{locales.PreRequestScript(), searchFieldPreScript, r.Pre},
		{locales.PostResponseScript(), searchFieldPostScript, r.Post},
		{locales.Description(), searchFieldDescription, r.Description},
		{locales.GraphqlQuery(), searchFieldOther, r.GraphQL.Query},
		{locales.GraphqlVariables(), searchFieldOther, r.GraphQL.Variables},
		{locales.GrpcMethod(), searchFieldOther, r.GRPC.Method},
	}
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
		scanSavedRequests, scanEnvironments, scanFlows,
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

// scanSavedRequests walks the collection fields in saved order, so the UI can
// group adjacent results under one resource.
func scanSavedRequests(ctx context.Context, s searchSnapshot, add func(searchResult) bool) bool {
	for _, r := range s.requests {
		if ctx.Err() != nil {
			return false
		}
		resource := r.Name
		if r.Collection != "" {
			resource = r.Collection + " / " + resource
		}
		key, resource := s.secrets.key(r.key()), s.secrets.resource(resource)
		for _, field := range searchFieldsOf(r) {
			label, kind := field.label, field.kind
			if !scanField(ctx, s, func(line string, number, from, to int) searchResult {
				return searchResult{source: searchCollections, key: key, resource: resource,
					field: label, kind: kind, text: line, start: from, end: to, line: number}
			}, add, field.text) {
				return false
			}
		}
	}
	return true
}

// scanEnvironments includes the environment names, its variables and the
// globals. A credential or conventionally sensitive variable keeps its name
// searchable but never its value, in either the Initial or Current column,
// as in the variable editor.
func scanEnvironments(ctx context.Context, s searchSnapshot, add func(searchResult) bool) bool {
	envs := slices.Clone(s.envs)
	slices.SortFunc(envs, func(a, b environment) int { return strings.Compare(a.Name, b.Name) })
	for _, env := range envs {
		if ctx.Err() != nil {
			return false
		}
		name, key := s.secrets.resource(env.Name), s.secrets.key(env.Name)
		if !scanField(ctx, s, func(line string, number, from, to int) searchResult {
			return searchResult{source: searchEnvironments, key: key, resource: name,
				field: locales.Name(), text: line, start: from, end: to, line: number, env: env.Name}
		}, add, env.Name) {
			return false
		}
		for _, name := range slices.Sorted(maps.Keys(env.Vars)) {
			sensitive := env.Credentials[name] || sensitiveVariable(name)
			if !scanVariable(ctx, s, add, env.Name, name, locales.Initial(), env.Vars[name], !sensitive) {
				return false
			}
		}
		for _, name := range slices.Sorted(maps.Keys(env.Current)) {
			sensitive := env.Credentials[name] || sensitiveVariable(name)
			if !scanVariable(ctx, s, add, env.Name, name, locales.Current(), env.Current[name], !sensitive) {
				return false
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.globals)) {
		if !scanVariable(ctx, s, add, "", name, locales.GlobalVariable(), s.globals[name], !sensitiveVariable(name)) {
			return false
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.globalsCurrent)) {
		if !scanVariable(ctx, s, add, "", name, locales.GlobalVariable(), s.globalsCurrent[name], !sensitiveVariable(name)) {
			return false
		}
	}
	return true
}

func scanVariable(ctx context.Context, s searchSnapshot, add func(searchResult) bool, env, name, field, value string, showValue bool) bool {
	text := name
	if showValue {
		text += " = " + value
	}
	key, resource := s.secrets.key(env), s.secrets.resource(cmp.Or(env, locales.GlobalVariable()))
	return scanField(ctx, s, func(line string, number, from, to int) searchResult {
		return searchResult{source: searchEnvironments, key: key, resource: resource,
			field: field, text: line, start: from, end: to, line: number, env: env, variable: name}
	}, add, text)
}

// scanFlows searches the URL and the header blocks it already holds, plus
// the head of a spilled body up to the app's existing display limit.
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
			if !scanField(ctx, s, func(line string, number, from, to int) searchResult {
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

func scanField(ctx context.Context, s searchSnapshot, result func(string, int, int, int) searchResult, add func(searchResult) bool, text string) bool {
	if text == "" || !s.secrets.safe(text) {
		return true
	}
	number := 0
	for line := range strings.Lines(text) {
		number++
		if ctx.Err() != nil {
			return false
		}
		line = strings.TrimSuffix(line, "\r")
		if !s.secrets.safe(line) {
			continue
		}
		from, to, ok := s.query.find(line)
		if !ok {
			continue
		}
		if !add(result(line, number, from, to)) {
			return false
		}
	}
	return true
}
