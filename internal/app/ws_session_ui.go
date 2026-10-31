package app

import (
	"encoding/hex"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/gorilla/websocket"
)

type websocketWorkbench struct {
	open                                             bool
	url, headers, message, query, direction, problem string
	session                                          *wsSession
	picked                                           int
	reader                                           *editor.State
	sent                                             chan error
	sending                                          bool
	scroll                                           ui.ScrollState
}

func (a *app) openSocket() {
	s := &a.socket
	state, _, _ := s.session.snapshot()
	if state == "Not connected" || state == "Disconnected" {
		if d := a.requestDocument; d != nil && request.IsWebSocket(d.composeURL) {
			r, err := d.value().Prepare(a.activeVars())
			if err != nil {
				s.problem = err.Error()
			} else {
				s.url, s.headers, s.message = r.URL, request.HeaderBlock(r.Header), r.Body
			}
		}
	}
	s.open = true
}

func (a *app) socketModal(c *ui.Context, t *ui.Theme) {
	s := &a.socket
	if !s.open {
		return
	}
	select {
	case err := <-s.sent:
		s.sending = false
		if err != nil {
			s.problem = err.Error()
		}
	default:
	}
	state, events, evicted := s.session.snapshot()
	active := state == "Connected" || state == "Connecting" || state == "Disconnecting"
	_, h := c.Size()
	if uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(h - 100).Children(func() {
			uikit.DialogTitle(c, "WebSocket session · "+state)
			ui.Scroll(c).MaxHeight(260).MinHeight(0).Shrink(1).Gap(t.Space(2)).Children(func() {
				ui.Text(c, "Connect starts a fresh timeline. No automatic reconnect, scripts or restore. Closing this panel disconnects. Auth and cookies may contain secrets.").FontSize(uikit.TextBody).TextColor(t.TextMuted)
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					ui.TextInput(c, &s.url).Label("WebSocket URL").Placeholder("ws://127.0.0.1:8080/socket").Grow(1).MinWidth(0).Height(uikit.HeightControl).Font(uikit.FontMono).FontSize(uikit.TextBody).Disabled(active)
					if uikit.SecondaryButton(c, "Connect").Disabled(active || a.srv == nil || !a.srv.Running() || !request.IsWebSocket(s.url)).Clicked() {
						s.session = openWSSession(a.srv.Addr(), &request.Prepared{Method: "GET", URL: s.url, Header: request.ParseHeaderBlock(s.headers)}, a.srv.CACertificate(), a.requestCookies(), a.invalidate)
						s.problem = ""
						s.picked = 0
						s.reader = nil
					}
					if uikit.SecondaryButton(c, "Disconnect").Disabled(!active).Clicked() {
						s.session.disconnect()
					}
				})
				ui.TextArea(c, &s.headers).Label("WebSocket handshake headers").Placeholder("Authorization: Bearer …").Height(54).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Disabled(active)
				if s.problem != "" {
					ui.Text(c, s.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					ui.TextInput(c, &s.message).Label("WebSocket message").Placeholder("Text message").Grow(1).MinWidth(0).Height(uikit.HeightControl).Font(uikit.FontMono).FontSize(uikit.TextBody)
					if uikit.PrimaryButton(c, "Send text").Disabled(state != "Connected" || s.sending).Clicked() {
						s.problem = ""
						s.sent = make(chan error, 1)
						s.sending = true
						session, data, done := s.session, []byte(s.message), s.sent
						go func() { done <- session.send(websocket.TextMessage, data); a.invalidate() }()
					}
				})
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					ui.TextInput(c, &s.query).Label("Search session messages").Placeholder("Search retained payloads and events").Grow(1).MinWidth(160).Height(uikit.HeightControl).FontSize(uikit.TextBody)
					if s.direction == "" {
						s.direction = "All"
					}
					ui.Select(c, &s.direction, []string{"All", "In", "Out", "Events"}).Width(100).Height(uikit.HeightControl).FontSize(uikit.TextBody).Label("Session direction")
					ui.Text(c, fmt.Sprintf("%d retained · %d evicted", len(events), evicted)).FontSize(uikit.TextMeta).FontFeatures("tnum")
					if uikit.SecondaryButton(c, "Latest message").Clicked() {
						s.scroll.Y = math.MaxFloat32
					}
				})
			})
			ui.Row(c).Grow(1).MinHeight(84).Gap(t.Space(4)).AlignItems(ui.Stretch).Children(func() {
				if s.scroll.Y >= s.scroll.MaxY {
					s.scroll.Y = math.MaxFloat32
				}
				ui.Scroll(c).TrackScroll(&s.scroll).Width(280).MinWidth(0).Shrink(1).Gap(t.Space(1)).Children(func() {
					for _, e := range events {
						if s.direction != "All" && !(s.direction == e.Direction || s.direction == "Events" && e.Direction == "") {
							continue
						}
						if !strings.Contains(strings.ToLower(string(e.Data)+e.Detail), strings.ToLower(s.query)) {
							continue
						}
						kind := "Text"
						if e.Kind == websocket.BinaryMessage {
							kind = "Binary"
						}
						label := fmt.Sprintf("#%d %s · %s %s · %d B", e.Sequence, e.At.Format("15:04:05.000"), e.Direction, kind, e.Size)
						if e.Direction == "" {
							label = fmt.Sprintf("#%d %s", e.Sequence, e.Detail)
						}
						b := uikit.SecondaryButton(c, label).Key(fmt.Sprint(e.Sequence)).Label(fmt.Sprintf("Session message %d", e.Sequence)).FillWidth().Tooltip(label)
						if e.Sequence == s.picked {
							b.Background(t.Accent).TextColor(t.AccentText)
						}
						if b.Clicked() {
							s.picked = e.Sequence
							text := e.Detail
							if e.Direction != "" {
								text = string(e.Data)
								if e.Kind == websocket.BinaryMessage || !utf8.Valid(e.Data) {
									text = hex.Dump(e.Data)
								}
							}
							if len(e.Data) < e.Size {
								text += fmt.Sprintf("\nRetained %d of %d bytes", len(e.Data), e.Size)
							}
							s.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, Label: "Session payload"}
						}
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
					if s.reader == nil {
						ui.Text(c, "Select a message or connection event. Binary payloads display as hex.").FontSize(uikit.TextBody).TextColor(t.TextMuted)
					} else {
						requestCodeEditor(c, s.reader)
					}
				})
			})
			ui.Text(c, "Latest 200 events · 64 KiB retained per message · 1 MiB receive/send limit. Close codes are shown only when a peer close frame was observed. Full proxy-captured traffic remains in Capture.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Copy shown payload").Disabled(s.reader == nil).Tooltip("Copies displayed text/hex, not hidden bytes; review secrets before sharing").Clicked() {
					c.Services().WriteClipboard(s.reader.Buffer.String())
				}
				if uikit.SecondaryButton(c, "Disconnect & close").Clicked() {
					s.session.disconnect()
					s.open = false
				}
			})
		})
	}).Dismissed() {
		s.session.disconnect()
	}
}
