package app

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// compareTarget names an exchange without holding a row or a body: a numeric
// capture ID, or a retained run ID. The scope keeps a run's origin distinct
// in a message even if a history is cleared and its IDs start over.
type compareTarget struct {
	kind  string // "flow" or "run"
	id    int
	scope string
}

func flowTarget(f flow) compareTarget { return compareTarget{kind: "flow", id: f.id} }

func runTarget(r experimentRecord) compareTarget {
	t := compareTarget{kind: "run", id: r.ID}
	switch {
	case r.Source != nil:
		t.scope = "source:" + r.Source.Key
	case r.DraftKey != "":
		t.scope = "draft:" + r.DraftKey
	case r.Draft.ID != "":
		t.scope = "asset:" + r.Draft.ID
	}
	return t
}

func (t compareTarget) valid() bool {
	return t.id > 0 && (t.kind == "flow" || t.kind == "run")
}

func (t compareTarget) same(other compareTarget) bool {
	return t.kind == other.kind && t.id == other.id && t.scope == other.scope
}

func (t compareTarget) label() string {
	if t.kind == "run" {
		return locales.RunNumberLabel(t.id)
	}
	return locales.CaptureNumber(t.id)
}

// compareBodySource holds only a bounded string header and a spill path. The
// bytes, their hash and the normalized text are made on a worker, never in a
// frame that also paints the traffic table.
type compareBodySource struct {
	inline      string
	file        string
	size        int
	contentType string
}

type compareSource struct {
	target                 compareTarget
	label, method, url     string
	status, millis         int
	reqHeaders, resHeaders string
	req, res               compareBodySource
}

// comparePart is one side of one section's diff. A summary replaces both
// texts for a binary or oversized body, so the two sides still line up.
type comparePart struct {
	raw, normalized string
	summary         string
	size            int
	hash            string
}

func (p comparePart) text(raw bool) string {
	if raw {
		return p.raw
	}
	return p.normalized
}

type compareExchange struct {
	target                 compareTarget
	label, method, url     string
	status, millis         int
	reqHeaders, resHeaders comparePart
	reqBody, resBody       comparePart
}

type comparePrepared struct {
	generation uint64
	sides      [2]compareExchange
}

// comparePartKind is the section being diffed. The localized name is derived
// when it is drawn, so changing language while the dialog is open cannot make
// the view lose track of its section.
type comparePartKind uint8

const (
	comparePartResponseBody comparePartKind = iota
	comparePartResponseHeaders
	comparePartRequestBody
	comparePartRequestHeaders
)

var comparePartOptions = [...]comparePartKind{
	comparePartResponseBody, comparePartResponseHeaders, comparePartRequestBody, comparePartRequestHeaders,
}

func (p comparePartKind) label() string {
	switch p {
	case comparePartResponseHeaders:
		return locales.ResponseHeaders()
	case comparePartRequestBody:
		return locales.RequestBody()
	case comparePartRequestHeaders:
		return locales.RequestHeaders()
	default:
		return locales.ResponseBody()
	}
}

// compareState is the two-command selection and the view reading its result.
// A generation drops a worker's answer when a newer pair was chosen meanwhile.
type compareState struct {
	selected    compareTarget
	open, ready bool
	mu          sync.Mutex
	generation  uint64
	pending     *comparePrepared
	sources     [2]compareSource
	sides       [2]compareExchange
	labels      [2]string
	part        comparePartKind
	raw         bool
	swapped     bool
	diff        *editor.DiffState
	diffKey     string
}

// nextGeneration invalidates any answer still in flight and returns the id
// the worker for the new pair must carry.
func (s *compareState) nextGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.pending = nil
	return s.generation
}

// publish keeps the current generation's answer in one slot. It cannot be
// dropped because a bounded channel was full: that would leave the dialog on
// Reading forever. An answer from a superseded pair is never published.
func (s *compareState) publish(result comparePrepared) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.generation == s.generation {
		latest := result
		s.pending = &latest
	}
}

func (s *compareState) take() (comparePrepared, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return comparePrepared{}, false
	}
	result := *s.pending
	s.pending = nil
	return result, true
}

// compareSourceOf copies what a comparison needs from a capture or a run. It
// never reads a spill file: the inline string headers are cheap to copy.
func (a *app) compareSourceOf(t compareTarget) (compareSource, bool) {
	s := compareSource{target: t, label: t.label()}
	if t.kind == "flow" {
		i := slices.IndexFunc(a.flows, func(f flow) bool { return f.id == t.id })
		if i < 0 {
			return compareSource{}, false
		}
		f := a.flows[i]
		s.method, s.url, s.status, s.millis = f.method, f.url, f.status, f.millis
		s.reqHeaders, s.resHeaders = f.reqHeaders, f.resHeaders
		s.req = compareBodySource{inline: f.reqBody, file: f.reqFile, size: f.reqSize,
			contentType: f.bodyContentType(false)}
		s.res = compareBodySource{inline: f.resBody, file: f.resFile, size: f.resSize,
			contentType: f.bodyContentType(true)}
		return s, true
	}
	if t.kind != "run" {
		return compareSource{}, false
	}
	r := a.experiments.record(t.id)
	if r == nil {
		return compareSource{}, false
	}
	s.method, s.url = r.Draft.Method, r.Draft.URL
	s.req = compareBodySource{inline: string(r.RequestBody), file: r.RequestBodyFile, contentType: "text/plain"}
	s.res = compareBodySource{inline: string(r.ResponseBody), file: r.ResponseBodyFile, contentType: "text/plain"}
	if e := r.Exchange; e != nil {
		s.method, s.url, s.status, s.millis = e.Method, e.URL, e.Status, e.Millis
		s.reqHeaders, s.resHeaders = request.HeaderBlock(e.RequestHeader), request.HeaderBlock(e.ResponseHeader)
		s.req.size, s.res.size = e.RequestBodySize, e.ResponseBodySize
		s.req.contentType, s.res.contentType = e.RequestHeader.Get("Content-Type"), e.ResponseHeader.Get("Content-Type")
	} else {
		s.req.size, s.res.size = len(r.RequestBody), len(r.ResponseBody)
	}
	return s, true
}

func (a *app) sourcePair(left, right compareTarget) ([2]compareSource, bool) {
	var pair [2]compareSource
	for i, t := range [2]compareTarget{left, right} {
		s, ok := a.compareSourceOf(t)
		if !ok {
			return pair, false
		}
		pair[i] = s
	}
	return pair, true
}

// selectForCompare holds one side until the second command arrives, as the
// workbench's Select for Compare holds a resource in its comparison state.
func (a *app) selectForCompare(t compareTarget) {
	if !t.valid() {
		return
	}
	a.compare.selected = t
	a.invalidate()
}

func (a *app) compareWithSelected(t compareTarget) {
	if !t.valid() {
		return
	}
	held := a.compare.selected
	if !held.valid() {
		a.warn(locales.SelectFirstExchangeForCompare())
		return
	}
	if held.same(t) {
		a.warn(locales.CompareNeedsTwoExchanges())
		return
	}
	a.startCompare(held, t)
}

func (a *app) compareTwo(left, right compareTarget) {
	if !left.valid() || !right.valid() || left.same(right) {
		return
	}
	a.startCompare(left, right)
}

func (a *app) startCompare(left, right compareTarget) {
	sources, ok := a.sourcePair(left, right)
	if !ok {
		a.warn(locales.CompareSideIsNoLongerAvailable())
		return
	}
	s := &a.compare
	gen := s.nextGeneration()
	s.open, s.ready = true, false
	s.sources = sources
	s.labels = [2]string{left.label(), right.label()}
	s.sides = [2]compareExchange{}
	s.part, s.raw, s.swapped = comparePartResponseBody, false, false
	s.diff, s.diffKey = nil, ""
	go guarded(func() {
		prepared := comparePrepared{generation: gen}
		for i, source := range sources {
			prepared.sides[i] = prepareCompareExchange(source)
		}
		s.publish(prepared)
		a.invalidate()
	})
}

// prepareCompareExchange reads each body within the app's existing display
// bound and builds both diff forms in one pass over those bytes.
func prepareCompareExchange(s compareSource) compareExchange {
	out := compareExchange{target: s.target, label: s.label, method: s.method,
		url: s.url, status: s.status, millis: s.millis}
	out.reqHeaders = compareHeaders(s.reqHeaders)
	out.resHeaders = compareHeaders(s.resHeaders)
	out.reqBody = prepareCompareBody(s.req)
	out.resBody = prepareCompareBody(s.res)
	return out
}

func compareHeaders(block string) comparePart {
	// HeaderBlock canonicalizes names and sorts them, so a changed header
	// order or case is not a difference. Repeated values keep their order.
	normalized := request.HeaderBlock(request.ParseHeaderBlock(block))
	return comparePart{raw: block, normalized: normalized}
}

func prepareCompareBody(src compareBodySource) comparePart {
	data, size, problem := readCompareBody(src)
	sum := sha256.Sum256(data)
	part := comparePart{size: size, hash: hex.EncodeToString(sum[:])[:12], raw: string(data), normalized: string(data)}
	switch {
	case problem != "":
		part.summary = locales.CompareBodyUnavailable(sizeName(size), part.hash, problem)
	case inspect.BinaryContentType(src.contentType) || !utf8.Valid(data):
		part.summary = locales.CompareBinaryBody(sizeName(size), part.hash)
	case size > maxBodyView || len(data) > maxBodyView:
		part.summary = locales.CompareLargeBody(sizeName(size), part.hash)
	default:
		if decoded, _, ok := inspect.DisplayText(src.contentType, data); ok {
			part.raw, part.normalized = decoded, decoded
		}
		part.normalized, _ = jsonc.NormalizedJSON(part.normalized)
	}
	// Pretty-printing can inflate a body. Keep both diff buffers, and the
	// copies editor.NewBuffer makes on the UI thread, inside the same bound.
	if part.summary == "" && len(part.normalized) > maxBodyView {
		part.summary = locales.CompareLargeBody(sizeName(size), part.hash)
	}
	if part.summary != "" {
		part.raw, part.normalized = part.summary, part.summary
	}
	return part
}

// readCompareBody reuses the display limit: spill files are read once, up to
// the same 8 MiB the reader shows, and an inline head is not copied again
// when its full size is already in memory.
func readCompareBody(src compareBodySource) ([]byte, int, string) {
	if src.file != "" {
		f, err := os.Open(src.file)
		if err != nil {
			return nil, src.size, err.Error()
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxBodyView+1))
		if err != nil {
			return nil, src.size, err.Error()
		}
		size := src.size
		if size < len(data) {
			size = len(data)
		}
		return data, size, ""
	}
	if src.size > len(src.inline) && src.size <= maxBodyView {
		// A short inline head with a larger recorded size is a partial
		// capture; the reader has the same bytes and says so through size.
		return []byte(src.inline), src.size, ""
	}
	return []byte(src.inline), max(src.size, len(src.inline)), ""
}

func sizeName(size int) string {
	if size == 0 {
		return "0 B"
	}
	return humanBytes(size)
}

// drainCompare publishes a finished pair on the UI thread. A result from an
// older pair is dropped rather than shown over the newer one's frame.
func (a *app) drainCompare() {
	s := &a.compare
	if result, ok := s.take(); ok && s.open && result.generation == s.generation {
		s.sides, s.ready = result.sides, true
		s.diff, s.diffKey = nil, ""
		a.invalidate()
	}
}

// runCompareActions is the two-command pair on a run-history row. It uses
// the same ids and the same held side as the traffic table's row actions.
func (a *app) runCompareActions(t compareTarget) []action {
	held := a.compare.selected
	return []action{
		do("compare.select", locales.SelectForCompare(), func() { a.selectForCompare(t) }),
		do("compare.with-selected", locales.CompareWithSelected(), func() { a.compareWithSelected(t) }).
			when(held.valid() && !held.same(t), locales.SelectFirstExchangeForCompare()),
	}
}

// clearCompareSelection drops the held side. It is called when a capture is
// deleted or cleared, so Compare with Selected never names a row that is gone.
func (a *app) clearCompareSelection(t compareTarget) {
	if a.compare.selected.same(t) {
		a.compare.selected = compareTarget{}
	}
}

func (s *compareState) exchange(i int) compareExchange {
	if s.swapped {
		i = 1 - i
	}
	return s.sides[i]
}

func (s *compareState) partTexts(e compareExchange) (string, string) {
	switch s.part {
	case comparePartRequestHeaders:
		return e.reqHeaders.raw, e.reqHeaders.normalized
	case comparePartResponseHeaders:
		return e.resHeaders.raw, e.resHeaders.normalized
	case comparePartRequestBody:
		return e.reqBody.raw, e.reqBody.normalized
	default:
		return e.resBody.raw, e.resBody.normalized
	}
}

// compareModal is a read-only, side-by-side view of the two retained
// exchanges. It owns its buffers, so the traffic table's own diffs keep their
// scroll position while this dialog is open.
func (a *app) compareModal(c *ui.Context, t *ui.Theme) {
	s := &a.compare
	if !s.open {
		return
	}
	title := locales.CompareExchanges()
	_, height := c.Size()
	uikit.Modal(c, &s.open, title, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(700, height-80)).Children(func() {
			uikit.DialogTitle(c, title)
			if !s.ready {
				uikit.Hint(c, locales.CompareReadingBodies()).Padding(t.Space(uikit.SpaceXL))
				uikit.DialogFooter(c).Children(func() {
					if uikit.PrimaryButton(c, locales.Close()).Clicked() {
						s.open = false
					}
				})
				return
			}
			ui.Row(c).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Wrap().Children(func() {
				uikit.MenuSelect(c, s.part.label(), func(m *ui.Menu) {
					for _, part := range comparePartOptions {
						if m.Item(part.label()).Chosen() {
							s.part = part
							s.diffKey = ""
						}
					}
				}).Label(locales.CompareContent()).Width(165).Height(uikit.HeightRow).Radius(uikit.RadiusControl)
				label, tip := locales.CompareNormalized(), locales.CompareNormalizedTip()
				if s.raw {
					label, tip = locales.CompareRaw(), locales.CompareRawTip()
				}
				if uikit.Tip(c, uikit.SecondaryButton(c, label), tip).Clicked() {
					s.raw = !s.raw
					s.diffKey = ""
				}
				if uikit.SecondaryButton(c, locales.SwapSides()).Clicked() {
					s.swapped = !s.swapped
				}
				if uikit.SecondaryButton(c, locales.Split()).Clicked() && s.diff != nil {
					s.diff.Layout = editor.DiffSplit
				}
				if uikit.SecondaryButton(c, locales.Unified()).Clicked() && s.diff != nil {
					s.diff.Layout = editor.DiffUnified
				}
			})
			ui.Row(c).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceL)).Children(func() {
				for i := range 2 {
					e := s.exchange(i)
					side := locales.LeftSide()
					if i == 1 {
						side = locales.RightSide()
					}
					uikit.Tip(c, uikit.Caption(c, locales.CompareExchangeLine(side, e.label, e.method, e.url,
						strconv.Itoa(e.status))).FontFeatures("tnum").SingleLine().Grow(1), e.url)
				}
			})
			left, right := s.exchange(0), s.exchange(1)
			oldRaw, oldNormalized := s.partTexts(left)
			newRaw, newNormalized := s.partTexts(right)
			oldText, newText := oldNormalized, newNormalized
			if s.raw {
				oldText, newText = oldRaw, newRaw
			}
			key := strings.Join([]string{strconv.Itoa(int(s.part)), strconv.FormatBool(s.raw), left.target.label(),
				right.target.label(), strconv.FormatBool(s.swapped)}, "|")
			if s.diff == nil || s.diffKey != key {
				layout := editor.DiffSplit
				if s.diff != nil {
					layout = s.diff.Layout
				}
				s.diff = &editor.DiffState{Old: editor.NewBuffer(oldText), New: editor.NewBuffer(newText),
					Layout: layout, SplitPercent: a.diffSplit}
				s.diffKey = key
			}
			s.diff.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
			s.diff.Theme = uikit.EditorTheme(t)
			editor.DiffView(c, s.diff).Grow(1).MinHeight(0).FillWidth()
			a.diffSplit = s.diff.SplitPercent
			uikit.Meta(c, locales.CompareReadOnlyNote()).Padding(0, t.Space(uikit.SpaceL))
			uikit.DialogFooter(c).Children(func() {
				if uikit.PrimaryButton(c, locales.Close()).Clicked() {
					s.open = false
				}
			})
		})
	})
}
