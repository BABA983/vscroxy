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
	w := &tokenLinkPreview{open: true, busy: true, flows: slices.Clone(flows)}
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
			uikit.Hint(c, "Read-only evidence, not proof of authentication. Values are hidden; no scripts are executed and no requests are sent.")
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
					})
				}
				if w.analysis.Skipped > 0 {
					uikit.Hint(c, fmt.Sprintf("%d responses were not inspected (non-JSON, incomplete, unavailable or outside analysis limits).", w.analysis.Skipped))
				}
			})
			uikit.DialogFooter(c).Children(func() {
				uikit.Meta(c, "Preview only · nothing changed").Grow(1)
				uikit.SecondaryButton(c, "Close token links").OnClick(func() { w.open = false; w.cancel() })
			})
		})
	})
	if !w.open {
		w.cancel()
	}
}
