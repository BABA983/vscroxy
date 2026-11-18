package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// diffPoolLimit keeps a pool readable and its comparison cheap; the table's
// selection can hold more, and a new pool takes the first captures.
const diffPoolLimit = 8

// openPool starts a pool from the selected captures, in table order.
func (s *captureAnalysis) openPool(selected []flow) {
	s.pool = s.pool[:0]
	for _, f := range selected {
		if len(s.pool) == diffPoolLimit {
			break
		}
		if !slices.Contains(s.pool, f.id) {
			s.pool = append(s.pool, f.id)
		}
	}
	s.poolLeft, s.poolRight, s.tab = "", "", 2
}

// addToPool appends captures the pool does not hold yet.
func (s *captureAnalysis) addToPool(selected []flow) {
	for _, f := range selected {
		if len(s.pool) == diffPoolLimit {
			return
		}
		if !slices.Contains(s.pool, f.id) {
			s.pool = append(s.pool, f.id)
		}
	}
}

// poolFlows resolves the pool's IDs against the captures still retained.
func (a *app) poolFlows() []flow {
	var flows []flow
	for _, id := range a.analysis.pool {
		if i := slices.IndexFunc(a.flows, func(f flow) bool { return f.id == id }); i >= 0 {
			flows = append(flows, a.flows[i])
		}
	}
	return flows
}

func poolLabel(f flow) string {
	return fmt.Sprintf("#%d %s %s", f.id, f.method, f.path)
}

// diffPool compares any two captures kept in the pool. The pool survives
// table selections and row changes until a new pool is started.
func (a *app) diffPool(c *ui.Context, selected []flow) {
	t := c.Theme()
	s := &a.analysis
	flows := a.poolFlows()
	ui.Row(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Wrap().Children(func() {
		uikit.Meta(c, locales.DiffPoolCount(len(flows))).Grow(1)
		if uikit.Tip(c, uikit.SecondaryButton(c, locales.AddSelectionToPool()).Disabled(len(flows) == diffPoolLimit), locales.DiffPoolLimit(diffPoolLimit)).Clicked() {
			s.addToPool(selected)
		}
		if uikit.SecondaryButton(c, locales.ClearPool()).Clicked() {
			s.pool, s.poolLeft, s.poolRight = nil, "", ""
		}
	})
	if len(flows) < 2 {
		uikit.Hint(c, locales.DiffPoolNeedsTwo()).Padding(t.Space(uikit.SpaceXL))
		return
	}
	ui.Row(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Wrap().Children(func() {
		for _, f := range flows {
			label := poolLabel(f)
			ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
				uikit.Caption(c, label).TextColor(uikit.Colors(t).MutedForeground)
				if uikit.RowIconButton(c, locales.RemoveFromPool(), locales.RemoveFromPool(), closeIcon).Clicked() {
					s.pool = slices.DeleteFunc(s.pool, func(id int) bool { return id == f.id })
					if s.poolLeft == label || s.poolRight == label {
						s.poolLeft, s.poolRight = "", ""
					}
				}
			})
		}
	})
	labels := make([]string, len(flows))
	for i, f := range flows {
		labels[i] = poolLabel(f)
	}
	if slices.Index(labels, s.poolLeft) < 0 {
		s.poolLeft = labels[0]
	}
	if slices.Index(labels, s.poolRight) < 0 || s.poolRight == s.poolLeft {
		s.poolRight = labels[1]
		if s.poolRight == s.poolLeft {
			s.poolRight = labels[0]
		}
	}
	ui.Column(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		for _, side := range []struct {
			label string
			value *string
		}{{locales.LeftSide(), &s.poolLeft}, {locales.RightSide(), &s.poolRight}} {
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, side.label).Width(70)
				uikit.SelectLabels(c, side.value, labels, optionLabel).Label(side.label).Grow(1)
			})
		}
	})
	a.compareCaptures(c, flows[slices.Index(labels, s.poolLeft)], flows[slices.Index(labels, s.poolRight)])
}

// exportCaptureDiff writes the current comparison as a unified diff file.
func (a *app) exportCaptureDiff(left, right flow) {
	part := strings.ToLower(strings.ReplaceAll(a.analysis.part, " ", "-"))
	oldName := fmt.Sprintf("capture-%d-%s.txt", left.id, part)
	newName := fmt.Sprintf("capture-%d-%s.txt", right.id, part)
	data := captureDiff(oldName, newName, a.analysis.content(left), a.analysis.content(right))
	if data == "" {
		a.inform(locales.CapturesHaveNoDifferences())
		return
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       locales.ExportUnifiedDiff(),
		DefaultPath: fmt.Sprintf("capture-%d-%d.diff", left.id, right.id),
		Filters:     []mygo.FileFilter{{Name: "Unified diff", Extensions: []string{"diff"}}},
	})
	if err != nil || path == "" {
		return
	}
	if err := store.WriteFile(path, []byte(data)); err != nil {
		a.fail(locales.ExportUnifiedDiff(), err)
		return
	}
	a.done(locales.ExportedUnifiedDiff())
}
