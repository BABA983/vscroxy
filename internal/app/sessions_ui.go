package app

import (
	"cmp"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *app) sessionsView(c *ui.Context, t *ui.Theme) {
	s := &a.sessions
	item := s.current()
	ui.Column(c).Fill().Children(func() {
		ui.ScrollHorizontal(c).Height(uikit.HeightTabs).FillWidth().Children(func() {
			ui.Row(c).FillHeight().AlignItems(ui.Center).Children(func() {
				for _, doc := range s.items {
					if !doc.open {
						continue
					}
					ui.Row(c.Key(doc.key)).FillHeight().AlignItems(ui.Center).Children(func() {
						if uikit.ToolbarButton(c, "Session: "+doc.data.Title, "Historical session · read-only payloads", doc == item).Children(func() {
							uikit.Glyph(c, clockIcon)
							uikit.Body(c, doc.data.Title).SingleLine().MaxWidth(220)
						}).Clicked() {
							a.selectSession(doc.key)
						}
						if uikit.IconButton(c, "Close session: "+doc.data.Title, "Close tab; keep the saved session", closeIcon).Clicked() {
							doc.closeTab()
							if doc == item {
								s.active = ""
								for _, next := range s.items {
									if next.open {
										s.active = next.key
										break
									}
								}
							}
						}
					})
				}
			})
		})
		ui.Divider(c)
		if item == nil || !item.open {
			ui.Column(c).Grow(1).Center().Children(func() {
				uikit.EmptyState(c, "Sessions", "Open a session file or save evidence from live traffic.", func() {
					if uikit.PrimaryButton(c, "Open session…").Clicked() {
						a.openSession()
					}
				})
			})
			return
		}
		if !item.loaded {
			ui.Column(c).Grow(1).Center().Children(func() {
				if item.saveErr != "" {
					ui.Text(c, item.saveErr).FontSize(uikit.TextMeta).TextColor(t.Danger)
				} else {
					uikit.Hint(c, "Opening session…")
				}
			})
			return
		}
		ui.Row(c).Height(uikit.HeightBar).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			if uikit.Input(c, &item.data.Title).Label("Session title").Grow(1).MinWidth(0).Changed() {
				item.persist(a.invalidate)
			}
			uikit.Meta(c, "Historical · read-only payloads")
			if uikit.CompactButton(c, "Delete…").Clicked() {
				a.confirmDeleteSession(item)
			}
			if uikit.CompactButton(c, "Export session…").Clicked() {
				data, err := json.Marshal(item.data)
				if err != nil {
					s.problem = err.Error()
				} else {
					a.reviewExport("Export session", "vscroxy-session.json", data, nil)
				}
			}
		})
		for _, problem := range []string{s.problem, item.saveErr} {
			if problem != "" {
				ui.Text(c, problem).FontSize(uikit.TextMeta).TextColor(t.Danger).Padding(t.Space(2))
			}
		}
		if s.comparing && s.left != nil && len(item.data.Flows) > 0 {
			if uikit.CompactButton(c, "Back to session").Clicked() {
				s.comparing = false
			}
			left, right := *s.left, item.data.Flows[item.picked].flow()
			left.id, right.id = 1, 2
			s.comparison.compare(c, left, right, [2]string{s.leftLabel, item.data.Title}, &a.diffSplit)
			return
		}
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(min(340, a.contentWidth(c)*0.36)).FillHeight().Padding(t.Space(2)).Gap(t.Space(1)).Children(func() {
				uikit.Input(c, &item.query).Label("Search session").Placeholder("URL, status or note…").FillWidth()
				uikit.Meta(c, fmt.Sprintf("%d %s", len(item.data.Flows), plural(len(item.data.Flows), "exchange", "exchanges")))
				ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
					rows := make([]int, len(item.data.Flows))
					for i := range rows {
						rows[i] = i
					}
					slices.SortStableFunc(rows, func(i, j int) int {
						if item.data.Flows[i].Pinned == item.data.Flows[j].Pinned {
							return 0
						}
						if item.data.Flows[i].Pinned {
							return -1
						}
						return 1
					})
					matched := 0
					for _, i := range rows {
						f := item.data.Flows[i]
						if !f.matches(item.data.Notes, item.query) {
							continue
						}
						matched++
						row := uikit.RowButton(c.Key(strconv.Itoa(i)), fmt.Sprintf("Read exchange %d", f.ID)).Height(uikit.HeightBar).FillWidth().Tooltip(f.URL)
						if item.picked == i {
							uikit.Selected(c, row, !a.windowBehind)
						}
						row.Children(func() {
							if f.Pinned {
								uikit.Glyph(c, pinIcon)
							}
							ui.Text(c, cmp.Or(f.Method, "—")).Font(uikit.FontMono).FontSize(uikit.TextMeta).Width(46).TextColor(uikit.MethodColor(f.Method, t))
							ui.Text(c, strconv.Itoa(f.Status)).FontSize(uikit.TextMeta).Width(30).TextColor(uikit.StatusColor(f.Status, t))
							uikit.Meta(c, cmp.Or(f.URL, f.Path)).SingleLine().Grow(1).MinWidth(0)
							if dot, ok := highlightColor(f.Color); ok {
								ui.Box(c).Size(8, 8).Radius(uikit.RadiusControl).Background(dot).Tooltip(f.Color)
							}
						})
						if row.Clicked() {
							item.picked, item.reveal, item.reader = i, false, nil
						}
					}
					if matched == 0 {
						uikit.Hint(c, "No matching exchanges")
					}
				})
			})
			ui.Divider(c).Width(1).FillHeight()
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() { a.sessionDetail(c, t, item) })
		})
	})
}

func (a *app) sessionDetail(c *ui.Context, t *ui.Theme, item *savedSession) {
	if len(item.data.Flows) == 0 {
		if uikit.Input(c, &item.data.Notes).Label("Session notes").Placeholder("Session notes…").Changed() {
			item.persist(a.invalidate)
		}
		uikit.EmptyState(c, "Empty session", "This saved session contains no exchanges", nil)
		return
	}
	f := &item.data.Flows[item.picked]
	s := &a.sessions
	ui.Row(c).Padding(t.Space(2)).Gap(t.Space(1)).Wrap().Children(func() {
		label := "Pin"
		if f.Pinned {
			label = "Unpin"
		}
		if uikit.CompactButton(c, label).Clicked() {
			f.Pinned = !f.Pinned
			item.persist(a.invalidate)
		}
		if uikit.CompactButton(c, "Use as left").Clicked() {
			left := f.flow()
			s.left, s.leftLabel = &left, item.data.Title
			s.comparison = captureAnalysis{}
		}
		if uikit.CompactButton(c, "Compare with left").Disabled(s.left == nil).Clicked() {
			s.comparing, s.comparison = true, captureAnalysis{}
		}
		label = "Reveal credentials"
		if item.reveal {
			label = "Hide credentials"
		}
		if uikit.CompactButton(c, label).Clicked() {
			item.reveal, item.reader = !item.reveal, nil
		}
	})
	uikit.Meta(c, f.URL).Padding(0, t.Space(2)).SingleLine().Tooltip(f.URL)
	ui.Row(c).Padding(t.Space(2)).Gap(t.Space(2)).Children(func() {
		if uikit.Input(c, &f.Note).Label("Session exchange note").Placeholder("Add a note…").Grow(1).Changed() {
			item.persist(a.invalidate)
		}
		uikit.MenuSelect(c, cmp.Or(f.Color, "No color"), func(m *ui.Menu) {
			for _, color := range []string{"No color", "Red", "Orange", "Yellow", "Green", "Blue", "Purple"} {
				if m.Item(color).Chosen() {
					f.Color = color
					if color == "No color" {
						f.Color = ""
					}
					item.persist(a.invalidate)
				}
			}
		}).Label("Session color").Width(90)
	})
	if uikit.Input(c, &item.data.Notes).Label("Session notes").Placeholder("Session notes…").Margin(0, t.Space(2), t.Space(2)).Changed() {
		item.persist(a.invalidate)
	}
	ui.Row(c).Padding(0, t.Space(2)).Gap(t.Space(2)).Children(func() {
		if uikit.Segmented(c, &item.side, "Request", "Response").Changed() {
			item.reader = nil
		}
		if uikit.Segmented(c, &item.tab, "Headers", "Body", "Messages", "Connection").Changed() {
			item.reader = nil
		}
	})
	if item.reader == nil {
		body, block, headers, trailers := f.ReqBody, f.ReqHeaders, f.ReqHeader, f.ReqTrailer
		if item.side == 1 {
			body, block, headers, trailers = f.ResBody, f.ResHeaders, f.RespHeader, f.ResTrailer
		}
		wire := f.flow()
		text := wire.body(item.side == 1)
		if item.tab == 0 {
			var lines []string
			pairs := headerPairs(block, headers)
			pairs = append(pairs, headerPairs("", trailers)...)
			for _, pair := range pairs {
				value := pair.Value
				if !item.reveal && sensitiveHeader(pair.Name) {
					value = "•••••••• masked"
				}
				lines = append(lines, pair.Name+": "+value)
			}
			text = strings.Join(lines, "\n")
			if text == "" {
				text = "No headers recorded"
			}
		} else if item.tab == 2 {
			var lines []string
			for _, fr := range item.data.Frames[f.ID] {
				payload := fr.Data
				if len(fr.Payload) > 0 {
					payload = fmt.Sprintf("%x", fr.Payload)
				}
				lines = append(lines, fmt.Sprintf("%s · %s · %s\n%s", fr.Direction, fr.Kind, fr.EventName, payload))
			}
			text = strings.Join(lines, "\n\n")
		} else if item.tab == 3 {
			text = wire.connectionDiagnostics()
		} else if body != "" {
			text = body
		}
		if !utf8.ValidString(text) {
			text = hex.Dump([]byte(text))
		}
		item.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Session payload"}
	}
	requestCodeEditor(c, item.reader)
}
