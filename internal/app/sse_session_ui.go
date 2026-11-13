package app

import (
	"fmt"
	"math"
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
	picked                       int
	reader                       *editor.State
	scroll                       ui.ScrollState
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
	if uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(h - 100).Children(func() {
			uikit.DialogTitle(c, locales.SseSessionState(stateLabel(state)))
			ui.Scroll(c).MaxHeight(240).MinHeight(0).Shrink(1).Gap(t.Space(2)).Padding(0, t.Space(1)).Children(func() {
				uikit.Hint(c, locales.GetEventStreamsThroughThe())
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					uikit.Input(c, &s.url).Label(locales.SseUrl()).Placeholder("https://api.example.com/events").Grow(1).MinWidth(0).Font(uikit.FontMono).Disabled(!finished)
					if uikit.PrimaryButton(c, locales.ConnectSse()).Disabled(!finished || a.srv == nil || !a.srv.Running() || strings.TrimSpace(s.url) == "").Clicked() {
						u, err := url.Parse(s.url)
						if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
							s.problem = "Enter an absolute HTTP(S) URL without embedded credentials"
						} else {
							s.session = openSSESession(a.srv.Addr(), &request.Prepared{Method: "GET", URL: s.url, Header: request.ParseHeaderBlock(s.headers)}, a.srv.CACertificate(), a.certificates, a.requestCookies(), s.reconnect, a.invalidate)
							s.problem, s.query, s.picked, s.reader = "", "", 0, nil
							s.scroll = ui.ScrollState{}
						}
					}
					if uikit.SecondaryButton(c, locales.StopSse()).Disabled(finished).Clicked() {
						s.session.stop()
					}
				})
				ui.TextArea(c, &s.headers).Label(locales.SseRequestHeaders()).Placeholder("Authorization: Bearer …\nLast-Event-ID: …").Height(54).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Disabled(!finished)
				ui.Checkbox(c, &s.reconnect, "Reconnect on EOF / network failure").Disabled(!finished)
				if s.problem != "" {
					ui.Text(c, s.problem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger)
				}
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					uikit.Input(c, &s.query).Label(locales.SearchSseEvents()).Placeholder(locales.SearchTypeIdPayloadAnd()).Grow(1).MinWidth(0)
					ui.Text(c, locales.RetainedEvicted(len(events), evicted)).FontSize(uikit.Scaled(uikit.TextMeta)).FontFeatures("tnum")
					if uikit.SecondaryButton(c, locales.LatestEvent()).Clicked() {
						s.scroll.Y = math.MaxFloat32
					}
				})
			})
			ui.Row(c).Grow(1).MinHeight(84).Gap(t.Space(4)).AlignItems(ui.Stretch).Children(func() {
				if s.scroll.Y >= s.scroll.MaxY {
					s.scroll.Y = math.MaxFloat32
				}
				ui.Scroll(c).TrackScroll(&s.scroll).Width(280).MinWidth(0).Shrink(1).Gap(t.Space(1)).Children(func() {
					found := 0
					for _, e := range events {
						if !strings.Contains(strings.ToLower(e.eventType+"\n"+e.id+"\n"+e.data+"\n"+e.detail), strings.ToLower(s.query)) {
							continue
						}
						found++
						label := fmt.Sprintf("#%d %s · %s · %d B", e.seq, e.at.Format("15:04:05.000"), clamp(e.eventType, 24), e.size)
						if e.detail != "" {
							first, _, _ := strings.Cut(e.detail, "\n")
							label = fmt.Sprintf("#%d %s", e.seq, first)
						}
						b := uikit.Tip(c, uikit.SecondaryButton(c, label).Key(fmt.Sprint(e.seq)).Label(locales.SseEventLabel(e.seq)).FillWidth(), label)
						if e.seq == s.picked {
							uikit.Selected(c, b, !a.windowBehind)
						}
						if b.Clicked() {
							s.picked = e.seq
							s.reader = nil
						}
					}
					if found == 0 {
						uikit.Hint(c, locales.NoMatchingEventsYet())
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(t.Space(2)).Children(func() {
					for _, e := range events {
						if e.seq != s.picked {
							continue
						}
						text := e.detail
						if e.detail == "" {
							text = e.data
							uikit.Meta(c, "Type: "+clamp(e.eventType, 80)+" · ID: "+clamp(e.id, 80))
							if uikit.SecondaryButton(c, locales.CopyEventId()).Clicked() {
								c.Services().WriteClipboard(e.id)
							}
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
			uikit.Meta(c, locales.Latest200Events64Kib())
			uikit.DialogFooter(c).Children(func() {
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
