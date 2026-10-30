package app

import (
	"cmp"
	"fmt"
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
		ui.Column(c).Fill().Center().Gap(10).Children(func() {
			ui.Icon(c, layersIcon).Size(28, 28).TextColor(t.TextMuted)
			ui.Text(c, "One investigation, one story").FontSize(15).Bold()
			ui.Text(c, "Collect the exchanges that matter, compare them, and export the evidence you reviewed.").FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "Add a request from Capture's inspector, or open saved sessions.").FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, "New investigation").Clicked() {
					b.create("")
				}
				if ui.Button(c, "Historical sessions…").Clicked() {
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
	ui.Row(c).Height(44).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		title := ui.TextInput(c, &inv.Title).Grow(1).Height(30).Label("Investigation title").
			Placeholder("Untitled investigation").FontSize(13)
		if title.Changed() {
			b.mark()
		}
		started := "started " + inv.Created.Local().Format("Jan 2 15:04")
		ui.Text(c, fmt.Sprintf("%d exchanges · %s", len(inv.Items), started)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		if ui.Button(c, "Export evidence…").Height(26).FontSize(11).Disabled(len(b.picked) == 0).
			Tooltip("Export the selected exchanges after reviewing the raw JSON").Clicked() {
			a.exportInvestigation(c)
		}
		f, hasFlow := a.current()
		ui.MenuButton(c, "⋯", func(m *ui.Menu) {
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
		}).Label("Investigation options").Height(26).Padding(0, 6).FontSize(14).TextColor(t.TextMuted).Border(0, t.Border)
	})
	if b.notesOpen {
		notes := ui.TextArea(c, &inv.Notes).Height(52).Label("Investigation notes").
			Placeholder("What are you trying to explain? Facts, not guesses.")
		notes.Margin(0, 12, 4)
		if notes.Changed() {
			b.mark()
		}
		ui.Row(c).Padding(0, 12, 4).Justify(ui.End).Children(func() {
			if ui.Button(c, "Done").Height(22).FontSize(11).Clicked() {
				b.notesOpen = false
			}
		})
	} else {
		summary := cmp.Or(inv.Notes, "Add notes: what are you trying to explain?")
		note := ui.ButtonBase(c).Label("Edit notes").Height(26).Padding(0, 8).Radius(4).AlignItems(ui.Center).
			TextColor(t.TextMuted).Tooltip(inv.Notes)
		if note.Hovered() {
			note.TextColor(t.Text)
		}
		if note.Clicked() {
			b.notesOpen = true
		}
		note.Margin(0, 12, 6)
		note.Children(func() {
			ui.Icon(c, noteIcon).Size(12, 12)
			ui.Text(c, summary).FontSize(11).SingleLine().MaxWidth(900)
		})
	}
	if b.problem != "" {
		ui.Text(c, b.problem).FontSize(11).TextColor(t.Danger).Padding(0, 12, 6).SingleLine().Tooltip(b.problem)
	}
}

func (a *app) boardToolbar(c *ui.Context, t *ui.Theme, inv *investigation) {
	b := &a.board
	ui.Row(c).Height(34).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		search := ui.TextInput(c, &b.query).Label("Search investigation").
			Placeholder("Search method, URL, status, notes or errors…").FontSize(11).Grow(1)
		if search.Changed() {
			b.list = ui.ListState{}
		}
		ui.Text(c, fmt.Sprintf("%d / %d selected", len(b.picked), len(inv.Items))).FontSize(11).TextColor(t.TextMuted).SingleLine()
		if ui.Button(c, "Compare two").Height(24).FontSize(11).Disabled(len(b.picked) != 2).Clicked() {
			b.comparing = true
		}
		if ui.Button(c, "Clear selection").Height(24).FontSize(11).Disabled(len(b.picked) == 0).Clicked() {
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
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				if len(inv.Items) == 0 {
					ui.Text(c, "No exchanges yet").FontSize(13)
					ui.Text(c, "Inspect a request in Capture and choose Add to investigation.").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Text(c, "No exchange matches this search").FontSize(13)
					if ui.Button(c, "Clear search").FontSize(11).Clicked() {
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
			row := ui.ButtonBase(c).Label("Read "+boardItemLabel(item)).Height(26).Padding(3, 8).Gap(8).Radius(4).
				AlignItems(ui.Center).FillWidth()
			if item.ID == b.selected {
				row.Background(t.SurfaceHover)
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
				ui.Text(c, cmp.Or(f.Method, "—")).FontSize(11).Font("monospace").Width(58).SingleLine().TextColor(t.Text)
				status := fmt.Sprint(f.Status)
				statusColor := statusColor(f.Status, t)
				if f.ConnectionError != "" {
					status, statusColor = "Failed", t.Danger
				}
				ui.Text(c, status).FontSize(11).Width(46).SingleLine().TextColor(statusColor)
				ui.Text(c, cmp.Or(f.URL, f.Path)).FontSize(11).Font("monospace").SingleLine().Grow(1).Tooltip(f.URL).TextColor(t.Text)
				if note := cmp.Or(item.Note, f.Note); note != "" {
					ui.Icon(c, noteIcon).Size(11, 11).TextColor(t.TextMuted).Tooltip(note)
				}
				at := "—"
				if !f.At.IsZero() {
					at = f.At.Local().Format("15:04:05")
				}
				ui.Text(c, at).FontSize(10).Font("monospace").TextColor(t.TextMuted)
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
			ui.Text(c, "Choose an exchange to read it").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	f := investigationFlow(item.Source)
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, cmp.Or(f.method, "REQUEST")).FontSize(12).Bold().TextColor(methodColor(f.method, t))
			status, statusText := fmt.Sprintf("%d %s", f.status, http.StatusText(f.status)), f.statusColor(t)
			if f.conn.error != "" {
				status, statusText = "Failed", t.Danger
			}
			ui.Text(c, status).FontSize(12).TextColor(statusText)
			ui.Text(c, fmt.Sprintf("%d ms · %s", f.millis, humanBytes(f.bytes))).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if hasSensitiveHeader(f.reqHeader) || hasSensitiveHeader(f.respHeader) {
				label := "Reveal credentials"
				if b.reveal {
					label = "Hide credentials"
				}
				if ui.ButtonBase(c).Label("Reveal credentials").Height(22).Padding(0, 6).Radius(4).AlignItems(ui.Center).
					TextColor(t.TextMuted).Tooltip("Credentials are masked so a shared screen does not leak them").Children(func() {
					ui.Text(c, label).FontSize(11)
				}).Clicked() {
					b.reveal = !b.reveal
				}
			}
			if ui.Button(c, "Remove").Height(22).FontSize(11).Clicked() {
				b.removeItem(item.ID)
			}
		})
		ui.Text(c, cmp.Or(f.url, f.host+f.path)).FontSize(11).Font("monospace").TextColor(t.TextMuted).
			Padding(0, 12, 4).SingleLine().Tooltip(f.url)
		note := ui.TextInput(c, &item.Note).Label("Exchange note").Placeholder("What does this exchange show?").FontSize(11)
		note.Margin(0, 12, 6)
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
			ui.Row(c).Padding(6, 12, 2).Children(func() {
				seg := ui.SegmentedBase(c, &choice, 2)
				seg.Track.Padding(2).Radius(6).Border(1, t.Border).Background(t.Background).Children(func() {
					for i, name := range []string{"Request", "Response"} {
						s := seg.Segment(i).Label(name).Height(22).Padding(0, 8).Radius(4).AlignItems(ui.Center).TextColor(t.TextMuted)
						if i == choice {
							s.Background(t.SurfaceHover).TextColor(t.Text)
						}
						s.Children(func() { ui.Text(c, name).FontSize(11) })
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
		ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted).Padding(0, 6, 0, 4)
			flatTabs(c, &b.previewTab[side], title+" pane", "Headers", "Body")
			ui.Spacer(c)
			ui.Text(c, line).FontSize(10).Font("monospace").TextColor(t.TextMuted).SingleLine()
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(0, 12, 8).Gap(3).Children(func() {
				if b.previewTab[side] == 0 {
					if len(headers) == 0 {
						ui.Text(c, "No headers recorded").FontSize(11).TextColor(t.TextMuted)
					}
					for _, key := range slices.Sorted(maps.Keys(headers)) {
						value := strings.Join(headers[key], ", ")
						if sensitiveHeader(key) && !b.reveal {
							value = "••••••••  masked"
						}
						ui.Text(c, key+": "+value).FontSize(10).Font("monospace").TextColor(t.TextMuted).Selectable().
							Tooltip(key + "\n" + value)
					}
					return
				}
				if body != "" {
					ui.Text(c, boardBody(body)).FontSize(11).Font("monospace").Selectable()
				} else {
					ui.Text(c, "Empty body").FontSize(11).TextColor(t.TextMuted)
				}
				if problem != "" {
					ui.Text(c, problem).FontSize(10).TextColor(t.Warning)
				}
			})
		})
	})
}

// boardFailure layers the evidence: what was recorded, the raw error, then
// causes that are explicitly not confirmed.
func (a *app) boardFailure(c *ui.Context, t *ui.Theme, f flow) {
	e := f.conn.explanation()
	ui.Column(c).Margin(0, 12, 6).Padding(8, 10).Gap(4).Radius(6).Background(t.Surface).Children(func() {
		ui.Text(c, fmt.Sprintf("Recorded failure · %s · %s", cmp.Or(f.conn.errorStage, "unknown stage"), f.conn.error)).
			FontSize(11).TextColor(t.Danger).SingleLine().Tooltip(f.conn.error)
		ui.Text(c, "Possible causes · not confirmed").FontSize(10).Bold().TextColor(t.TextMuted)
		ui.Text(c, e.causes).FontSize(11)
		ui.Text(c, "What to try").FontSize(10).Bold().TextColor(t.TextMuted)
		ui.Text(c, e.steps).FontSize(11)
		if f.conn.log != "" {
			ui.Text(c, "Connection log retained with this exchange").FontSize(10).TextColor(t.TextMuted)
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
	ui.Row(c).Height(34).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.ButtonBase(c).Size(24, 24).Radius(5).Label("Back to evidence").Tooltip("Back to the investigation").
			TextColor(t.TextMuted).Children(func() {
			ui.Icon(c, backIcon).Size(13, 13)
		}).Clicked() {
			b.comparing = false
		}
		ui.Text(c, "Compare two exchanges").FontSize(12).Bold()
		ui.Text(c, "Read-only snapshots; neither side can be edited here").FontSize(11).TextColor(t.TextMuted)
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
	appModal(c, &b.deleting, func() {
		ui.Column(c).Width(440).Gap(14).Children(func() {
			dialogTitle(c, "Delete investigation?")
			ui.Text(c, fmt.Sprintf("%q and its %d exchanges are removed from this app. Export evidence first if you need to keep it.", inv.Title, len(inv.Items))).
				TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					b.deleting = false
				}
				if ui.PrimaryButton(c, "Delete investigation").Clicked() {
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
