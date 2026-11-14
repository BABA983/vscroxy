package app

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/gorilla/websocket"
)

type websocketWorkbench struct {
	open                                             bool
	url, headers, message, query, direction, problem string
	subprotocols, encoding                           string
	session                                          *wsSession
	picked                                           int
	viewMode                                         int
	protobuf                                         protoInspector
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
				s.subprotocols = r.Header.Get("Sec-WebSocket-Protocol")
				r.Header.Del("Sec-WebSocket-Protocol")
				s.url, s.headers, s.message = r.URL, request.HeaderBlock(r.Header), r.Body
			}
		}
	}
	if s.url == "" {
		s.url = "ws://127.0.0.1:8080/socket"
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
	if uikit.Modal(c, &s.open, locales.WebsocketSessionState(stateLabel(state)), func() {
		connect := func() {
			header := request.ParseHeaderBlock(s.headers)
			if s.subprotocols != "" && header.Get("Sec-WebSocket-Protocol") != "" {
				s.problem = "Enter subprotocols in the dedicated field or headers, not both"
				return
			}
			if s.subprotocols != "" {
				header.Set("Sec-WebSocket-Protocol", s.subprotocols)
			}
			s.session = openWSSession(a.srv.Addr(), &request.Prepared{Method: "GET", URL: s.url, Header: header}, a.srv.CACertificate(), a.certificates, a.requestCookies(), a.invalidate)
			s.problem = ""
			s.picked = 0
			s.reader = nil
		}
		canConnect := a.srv != nil && a.srv.Running() && request.IsWebSocket(s.url)
		uikit.DialogColumn(c, uikit.DialogLarge).Height(h - 100).Children(func() {
			uikit.DialogTitle(c, locales.WebsocketSessionState(stateLabel(state)))
			uikit.SettingsGroup(c, locales.Connection(), func() {
				uikit.SettingsRow(c, locales.Url(), func() ui.Element {
					in := uikit.Input(c, &s.url).Label(locales.WebsocketUrl()).Placeholder("ws://127.0.0.1:8080/socket").
						Font(uikit.FontMono).Disabled(active)
					uikit.Tip(c, in, locales.WebsocketUrlHelp())
					return in
				}, "")
				uikit.SettingsRow(c, locales.Subprotocols(), func() ui.Element {
					in := uikit.Input(c, &s.subprotocols).Label(locales.WebsocketSubprotocols()).
						Placeholder(locales.SubprotocolsInPreferenceOrderE()).Disabled(active)
					uikit.Tip(c, in, locales.WebsocketSubprotocolsHelp())
					return in
				}, "")
				uikit.SettingsRowMultiline(c, locales.Headers(), func() ui.Element {
					return uikit.TextArea(c, &s.headers).Label(locales.WebsocketHandshakeHeaders()).Placeholder("Authorization: Bearer …").
						Height(54).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Disabled(active)
				}, locales.WebsocketHeadersHelp())
			})
			if s.problem != "" {
				ui.Text(c, s.problem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				ui.Spacer(c)
				if active {
					if uikit.SecondaryButton(c, locales.Disconnect()).Clicked() {
						s.session.disconnect()
					}
				} else {
					b := uikit.Tip(c, uikit.PrimaryButton(c, locales.Connect()).Disabled(!canConnect), locales.SocketConnectTip())
					if b.Clicked() {
						connect()
					}
				}
			})
			if active || len(events) > 0 {
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					if s.encoding == "" {
						s.encoding = "Text"
					}
					uikit.SelectLabels(c, &s.encoding, []string{"Text", "Hex", "Base64"}, optionLabel).Label(locales.WebsocketMessageEncoding()).Width(110)
					uikit.Input(c, &s.message).Label(locales.WebsocketMessage()).Placeholder(locales.TextHexBytesOrPadded()).Grow(1).MinWidth(0).Font(uikit.FontMono)
					label := locales.SendText()
					if s.encoding != "Text" {
						label = locales.SendBinary()
					}
					var send ui.Element
					if state == "Connected" {
						send = uikit.PrimaryButton(c, label).Disabled(s.sending)
					} else {
						send = uikit.SecondaryButton(c, label).Disabled(true)
					}
					if send.Clicked() {
						kind, data, err := websocket.TextMessage, []byte(s.message), error(nil)
						switch s.encoding {
						case "Hex":
							kind = websocket.BinaryMessage
							data, err = hex.DecodeString(strings.Join(strings.Fields(s.message), ""))
						case "Base64":
							kind = websocket.BinaryMessage
							data, err = base64.StdEncoding.DecodeString(s.message)
						}
						if err != nil {
							s.problem = "Invalid " + s.encoding + " message; nothing sent"
						} else {
							s.problem = ""
							s.sent = make(chan error, 1)
							s.sending = true
							session, done := s.session, s.sent
							go func() { done <- session.send(kind, data); a.invalidate() }()
						}
					}
				})
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					uikit.Input(c, &s.query).Label(locales.SearchSessionMessages()).Placeholder(locales.SearchRetainedPayloadsAndEvents()).Grow(1).MinWidth(160)
					if s.direction == "" {
						s.direction = "All"
					}
					uikit.SelectLabels(c, &s.direction, directions, optionLabel).Width(100).Label(locales.SessionDirection())
					retained := uikit.Meta(c, locales.RetainedEvicted(len(events), evicted))
					uikit.Tip(c, retained, locales.Latest200Events64Kib2())
					if uikit.SecondaryButton(c, locales.LatestMessage()).Clicked() {
						s.scroll.Y = math.MaxFloat32
					}
				})
				if len(events) == 0 {
					uikit.EmptyState(c, locales.Connected(), locales.WaitingForMessages(), nil)
				} else {
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
								b := uikit.Tip(c, uikit.SecondaryButton(c, label).Key(fmt.Sprint(e.Sequence)).Label(locales.SessionMessageLabel(e.Sequence)).FillWidth(), label)
								if e.Sequence == s.picked {
									uikit.Selected(c, b, !a.windowBehind)
								}
								if b.Clicked() {
									s.picked = e.Sequence
									s.viewMode = 0
									if e.Kind == websocket.BinaryMessage {
										s.viewMode = 1
									}
									if !utf8.Valid(e.Data) {
										s.viewMode = 1
									}
									s.reader = &editor.State{Buffer: editor.NewBuffer(e.Detail), ReadOnly: true, NoScrollPastEnd: true, Label: locales.SessionPayload()}
								}
							}
						})
						ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
							if s.reader == nil {
								uikit.Hint(c, locales.SelectAMessage())
							} else {
								for _, e := range events {
									if e.Sequence != s.picked || e.Direction == "" {
										continue
									}
									uikit.Segmented(c, &s.viewMode, locales.Text(), locales.Hex(), locales.Protobuf()).Label(locales.SessionPayloadFormat())
									if s.viewMode == 2 {
										s.protobuf.message(c, a, fmt.Sprintf("socket:%p:%d", s.session, e.Sequence), messageView{data: string(e.Data), size: e.Size})
										return
									}
									text := string(e.Data)
									if s.viewMode == 1 {
										text = hex.Dump(e.Data)
									}
									if len(e.Data) < e.Size {
										text += fmt.Sprintf("\nRetained %d of %d bytes", len(e.Data), e.Size)
									}
									if s.reader.Buffer.String() != text {
										s.reader.Buffer = editor.NewBuffer(text)
									}
									break
								}
								requestCodeEditor(c, s.reader)
							}
						})
					})
				}
			} else {
				uikit.EmptyState(c, locales.NotConnected(), locales.ConnectToStartAFreshTimeline(), nil)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.CopyShownPayload()).Disabled(s.reader == nil || s.viewMode == 2), locales.CopiesDisplayedTextHexUse()).Clicked() {
					c.Services().WriteClipboard(s.reader.Buffer.String())
				}
				closeLabel := locales.DisconnectClose()
				if !active {
					closeLabel = locales.Close()
				}
				if uikit.SecondaryButton(c, closeLabel).Clicked() {
					s.session.disconnect()
					s.open = false
				}
			})
		})
	}).Dismissed() {
		s.session.disconnect()
	}
	if !s.open {
		s.protobuf.stop()
	}
}
