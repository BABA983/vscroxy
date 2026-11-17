package app

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type socketIODocument struct {
	session *socketIOSession
	// recorded is the session whose summary History already holds.
	recorded                             *socketIOSession
	tab, selected                        int
	problem, ackID, query, readerKey     string
	authEditor, attachmentEditor, reader *editor.State
	authFor, attachmentsFor              string
	split                                float32
	list                                 ui.ListState
}

func (d *requestDocument) socketIOState() *socketIODocument {
	if d.socketIOUI == nil {
		d.socketIOUI = &socketIODocument{split: 240}
	}
	return d.socketIOUI
}

func (u *socketIODocument) stop() {
	if u.session != nil {
		u.session.cancel()
	}
}

func (a *app) drainSocketIO() {
	for _, d := range a.documents {
		if u := d.socketIOUI; d.protocol == "Socket.IO" && u != nil && u.session != nil && u.session.snapshot().finished {
			d.composeWaiting, d.sendCancel, d.sendCancelling = false, nil, false
		}
	}
}

func (a *app) connectSocketIO(d *requestDocument) {
	u := d.socketIOState()
	if a.srv == nil || !a.srv.Running() {
		u.problem = locales.StartCaptureBeforeSocketIO()
		return
	}
	r := d.value().Request
	in, vars := a.sendContext(r), a.activeVars()
	prepared, cfg, err := r.PrepareSocketIO(in, vars)
	if err != nil {
		u.problem = err.Error()
		return
	}
	cfg.Namespace = cmp.Or(cfg.Namespace, "/")
	u.session = openSocketIOSession(a.srv.Addr(), a.srv.CACertificate(), a.certificates, a.requestCookies(), r, in, vars, prepared, cfg, a.invalidate)
	u.problem, u.query, u.readerKey, u.reader, u.selected = "", "", "", nil, 0
	u.list = ui.ListState{}
	d.composeWaiting, d.sendCancel, d.sendCancelling = true, u.session.cancel, false
	d.historyPick, d.comparing = 0, false
}

func (a *app) sendSocketIOMessage(d *requestDocument, reply bool) {
	u := d.socketIOState()
	s := u.session
	if s == nil {
		return
	}
	// Messages use the connection's original endpoint and scope. Changing the
	// active environment cannot redirect a running session or its Vault reads.
	r := s.input
	r.Body = cmp.Or(strings.TrimSpace(d.composeBody), "[]")
	r.SocketIO.Event, r.SocketIO.Attachments, r.SocketIO.WantACK = d.socketIOConfig.Event, d.socketIOConfig.Attachments, d.socketIOConfig.WantACK
	p, cfg, err := r.PrepareSocketIO(s.scope, s.vars)
	if err != nil {
		u.problem = err.Error()
		return
	}
	attachments, err := request.SocketIOAttachments(cfg.Attachments)
	if err != nil {
		u.problem = err.Error()
		return
	}
	var id *uint64
	if reply {
		n, err := strconv.ParseUint(u.ackID, 10, 64)
		if err != nil {
			u.problem = locales.SelectPendingACK()
			return
		}
		id = new(n)
	}
	u.problem = ""
	s.send(cfg.Event, p.Body, attachments, cfg.WantACK, id, request.SocketIOMessageDoc{
		Event: d.socketIOConfig.Event, Args: d.composeBody,
		Attachments: d.socketIOConfig.Attachments, WantACK: d.socketIOConfig.WantACK,
	})
}

func (a *app) socketIOActions(d *requestDocument, editing bool) []action {
	ready, ack := false, false
	if editing && d.protocol == "Socket.IO" {
		u := d.socketIOState()
		s := u.session.snapshot()
		ready = s.ready && !s.busy && !d.sendCancelling
		ack = ready && slices.Contains(s.owed, u.ackID)
	}
	return []action{
		do("request.socketio-emit", locales.EmitSocketIoEvent(), func() { a.sendSocketIOMessage(d, false) }).when(ready, locales.ConnectASocketIoDocument2()),
		do("request.socketio-ack", locales.ReplySocketIoAck(), func() { a.sendSocketIOMessage(d, true) }).when(ack, locales.ChooseAPendingServerAck()),
		do("request.socketio-emit-saved", locales.EmitSaved(), func() { a.emitSocketIOSaved(d) }).
			when(ready && len(d.socketIOConfig.Messages) > 0, locales.ConnectASocketIoDocument()),
	}
}

func (a *app) socketIOEditor(c *ui.Context, t *ui.Theme, d *requestDocument) {
	u := d.socketIOState()
	s := u.session.snapshot()
	sent := u.session.sentMessages()
	width := a.editorPaneWidthAt(c, &a.editorReqPane, draftPercent)
	ui.Column(c).Fill().Children(func() {
		protocolTabRow(c, &u.tab, locales.SocketIOEditorTabPrefix(), []string{
			locales.Event(), locales.Attachments(), locales.Connection(), locales.RequestHeadersLabel(), locales.HttpAuth(), locales.Saved(),
		}, width)
		if u.problem != "" {
			uikit.ErrorText(c, u.problem).MaxHeight(55).Padding(0, t.Space(uikit.SpaceL))
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
			switch u.tab {
			case 0:
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Input(c, &d.socketIOConfig.Event).Label(locales.SocketIoEventName()).Placeholder(locales.EventNameEGEcho()).Grow(1).MinWidth(0)
					uikit.Checkbox(c, &d.socketIOConfig.WantACK, locales.RequestAck())
					a.actionButton(c, "request.socketio-emit", "Emit event")
				})
				if len(s.owed) > 0 {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
						uikit.Label(c, locales.SocketIoAckRequest())
						if !slices.Contains(s.owed, u.ackID) {
							u.ackID = s.owed[0]
						}
						uikit.Select(c, &u.ackID, s.owed).Label(locales.SocketIoAckRequest()).Width(88).Disabled(!s.ready || s.busy)
						a.actionButton(c, "request.socketio-ack", "Reply ACK")
					})
				}
				uikit.Panel(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM)).Children(func() {
					authoredText(c, &d.composeBody, &d.bodyEditor, &d.bodyFor, "Socket.IO arguments JSON", "json")
				})
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					namespace := cmp.Or(d.socketIOConfig.Namespace, "/")
					uikit.Meta(c, locales.SocketIoSessionLine(stateLabel(s.state), namespace)).Grow(1).SingleLine()
					if len(s.owed) > 0 {
						uikit.Meta(c, locales.SocketIoPendingAck(fmt.Sprint(len(s.owed)))).SingleLine()
					}
				})
			case 1:
				uikit.Tip(c, uikit.Meta(c, locales.AttachmentsHelp()), locales.BinaryAttachmentsJsonArrayOf())
				authoredText(c, &d.socketIOConfig.Attachments, &u.attachmentEditor, &u.attachmentsFor, "Socket.IO attachments JSON", "json")
			case 2:
				ui.Scroll(c).Grow(1).Gap(t.Space(uikit.SpaceXL)).Padding(0, t.Space(uikit.SpaceXS)).Disabled(d.composeWaiting).Children(func() {
					uikit.SettingsGroup(c, locales.Connection(), func() {
						uikit.SettingsRow(c, locales.Namespace(), func() ui.Element {
							in := uikit.Input(c, &d.socketIOConfig.Namespace).Label(locales.SocketIoNamespace()).Placeholder(locales.DefaultText())
							uikit.Tip(c, in, locales.SocketIoNamespaceHelp())
							return in
						}, "")
						uikit.SettingsRowMultiline(c, locales.NamespaceAuth(), func() ui.Element {
							return ui.Box(c).Height(180).FillWidth().Children(func() {
								authoredText(c, &d.socketIOConfig.Auth, &u.authEditor, &u.authFor, "Socket.IO namespace auth JSON", "json")
							})
						}, locales.NamespaceAuthJsonObjectVariables())
					})
				})
			case 3:
				ui.Column(c).Fill().Disabled(d.composeWaiting).Children(func() { a.headerEditor(c, d, t) })
			case 4:
				ui.Column(c).Fill().Disabled(d.composeWaiting).Children(func() { a.authEditor(c, d, t) })
			case 5:
				a.socketIOSavedEditor(c, t, d, sent)
			}
		})
	})
}

func (a *app) socketIOResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	u := d.socketIOState()
	s := u.session.snapshot()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Body(c, locales.SocketIoMessages()).Grow(1)
			if u.session != nil {
				uikit.Meta(c, stateLabel(s.state)).SingleLine()
			}
		})
		if u.session == nil {
			uikit.EmptyState(c, locales.NoSocketIoConnectionYet(), locales.ConnectToEmitAndReceive(), nil)
			return
		}
		if s.problem != "" {
			uikit.ErrorText(c, s.problem).MaxHeight(55).Padding(0, t.Space(uikit.SpaceL))
		}
		ui.Row(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &u.query).Label(locales.SearchSocketIoMessages()).Placeholder(locales.SearchEventOrPayload()).Grow(1).MinWidth(0)
			uikit.Meta(c, locales.RetainedPreview2(len(s.events), s.evicted)).SingleLine()
		})
		query := strings.ToLower(strings.TrimSpace(u.query))
		var events []socketIOEvent
		for _, e := range s.events {
			if query == "" || strings.Contains(strings.ToLower(e.name+" "+e.text), query) {
				events = append(events, e)
			}
		}
		if len(events) == 0 {
			if strings.TrimSpace(u.query) != "" {
				uikit.EmptyState(c, locales.NoMatchingSocketIoMessages(), locales.ChangeTheSearchOrWait3(), nil)
			} else {
				uikit.EmptyState(c, locales.NoSocketIoEventsYet(), locales.EmitOrWaitForTheServer(), nil)
			}
			return
		}
		ui.Split(c, &u.split, func() {
			u.list.Selected = &u.selected
			u.list.Key = func(i int) any { return events[i].seq }
			u.list.Label = func(i int) string { return fmt.Sprintf("Socket.IO message %d %s", events[i].seq, events[i].name) }
			uikit.DataListIndexed(c, &u.list, len(events), func(i int) {
				e := events[i]
				ui.Column(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Children(func() {
					uikit.Body(c, e.direction+" · "+e.name).SingleLine()
					uikit.Meta(c, locales.EventLineSize(e.seq, e.at.Format("15:04:05.000"), e.size)).SingleLine()
				})
			}).Grow(1)
		}, func() {
			// A selected key the list no longer has comes back as -1: the event
			// went away, and the detail says nothing is selected rather than
			// showing the oldest one left.
			settleEvent(&u.selected, len(events))
			e := events[u.selected]
			key := fmt.Sprint(e.seq)
			if u.readerKey != key {
				u.readerKey = key
				u.reader = &editor.State{Buffer: editor.NewBuffer(e.text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.SocketIoMessagePayload()}
			}
			ui.Column(c).Fill().Children(func() {
				ui.Row(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).Children(func() {
					uikit.Meta(c, e.direction+" · "+e.name).Grow(1).SingleLine()
					services, text := c.Services(), e.text
					uikit.SecondaryButton(c, locales.CopyShownMessage()).OnClick(func() { services.WriteClipboard(text) })
				})
				requestCodeEditor(c, u.reader)
			})
		}).Grow(1)
	})
}

// emitSocketIOSaved sends the document's saved messages in order on the live
// session. The session keeps one busy period and records each emit.
func (a *app) emitSocketIOSaved(d *requestDocument) {
	u := d.socketIOState()
	if u.session == nil {
		return
	}
	messages := slices.Clone(d.socketIOConfig.Messages)
	u.problem = ""
	session := u.session
	go func() {
		_ = session.emitAll(messages) // the session shows a failure in its problem
		a.invalidate()
	}()
}

// socketIOSavedEditor is the Saved tab: the request's emit list, which the
// session replays in order.
func (a *app) socketIOSavedEditor(c *ui.Context, t *ui.Theme, d *requestDocument, sent []request.SocketIOMessageDoc) {
	remove := -1
	uikit.Meta(c, locales.SocketIOSavedMessagesHelp())
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
		if uikit.SecondaryButton(c, locales.AddCurrentEvent()).Clicked() {
			d.socketIOConfig.Messages = append(d.socketIOConfig.Messages, request.SocketIOMessageDoc{
				Event: d.socketIOConfig.Event, Args: d.composeBody,
				Attachments: d.socketIOConfig.Attachments, WantACK: d.socketIOConfig.WantACK,
			})
		}
		a.actionButton(c, "request.socketio-emit-saved", "Emit saved")
		if uikit.SecondaryButton(c, locales.SaveSentMessages()).Disabled(len(sent) == 0).Clicked() {
			d.socketIOConfig.Messages = slices.Clone(sent)
			a.done(locales.SavedTheSessionMessages(len(sent)))
		}
		uikit.Meta(c, locales.SavedMessagesCount(len(d.socketIOConfig.Messages)))
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
		for i := range d.socketIOConfig.Messages {
			message := &d.socketIOConfig.Messages[i]
			row := ui.Row(c.Key(i)).AlignItems(ui.Start).Gap(t.Space(uikit.SpaceM))
			row.Children(func() {
				uikit.Caption(c, strconv.Itoa(i+1)).
					TextColor(t.TextMuted).Width(t.Space(uikit.SpaceL)).Padding(t.Space(uikit.SpaceM), 0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					uikit.Input(c, &message.Event).Label(locales.SocketIoEventName()).
						Placeholder(locales.EventNameEGEcho()).Font(uikit.FontMono)
					uikit.TextArea(c, &message.Args).Label(locales.SocketIoArgumentsOf(i + 1)).
						Placeholder("[]").Height(54).Font(uikit.FontMono).
						Grow(1).MinWidth(0)
				})
				ui.Box(c).Width(uikit.WidthPicker).Padding(t.Space(uikit.SpaceM), 0).Children(func() {
					uikit.Checkbox(c, &message.WantACK, locales.RequestAck())
				})
				label := locales.RemoveMessage(i + 1)
				if uikit.RowIconButton(c, label, label, closeIcon).Clicked() {
					remove = i
				}
			})
		}
	})
	if remove >= 0 {
		d.socketIOConfig.Messages = slices.Delete(d.socketIOConfig.Messages, remove, remove+1)
	}
}
