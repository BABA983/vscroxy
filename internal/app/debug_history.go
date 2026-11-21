package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A source is an immutable, bounded capture snapshot. Its identity survives
// checkpoints and session files; a reused numeric ID or URL is not a link.
type debugSource struct {
	Key      string
	Exchange sessionFlow
}

func snapshotDebugFlow(f flow) sessionFlow {
	req, reqSize, reqErr := experimentPayload(f.reqBody, f.reqFile, f.reqSize, f.reqError)
	res, resSize, resErr := experimentPayload(f.resBody, f.resFile, f.resSize, f.resError)
	f.reqBody, f.reqFile, f.reqSize, f.reqError = string(req), "", reqSize, reqErr
	f.resBody, f.resFile, f.resSize, f.resError = string(res), "", resSize, resErr
	f.reqHeader, f.respHeader = f.reqHeader.Clone(), f.respHeader.Clone()
	f.reqTrailer, f.resTrailer = f.reqTrailer.Clone(), f.resTrailer.Clone()
	f.actions = slices.Clone(f.actions)
	return sessionFrom([]flow{f}, nil).Flows[0]
}

func (d *requestDocument) setDebugSource(source *debugSource) {
	d.origin = source
	if source == nil {
		return
	}
	f := source.Exchange.flow()
	d.sourceFlow, d.source = f.id, &f
}

func (r *experimentRecord) keepWire(f flow) bool {
	if r.RunKey == "" || r.RunKey != f.debugRun || len(r.Wire) >= 16 || slices.ContainsFunc(r.Wire, func(w sessionFlow) bool { return w.DebugKey == f.debugKey }) {
		return false
	}
	// A checkpoint writer may still be reading the preceding slice.
	r.Wire = append(slices.Clone(r.Wire), snapshotDebugFlow(f))
	// A proxy-generated 502 is not an upstream HTTP error. Only the exact
	// run's connection evidence can distinguish it from a genuine 502.
	if f.conn.error != "" && (r.Outcome == "Response received" || r.Outcome == "HTTP error response" || r.Outcome == "Checks failed") {
		r.Outcome, r.Summary = "Network error", f.conn.errorStage+": "+f.conn.error
	}
	return true
}

func (a *app) openSendHistory(d *requestDocument) {
	if d.origin != nil && d.assetID == "" {
		a.openDebugHistory(d.origin)
		return
	}
	s := &a.experiments
	s.assetID, s.status, s.sourceFilter = d.assetID, "", ""
	s.scope, s.draftKey, s.open = nil, d.debugKey, true
	s.picked, s.reader, s.tab = 0, nil, 0
	for _, r := range s.log.Records {
		if s.includes(r) {
			s.pick(r.ID)
			break
		}
	}
	// A historical send opened as a new draft keeps its exact result link,
	// even though future sends belong to the new draft's history.
	if r := s.record(d.experimentID); r != nil && r.DraftKey != "" {
		s.draftKey = r.DraftKey
		s.pick(d.experimentID)
	}
}

func (a *app) openDebugHistory(source *debugSource) {
	if source == nil || source.Key == "" {
		return
	}
	s := &a.experiments
	s.draftKey, s.assetID, s.status, s.sourceFilter = "", "", "", ""
	s.scope, s.open, s.picked = source, true, 0
	s.reader, s.sourceComparison = nil, captureAnalysis{}
	s.tab = 2
	for _, r := range s.log.Records {
		if r.Source != nil && r.Source.Key == source.Key {
			s.pick(r.ID)
			break
		}
	}
}

func (s *experimentWorkbench) includes(r experimentRecord) bool {
	linked := s.scope == nil && (s.draftKey == "" && s.assetID == "" || s.draftKey != "" && r.DraftKey == s.draftKey || s.assetID != "" && r.Draft.ID == s.assetID) ||
		s.scope != nil && r.Source != nil && r.Source.Key == s.scope.Key
	return linked && r.matchesHistory(s.status, s.sourceFilter)
}

func (a *app) debugDrafts(c *ui.Context) {
	s := &a.experiments
	t := c.Theme()
	if s.scope == nil {
		if s.draftKey != "" || s.assetID != "" {
			uikit.Meta(c, locales.OnlyRunsLinkedToThis())
			if uikit.SecondaryButton(c, locales.ShowAllHistory()).Clicked() {
				s.draftKey, s.assetID = "", ""
			}
		}
		return
	}
	uikit.Body(c, locales.SourceSnapshot(s.scope.Exchange.ID)).FontFeatures("tnum").FontWeight(500)
	uikit.Meta(c, locales.OnlyExplicitlyLinkedDraftsAnd())
	index := slices.IndexFunc(a.flows, func(f flow) bool { return f.debugKey == s.scope.Key })
	// The column's own padding does not reach a stretched control, so the
	// buttons keep the text's edges instead of the panel's.
	inset := t.Space(uikit.SpaceXS)
	if uikit.SecondaryButton(c, locales.InspectSourceCapture()).Margin(0, inset).Disabled(index < 0).Clicked() {
		s.open = false
		a.inspectCapture(a.flows[index].id)
	}
	if index < 0 {
		uikit.Meta(c, locales.OriginalCaptureUnavailableSnapshotRetained())
	}
	if uikit.SecondaryButton(c, locales.ShowAllHistory()).Margin(0, inset).Clicked() {
		s.scope, s.draftKey = nil, ""
	}
	// A section is a heading and its space, not a rule: the column is one
	// surface, and a line under a sentence reads as an underline on it.
	uikit.Body(c, locales.OpenDrafts()).FontWeight(500).Padding(t.Space(uikit.SpaceS), 0, 0, 0)
	n := 0
	for _, d := range a.documents {
		if d.origin == nil || s.scope == nil || d.origin.Key != s.scope.Key {
			continue
		}
		n++
		if uikit.Tip(c, uikit.SecondaryButton(c, d.title()).Label(locales.OpenLinkedDraft(n)), d.composeMethod+" "+d.composeURL).Clicked() {
			a.activateDocument(d)
			s.open = false
			d.urlInput.Focus()
		}
	}
	if n == 0 {
		uikit.Caption(c, locales.NoOpenDraftsForThis())
	}
	uikit.Body(c, locales.History()).FontWeight(500).Padding(t.Space(uikit.SpaceS), 0, 0, 0)
}

func (a *app) debugWire(c *ui.Context, r experimentRecord) {
	s := &a.experiments
	if len(r.Wire) == 0 {
		uikit.Meta(c, locales.NoWireEvidenceRetainedYet())
		return
	}
	options := make([]string, len(r.Wire))
	for i, wire := range r.Wire {
		options[i] = fmt.Sprintf("%d · Capture #%d · %s · %d · %s", i+1, wire.ID, wire.Method, wire.Status, wire.URL)
	}
	s.wire = min(s.wire, len(options)-1)
	choice := options[s.wire]
	if uikit.Select(c, &choice, options).Label(locales.RecordedWireExchange()).FillWidth().
		Height(uikit.HeightRow).Radius(uikit.RadiusControl).Changed() {
		s.wire = slices.Index(options, choice)
	}
	wire := r.Wire[s.wire]
	effects := locales.NoneRecordedOnExchange()
	if len(wire.Actions) > 0 {
		effects = strings.Join(wire.Actions, " · ")
	}
	uikit.Tip(c, uikit.Body(c, locales.RecordedInterventions(effects)).SingleLine(), effects)
	uikit.Meta(c, locales.WireEvidenceNotCurrentRules())
}

func (s *experimentWorkbench) compareSource(c *ui.Context, r experimentRecord, split *float32) {
	if r.Source == nil {
		uikit.Body(c, locales.ThisRunHasNoRecorded())
		return
	}
	left, right := r.Source.Exchange.flow(), r.compared("")
	left.id, right.id = 1, 2
	s.sourceComparison.compare(c, left, right, [2]string{fmt.Sprintf("Source #%d · snapshot", r.Source.Exchange.ID), fmt.Sprintf("Experiment #%d", r.ID)}, split, nil)
}

func newDebugSource(f flow) *debugSource {
	if f.debugKey == "" {
		f.debugKey = uuid.New().String()
	}
	return &debugSource{Key: f.debugKey, Exchange: snapshotDebugFlow(f)}
}
