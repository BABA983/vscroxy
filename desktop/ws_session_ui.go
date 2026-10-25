package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

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
		if d := a.requestDocument; d != nil && isWebSocket(d.composeURL) {
			r, err := prepareRequest(d.value(), a.activeVars())
			if err != nil {
				s.problem = err.Error()
			} else {
				s.url, s.headers, s.message = r.URL, headerBlock(r.header), r.Body
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
	w, h := c.Size()
	if appModal(c, &s.open, func() {
		ui.Column(c).Width(min(1150, w-80)).Height(h - 100).Gap(8).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				dialogTitle(c, "WebSocket session · "+state).Grow(1)
				if ui.Button(c, "Disconnect & close").Clicked() {
					s.session.disconnect()
					s.open = false
				}
			})
			ui.Text(c, "Connect starts a fresh timeline. No automatic reconnect, scripts or restore. Closing this panel disconnects. Auth and cookies may contain secrets.").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &s.url).Label("WebSocket URL").Placeholder("ws://127.0.0.1:8080/socket").Grow(1).Disabled(active)
				if ui.Button(c, "Connect").Disabled(active || a.srv == nil || !a.srv.Running() || !isWebSocket(s.url)).Clicked() {
					s.session = openWSSession(a.srv.Addr(), &jsRequest{Method: "GET", URL: s.url, header: parseHeaderBlock(s.headers)}, a.srv.CACertificate(), a.requestCookies(), a.invalidate)
					s.problem = ""
					s.picked = 0
					s.reader = nil
				}
				if ui.Button(c, "Disconnect").Disabled(!active).Clicked() {
					s.session.disconnect()
				}
			})
			ui.TextArea(c, &s.headers).Label("WebSocket handshake headers").Placeholder("Authorization: Bearer …").Height(54).Disabled(active)
			if s.problem != "" {
				ui.Text(c, s.problem).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &s.message).Label("WebSocket message").Placeholder("Text message").Grow(1)
				if ui.PrimaryButton(c, "Send text").Disabled(state != "Connected" || s.sending).Clicked() {
					s.problem = ""
					s.sent = make(chan error, 1)
					s.sending = true
					session, data, done := s.session, []byte(s.message), s.sent
					go func() { done <- session.send(websocket.TextMessage, data); a.invalidate() }()
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &s.query).Label("Search session messages").Placeholder("Search retained payloads and events").Grow(1)
				if s.direction == "" {
					s.direction = "All"
				}
				ui.Select(c, &s.direction, []string{"All", "In", "Out", "Events"}).Width(100).Label("Session direction")
				ui.Text(c, fmt.Sprintf("%d retained · %d evicted", len(events), evicted)).FontSize(11)
				if ui.Button(c, "Latest message").Clicked() {
					s.scroll.Y = math.MaxFloat32
				}
			})
			ui.Row(c).Grow(1).Gap(10).AlignItems(ui.Stretch).Children(func() {
				if s.scroll.Y >= s.scroll.MaxY {
					s.scroll.Y = math.MaxFloat32
				}
				ui.Scroll(c).TrackScroll(&s.scroll).Width(300).Gap(3).Children(func() {
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
						b := ui.Button(c, label).Key(fmt.Sprint(e.Sequence)).Label(fmt.Sprintf("Session message %d", e.Sequence)).FillWidth().Height(36).Tooltip(label)
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
				ui.Column(c).Grow(1).Children(func() {
					if s.reader == nil {
						ui.Text(c, "Select a message or connection event. Binary payloads display as hex.").FontSize(12).TextColor(t.TextMuted)
					} else {
						editor.View(c, s.reader).Grow(1)
					}
					if ui.Button(c, "Copy shown payload").Disabled(s.reader == nil).Tooltip("Copies displayed text/hex, not hidden bytes; review secrets before sharing").Clicked() {
						c.Services().WriteClipboard(s.reader.Buffer.String())
					}
				})
			})
			ui.Text(c, "Latest 200 events · 64 KiB retained per message · 1 MiB receive/send limit. Close codes are shown only when a peer close frame was observed. Full proxy-captured traffic remains in Capture.").FontSize(10).TextColor(t.TextMuted)
		})
	}).Dismissed() {
		s.session.disconnect()
	}
}
