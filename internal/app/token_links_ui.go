package app

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// A preview owns immutable capture snapshots. Neither closing it nor a newer
// analysis may let an older worker publish into the current view.
type tokenLinkPreview struct {
	open, busy bool
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
	if old := a.tokenLinks; old != nil && old.cancel != nil {
		old.cancel()
	}
	w := &tokenLinkPreview{open: true, busy: true, flows: slices.Clone(flows), config: tokenLinkConfig{Environment: a.activeEnv, Variable: "linked_token"}}
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
	work := func() {
		result, err := recognizeTokenLinks(ctx, w.flows)
		cancel()
		finish := func() {
			if a.tokenLinks != w || !w.open {
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
}

func (a *app) tokenLinksModal(c *ui.Context, t *ui.Theme) {
	w := a.tokenLinks
	if w == nil || !w.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &w.open, func() {
		uikit.DialogColumn(c, 820).Height(min(560, height-80)).Children(func() {
			uikit.DialogTitle(c, "Token links")
			uikit.Hint(c, fmt.Sprintf("%d selected captures · JSON response → later Bearer Authorization · same origin only", len(w.flows)))
			uikit.Hint(c, "Evidence, not proof of authentication. Values are hidden. Changes require review and explicit Apply; no requests are sent.")
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Children(func() {
				if w.busy {
					ui.Spinner(c).Size(20, 20)
					uikit.Body(c, "Analyzing complete JSON responses…")
					return
				}
				if w.problem != "" {
					ui.Text(c, w.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
					return
				}
				if w.applied {
					uikit.Body(c, "Token link applied").FontWeight(600)
					uikit.Hint(c, "Select the target environment, then run the source before its consumers. The new Current value starts empty; captured credentials were not copied. Existing scripts were retained.")
					return
				}
				if p := w.plan; p != nil {
					uikit.Body(c, "Review generated changes").FontWeight(600)
					uikit.Body(c, "Source request: "+p.Source)
					uikit.Body(c, "Consumer requests: "+strings.Join(p.Consumers, ", ")).Wrap()
					uikit.Body(c, "Environment: "+p.Environment+" · new Current: "+p.Variable)
					uikit.Hint(c, "Existing scripts are kept. Clear-before-login is prepended; extraction and consumer guards are appended. Other Auth types, dirty drafts and stale proposals are refused. Review authored scripts for deliberate overrides.")
					uikit.Hint(c, "Native source binding clears the token before checks and all inherited/authored setup, even on script compilation failure. Postman export keeps script fallback only and warns that this early-reset guarantee is not portable.")
					uikit.Hint(c, "Current is plaintext local storage, not Vault. The generated scripts are bound to this environment and require its Current slot. A failed multi-file save can keep the empty slot and partial files; reload before preparing again.")
					for _, script := range []struct{ label, text string }{{"Source · pre-request", p.SourcePre}, {"Source · post-response", p.SourcePost}, {"Consumers · pre-request", p.ConsumerPre}} {
						uikit.Body(c, script.label).FontWeight(600)
						uikit.Mono(c, script.text).Wrap()
					}
					ui.Checkbox(c, &w.reviewed, "I reviewed these script and credential changes").FontSize(uikit.TextBody)
					return
				}
				if len(w.analysis.Links) == 0 {
					uikit.Body(c, "No unambiguous token links found")
					uikit.Hint(c, "Select a login response and its later authenticated requests. Only token/access_token/id_token string fields are considered; overlapping requests and ambiguous sources are excluded.")
				}
				for i, link := range w.analysis.Links {
					ui.Column(c.Key(i)).Padding(t.Space(3)).Gap(t.Space(2)).Border(1, t.Border).Radius(uikit.RadiusPanel).Children(func() {
						uikit.Body(c, fmt.Sprintf("Response #%d → %d Bearer requests", link.Source, len(link.Consumers))).FontWeight(600)
						path, _ := json.Marshal(link.Path)
						uikit.Mono(c, "JSON path segments: "+string(path)).Wrap()
						ids := make([]string, len(link.Consumers))
						for j, id := range link.Consumers {
							ids[j] = "#" + strconv.Itoa(id)
						}
						uikit.Body(c, "Consumers: "+strings.Join(ids, ", ")).Wrap()
						uikit.Meta(c, "Exact value match · token •••• · capture-time ordering")
						if len(w.analysis.Links) > 1 {
							uikit.SecondaryButton(c, fmt.Sprintf("Select token link %d", i+1)).Disabled(w.active == i).OnClick(func() { w.active = i })
						}
					})
				}
				if w.analysis.Skipped > 0 {
					uikit.Hint(c, fmt.Sprintf("%d responses were not inspected (non-JSON, incomplete, unavailable or outside analysis limits).", w.analysis.Skipped))
				}
				if len(w.analysis.Links) > 0 {
					uikit.Body(c, "Apply to saved requests").FontWeight(600)
					uikit.Hint(c, "Save the source and consumers to a collection first. Each capture must match one saved request; no request is created automatically.")
					ui.Field(c, "Collection / folder", func() { uikit.Select(c, &w.config.Collection, a.collectionNames()).Label("Token link collection") })
					ui.Field(c, "Environment", func() { uikit.Select(c, &w.config.Environment, a.envNames()).Label("Token link environment") })
					ui.Field(c, "New local Current variable", func() { uikit.Input(c, &w.config.Variable).Label("Token link variable") })
				}
			})
			if w.applyError != "" {
				ui.Text(c, w.applyError).FontSize(uikit.TextBody).TextColor(t.Danger).MaxHeight(64)
			}
			uikit.DialogFooter(c).Children(func() {
				state := "Preview only · nothing changed"
				if w.applied {
					state = "Saved · no requests sent"
				} else if w.plan != nil && w.plan.reserved {
					state = "Private slot saved · review save error"
				}
				uikit.Meta(c, state).Grow(1).MinWidth(0)
				uikit.SecondaryButton(c, "Close token links").OnClick(func() { w.open = false; w.cancel() })
				if !w.busy && w.problem == "" && !w.applied && len(w.analysis.Links) > 0 {
					if w.plan == nil {
						uikit.PrimaryButton(c, "Review token link changes").OnClick(func() {
							p, err := a.planTokenLink(w, w.active, w.config)
							if err != nil {
								w.applyError = err.Error()
							} else {
								w.plan, w.applyError, w.reviewed = p, "", false
							}
						})
					} else {
						uikit.SecondaryButton(c, "Back to token evidence").OnClick(func() { w.plan, w.applyError, w.reviewed = nil, "", false })
						uikit.PrimaryButton(c, "Apply token link").Disabled(!w.reviewed).OnClick(func() {
							if err := a.applyTokenLink(w.plan); err != nil {
								w.applyError = err.Error()
								return
							}
							w.applied, w.plan, w.applyError = true, nil, ""
							a.done("Token link saved; run the source before its consumers")
						})
					}
				}
			})
		})
	})
	if !w.open {
		w.cancel()
	}
}
