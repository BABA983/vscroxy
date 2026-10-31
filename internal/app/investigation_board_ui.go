package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// boardView is the investigation itself: its name and notes, the evidence it
// holds, and a read-only reading of one exchange beside the list.
func (a *app) boardView(c *ui.Context, t *ui.Theme) {
	b := &a.board
	inv := b.current()
	if inv == nil {
		ui.Column(c).Fill().Center().Gap(t.Space(2)).Children(func() {
			ui.Icon(c, layersIcon).Size(uikit.HeightControl, uikit.HeightControl).TextColor(t.TextMuted)
			uikit.Heading(c, "One investigation, one story")
			uikit.Hint(c, "Collect the exchanges that matter, compare them, and export the evidence you reviewed.")
			uikit.Hint(c, "Add a request from Capture's inspector, or open saved sessions.")
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				if uikit.PrimaryButton(c, "New investigation").Clicked() {
					b.create("")
				}
				if uikit.SecondaryButton(c, "Historical sessions…").Clicked() {
					a.investigations.show()
				}
			})
		})
		return
	}
	ui.Column(c).Fill().Children(func() {
		a.boardHeader(c, t, inv)
		ui.Divider(c)
		if b.comparing && len(b.picked) == 2 {
			a.boardCompare(c, t, inv)
			return
		}
		a.boardToolbar(c, t, inv)
		if b.percent == 0 {
			b.percent = 46
		}
		list := func() { a.boardList(c, t, inv) }
		detail := func() { a.boardPreview(c, t, inv) }
		if a.contentWidth(c) < 900 {
			ui.SplitVerticalPercent(c.Key("investigation"), &b.percent, list, detail).Grow(1)
			return
		}
		ui.SplitPercent(c.Key("investigation"), &b.percent, list, detail).Grow(1)
	})
}

func (a *app) boardHeader(c *ui.Context, t *ui.Theme, inv *investigation) {
	b := &a.board
	ui.Row(c).Height(uikit.HeightBar).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		title := uikit.Input(c, &inv.Title).Grow(1).Label("Investigation title").
			Placeholder("Untitled investigation").FontSize(uikit.TextBody)
		if title.Changed() {
			b.mark()
		}
		started := "started " + inv.Created.Local().Format("Jan 2 15:04")
		uikit.Meta(c, fmt.Sprintf("%d exchanges · %s", len(inv.Items), started)).SingleLine()
		if uikit.CompactButton(c, "Export evidence…").Disabled(len(b.picked) == 0).
			Tooltip("Export the selected exchanges after reviewing the raw JSON").Clicked() {
			a.exportInvestigation(c)
		}
		f, hasFlow := a.current()
		ui.PopoverMenuButton(c, "⋯", func(m *ui.Menu) {
			if m.Item("Add the inspected capture").Disabled(!hasFlow).Chosen() {
				a.addToInvestigation(f)
			}
			if m.Item("Clear selection").Disabled(len(b.picked) == 0).Chosen() {
				b.picked, b.comparing = nil, false
			}
			m.Separator()
			if m.Item("Delete investigation…").Chosen() {
				b.deleting = true
			}
		}).
			Label("Investigation options").Tooltip("Investigation options").Height(uikit.HeightControl).Padding(0, t.Space(2)).FontSize(uikit.TextTitle).TextColor(t.TextMuted).Border(0, t.Border)
	})
	if b.notesOpen {
		notes := ui.TextArea(c, &inv.Notes).Height(52).Label("Investigation notes").
			Placeholder("What are you trying to explain? Facts, not guesses.")
		notes.Margin(0, t.Space(3), t.Space(1))
		if notes.Changed() {
			b.mark()
		}
		ui.Row(c).Padding(0, t.Space(3), t.Space(1)).Justify(ui.End).Children(func() {
			if uikit.CompactButton(c, "Done").Clicked() {
				b.notesOpen = false
			}
		})
	} else {
		summary := cmp.Or(inv.Notes, "Add notes: what are you trying to explain?")
		note := uikit.ToolbarButton(c, "Edit notes", inv.Notes, false)
		if note.Clicked() {
			b.notesOpen = true
		}
		note.Margin(0, t.Space(3), t.Space(2))
		note.Children(func() {
			ui.Icon(c, noteIcon).Size(uikit.IconInline, uikit.IconInline)
			ui.Text(c, summary).FontSize(uikit.TextMeta).SingleLine().MaxWidth(900)
		})
	}
	if b.problem != "" {
		ui.Text(c, b.problem).FontSize(uikit.TextMeta).TextColor(t.Danger).Padding(0, t.Space(3), t.Space(2)).SingleLine().Tooltip(b.problem)
	}
}

func (a *app) boardToolbar(c *ui.Context, t *ui.Theme, inv *investigation) {
	b := &a.board
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		search := uikit.Input(c, &b.query).Label("Search investigation").
			Placeholder("Search method, URL, status, notes or errors…").FontSize(uikit.TextMeta).Grow(1)
		if search.Changed() {
			b.list = ui.ListState{}
		}
		uikit.Meta(c, fmt.Sprintf("%d / %d selected", len(b.picked), len(inv.Items))).SingleLine()
		if uikit.CompactButton(c, "Compare two").Disabled(len(b.picked) != 2).Clicked() {
			b.comparing = true
		}
		if uikit.CompactButton(c, "Clear selection").Disabled(len(b.picked) == 0).Clicked() {
			b.picked, b.comparing = nil, false
		}
	})
	ui.Divider(c)
}

// boardList lists the investigation's exchanges. A row click reads it below;
// the checkbox chooses it for comparison and export.
func (a *app) boardList(c *ui.Context, t *ui.Theme, inv *investigation) {
	b := &a.board
	rows := b.visible(inv)
	ui.Column(c).Fill().Children(func() {
		if len(rows) == 0 {
			ui.Column(c).Grow(1).Center().Gap(t.Space(2)).Children(func() {
				if len(inv.Items) == 0 {
					uikit.Body(c, "No exchanges yet")
					uikit.Meta(c, "Inspect a request in Capture and choose Add to investigation.")
				} else {
					uikit.Body(c, "No exchange matches this search")
					if uikit.CompactButton(c, "Clear search").Clicked() {
						b.query = ""
						b.list = ui.ListState{}
					}
				}
			})
			return
		}
		b.list.Key = func(i int) any { return rows[i].ID }
		if b.selected == "" {
			b.selected = rows[0].ID
		}
		ui.List(c, &b.list, len(rows), func(i int) {
			item := rows[i]
			f := item.Source
			row := uikit.RowButton(c, "Read "+boardItemLabel(item)).Height(uikit.HeightControl)
			if item.ID == b.selected {
				uikit.Selected(c, row, b.list.Handle.Focused(c))
			}
			if row.Clicked() {
				if b.selected != item.ID {
					b.reveal = false
				}
				b.selected, b.row = item.ID, i
			}
			row.Children(func() {
				chosen := b.chosen(item.ID)
				if ui.Checkbox(c, &chosen, "").Label("Choose " + boardItemLabel(item)).Changed() {
					b.choose(item.ID, chosen)
				}
				ui.Text(c, cmp.Or(f.Method, "—")).FontSize(uikit.TextMeta).Font(uikit.FontMono).Width(58).SingleLine().TextColor(t.Text)
				status := fmt.Sprint(f.Status)
				ink := uikit.StatusColor(f.Status, t)
				if f.ConnectionError != "" {
					status, ink = "Failed", t.Danger
				}
				ui.Text(c, status).FontSize(uikit.TextMeta).Width(46).SingleLine().TextColor(ink)
				ui.Text(c, cmp.Or(f.URL, f.Path)).FontSize(uikit.TextMeta).Font(uikit.FontMono).SingleLine().Grow(1).Tooltip(f.URL).TextColor(t.Text)
				if note := cmp.Or(item.Note, f.Note); note != "" {
					ui.Icon(c, noteIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted).Tooltip(note)
				}
				at := "—"
				if !f.At.IsZero() {
					at = f.At.Local().Format("15:04:05")
				}
				uikit.Meta(c, at).Font(uikit.FontMono)
			})
		}).Grow(1)
	})
}

// boardPreview reads the chosen exchange with the capture inspector's own
// shape: a side switch when the pane is narrow, Headers and Body tabs, and
// the request and response beside each other only when both fit.
func (a *app) boardPreview(c *ui.Context, t *ui.Theme, inv *investigation) {
	b := &a.board
	_, item := b.find(b.selected)
	if item == nil {
		ui.Column(c).Fill().Center().Children(func() {
			uikit.Hint(c, "Choose an exchange to read it")
		})
		return
	}
	f := investigationFlow(item.Source)
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, cmp.Or(f.method, "REQUEST")).FontSize(uikit.TextBody).Bold().TextColor(uikit.MethodColor(f.method, t))
			status, statusText := fmt.Sprintf("%d %s", f.status, http.StatusText(f.status)), f.statusColor(t)
			if f.conn.error != "" {
				status, statusText = "Failed", t.Danger
			}
			ui.Text(c, status).FontSize(uikit.TextBody).TextColor(statusText)
			uikit.Meta(c, fmt.Sprintf("%d ms · %s", f.millis, humanBytes(f.bytes)))
			ui.Spacer(c)
			if hasSensitiveHeader(f.reqHeader) || hasSensitiveHeader(f.respHeader) {
				label := "Reveal credentials"
				if b.reveal {
					label = "Hide credentials"
				}
				if uikit.ToolbarButton(c, "Reveal credentials", "Credentials are masked so a shared screen does not leak them", b.reveal).
					Height(uikit.HeightCompact).Children(func() {
					ui.Text(c, label).FontSize(uikit.TextMeta)
				}).Clicked() {
					b.reveal = !b.reveal
				}
			}
			if uikit.CompactButton(c, "Remove").Clicked() {
				b.removeItem(item.ID)
			}
		})
		uikit.Meta(c, cmp.Or(f.url, f.host+f.path)).Font(uikit.FontMono).
			Padding(0, 12, 4).SingleLine().Tooltip(f.url)
		note := uikit.Input(c, &item.Note).Label("Exchange note").Placeholder("What does this exchange show?").FontSize(uikit.TextMeta)
		note.Margin(0, t.Space(3), t.Space(2))
		if note.Changed() {
			b.mark()
		}
		if f.conn.error != "" {
			a.boardFailure(c, t, f)
		}
		ui.Divider(c)
		// Two panes narrower than this read worse than one at a time.
		if a.contentWidth(c)*(100-b.percent)/100 < 700 {
			// The switch names the request first; boardMessage counts the
			// response as side 0.
			choice := 1 - b.previewSide
			ui.Row(c).Padding(t.Space(2), t.Space(3), t.Space(0.5)).Children(func() {
				seg := ui.SegmentedBase(c, &choice, 2)
				seg.Track.Padding(t.Space(0.5)).Radius(uikit.RadiusPanel).Border(1, t.Border).Background(t.Background).Children(func() {
					for i, name := range []string{"Request", "Response"} {
						s := seg.Segment(i).Label(name).Height(uikit.HeightCompact).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
						if i == choice {
							s.Background(t.SurfaceHover).TextColor(t.Text)
						}
						s.Children(func() { ui.Text(c, name).FontSize(uikit.TextMeta) })
					}
				})
				if seg.Track.Changed() {
					b.previewSide = 1 - choice
				}
			})
			a.boardMessage(c, t, f, b.previewSide)
			return
		}
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.boardMessage(c, t, f, 1)
			ui.Box(c).Width(1).FillHeight().Background(t.Border)
			a.boardMessage(c, t, f, 0)
		})
	})
}

// boardMessage reads one side of an exchange: its headers or its body, as the
// capture inspector does, never both merged into one run of text.
func (a *app) boardMessage(c *ui.Context, t *ui.Theme, f flow, side int) {
	b := &a.board
	headers, body, line, problem := f.respHeader, f.resBody, "No response", f.resError
	title := "Response"
	if side == 1 {
		headers, body, problem = f.reqHeader, f.reqBody, f.reqError
		line, title = f.method+" "+f.path, "Request"
	}
	if side == 0 && f.status != 0 {
		line = fmt.Sprintf("%d %s", f.status, http.StatusText(f.status))
	}
	ui.Column(c).Grow(1).Basis(0).FillHeight().Children(func() {
		ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(2)).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			uikit.Label(c, title).Padding(0, t.Space(2), 0, t.Space(1))
			uikit.Tabs(c, &b.previewTab[side], title+" pane", "Headers", "Body")
			ui.Spacer(c)
			uikit.Meta(c, line).Font(uikit.FontMono).SingleLine()
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(0, t.Space(3), t.Space(2)).Gap(t.Space(1)).Children(func() {
				if b.previewTab[side] == 0 {
					if len(headers) == 0 {
						uikit.Meta(c, "No headers recorded")
					}
					for _, key := range slices.Sorted(maps.Keys(headers)) {
						value := strings.Join(headers[key], ", ")
						if sensitiveHeader(key) && !b.reveal {
							value = "••••••••  masked"
						}
						uikit.Meta(c, key+": "+value).Font(uikit.FontMono).Selectable().
							Tooltip(key + "\n" + value)
					}
					return
				}
				if body != "" {
					ui.Text(c, boardBody(body)).FontSize(uikit.TextMeta).Font(uikit.FontMono).Selectable()
				} else {
					uikit.Meta(c, "Empty body")
				}
				if problem != "" {
					ui.Text(c, problem).FontSize(uikit.TextMeta).TextColor(t.Warning)
				}
			})
		})
	})
}

// boardFailure layers the evidence: what was recorded, the raw error, then
// causes that are explicitly not confirmed.
func (a *app) boardFailure(c *ui.Context, t *ui.Theme, f flow) {
	e := f.conn.explanation()
	ui.Column(c).Margin(0, t.Space(3), t.Space(2)).Padding(t.Space(2), t.Space(2)).Gap(t.Space(1)).Radius(uikit.RadiusPanel).Background(uikit.Colors(t).Panel).Children(func() {
		ui.Text(c, fmt.Sprintf("Recorded failure · %s · %s", cmp.Or(f.conn.errorStage, "unknown stage"), f.conn.error)).
			FontSize(uikit.TextMeta).TextColor(t.Danger).SingleLine().Tooltip(f.conn.error)
		uikit.Label(c, "Possible causes · not confirmed")
		ui.Text(c, e.causes).FontSize(uikit.TextMeta)
		uikit.Label(c, "What to try")
		ui.Text(c, e.steps).FontSize(uikit.TextMeta)
		if f.conn.log != "" {
			uikit.Meta(c, "Connection log retained with this exchange")
		}
	})
}

func (a *app) boardCompare(c *ui.Context, t *ui.Theme, inv *investigation) {
	b := &a.board
	items := b.pickedItems()
	if len(items) != 2 {
		b.comparing = false
		return
	}
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.IconButton(c, "Back to evidence", "Back to the investigation", backIcon).Clicked() {
			b.comparing = false
		}
		ui.Text(c, "Compare two exchanges").FontSize(uikit.TextBody).Bold()
		uikit.Meta(c, "Read-only snapshots; neither side can be edited here")
		ui.Spacer(c)
	})
	ui.Divider(c)
	left := investigationFlow(items[0].Source)
	right := investigationFlow(items[1].Source)
	left.id, right.id = 1, 2
	b.comparison.compare(c, left, right, [2]string{boardItemLabel(items[0]), boardItemLabel(items[1])})
}

// exportInvestigation builds the evidence bundle from the chosen exchanges and
// hands it to the review flow, which is where consent lives.
func (a *app) exportInvestigation(c *ui.Context) {
	b := &a.board
	inv := b.current()
	items := b.pickedItems()
	if inv == nil || len(items) == 0 {
		return
	}
	data, err := boardEvidence(inv, items)
	if err != nil {
		b.problem = "Evidence: " + err.Error()
		return
	}
	b.problem = ""
	a.investigations.showReview(data)
}

// addToInvestigation snapshots the inspected exchange into the board. The
// snapshot, not the live flow, is what the investigation keeps.
func (a *app) addToInvestigation(f flow) {
	inv, err := a.board.add(f)
	if err != nil {
		a.setMessage("Investigation: " + err.Error())
		return
	}
	a.setMessage(fmt.Sprintf("Added %s %s to %s", f.method, cmp.Or(f.path, f.url), inv.Title))
	a.board.note = inv.Title
}

// boardConfirmModal asks before an investigation and its evidence are gone.
func (a *app) boardConfirmModal(c *ui.Context, t *ui.Theme) {
	b := &a.board
	if !b.deleting {
		return
	}
	inv := b.current()
	if inv == nil {
		b.deleting = false
		return
	}
	uikit.Modal(c, &b.deleting, func() {
		ui.Column(c).Width(440).Gap(t.Space(3)).Children(func() {
			uikit.DialogTitle(c, "Delete investigation?")
			ui.Text(c, fmt.Sprintf("%q and its %d exchanges are removed from this app. Export evidence first if you need to keep it.", inv.Title, len(inv.Items))).
				TextColor(t.TextMuted)
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					b.deleting = false
				}
				if uikit.PrimaryButton(c, "Delete investigation").Clicked() {
					b.deleteActive()
					b.list = ui.ListState{}
					b.deleting = false
				}
			})
		})
	})
}

// boardSummary is the one line the footer shows for this workspace.
func (a *app) boardSummary() string {
	b := &a.board
	inv := b.current()
	if inv == nil {
		return fmt.Sprintf("%d investigations", len(b.items))
	}
	text := fmt.Sprintf("%d exchanges in %q", len(inv.Items), inv.Title)
	if len(b.picked) > 0 {
		text += fmt.Sprintf(" · %d selected", len(b.picked))
	}
	return text
}
