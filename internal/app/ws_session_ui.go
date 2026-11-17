package app

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
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
	// document is the draft the session was opened from. Its saved message
	// list is what Send saved replays and what Save sent writes back.
	document   *requestDocument
	saved      []request.WebSocketMessage
	wait       time.Duration
	sendingAll bool
	sentAll    chan error
	picked     int
	choice     int
	list       ui.ListState
	viewMode   int
	protobuf   protoInspector
	reader     *editor.State
	sent       chan error
	sending    bool
	// recorded is the session whose summary History already holds.
	recorded *wsSession
}

func (a *app) openSocket() {
	s := &a.socket
	state, _, _ := s.session.snapshot()
	if state == "Not connected" || state == "Disconnected" {
		s.document, s.saved = nil, nil
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
	s.document, s.saved = nil, nil
	if d := a.requestDocument; d != nil && request.IsWebSocket(d.composeURL) {
		s.document = d
		s.saved, s.wait = slices.Clone(d.wsMessages), d.webSocketConfig().Wait()
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
	case err := <-s.sentAll:
		s.sendingAll = false
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
			s.picked, s.choice = 0, 0
			s.list = ui.ListState{}
			s.reader = nil
		}
		canConnect := a.srv != nil && a.srv.Running() && request.IsWebSocket(s.url)
		uikit.DialogColumn(c, uikit.DialogLarge).Height(h - 100).Children(func() {
			uikit.DialogTitle(c, locales.WebsocketSessionState(stateLabel(state)))
			if active {
				// The handshake is fixed while it is open; keep one line for
				// the endpoint instead of three disabled fields, so the
				// messages and the footer keep the dialog's height.
				uikit.SettingsCard(c, "", func() {
					uikit.SettingsCardRow(c, locales.Connection(), "", func() {
						uikit.Tip(c, uikit.Meta(c, s.url).Font(uikit.FontMono).SingleLine(), s.url)
					}, s.url)
				})
			} else {
				uikit.SettingsGroup(c, locales.Connection(), func() {
					uikit.SettingsRow(c, locales.Url(), func() ui.Element {
						in := uikit.Input(c, &s.url).Label(locales.WebsocketUrl()).Placeholder("ws://127.0.0.1:8080/socket").
							Font(uikit.FontMono)
						uikit.Tip(c, in, locales.WebsocketUrlHelp())
						return in
					}, "")
					uikit.SettingsRow(c, locales.Subprotocols(), func() ui.Element {
						in := uikit.Input(c, &s.subprotocols).Label(locales.WebsocketSubprotocols()).
							Placeholder(locales.SubprotocolsInPreferenceOrderE())
						uikit.Tip(c, in, locales.WebsocketSubprotocolsHelp())
						return in
					}, "")
					uikit.SettingsRowMultiline(c, locales.Headers(), func() ui.Element {
						return uikit.TextArea(c, &s.headers).Label(locales.WebsocketHandshakeHeaders()).Placeholder("Authorization: Bearer …").
							Height(54).Font(uikit.FontMono).Radius(uikit.RadiusControl)
					}, locales.WebsocketHeadersHelp())
				})
			}
			if s.problem != "" {
				uikit.Callout(c, uikit.CalloutDanger, s.problem, nil)
			}
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.SendSavedMessages()).Disabled(state != "Connected" || len(s.saved) == 0 || s.sendingAll || s.sending), locales.SavedMessagesHelp()).Clicked() {
					s.sendingAll, s.problem = true, ""
					s.sentAll = make(chan error, 1)
					session, saved, wait, done := s.session, slices.Clone(s.saved), s.wait, s.sentAll
					go func() {
						done <- session.sendAll(saved, wait)
						a.invalidate()
					}()
				}
				sent := s.session.sentMessages()
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.SaveSentMessages()).Disabled(len(sent) == 0 || s.document == nil), locales.SavedMessagesHelp()).Clicked() {
					s.saved = savedWebSocketMessages(sent)
					a.markWebSocketSaved(s.document, s.saved)
					a.done(locales.SavedTheSessionMessages(len(s.saved)))
				}
				uikit.Meta(c, locales.SavedMessagesCount(len(s.saved)))
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
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
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
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().Children(func() {
					uikit.Input(c, &s.query).Label(locales.SearchSessionMessages()).Placeholder(locales.SearchRetainedPayloadsAndEvents()).Grow(1).MinWidth(uikit.WidthField)
					if s.direction == "" {
						s.direction = "All"
					}
					uikit.SelectLabels(c, &s.direction, directions, optionLabel).Width(100).Label(locales.SessionDirection())
					retained := uikit.Meta(c, locales.RetainedEvicted(len(events), evicted))
					uikit.Tip(c, retained, locales.Latest200Events64Kib2())
					if uikit.SecondaryButton(c, locales.LatestMessage()).Clicked() {
						s.list.ScrollToEnd()
					}
				})
				if len(events) == 0 {
					uikit.EmptyState(c, locales.Connected(), locales.WaitingForMessages(), nil)
				} else {
					var shown []wsSessionEvent
					for _, e := range events {
						if s.direction != "All" && !(s.direction == e.Direction || s.direction == "Events" && e.Direction == "") {
							continue
						}
						if !strings.Contains(strings.ToLower(string(e.Data)+e.Detail), strings.ToLower(s.query)) {
							continue
						}
						shown = append(shown, e)
					}
					ui.Row(c).Grow(1).MinHeight(84).Gap(t.Space(uikit.SpaceXL)).AlignItems(ui.Stretch).Children(func() {
						ui.Box(c).Width(280).MinWidth(0).Shrink(1).MinHeight(0).Children(func() {
							if len(shown) == 0 {
								uikit.Hint(c, locales.NoMatchingWebSocketMessages()).Padding(t.Space(uikit.SpaceM))
								return
							}
							previous := s.picked
							s.choice = -1
							for i, e := range shown {
								if e.Sequence == s.picked {
									s.choice = i
									break
								}
							}
							s.list.Selected = &s.choice
							s.list.Key = func(i int) any { return shown[i].Sequence }
							s.list.Label = func(i int) string { return locales.SessionMessageLabel(shown[i].Sequence) }
							uikit.DataListIndexed(c, &s.list, len(shown), func(i int) {
								e := shown[i]
								head := fmt.Sprintf("#%d · %s", e.Sequence, e.Direction)
								if e.Direction == "" {
									head = fmt.Sprintf("#%d · %s", e.Sequence, e.Detail)
								}
								kind := "Text"
								if e.Kind == websocket.BinaryMessage {
									kind = "Binary"
								}
								ui.Column(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Children(func() {
									uikit.Body(c, clamp(head, 48)).TextColor(t.Text).SingleLine()
									uikit.Meta(c, fmt.Sprintf("%s · %s · %d B", e.At.Format("15:04:05.000"), kind, e.Size)).SingleLine()
								})
							}).Grow(1).MinHeight(0)
							if s.choice >= 0 && s.choice < len(shown) && shown[s.choice].Sequence != previous {
								e := shown[s.choice]
								s.picked = e.Sequence
								s.viewMode = 0
								if e.Kind == websocket.BinaryMessage || !utf8.Valid(e.Data) {
									s.viewMode = 1
								}
								s.reader = &editor.State{Buffer: editor.NewBuffer(e.Detail), ReadOnly: true, NoScrollPastEnd: true, Label: locales.SessionPayload()}
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

// savedWebSocketMessages turns what a session sent into the request's saved
// list. Binary frames are kept as base64, the encoding that round-trips them.
func savedWebSocketMessages(sent []wsSessionSent) []request.WebSocketMessage {
	messages := make([]request.WebSocketMessage, 0, len(sent))
	for _, message := range sent {
		if message.Kind == websocket.BinaryMessage {
			messages = append(messages, request.WebSocketMessage{Encoding: "Base64", Payload: base64.StdEncoding.EncodeToString(message.Data)})
			continue
		}
		messages = append(messages, request.WebSocketMessage{Payload: string(message.Data)})
	}
	return messages
}

// markWebSocketSaved writes a session's messages into the draft. The Body tab
// shows them and the request's save action stores them; nothing is sent.
func (a *app) markWebSocketSaved(d *requestDocument, messages []request.WebSocketMessage) {
	if d == nil || !slices.Contains(a.documents, d) {
		return
	}
	d.wsMessages = slices.Clone(messages)
	a.invalidate()
}
