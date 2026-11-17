package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type sseWorkbench struct {
	open                         bool
	url, headers, query, problem string
	reconnect                    bool
	session                      *sseSession
	picked, choice               int
	list                         ui.ListState
	reader                       *editor.State
}

func (a *app) openSSE() {
	s := &a.sse
	_, finished, _, _ := s.session.snapshot()
	if finished {
		if d := a.requestDocument; d != nil && d.protocol != "gRPC" && !request.IsWebSocket(d.composeURL) {
			r, err := d.value().Prepare(a.activeVars())
			if err != nil {
				s.problem = err.Error()
			} else {
				s.url, s.headers, s.problem = r.URL, request.HeaderBlock(r.Header), ""
			}
		}
	}
	s.open = true
}

func (a *app) sseModal(c *ui.Context, t *ui.Theme) {
	s := &a.sse
	if !s.open {
		return
	}
	state, finished, events, evicted := s.session.snapshot()
	_, h := c.Size()
	start := func(reconnect bool) {
		u, err := url.Parse(s.url)
		if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
			s.problem = "Enter an absolute HTTP(S) URL without embedded credentials"
			return
		}
		s.session = openSSESession(a.srv.Addr(), &request.Prepared{Method: "GET", URL: s.url, Header: request.ParseHeaderBlock(s.headers)}, a.srv.CACertificate(), a.certificates, a.requestCookies(), reconnect || s.reconnect, a.invalidate)
		s.problem, s.query, s.picked, s.reader = "", "", 0, nil
		s.list, s.choice = ui.ListState{}, 0
	}
	stateColor := t.TextMuted
	switch state {
	case "Connected":
		stateColor = t.Success
	case "Connecting", "Reconnecting":
		stateColor = t.Warning
	case "Failed":
		stateColor = t.Danger
	}
	connected := !finished
	if uikit.Modal(c, &s.open, locales.SseSessionTitle(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(h - 100).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.DialogTitle(c, locales.SseSessionTitle())
				uikit.StatusPill(c, stateLabel(state), stateColor)
			})
			// The connection form is fixed above the stream: its URL, headers
			// and search stay in reach while the event list and payload take
			// the room the window has.
			ui.Column(c).Shrink(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.Hint(c, locales.GetEventStreamsThroughThe())
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
					uikit.Input(c, &s.url).Label(locales.SseUrl()).Placeholder("https://api.example.com/events").Grow(1).MinWidth(0).Font(uikit.FontMono).Disabled(connected)
					if finished {
						if uikit.PrimaryButton(c, locales.ConnectSse()).Disabled(a.srv == nil || !a.srv.Running() || strings.TrimSpace(s.url) == "").Clicked() {
							start(false)
						}
					} else {
						// A live session has no primary action: Stop is the
						// way out, Reconnect is the exception. Only a finished
						// session offers Connect/Reconnect as the main action.
						if uikit.SecondaryButton(c, locales.Reconnect()).Clicked() {
							s.session.stop()
							start(true)
						}
						if uikit.PrimaryButton(c, locales.StopSse()).Clicked() {
							s.session.stop()
						}
					}
				})
				uikit.TextArea(c, &s.headers).Label(locales.SseRequestHeaders()).Placeholder("Authorization: Bearer …\nLast-Event-ID: …").Height(54).Font(uikit.FontMono).Radius(uikit.RadiusControl).Disabled(connected)
				uikit.Checkbox(c, &s.reconnect, locales.ReconnectOnEofOrNetworkFailure()).Disabled(connected)
				if s.problem != "" {
					uikit.Callout(c, uikit.CalloutDanger, s.problem, nil)
				}
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
					uikit.Input(c, &s.query).Label(locales.SearchSseEvents()).Placeholder(locales.SearchTypeIdPayloadAnd()).Grow(1).MinWidth(0)
					uikit.Tip(c, uikit.Meta(c, locales.RetainedEvicted(len(events), evicted)).FontFeatures("tnum"), locales.Latest200Events64Kib())
					if uikit.SecondaryButton(c, locales.LatestEvent()).Clicked() {
						s.list.ScrollToEnd()
					}
				})
			})
			ui.Row(c).Grow(1).MinHeight(84).Gap(t.Space(uikit.SpaceXL)).AlignItems(ui.Stretch).Children(func() {
				var shown []sseSessionEvent
				for _, e := range events {
					if strings.Contains(strings.ToLower(e.eventType+"\n"+e.id+"\n"+e.data+"\n"+e.detail), strings.ToLower(s.query)) {
						shown = append(shown, e)
					}
				}
				uikit.Panel(c).Width(280).MinWidth(0).Shrink(1).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM)).Children(func() {
					if len(shown) == 0 {
						uikit.Hint(c, locales.NoMatchingEventsYet())
						return
					}
					// The events are data, not a column of buttons: the
					// selected row gets the list fill and its marker, and
					// every row starts at the leading edge.
					previous := s.picked
					s.choice = -1
					for i, e := range shown {
						if e.seq == s.picked {
							s.choice = i
							break
						}
					}
					s.list.Selected = &s.choice
					s.list.Key = func(i int) any { return shown[i].seq }
					s.list.Label = func(i int) string { return locales.SseEventLabel(shown[i].seq) }
					uikit.DataListIndexed(c, &s.list, len(shown), func(i int) {
						e := shown[i]
						head := e.eventType
						if e.detail != "" {
							head, _, _ = strings.Cut(e.detail, "\n")
						}
						if head == "" {
							head = "Event"
						}
						ui.Column(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Children(func() {
							uikit.Body(c, fmt.Sprintf("#%d · %s", e.seq, clamp(head, 48))).TextColor(t.Text).SingleLine()
							uikit.Meta(c, fmt.Sprintf("%s · %d B", e.at.Format("15:04:05.000"), e.size)).SingleLine()
						})
					}).Grow(1).MinHeight(0)
					if s.choice >= 0 && s.choice < len(shown) && shown[s.choice].seq != previous {
						s.picked = shown[s.choice].seq
						s.reader = nil
					}
				})
				uikit.Panel(c).Grow(1).MinWidth(0).MinHeight(0).Padding(t.Space(uikit.SpaceM)).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
						for _, e := range events {
							if e.seq != s.picked {
								continue
							}
							text := e.detail
							if e.detail == "" {
								text = e.data
								ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceM)).Children(func() {
									uikit.Meta(c, locales.TypeAndID(clamp(e.eventType, 80), clamp(e.id, 80))).Grow(1).MinWidth(0)
									if uikit.IconButton(c, locales.CopyEventId(), locales.CopyEventId(), copyIcon).Clicked() {
										c.Services().WriteClipboard(e.id)
									}
								})
							}
							if s.reader == nil {
								s.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, SoftWrap: true, NoScrollPastEnd: true, Label: locales.SseEventPayload()}
							}
							requestCodeEditor(c, s.reader)
							return
						}
						s.reader = nil
						if s.picked != 0 {
							uikit.Hint(c, locales.SelectedEventWasEvictedSelect())
						} else {
							uikit.Hint(c, locales.SelectAnEventToInspect())
						}
					})
				})
			})
			uikit.DialogFooter(c).Children(func() {
				uikit.Tip(c, ui.Icon(c, infoIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted), locales.Latest200Events64Kib())
				ui.Spacer(c)
				if uikit.SecondaryButton(c, locales.CopyShownSseData()).Disabled(s.reader == nil).Clicked() {
					c.Services().WriteClipboard(s.reader.Buffer.String())
				}
				if uikit.SecondaryButton(c, locales.StopClose()).Clicked() {
					s.session.stop()
					s.open = false
				}
			})
		})
	}).Dismissed() {
		s.session.stop()
	}
}
