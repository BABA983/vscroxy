package app

// Saved capture filters and independent capture views.
//
// A capture view is a tab over the one capture list: every view sees the same
// traffic, but keeps its own search, chip, content conditions, sort and chosen
// row, so narrowing one never narrows another. A saved filter is a named set
// of those conditions, kept in views.json, which any view can apply.
//
// The body condition is the part the app's own filter cannot do: it searches
// the whole of both sides, reading the spill files the capture core wrote, off
// the UI thread, once per flow and query.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

const (
	// bodyChunk is how much of a spilled body a search reads at a time.
	bodyChunk = 256 << 10
	// bodyWorkers bounds how many spilled bodies are read at once, so a wide
	// body condition cannot make the machine crawl.
	bodyWorkers = 4
)

// savedCaptureFilter is a named capture filter kept in views.json: the
// conditions of a view, without the row it had chosen.
type savedCaptureFilter struct {
	Name   string        `json:"name"`
	Query  string        `json:"query,omitempty"`
	Filter string        `json:"filter,omitempty"`
	Chip   int           `json:"chip,omitzero"`
	Status string        `json:"status,omitempty"`
	Header string        `json:"header,omitempty"`
	Body   string        `json:"body,omitempty"`
	Triage captureTriage `json:"triage,omitzero"`
}

// captureTriage is shared by live views and portable saved filters. Numeric
// conditions name their units in the UI; there are no implicit KiB conversions.
type captureTriage struct {
	Host     string `json:"host,omitempty"`
	Client   string `json:"client,omitempty"`
	Method   string `json:"method,omitempty"`
	Duration string `json:"duration,omitempty"`
	Size     string `json:"size,omitempty"`
	Color    string `json:"color,omitempty"`
	Note     string `json:"note,omitempty"`
}

func numericCondition(value int, expression string) (bool, bool) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return true, true
	}
	op := "="
	for _, prefix := range []string{">=", "<=", ">", "<", "="} {
		if rest, ok := strings.CutPrefix(expression, prefix); ok {
			op, expression = prefix, strings.TrimSpace(rest)
			break
		}
	}
	n, err := strconv.Atoi(expression)
	if err != nil || n < 0 {
		return false, false
	}
	switch op {
	case ">=":
		return value >= n, true
	case "<=":
		return value <= n, true
	case ">":
		return value > n, true
	case "<":
		return value < n, true
	default:
		return value == n, true
	}
}

func (q captureTriage) matches(f flow) bool {
	if q.Host != "" && q.Host != f.host || q.Client != "" && q.Client != f.process {
		return false
	}
	if q.Method != "" {
		matched := false
		for method := range strings.SplitSeq(q.Method, ",") {
			matched = matched || strings.EqualFold(strings.TrimSpace(method), f.method)
		}
		if !matched {
			return false
		}
	}
	duration, _ := numericCondition(f.millis, q.Duration)
	size, _ := numericCondition(f.resSize, q.Size)
	return duration && size && (q.Color == "" || q.Color == f.color || q.Color == "Marked" && f.color != "") &&
		strings.Contains(strings.ToLower(f.note), strings.ToLower(q.Note))
}

func (q captureTriage) summary() []string {
	var parts []string
	for _, field := range []struct{ name, value string }{
		{"host", q.Host}, {"app", q.Client},
		{"method", q.Method}, {"duration ms", q.Duration}, {"response bytes", q.Size}, {"color", q.Color}, {"note", q.Note},
	} {
		if field.value != "" {
			parts = append(parts, field.name+" "+field.value)
		}
	}
	return parts
}

// loadCaptureFilters reads the saved filters, an empty list when there is no
// file.
func loadCaptureFilters(path string) ([]savedCaptureFilter, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []savedCaptureFilter
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	// Earlier versions kept host/app in the single sidebar choice.
	for i := range list {
		f := &list[i]
		if host, ok := strings.CutPrefix(f.Filter, "host:"); ok {
			f.Triage.Host, f.Filter = host, "all"
		} else if client, ok := strings.CutPrefix(f.Filter, "client:"); ok {
			f.Triage.Client, f.Filter = client, "all"
		}
	}
	return list, nil
}

// writeCaptureFilters writes the filters sorted by name, through a temporary
// file, so a failure leaves the file before it whole.
func writeCaptureFilters(path string, list []savedCaptureFilter) error {
	if path == "" {
		return errors.New("no file to save the filters to")
	}
	slices.SortFunc(list, func(x, y savedCaptureFilter) int { return cmp.Compare(x.Name, y.Name) })
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}

// captureConditions is what narrows the table and how it is ordered, as one
// view holds it. The app's own filter fields mirror the active view.
type captureConditions struct {
	query  string
	filter string
	chip   int
	status string
	header string
	triage captureTriage
	sort   ui.SortOrder
}

// captureView is one tab over the capture list: the same traffic, its own
// conditions.
type captureView struct {
	id int
	// preset names the saved filter the conditions came from, if any.
	preset string
	captureConditions
	// body is this view's body condition. The app's own filter only ever saw
	// the 64 KiB heads of the bodies, so the view owns the deep one.
	body     string
	picked   int
	pickedID int
	// list keeps this view's scroll position across redraws and tab changes.
	list ui.ListState
	// reveal scrolls the tab into view once, when the view is new or chosen.
	reveal bool
}

// bodyJob is what a body search needs from a flow, copied on the UI thread so
// the workers never read the flow list while a frame writes it.
type bodyJob struct {
	id       int
	reqHead  string
	reqFile  string
	reqSize  int
	reqError string
	resHead  string
	resFile  string
	resSize  int
	resError string
}

// bodyJobOf takes one flow's bodies, the files they spilled to and how long
// they are.
func bodyJobOf(f flow) bodyJob {
	return bodyJob{
		id:      f.id,
		reqHead: f.reqBody, reqFile: f.reqFile, reqSize: f.reqSize,
		resHead: f.resBody, resFile: f.resFile, resSize: f.resSize,
		reqError: f.reqError, resError: f.resError,
	}
}

// bodyResult is one flow's answer: whether a side holds the needle, or why
// neither could be read.
type bodyResult struct {
	id      int
	matched bool
	err     string
}

// bodyCache is what the deep body search has answered for one query, so no
// frame reads a file again.
type bodyCache struct {
	query   string
	matches map[int]bool
	errs    map[int]string
}

// bodySearch is one deep body search in flight: its query, how many flows it
// covers, the flows being read and the answers that have come back.
type bodySearch struct {
	query  string
	ctx    context.Context
	cancel context.CancelFunc
	notify func()

	// inflight holds the flows a worker is reading, so a frame that rebuilds
	// the rows does not hand the same flow to a second reader. The UI thread
	// alone touches it.
	inflight map[int]bool

	mu      sync.Mutex
	total   int
	results []bodyResult
	taken   int
}

// begin marks the flows a batch will read, and drops the ones already being
// read, so each flow is read once for a query.
func (s *bodySearch) begin(jobs []bodyJob) []bodyJob {
	fresh := make([]bodyJob, 0, len(jobs))
	for _, job := range jobs {
		if s.inflight[job.id] {
			continue
		}
		s.inflight[job.id] = true
		fresh = append(fresh, job)
	}
	return fresh
}

// expect adds n flows to what the search covers.
func (s *bodySearch) expect(n int) {
	s.mu.Lock()
	s.total += n
	s.mu.Unlock()
}

// report keeps one answer and asks for a frame, so the UI thread can apply it.
func (s *bodySearch) report(r bodyResult) {
	s.mu.Lock()
	s.results = append(s.results, r)
	s.mu.Unlock()
	s.notify()
}

// take hands over the answers the UI thread has not merged yet, with how far
// the search has come.
func (s *bodySearch) take() (fresh []bodyResult, done, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh = slices.Clone(s.results[s.taken:])
	s.taken = len(s.results)
	return fresh, len(s.results), s.total
}

// progress is how many answers have come back of how many are expected.
func (s *bodySearch) progress() (done, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.results), s.total
}

// captureViews holds the capture views and the filters saved by name.
type captureViews struct {
	path   string
	views  []*captureView
	active int
	nextID int

	filters []savedCaptureFilter
	// problem is the last failure to read or write the saved filters. It is
	// shown, never turned into a claim that something was saved.
	problem string
	// note is what the saved filter dialog last did.
	note string

	// wrote is what was last put into the app's fields, so sync can tell an
	// edit from what the widgets were given.
	wrote captureConditions
	// rows is how many rows the last applyFilter left, so a frame that finds
	// another count knows something else rebuilt them without the body
	// condition.
	rows int

	// filterOpen edits the active view's status, header and body conditions.
	filterOpen bool
	editStatus string
	editHeader string
	editBody   string
	editTriage captureTriage

	// savedOpen edits the filters saved by name.
	savedOpen bool
	savedName string
	renaming  string

	cache bodyCache
	run   *bodySearch
	// slotsCh bounds the body readers; it is made on the UI thread, before
	// any worker starts.
	slotsCh chan struct{}
	// search is the reader the workers use; nil is the real one. Tests set it
	// to hold a search or fail it.
	search func(ctx context.Context, job bodyJob, needle string) (bool, error)
}

// init loads the saved filters from path and opens the first view. A file that
// cannot be read is reported in problem, not fatal: the window still works,
// without the filters it held.
func (v *captureViews) init(a *app, path string) {
	if len(v.views) > 0 {
		return
	}
	v.path = path
	v.load()
	v.newView(a)
}

// load reads the saved filters from the file.
func (v *captureViews) load() {
	v.filters = nil
	if v.path == "" {
		return
	}
	list, err := loadCaptureFilters(v.path)
	if err != nil {
		v.problem = "Saved capture filters: " + err.Error()
		return
	}
	v.filters = list
}

// activeView is the view the app's fields belong to.
func (v *captureViews) activeView() *captureView {
	if v.active < 0 || v.active >= len(v.views) {
		return nil
	}
	return v.views[v.active]
}

// label names a view's tab: the filter it came from, or its place.
func (v *captureViews) label(i int) string {
	if i < 0 || i >= len(v.views) {
		return ""
	}
	if name := v.views[i].preset; name != "" {
		return name
	}
	return fmt.Sprintf("View %d", i+1)
}

// conditionsOf is what the app's fields say now.
func conditionsOf(a *app) captureConditions {
	return captureConditions{
		query: a.query, filter: a.filter, chip: a.chip,
		status: a.filterStatus, header: a.filterHeader, sort: a.sort,
		triage: a.filterTriage,
	}
}

// bind puts the active view's conditions in the app's fields, so the widgets
// show them.
func (v *captureViews) bind(a *app) {
	view := v.activeView()
	if view == nil {
		return
	}
	a.query, a.filter, a.chip = view.query, view.filter, view.chip
	a.filterStatus, a.filterHeader = view.status, view.header
	a.filterTriage = view.triage
	a.sort, a.picked = view.sort, view.picked
	a.pickedID = view.pickedID
	// The app's own body filter would match the 64 KiB heads only: the view
	// owns the deep one.
	a.filterBody = ""
	v.wrote = view.captureConditions
}

// sync takes what the widgets changed into the active view and, when that
// narrows the table differently, rebuilds the rows. Call it once a frame,
// before the view is drawn.
func (v *captureViews) sync(a *app) {
	if v.activeView() == nil {
		v.init(a, v.path)
	}
	view := v.activeView()
	if view == nil {
		return
	}
	now := conditionsOf(a)
	// A body typed into the app's own filter dialog belongs to this view; it
	// never reaches the app's head-only matching.
	body := a.filterBody
	a.filterBody = ""
	changed := now != v.wrote
	if body != "" && body != view.body {
		view.body = body
		changed = true
	}
	// Another pass over the rows, the app's own filter for one, would drop the
	// body condition: its count says so.
	if !changed && view.body != "" && len(a.visible) != v.rows {
		changed = true
	}
	if !changed {
		view.sort, view.picked = a.sort, a.picked
		view.pickedID = a.pickedID
		return
	}
	view.captureConditions = now
	view.sort, view.picked = a.sort, a.picked
	v.wrote = now
	v.applyFilter(a)
	view.picked = a.picked
	view.pickedID = a.pickedID
}

// capture keeps what the widgets show in the active view, after the frame has
// drawn them. Call it once a frame, after the view.
func (v *captureViews) capture(a *app) {
	view := v.activeView()
	if view == nil {
		return
	}
	view.captureConditions = conditionsOf(a)
	view.sort, view.picked = a.sort, a.picked
	if f, ok := a.current(); ok {
		a.pickedID = f.id
	} else {
		a.pickedID = 0
	}
	view.pickedID = a.pickedID
}

// activate makes view i the one the app's fields belong to.
func (v *captureViews) activate(a *app, i int) {
	if i < 0 || i >= len(v.views) || i == v.active {
		return
	}
	v.active = i
	view := v.views[i]
	view.reveal = true
	v.bind(a)
	v.applyFilter(a)
}

// newView adds a view with nothing narrowed and makes it the active one.
func (v *captureViews) newView(a *app) {
	v.nextID++
	view := &captureView{
		id:     v.nextID,
		reveal: true,
		// A new view starts on everything, ordered as the table was.
		captureConditions: captureConditions{filter: "all", sort: a.sort},
	}
	v.views = append(v.views, view)
	v.active = len(v.views) - 1
	v.bind(a)
	v.applyFilter(a)
}

// closeView drops view i. The last view stays: the table always has one.
func (v *captureViews) closeView(a *app, i int) {
	if len(v.views) <= 1 || i < 0 || i >= len(v.views) {
		return
	}
	v.capture(a)
	v.views = slices.Delete(v.views, i, i+1)
	if i < v.active {
		v.active--
	}
	if v.active >= len(v.views) {
		v.active = len(v.views) - 1
	}
	v.views[v.active].reveal = true
	v.bind(a)
	v.applyFilter(a)
}

// applyFilter rebuilds the visible rows with the app's own fast conditions,
// which stay exactly as they were, and then with the active view's body
// condition.
func (v *captureViews) applyFilter(a *app) {
	if v.activeView() == nil {
		a.applyFilter()
		return
	}
	// The app's own body filter matches the 64 KiB heads only: the view owns
	// the deep one, so the app never sees it.
	a.filterBody = ""
	a.applyFilter()
	v.narrow(a)
	a.restoreSelection()
}

// narrow drops the rows the active view's body condition rules out from the
// rows the app's own filter built, and starts reading the bodies it does not
// know yet. Rows whose bodies are still being read stay hidden until the
// answer comes back, so the table never shows a row that does not match.
//
// Hook it at the end of app.applyFilter: every pass over the rows then keeps
// the body condition, and a fast condition that changes does not show the
// rows the body rules out for a frame.
func (v *captureViews) narrow(a *app) {
	view := v.activeView()
	if view == nil || strings.TrimSpace(view.body) == "" {
		v.stopSearch()
		v.rows = len(a.visible)
		return
	}
	body := view.body
	if v.cache.query != body {
		v.stopSearch()
		v.cache = bodyCache{query: body, matches: make(map[int]bool), errs: make(map[int]string)}
	}
	kept := a.visible[:0]
	var jobs []bodyJob
	for _, at := range a.visible {
		f := a.flows[at]
		if matched, known := v.cache.matches[f.id]; known {
			if matched {
				kept = append(kept, at)
			}
			continue
		}
		if _, unreadable := v.cache.errs[f.id]; unreadable {
			continue
		}
		jobs = append(jobs, bodyJobOf(f))
	}
	a.visible = kept
	v.rows = len(a.visible)
	v.startSearch(a, body, jobs)
}

// drain applies the answers the body readers have come back with, on the UI
// thread. Call it once a frame, before sync.
func (v *captureViews) drain(a *app) {
	s := v.run
	if s == nil {
		return
	}
	fresh, done, total := s.take()
	if len(fresh) == 0 {
		return
	}
	for _, r := range fresh {
		if r.err != "" {
			v.cache.errs[r.id] = r.err
			continue
		}
		v.cache.matches[r.id] = r.matched
	}
	view := v.activeView()
	if view != nil && view.body == s.query {
		v.applyFilter(a)
	}
	if done < total {
		a.invalidate()
	}
}

// stopSearch cancels the search in flight, if any. Its answers are dropped.
func (v *captureViews) stopSearch() {
	if v.run != nil {
		v.run.cancel()
		v.run = nil
	}
}

// slots is the readers' semaphore, made before the workers start.
func (v *captureViews) slots() chan struct{} {
	if v.slotsCh == nil {
		v.slotsCh = make(chan struct{}, bodyWorkers)
	}
	return v.slotsCh
}

// startSearch reads the bodies the active view still needs an answer for, at
// most bodyWorkers files at a time, cancellable by the next query.
func (v *captureViews) startSearch(a *app, query string, jobs []bodyJob) {
	if len(jobs) == 0 {
		return
	}
	s := v.run
	if s == nil || s.query != query {
		v.stopSearch()
		ctx, cancel := context.WithCancel(context.Background())
		s = &bodySearch{query: query, ctx: ctx, cancel: cancel, notify: a.invalidate, inflight: make(map[int]bool)}
		v.run = s
	}
	jobs = s.begin(jobs)
	if len(jobs) == 0 {
		return
	}
	s.expect(len(jobs))
	slots := v.slots()
	var next atomic.Int64
	for range min(len(jobs), bodyWorkers) {
		go func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(jobs) || s.ctx.Err() != nil {
					return
				}
				job := jobs[i]
				matched, err := v.read(s.ctx, job, query, slots)
				if s.ctx.Err() != nil {
					// The query changed under us: the answer is not wanted.
					return
				}
				result := bodyResult{id: job.id, matched: matched}
				if err != nil {
					result.err = err.Error()
				}
				s.report(result)
			}
		}()
	}
}

// read searches one flow's bodies, holding one of the readers' slots.
func (v *captureViews) read(ctx context.Context, job bodyJob, needle string, slots chan struct{}) (bool, error) {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-slots }()
	if v.search != nil {
		return v.search(ctx, job, needle)
	}
	return searchBody(ctx, job, needle)
}

// searchBody reports whether either side of the exchange holds needle. The
// sides are searched one after the other, never joined: a needle that straddles
// the end of the request and the start of the response does not match.
func searchBody(ctx context.Context, job bodyJob, needle string) (bool, error) {
	if job.reqError != "" || job.resError != "" {
		return false, fmt.Errorf("incomplete body: request %q; response %q", job.reqError, job.resError)
	}
	sides := [2]struct {
		head string
		file string
		size int
	}{
		{job.reqHead, job.reqFile, job.reqSize},
		{job.resHead, job.resFile, job.resSize},
	}
	for _, side := range sides {
		ok, err := bodySideContains(ctx, side.head, side.file, side.size, needle, bodyChunk)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// bodySideContains searches one side of the exchange. A spilled side is read
// from its file, all of it; the head is not a fallback when the file is gone,
// because it is not the body the user filtered for, and a head that is shorter
// than the body it stands for is reported rather than searched.
func bodySideContains(ctx context.Context, head, file string, size int, needle string, chunk int) (bool, error) {
	switch {
	case file != "":
		info, err := os.Stat(file)
		if err != nil {
			return false, err
		}
		if info.Size() < int64(size) {
			return false, fmt.Errorf("the body file is incomplete: %d of %d bytes", info.Size(), size)
		}
		return fileContains(ctx, file, needle, chunk)
	case size > len(head):
		return false, fmt.Errorf("the body is incomplete: %d of %d bytes", len(head), size)
	}
	return bytes.Contains(fold(nil, []byte(head)), fold(nil, []byte(needle))), nil
}

// fileContains reports whether the whole file holds needle, read in chunks of
// chunk bytes: no preview limit applies, so a match may sit past any of them.
func fileContains(ctx context.Context, path, needle string, chunk int) (bool, error) {
	if needle == "" {
		return true, nil
	}
	if chunk <= 0 {
		chunk = bodyChunk
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	folded := fold(nil, []byte(needle))
	// A match may span two reads: keep enough of the tail, in the bytes of the
	// file, for the longest match and for the rune a read may cut. A needle of
	// n bytes folds from at most n runes of at most four bytes each.
	overlap := 4*len(needle) + 8
	buf := make([]byte, chunk)
	var carry, window, scratch []byte
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, err := f.Read(buf)
		if n > 0 {
			window = append(append(window[:0], carry...), buf[:n]...)
			end := wholeRunes(window)
			scratch = fold(scratch, window[:end])
			if bytes.Contains(scratch, folded) {
				return true, nil
			}
			start := max(0, end-overlap)
			for start < end && !utf8.RuneStart(window[start]) {
				start++
			}
			carry = bytes.Clone(window[start:])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
	}
}

// fold lowercases src one rune at a time, so a needle matches the same way
// wherever a chunk boundary falls, and non-ASCII letters match in either case.
func fold(dst, src []byte) []byte {
	dst = dst[:0]
	for len(src) > 0 {
		r, size := utf8.DecodeRune(src)
		src = src[size:]
		dst = utf8.AppendRune(dst, unicode.ToLower(r))
	}
	return dst
}

// wholeRunes is where the whole runes of b end: a read that cut one leaves it
// to the next window.
func wholeRunes(b []byte) int {
	for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
		if !utf8.RuneStart(b[len(b)-i]) {
			continue
		}
		if r, size := utf8.DecodeRune(b[len(b)-i:]); r == utf8.RuneError && size == 1 {
			return len(b) - i
		}
		return len(b)
	}
	return len(b)
}

// status is what the capture header says about the body search: what it is
// reading, or what it could not read, and whether that is a failure. Empty
// when there is nothing to say.
func (v *captureViews) status() (text string, failure bool) {
	if s := v.run; s != nil {
		if done, total := s.progress(); done < total {
			return fmt.Sprintf("Searching bodies… %d of %d", done, total), false
		}
	}
	if n := len(v.cache.errs); n > 0 {
		return fmt.Sprintf("%d %s could not be read in full", n, plural(n, "body", "bodies")), true
	}
	return "", false
}

// bodyFilter is the active view's body condition, for a status bar that says
// what is narrowing the table.
func (v *captureViews) bodyFilter() string {
	if view := v.activeView(); view != nil {
		return view.body
	}
	return ""
}

// clearFilters drops the active view's content conditions, the body included,
// which the app's own clear cannot reach.
func (v *captureViews) clearFilters(a *app) {
	view := v.activeView()
	if view == nil {
		return
	}
	view.status, view.header, view.body = "", "", ""
	view.triage = captureTriage{}
	v.bind(a)
	v.applyFilter(a)
	a.setMessage("Filters cleared")
}

// resetFilters clears every condition in this view, not in other views or
// in the saved filter library. Sorting and the view itself stay in place.
func (v *captureViews) resetFilters(a *app) {
	a.query, a.filter, a.chip = "", "all", 0
	v.capture(a)
	v.clearFilters(a)
}

// Header filtering has the same case-insensitive contains contract as the
// dialog. Never move a credential into the always-visible, saveable chips.
func (a *app) headerFilterMenu(c *ui.Context, name string, values []string) func(*ui.Menu) {
	return func(m *ui.Menu) {
		m.EditItems()
		m.Item("Matches request or response headers").Disabled(true)
		if m.Item("Header exists: " + name).Chosen() {
			a.filterHeader = name
			c.AnimationFrame()
		}
		if sensitiveHeader(name) {
			m.Item("Credential values are not added to filters").Disabled(true)
			return
		}
		for i, value := range values {
			if m.Item(fmt.Sprintf("Contains value %d: %s", i+1, clamp(value, 50))).Chosen() {
				a.filterHeader = name + ": " + value
				c.AnimationFrame()
			}
		}
	}
}

// statusDetail says why a body could not be read, for a tooltip.
func (v *captureViews) statusDetail() string {
	var parts []string
	for _, err := range v.cache.errs {
		parts = append(parts, err)
		if len(parts) == 3 {
			break
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "\n")
}

// openFilter shows the active view's content conditions.
func (v *captureViews) openFilter(a *app) {
	view := v.activeView()
	if view == nil {
		return
	}
	v.editStatus, v.editHeader, v.editBody = view.status, view.header, view.body
	v.editTriage = view.triage
	v.filterOpen = true
}

// openSaved shows the saved filters, and the name they are saved under.
func (v *captureViews) openSaved(a *app) {
	v.capture(a)
	v.savedOpen = true
	v.problem = ""
	v.note = ""
	v.renaming = ""
	if v.savedName == "" {
		v.savedName = v.label(v.active)
	}
}

// controls lives in the sidebar: views and saved conditions never take
// vertical space away from the request table and inspector.
// rows is the saved-views part of the sidebar: the views of this session,
// the filters saved by name, and the two actions that manage them. It sits
// inside a SidebarSection, so the section's title opens and closes it.
func (v *captureViews) sidebar(c *ui.Context, t *ui.Theme, a *app) {
	if v.activeView() == nil {
		v.init(a, v.path)
	}
	closing := -1
	ui.Column(c).Gap(t.Space(0.5)).FillWidth().Children(func() {
		for i, view := range v.views {
			name := v.label(i)
			ui.Row(c.Key(view.id)).AlignItems(ui.Center).Children(func() {
				b := uikit.ToolbarButton(c, "Capture view: "+name, "Independent filters, selection and scroll position", i == v.active).FillWidth()
				if b.Clicked() {
					v.capture(a)
					v.activate(a, i)
				}
				if view.reveal {
					b.ScrollIntoView()
					view.reveal = false
				}
				b.Children(func() {
					ui.Icon(c, layersIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, name).SingleLine().Grow(1)
					if len(v.views) > 1 {
						if uikit.IconButton(c, "Close view: "+name, "Close this view", closeIcon).Clicked() {
							closing = i
						}
					}
				})
			})
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			if uikit.ToolbarButton(c, "New view", "A new view with its own filters, selection and scroll position", false).Grow(1).Justify(ui.Start).Children(func() {
				ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, "New view")
			}).Clicked() {
				v.capture(a)
				v.newView(a)
			}
			if uikit.IconButton(c, "Saved filters…", "Save current conditions, apply, rename or delete a saved filter", filterIcon).Margin(0, t.Space(2), 0, 0).Clicked() {
				v.openSaved(a)
			}
		})
		if len(v.filters) > 0 {
			uikit.Label(c, "SAVED FILTERS").LetterSpacing(0.5).Padding(t.Space(3), t.Space(2), t.Space(1), t.Space(2))
		}
		for _, f := range v.filters {
			active := v.activeView().saved(f.Name) == f
			b := uikit.ToolbarButton(c, "Quick filter: "+f.Name, "Apply saved conditions", active).FillWidth()
			if b.Clicked() {
				if active {
					v.resetFilters(a)
				} else {
					v.applySaved(a, f.Name)
				}
			}
			b.Children(func() {
				ui.Icon(c, filterIcon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, f.Name).SingleLine()
			})
		}
		if note, failure := v.status(); note != "" {
			color := t.TextMuted
			if failure {
				color = t.Danger
			}
			ui.Text(c, note).FontSize(uikit.TextMeta).TextColor(color).Tooltip(v.statusDetail())
		}
	})
	if closing >= 0 {
		v.closeView(a, closing)
	}
}

// modals draws the view's two dialogs: the conditions of the view being
// filtered, and the filters saved by name. Call it once a frame, beside the
// app's other dialogs.
func (v *captureViews) modals(c *ui.Context, t *ui.Theme, a *app) {
	v.filterModal(c, t, a)
	v.savedModal(c, t, a)
}

// filterModal narrows the active view by status, header and body, beside the
// search box which matches the URL. The body is searched in full, on both
// sides, spilled bodies included.
func (v *captureViews) filterModal(c *ui.Context, t *ui.Theme, a *app) {
	if !v.filterOpen {
		return
	}
	uikit.Modal(c, &v.filterOpen, func() {
		ui.Column(c).Width(560).Gap(t.Space(2)).Children(func() {
			uikit.DialogTitle(c, "Filter "+v.label(v.active))
			uikit.Meta(c, "All fields combine with AND. Host and app match exactly. Status takes 404, 4xx, 200-299 or >=400; commas mean OR. Header values use case-insensitive contains on either side. Body searches both sides in full, including spilled files.")
			problem := ""
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				ui.Field(c, "Host (exact, including port)", func() {
					uikit.Input(c, &v.editTriage.Host).Label("Filter host").Placeholder("api.example.com")
				}).Grow(1)
				ui.Field(c, "App (exact name)", func() {
					uikit.Input(c, &v.editTriage.Client).Label("Filter app").Placeholder("Browser")
				}).Grow(1)
			})
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				ui.Field(c, "Method (comma-separated)", func() {
					uikit.Input(c, &v.editTriage.Method).Label("Filter method").Placeholder("GET, POST")
				}).Grow(1)
				ui.Field(c, "Color", func() {
					colors := []string{"Any", "Marked"}
					for _, color := range uikit.MarkColors {
						colors = append(colors, color.Name)
					}
					color := cmp.Or(v.editTriage.Color, "Any")
					if uikit.Select(c, &color, colors).Label("Filter color").Changed() {
						v.editTriage.Color = color
						if color == "Any" {
							v.editTriage.Color = ""
						}
					}
				}).Width(150)
			})
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				for _, field := range []struct {
					label string
					value *string
				}{
					{"Duration (ms)", &v.editTriage.Duration}, {"Response size (bytes)", &v.editTriage.Size},
				} {
					if _, valid := numericCondition(0, *field.value); !valid {
						problem = "Use a non-negative integer, optionally prefixed with <, <=, =, >= or >."
					}
					ui.Field(c, field.label, func() {
						uikit.Input(c, field.value).Label(field.label).Placeholder(">=500")
					}).Grow(1)
				}
			})
			if strings.TrimSpace(v.editStatus) != "" {
				for part := range strings.SplitSeq(v.editStatus, ",") {
					if _, _, ok := statusBounds(part); !ok {
						problem = "Use HTTP status codes (100–599), ranges, comparisons or comma-separated alternatives."
						break
					}
				}
			}
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				ui.Field(c, "Status", func() {
					uikit.Input(c, &v.editStatus).Label("Filter status").Placeholder("4xx, 5xx")
				}).Grow(1)
				ui.Field(c, "Header", func() {
					uikit.Input(c, &v.editHeader).Placeholder("content-type: json")
				}).Grow(1)
			})
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				ui.Field(c, "Note contains", func() {
					uikit.Input(c, &v.editTriage.Note).Label("Filter note").Placeholder("case-insensitive text")
				}).Grow(1)
				ui.Field(c, "Body", func() {
					uikit.Input(c, &v.editBody).Label("Body contains").Placeholder("\"error\":")
				}).Grow(1)
			})
			if problem != "" {
				ui.Text(c, problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			if note, failure := v.status(); note != "" {
				color := t.TextMuted
				if failure {
					color = t.Danger
				}
				ui.Text(c, note).FontSize(uikit.TextMeta).TextColor(color).Tooltip(v.statusDetail())
			}
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if uikit.SecondaryButton(c, "Clear").Clicked() {
					v.editStatus, v.editHeader, v.editBody = "", "", ""
					v.editTriage = captureTriage{}
					v.commitFilter(a)
					v.filterOpen = false
				}
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					v.filterOpen = false
				}
				if uikit.PrimaryButton(c, "Apply").Disabled(problem != "").Clicked() {
					v.commitFilter(a)
					v.filterOpen = false
				}
			})
		})
	})
}

// commitFilter puts the conditions the dialog holds in the active view.
func (v *captureViews) commitFilter(a *app) {
	view := v.activeView()
	if view == nil {
		return
	}
	view.status = strings.TrimSpace(v.editStatus)
	view.header = strings.TrimSpace(v.editHeader)
	view.body = v.editBody
	view.triage = v.editTriage
	view.triage.Host = strings.TrimSpace(view.triage.Host)
	view.triage.Client = strings.TrimSpace(view.triage.Client)
	view.triage.Method = strings.TrimSpace(view.triage.Method)
	v.bind(a)
	v.applyFilter(a)
	if strings.TrimSpace(view.body) != "" {
		a.setMessage("Body filter: " + strconv.Quote(strings.TrimSpace(view.body)))
	}
}

// savedModal lists the filters saved by name: each can be applied to the
// active view, renamed or deleted, and the active view's conditions can be
// saved under the name typed.
func (v *captureViews) savedModal(c *ui.Context, t *ui.Theme, a *app) {
	if !v.savedOpen {
		return
	}
	uikit.Modal(c, &v.savedOpen, func() {
		ui.Column(c).Width(560).Gap(t.Space(2)).Children(func() {
			uikit.DialogTitle(c, "Saved capture filters")
			uikit.Meta(c, "A filter keeps this view's URL, content and triage conditions. Applying one changes this view; the other views keep their own.")
			if len(v.filters) == 0 {
				uikit.Meta(c, "No saved filters yet.")
			}
			for _, f := range v.filters {
				name := f.Name
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Body(c, name).Grow(1).SingleLine().Tooltip(name)
					if uikit.CompactButton(c, "Apply").Label("Apply " + strconv.Quote(name)).Clicked() {
						v.applySaved(a, name)
					}
					if uikit.CompactButton(c, "Rename").Label("Rename " + strconv.Quote(name)).Clicked() {
						v.renaming = name
						v.savedName = name
					}
					if uikit.CompactButton(c, "Delete").Label("Delete " + strconv.Quote(name)).Clicked() {
						v.deleteSaved(a, name)
					}
				})
			}
			ui.Field(c, "Filter name", func() {
				uikit.Input(c, &v.savedName).Placeholder("Name this view's conditions")
			})
			if v.renaming != "" {
				uikit.Meta(c, "Renaming "+strconv.Quote(v.renaming)+". Type the new name, then Rename filter.")
			}
			if v.problem != "" {
				ui.Text(c, v.problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			if v.note != "" {
				uikit.Meta(c, v.note)
			}
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if uikit.SecondaryButton(c, "Close").Clicked() {
					v.savedOpen = false
					v.renaming = ""
				}
				if v.renaming != "" {
					if uikit.SecondaryButton(c, "Rename filter").Clicked() {
						v.renameSaved(a)
					}
				}
				if uikit.PrimaryButton(c, "Save filter").Clicked() {
					v.saveCurrent(a)
				}
			})
		})
	})
	if !v.savedOpen {
		v.renaming = ""
	}
}

// applySaved puts a saved filter's conditions in the active view, which keeps
// its place in the tabs.
func (v *captureViews) applySaved(a *app, name string) {
	view := v.activeView()
	at := slices.IndexFunc(v.filters, func(f savedCaptureFilter) bool { return f.Name == name })
	if view == nil || at < 0 {
		return
	}
	f := v.filters[at]
	v.capture(a)
	view.query, view.filter, view.chip = f.Query, f.Filter, f.Chip
	view.status, view.header, view.body = f.Status, f.Header, f.Body
	view.triage = f.Triage
	view.preset = f.Name
	view.reveal = true
	v.bind(a)
	v.applyFilter(a)
	v.problem = ""
	v.note = "Applied " + strconv.Quote(name) + " to this view."
	a.setMessage("Filter applied: " + name)
}

// saveCurrent saves the active view's conditions under the name typed,
// replacing the filter of that name.
func (v *captureViews) saveCurrent(a *app) {
	view := v.activeView()
	name := strings.TrimSpace(v.savedName)
	switch {
	case view == nil:
		return
	case name == "":
		v.problem = "A saved filter needs a name."
		return
	}
	f := view.saved(name)
	list := slices.Clone(v.filters)
	replaced := false
	for i := range list {
		if list[i].Name == name {
			list[i], replaced = f, true
		}
	}
	if !replaced {
		list = append(list, f)
	}
	if err := writeCaptureFilters(v.path, list); err != nil {
		v.problem = "Could not save filters: " + err.Error()
		return
	}
	v.filters = list
	v.problem = ""
	view.preset = name
	v.note = "Saved " + strconv.Quote(name) + "."
	a.setMessage("Saved capture filter " + name)
}

func (v *captureView) saved(name string) savedCaptureFilter {
	return savedCaptureFilter{Name: name, Query: v.query, Filter: v.filter, Chip: v.chip,
		Status: v.status, Header: v.header, Body: v.body, Triage: v.triage}
}

// renameSaved renames the filter the dialog is renaming to the name typed.
func (v *captureViews) renameSaved(a *app) {
	from := v.renaming
	to := strings.TrimSpace(v.savedName)
	switch {
	case from == "":
		return
	case to == "":
		v.problem = "A saved filter needs a name."
		return
	case to != from && slices.ContainsFunc(v.filters, func(f savedCaptureFilter) bool { return f.Name == to }):
		v.problem = "A filter named " + strconv.Quote(to) + " already exists."
		return
	}
	list := slices.Clone(v.filters)
	for i := range list {
		if list[i].Name == from {
			list[i].Name = to
		}
	}
	if err := writeCaptureFilters(v.path, list); err != nil {
		v.problem = "Could not save filters: " + err.Error()
		return
	}
	v.filters = list
	for _, view := range v.views {
		if view.preset == from {
			view.preset = to
		}
	}
	v.renaming = ""
	v.problem = ""
	v.note = "Renamed " + strconv.Quote(from) + " to " + strconv.Quote(to) + "."
	a.setMessage("Renamed capture filter to " + to)
}

// deleteSaved drops the filter of that name. The views it was applied to keep
// the conditions they show.
func (v *captureViews) deleteSaved(a *app, name string) {
	list := slices.DeleteFunc(slices.Clone(v.filters), func(f savedCaptureFilter) bool { return f.Name == name })
	if err := writeCaptureFilters(v.path, list); err != nil {
		v.problem = "Could not save filters: " + err.Error()
		return
	}
	v.filters = list
	for _, view := range v.views {
		if view.preset == name {
			view.preset = ""
		}
	}
	v.problem = ""
	v.note = "Deleted " + strconv.Quote(name) + "."
	a.setMessage("Deleted capture filter " + name)
}
