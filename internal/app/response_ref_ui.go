package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// responseRefSourceName names the saved source request by its current label.
// The label is only how the reference is written; resolution settles it to
// the request ID before reading a response.
func (a *app) responseRefSourceName(flowID int, w *tokenLinkPreview) string {
	at := slices.IndexFunc(w.flows, func(f flow) bool { return f.id == flowID })
	if at < 0 {
		return fmt.Sprintf("#%d", flowID)
	}
	matches := a.matchingRequestsWithVars(w.flows[at], a.activeVars())
	if len(matches) == 1 {
		return matches[0].Name
	}
	return fmt.Sprintf("#%d", flowID)
}

func (a *app) responseRefsView(c *ui.Context, t *ui.Theme, w *tokenLinkPreview) {
	uikit.Body(c, locales.ResponseReferenceSuggestions()).FontWeight(500)
	uikit.Hint(c, locales.ResponseReferencePreviewHint())
	if w.refError != "" {
		uikit.ErrorText(c, w.refError).MaxHeight(64)
	}
	if w.refApplied {
		uikit.Body(c, locales.ResponseReferenceApplied()).FontWeight(500)
		return
	}
	if p := w.refPlan; p != nil {
		uikit.Body(c, locales.ReviewResponseReferences()).FontWeight(500)
		uikit.Body(c, locales.ResponseReferenceExpression(p.Expression)).Wrap()
		names := make([]string, len(p.Consumers))
		for i, id := range p.Consumers {
			names[i] = id
			if at := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.ID == id }); at >= 0 {
				names[i] = a.requests[at].Name
			}
		}
		uikit.Body(c, locales.ConsumerRequestsLabel(strings.Join(names, ", "))).Wrap()
		uikit.Hint(c, locales.ExistingScriptsAreKeptClear())
		uikit.Checkbox(c, &w.refReviewed, locales.ReviewedResponseReferences())
		uikit.SecondaryButton(c, locales.BackToResponseReferences()).OnClick(func() {
			w.refPlan, w.refReviewed, w.refError = nil, false, ""
		})
		uikit.PrimaryButton(c, locales.ApplyResponseReferences()).Disabled(!w.refReviewed).OnClick(func() {
			if err := a.applyResponseRefs(w.refPlan); err != nil {
				w.refError = err.Error()
				return
			}
			w.refApplied, w.refPlan, w.refError = true, nil, ""
			a.done(locales.ResponseReferenceApplied())
		})
		return
	}
	if len(w.refAnalysis.Suggestions) == 0 {
		uikit.Body(c, locales.NoResponseReferencesFound())
		return
	}
	for i, suggestion := range w.refAnalysis.Suggestions {
		ui.Column(c.Key("ref-"+strconv.Itoa(i))).Padding(t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Border(1, t.Border).Radius(uikit.RadiusPanel).Children(func() {
			name := a.responseRefSourceName(suggestion.Source, w)
			uikit.Body(c, locales.ResponseReferenceSourceLabel(suggestion.Source, len(suggestion.Consumers))).FontWeight(500)
			uikit.Mono(c, responseRefExpression(name, suggestion.Path)).Wrap()
			uikit.Meta(c, locales.MaskedResponseValue(len(suggestion.token)))
			uikit.Meta(c, locales.ExactValueMatchTokenCapture())
			uikit.SecondaryButton(c, locales.PreviewResponseReference()).OnClick(func() {
				p, err := a.planResponseRefs(w, i)
				if err != nil {
					w.refError = err.Error()
					return
				}
				w.refPlan, w.refIndex, w.refReviewed, w.refError = p, i, false, ""
			})
		})
	}
}
