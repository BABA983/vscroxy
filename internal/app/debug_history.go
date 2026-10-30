package app

import (
	"fmt"
	"slices"
	"strings"
	"uuid"

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
	f := investigationFlow(source.Exchange)
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
	if d.origin != nil {
		a.openDebugHistory(d.origin)
		return
	}
	s := &a.experiments
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
	s.draftKey = ""
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
	return s.scope == nil && (s.draftKey == "" || r.DraftKey == s.draftKey) ||
		s.scope != nil && r.Source != nil && r.Source.Key == s.scope.Key
}

func (a *app) debugDrafts(c *ui.Context) {
	s := &a.experiments
	if s.scope == nil {
		if s.draftKey != "" {
			ui.Text(c, "Only sends linked to this draft; never matched by URL.").FontSize(textMeta).TextColor(c.Theme().TextMuted)
			if secondaryButton(c, "Show all experiments").FillWidth().Clicked() {
				s.draftKey = ""
			}
			ui.Divider(c)
		}
		return
	}
	ui.Text(c, fmt.Sprintf("Source snapshot · Capture #%d", s.scope.Exchange.ID)).FontSize(textBody).FontFeatures("tnum").Bold()
	ui.Text(c, "Only explicitly linked drafts and retained runs; never matched by URL.").FontSize(textMeta).TextColor(c.Theme().TextMuted)
	index := slices.IndexFunc(a.flows, func(f flow) bool { return f.debugKey == s.scope.Key })
	if secondaryButton(c, "Inspect source capture").FillWidth().Disabled(index < 0).Clicked() {
		s.open = false
		a.inspectCapture(a.flows[index].id)
	}
	if index < 0 {
		ui.Text(c, "Original capture unavailable; snapshot retained.").FontSize(textMeta).TextColor(c.Theme().TextMuted)
	}
	if secondaryButton(c, "Show all experiments").FillWidth().Clicked() {
		s.scope, s.draftKey = nil, ""
	}
	ui.Divider(c)
	ui.Text(c, "Open drafts").FontSize(textBody).Bold()
	n := 0
	for _, d := range a.documents {
		if d.origin == nil || s.scope == nil || d.origin.Key != s.scope.Key {
			continue
		}
		n++
		if secondaryButton(c, d.title()).Label(fmt.Sprintf("Open linked draft %d", n)).FillWidth().Tooltip(d.composeMethod + " " + d.composeURL).Clicked() {
			a.requestDocument, a.requestChoice, a.workspace = d, d.savedKey, 1
			d.revealTab, s.open = true, false
			d.urlInput.Focus()
		}
	}
	if n == 0 {
		ui.Text(c, "No open drafts for this source.").FontSize(textMeta)
	}
	ui.Divider(c)
	ui.Text(c, "Retained experiments").FontSize(textBody).Bold()
}

func (a *app) debugWire(c *ui.Context, r experimentRecord) {
	s := &a.experiments
	if r.RunKey == "" {
		ui.Text(c, "Legacy run · wire provenance was not recorded.").FontSize(textMeta).TextColor(c.Theme().TextMuted)
		return
	}
	if len(r.Wire) == 0 {
		ui.Text(c, "No wire evidence retained yet; no rule outcome can be inferred.").FontSize(textMeta).TextColor(c.Theme().TextMuted)
		return
	}
	options := make([]string, len(r.Wire))
	for i, wire := range r.Wire {
		options[i] = fmt.Sprintf("%d · Capture #%d · %s · %d · %s", i+1, wire.ID, wire.Method, wire.Status, wire.URL)
	}
	s.wire = min(s.wire, len(options)-1)
	choice := options[s.wire]
	if ui.Select(c, &choice, options).Label("Recorded wire exchange").FillWidth().
		Height(heightControl).FontSize(textBody).Radius(radiusControl).Changed() {
		s.wire = slices.Index(options, choice)
	}
	wire := r.Wire[s.wire]
	effects := "None recorded on this exchange"
	if len(wire.Actions) > 0 {
		effects = strings.Join(wire.Actions, " · ")
	}
	ui.Text(c, "Recorded interventions: "+effects).FontSize(textBody).SingleLine().Tooltip(effects)
	ui.Text(c, "Wire evidence, not current rules. Redirect hops stay separate; source comparison uses the client result.").FontSize(textMeta).TextColor(c.Theme().TextMuted)
}

func (s *experimentWorkbench) compareSource(c *ui.Context, r experimentRecord) {
	if r.Source == nil {
		ui.Text(c, "This run has no recorded capture source.").FontSize(textBody)
		return
	}
	left, right := investigationFlow(r.Source.Exchange), r.compared("")
	left.id, right.id = 1, 2
	s.sourceComparison.compare(c, left, right, [2]string{fmt.Sprintf("Source #%d · snapshot", r.Source.Exchange.ID), fmt.Sprintf("Experiment #%d", r.ID)})
}

func newDebugSource(f flow) *debugSource {
	if f.debugKey == "" {
		f.debugKey = uuid.New().String()
	}
	return &debugSource{Key: f.debugKey, Exchange: snapshotDebugFlow(f)}
}
