package main

import (
	"cmp"
	"fmt"
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
	ui.Row(c).Padding(2, 12).Gap(8).Wrap().Children(func() {
		if f.replayOf != 0 {
			present := slices.ContainsFunc(a.flows, func(source flow) bool { return source.id == f.replayOf })
			if ui.Button(c, fmt.Sprintf("Replay of #%d", f.replayOf)).FontSize(11).Disabled(!present).
				Tooltip("Inspect source capture; unavailable if it was removed from this session").Clicked() {
				a.inspectCapture(f.replayOf)
			}
			if ui.Button(c, "Compare with source").FontSize(11).Disabled(!present).Clicked() {
				a.inspectCapture(f.replayOf)
				a.chosen.Add(f.id)
				a.analysis = captureAnalysis{tab: 1}
			}
		}
		if len(replays) > 0 {
			ui.MenuButton(c, fmt.Sprintf("Replays (%d)", len(replays)), func(m *ui.Menu) {
				for _, replay := range replays {
					if m.Item(fmt.Sprintf("#%d · %s · %d · %d ms", replay.id, replay.path, replay.status, replay.millis)).Chosen() {
						a.inspectCapture(replay.id)
					}
				}
			}).FontSize(11)
		}
		ui.Text(c, "Links stay with saved sessions").FontSize(10).TextColor(t.TextMuted)
	})
}

func (a *app) analysisDetail(c *ui.Context, selected []flow) {
	t := c.Theme()
	s := &a.analysis
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(6, 12).Gap(10).Wrap().Children(func() {
			ui.Text(c, fmt.Sprintf("%d requests selected", len(selected))).FontSize(13).Bold().Grow(1)
			if ui.Button(c, "Summary").FontSize(11).Clicked() {
				s.tab = 0
			}
			if ui.Button(c, "Compare two").FontSize(11).Disabled(len(selected) != 2).Tooltip("Choose exactly two rows with Ctrl/Cmd-click").Clicked() {
				s.tab = 1
			}
			if ui.Button(c, "Inspect focused").FontSize(11).Clicked() {
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
		ui.Scroll(c).Grow(1).Padding(12).Gap(10).Children(func() {
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
			ui.Row(c).Gap(20).Wrap().Children(func() {
				ui.Text(c, fmt.Sprintf("Response bytes: %d B", bytes)).FontSize(12).Bold()
				ui.Text(c, fmt.Sprintf("Errors: %d", errors)).FontSize(12).TextColor(t.Danger)
				ui.Text(c, fmt.Sprintf("Incomplete bodies: %d", incomplete)).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Text(c, fmt.Sprintf("Average: %.1f ms · P50: %d ms · Max: %d ms", float64(duration)/float64(len(selected)), times[(len(times)-1)/2], times[len(times)-1])).FontSize(12)
			ui.Text(c, "Recorded per-capture durations, not elapsed session time. P50 uses nearest rank. Errors include HTTP 4xx/5xx, connection failures and incomplete bodies.").FontSize(11).TextColor(t.TextMuted)
			ui.Divider(c)
			ui.Text(c, "Slowest requests").FontSize(12).Bold()
			slowest := slices.Clone(selected)
			slices.SortStableFunc(slowest, func(a, b flow) int { return cmp.Compare(b.millis, a.millis) })
			for _, f := range slowest[:min(5, len(slowest))] {
				ui.Row(c).Gap(10).Children(func() {
					if ui.Button(c, fmt.Sprintf("Inspect #%d", f.id)).FontSize(11).Clicked() {
						a.inspectCapture(f.id)
					}
					ui.Text(c, f.method+" "+f.host+f.path).FontSize(11).SingleLine().Grow(1).Tooltip(f.url)
					ui.Text(c, fmt.Sprintf("%d ms", f.millis)).FontSize(11).Bold().Width(65)
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
	if s.swapped {
		left, right = right, left
		labels[0], labels[1] = labels[1], labels[0]
	}
	if s.part == "" {
		s.part = "Response body"
	}
	ui.Row(c).Padding(6, 12).Gap(8).Wrap().Children(func() {
		ui.Select(c, &s.part, []string{"Response body", "Response headers", "Request body", "Request headers"}).Label("Compare content").Width(165)
		if ui.Button(c, "Swap sides").FontSize(11).Clicked() {
			s.swapped = !s.swapped
			left, right = right, left
			labels[0], labels[1] = labels[1], labels[0]
		}
		if ui.Button(c, "Split").FontSize(11).Clicked() && s.diff != nil {
			s.diff.Layout = editor.DiffSplit
		}
		if ui.Button(c, "Unified").FontSize(11).Clicked() && s.diff != nil {
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
	ui.Row(c).Padding(2, 12).Gap(12).Children(func() {
		for i, f := range []flow{left, right} {
			label := "Left"
			if i == 1 {
				label = "Right"
			}
			ui.Text(c, fmt.Sprintf("%s %s · %s %s · %d · %d ms", label, labels[i], f.method, f.path, f.status, f.millis)).FontSize(11).SingleLine().Grow(1).Tooltip(f.url)
		}
	})
	ui.Text(c, "Read-only comparison · bodies limited to 8 MiB; truncation/incomplete notices are included").FontSize(10).TextColor(c.Theme().TextMuted).Padding(2, 12)
	editor.DiffView(c, s.diff).Grow(1).FillWidth()
}
