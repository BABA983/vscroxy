package app

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A preview owns immutable capture snapshots. Neither closing it nor a newer
// analysis may let an older worker publish into the current view.
type tokenLinkPreview struct {
	open, busy bool
	id         string
	expires    time.Time
	valid      bool
	visible    bool
	cancel     context.CancelFunc
	flows      []flow
	analysis   tokenAnalysis
	problem    string
	config     tokenLinkConfig
	active     int
	plan       *tokenLinkPlan
	applyError string
	reviewed   bool
	applied    bool
}

func (a *app) tokenLinkScope() []flow {
	if a.chosen.Len() > 1 {
		return a.selectedFlows()
	}
	flows := slices.DeleteFunc(slices.Clone(a.flows), func(f flow) bool { return f.kind != "" })
	slices.SortFunc(flows, func(x, y flow) int { return y.id - x.id })
	return flows[:min(len(flows), 200)]
}

func (a *app) openTokenLinks(flows []flow) {
	a.startTokenLinks(flows, true)
}

func (a *app) startTokenLinks(flows []flow, visible bool) *tokenLinkPreview {
	if old := a.tokenLinks; old != nil && old.cancel != nil {
		old.close()
	}
	w := &tokenLinkPreview{id: uuid.New().String(), expires: time.Now().Add(10 * time.Minute), valid: true, visible: visible, open: visible, busy: true, flows: slices.Clone(flows), config: tokenLinkConfig{Environment: a.activeEnv, Variable: "linked_token"}}
	if s := a.collectionRecording; s != nil {
		w.config.Collection, w.config.Environment = s.Collection, s.Environment
	}
	a.tokenLinks = w
	for i := range w.flows {
		f := &w.flows[i]
		f.reqHeader, f.respHeader = f.reqHeader.Clone(), f.respHeader.Clone()
		// The analysis needs no request payloads, examples, or trailers.
		f.reqBody, f.reqFile = "", ""
		f.reqTrailer, f.resTrailer = nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	w.cancel = cancel
	win := a.win
	snapshots := w.flows
	work := func() {
		result, err := recognizeTokenLinks(ctx, snapshots)
		cancel()
		finish := func() {
			if a.tokenLinks != w || !w.valid {
				return
			}
			w.busy, w.analysis = false, result
			if err != nil {
				w.problem = err.Error()
			}
		}
		if win == nil {
			finish()
		} else {
			win.Update(finish)
		}
	}
	if win == nil {
		work()
	} else {
		go work()
	}
	return w
}

func (w *tokenLinkPreview) close() {
	w.open, w.valid = false, false
	w.cancel()
	w.flows, w.analysis, w.plan = nil, tokenAnalysis{}, nil
}

func (a *app) tokenLinksModal(c *ui.Context, t *ui.Theme) {
	w := a.tokenLinks
	if w == nil || !w.open {
		return
	}
	if !time.Now().Before(w.expires) {
		w.close()
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &w.open, locales.TokenLinks(), func() {
		uikit.DialogColumn(c, 820).Height(min(560, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.TokenLinks())
			uikit.Hint(c, locales.TokenLinksSelected(len(w.flows)))
			uikit.Hint(c, locales.EvidenceNotProofOfAuthentication())
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
				if w.busy {
					ui.Spinner(c).Size(20, 20)
					uikit.Body(c, locales.AnalyzingCompleteJsonResponses())
					return
				}
				if w.problem != "" {
					ui.Text(c, w.problem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger)
					return
				}
				if w.applied {
					uikit.Body(c, locales.TokenLinkApplied()).FontWeight(500)
					uikit.Hint(c, locales.SelectTheTargetEnvironmentThen())
					return
				}
				if p := w.plan; p != nil {
					uikit.Body(c, locales.ReviewGeneratedChanges()).FontWeight(500)
					uikit.Body(c, locales.SourceRequestLabel(p.Source))
					uikit.Body(c, locales.ConsumerRequestsLabel(strings.Join(p.Consumers, ", "))).Wrap()
					uikit.Body(c, locales.EnvironmentNewCurrent(p.Environment, p.Variable))
					uikit.Hint(c, locales.ExistingScriptsAreKeptClear())
					uikit.Hint(c, locales.NativeSourceBindingClearsThe())
					uikit.Hint(c, locales.CurrentIsPlaintextLocalStorage())
					for _, script := range []struct{ label, text string }{{"Source · pre-request", p.SourcePre}, {"Source · post-response", p.SourcePost}, {"Consumers · pre-request", p.ConsumerPre}} {
						uikit.Body(c, script.label).FontWeight(500)
						uikit.Mono(c, script.text).Wrap()
					}
					ui.Checkbox(c, &w.reviewed, "I reviewed these script and credential changes").FontSize(uikit.Scaled(uikit.TextBody))
					return
				}
				if len(w.analysis.Links) == 0 {
					uikit.Body(c, locales.NoUnambiguousTokenLinksFound())
					uikit.Hint(c, locales.SelectALoginResponseAnd())
				}
				for i, link := range w.analysis.Links {
					ui.Column(c.Key(i)).Padding(t.Space(3)).Gap(t.Space(2)).Border(1, t.Border).Radius(uikit.RadiusPanel).Children(func() {
						uikit.Body(c, locales.TokenLinkRow(link.Source, len(link.Consumers))).FontWeight(500)
						path, _ := json.Marshal(link.Path)
						uikit.Mono(c, "JSON path segments: "+string(path)).Wrap()
						ids := make([]string, len(link.Consumers))
						for j, id := range link.Consumers {
							ids[j] = "#" + strconv.Itoa(id)
						}
						uikit.Body(c, locales.ConsumersLabel(strings.Join(ids, ", "))).Wrap()
						uikit.Meta(c, locales.ExactValueMatchTokenCapture())
						if len(w.analysis.Links) > 1 {
							uikit.SecondaryButton(c, locales.SelectTokenLink(i+1)).Disabled(w.active == i).OnClick(func() { w.active = i })
						}
					})
				}
				if w.analysis.Skipped > 0 {
					uikit.Hint(c, locales.TokenLinksSkipped(w.analysis.Skipped))
				}
				if len(w.analysis.Links) > 0 {
					uikit.Body(c, locales.ApplyToSavedRequests()).FontWeight(500)
					uikit.Hint(c, locales.SaveTheSourceAndConsumers())
					ui.Field(c, locales.CollectionFolder(), func() {
						uikit.Select(c, &w.config.Collection, a.collectionNames()).Label(locales.TokenLinkCollection())
					})
					ui.Field(c, locales.Environment(), func() { uikit.Select(c, &w.config.Environment, a.envNames()).Label(locales.TokenLinkEnvironment()) })
					ui.Field(c, locales.NewLocalCurrentVariable(), func() { uikit.Input(c, &w.config.Variable).Label(locales.TokenLinkVariable()) })
				}
			})
			if w.applyError != "" {
				ui.Text(c, w.applyError).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger).MaxHeight(64)
			}
			uikit.DialogFooter(c).Children(func() {
				state := "Preview only · nothing changed"
				if w.applied {
					state = "Saved · no requests sent"
				} else if w.plan != nil && w.plan.reserved {
					state = "Private slot saved · review save error"
				}
				uikit.Meta(c, state).Grow(1).MinWidth(0)
				uikit.SecondaryButton(c, locales.CloseTokenLinks()).OnClick(w.close)
				if !w.busy && w.problem == "" && !w.applied && len(w.analysis.Links) > 0 {
					if w.plan == nil {
						uikit.PrimaryButton(c, locales.ReviewTokenLinkChanges()).OnClick(func() {
							p, err := a.planTokenLink(w, w.active, w.config)
							if err != nil {
								w.applyError = err.Error()
							} else {
								w.plan, w.applyError, w.reviewed = p, "", false
							}
						})
					} else {
						uikit.SecondaryButton(c, locales.BackToTokenEvidence()).OnClick(func() { w.plan, w.applyError, w.reviewed = nil, "", false })
						uikit.PrimaryButton(c, locales.ApplyTokenLink()).Disabled(!w.reviewed).OnClick(func() {
							if err := a.applyTokenLink(w.plan); err != nil {
								w.applyError = err.Error()
								return
							}
							w.applied, w.plan, w.applyError = true, nil, ""
							a.done(locales.TokenLinkSavedRunThe())
						})
					}
				}
			})
		})
	})
	if !w.open {
		w.close()
	}
}
