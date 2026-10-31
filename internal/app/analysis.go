package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// captureAnalysis keeps the reader's comparison and scroll position while
// unrelated traffic arrives. The pair is identified by capture IDs, not rows.
type captureAnalysis struct {
	tab      int
	part     string
	swapped  bool
	pair     [2]int
	diffPart string
	diff     *editor.DiffState
}

func (a *app) inspectCapture(id int) {
	if !slices.ContainsFunc(a.flows, func(f flow) bool { return f.id == id }) {
		a.setMessage("Capture is no longer in this session")
		return
	}
	// Reveal a hidden relationship in a separate view, never by erasing the
	// reader's existing search, filters and list position.
	if !slices.ContainsFunc(a.visible, func(i int) bool { return a.flows[i].id == id }) {
		a.views.capture(a)
		a.views.newView(a)
		a.views.activeView().preset = fmt.Sprintf("Capture #%d", id)
		a.setMessage("Opened linked capture in a new view; previous filters preserved")
	}
	a.chosen.Clear()
	a.chosen.Add(id)
	a.pickedID, a.workspace = id, 0
	a.restoreSelection()
	if view := a.views.activeView(); view != nil {
		// Show the row without centering it: a centered offset can leave a
		// sliver of the row above cut by the header.
		view.list.ScrollIntoView(a.picked)
	}
}

func (a *app) replayLinks(c *ui.Context, f flow) {
	var replays []flow
	for _, candidate := range a.flows {
		if candidate.replayOf == f.id {
			replays = append(replays, candidate)
		}
	}
	if f.replayOf == 0 && len(replays) == 0 {
		return
	}
	t := c.Theme()
	ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(func() {
		if f.replayOf != 0 {
			present := slices.ContainsFunc(a.flows, func(source flow) bool { return source.id == f.replayOf })
			if uikit.SecondaryButton(c, fmt.Sprintf("Replay of #%d", f.replayOf)).Disabled(!present).
				Tooltip("Inspect source capture; unavailable if it was removed from this session").Clicked() {
				a.inspectCapture(f.replayOf)
			}
			if uikit.SecondaryButton(c, "Compare with source").Disabled(!present).Clicked() {
				a.inspectCapture(f.replayOf)
				a.chosen.Add(f.id)
				a.analysis = captureAnalysis{tab: 1}
			}
		}
		if len(replays) > 0 {
			ui.PopoverMenuButton(c, fmt.Sprintf("Replays (%d)", len(replays)), func(m *ui.Menu) {
				for _, replay := range replays {
					if m.Item(fmt.Sprintf("#%d · %s · %d · %d ms", replay.id, replay.path, replay.status, replay.millis)).Chosen() {
						a.inspectCapture(replay.id)
					}
				}
			}).Height(uikit.HeightControl).FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
		}
		ui.Text(c, "Links stay with saved sessions").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
	})
}

func (a *app) analysisDetail(c *ui.Context, selected []flow) {
	t := c.Theme()
	s := &a.analysis
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).MinHeight(uikit.HeightLarge).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, fmt.Sprintf("%d requests selected", len(selected))).FontSize(uikit.TextBody).FontFeatures("tnum").Bold().Grow(1)
			if uikit.SecondaryButton(c, "Summary").Clicked() {
				s.tab = 0
			}
			if uikit.SecondaryButton(c, "Compare two").Disabled(len(selected) != 2).Tooltip("Choose exactly two rows with Ctrl/Cmd-click").Clicked() {
				s.tab = 1
			}
			if uikit.SecondaryButton(c, "Inspect focused").Clicked() {
				if f, ok := a.current(); ok {
					a.inspectCapture(f.id)
				}
			}
		})
		ui.Divider(c)
		if s.tab == 1 && len(selected) == 2 {
			a.compareCaptures(c, selected[0], selected[1])
			return
		}
		ui.Scroll(c).Grow(1).Padding(t.Space(3)).Gap(t.Space(3)).Children(func() {
			var bytes, duration int64
			errors, incomplete := 0, 0
			times := make([]int, 0, len(selected))
			for _, f := range selected {
				bytes += int64(f.bytes)
				duration += int64(f.millis)
				times = append(times, f.millis)
				if f.status >= 400 || f.conn.error != "" || f.reqError != "" || f.resError != "" {
					errors++
				}
				if f.reqError != "" || f.resError != "" {
					incomplete++
				}
			}
			slices.Sort(times)
			ui.Row(c).Gap(t.Space(4)).Wrap().Children(func() {
				ui.Text(c, fmt.Sprintf("Response bytes: %d B", bytes)).FontSize(uikit.TextBody).FontFeatures("tnum").Bold()
				ui.Text(c, fmt.Sprintf("Errors: %d", errors)).FontSize(uikit.TextBody).FontFeatures("tnum").TextColor(t.Danger)
				ui.Text(c, fmt.Sprintf("Incomplete bodies: %d", incomplete)).FontSize(uikit.TextBody).FontFeatures("tnum").TextColor(t.TextMuted)
			})
			ui.Text(c, fmt.Sprintf("Average: %.1f ms · P50: %d ms · Max: %d ms", float64(duration)/float64(len(selected)), times[(len(times)-1)/2], times[len(times)-1])).FontSize(uikit.TextBody).FontFeatures("tnum")
			ui.Text(c, "Recorded per-capture durations, not elapsed session time. P50 uses nearest rank. Errors include HTTP 4xx/5xx, connection failures and incomplete bodies.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
			ui.Divider(c)
			ui.Text(c, "Slowest requests").FontSize(uikit.TextBody).Bold()
			slowest := slices.Clone(selected)
			slices.SortStableFunc(slowest, func(a, b flow) int { return cmp.Compare(b.millis, a.millis) })
			for _, f := range slowest[:min(5, len(slowest))] {
				ui.Row(c).Gap(t.Space(2)).MinHeight(uikit.HeightControl).AlignItems(ui.Center).Children(func() {
					if uikit.SecondaryButton(c, fmt.Sprintf("Inspect #%d", f.id)).Clicked() {
						a.inspectCapture(f.id)
					}
					ui.Text(c, f.method+" "+f.host+f.path).FontSize(uikit.TextBody).SingleLine().Grow(1).Tooltip(f.url)
					ui.Text(c, fmt.Sprintf("%d ms", f.millis)).FontSize(uikit.TextMeta).FontFeatures("tnum").Bold().Width(65)
				})
			}
		})
	})
}

func (a *app) compareCaptures(c *ui.Context, left, right flow) {
	a.analysis.compare(c, left, right, [2]string{fmt.Sprintf("#%d", left.id), fmt.Sprintf("#%d", right.id)})
}

// Each reader owns its diff buffers and position, including request documents.
func (s *captureAnalysis) compare(c *ui.Context, left, right flow, labels [2]string) {
	t := c.Theme()
	if s.swapped {
		left, right = right, left
		labels[0], labels[1] = labels[1], labels[0]
	}
	if s.part == "" {
		s.part = "Response body"
	}
	ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(func() {
		uikit.Select(c, &s.part, []string{"Response body", "Response headers", "Request body", "Request headers"}).
			Label("Compare content").Width(165).Height(uikit.HeightControl).FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
		if uikit.SecondaryButton(c, "Swap sides").Clicked() {
			s.swapped = !s.swapped
			left, right = right, left
			labels[0], labels[1] = labels[1], labels[0]
		}
		if uikit.SecondaryButton(c, "Split").Clicked() && s.diff != nil {
			s.diff.Layout = editor.DiffSplit
		}
		if uikit.SecondaryButton(c, "Unified").Clicked() && s.diff != nil {
			s.diff.Layout = editor.DiffUnified
		}
	})
	pair := [2]int{left.id, right.id}
	if s.diff == nil || s.pair != pair || s.diffPart != s.part {
		text := func(f flow) string {
			switch s.part {
			case "Request body":
				return f.body(false)
			case "Request headers":
				return f.reqHeaders
			case "Response headers":
				return f.resHeaders
			default:
				return f.body(true)
			}
		}
		layout := editor.DiffSplit
		if s.diff != nil {
			layout = s.diff.Layout
		}
		s.diff = &editor.DiffState{Old: editor.NewBuffer(text(left)), New: editor.NewBuffer(text(right)), Layout: layout}
		s.pair, s.diffPart = pair, s.part
	}
	ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(3)).Children(func() {
		for i, f := range []flow{left, right} {
			label := "Left"
			if i == 1 {
				label = "Right"
			}
			ui.Text(c, fmt.Sprintf("%s %s · %s %s · %d · %d ms", label, labels[i], f.method, f.path, f.status, f.millis)).
				FontSize(uikit.TextMeta).FontFeatures("tnum").SingleLine().Grow(1).Tooltip(f.url)
		}
	})
	ui.Text(c, "Read-only comparison · bodies limited to 8 MiB; truncation/incomplete notices are included").
		FontSize(uikit.TextMeta).TextColor(t.TextMuted).Padding(t.Space(1), t.Space(3))
	s.diff.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	s.diff.Theme = uikit.EditorTheme(t)
	editor.DiffView(c, s.diff).Grow(1).FillWidth()
}
