package main

// The Messages tab: what a WebSocket or SSE flow carried, message by message.
//
// The capture keeps the whole of each message, spilling a long one to disk, and
// numbers the messages of a flow so a loss shows as a gap. The tab keeps a
// bounded window of them per flow, searches and filters it, and shows the
// chosen message in full. Everything here takes what it needs as an argument:
// the app, the flow and the pane's own state, never a field the app may not
// have yet.

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo/ui"
)

const (
	// maxMessages bounds what the app keeps for one flow, as the capture
	// bounds what one stream keeps: the messages past it leave the window and
	// stay counted, so a long stream stays bounded in memory without hiding
	// what it lost.
	maxMessages = 200
	// maxPreview is how many characters of a message a row of the list shows.
	maxPreview = 160
	// maxMessageText bounds what the detail lays out as text: a message longer
	// than this shows its head, and the line above says so, because a pane that
	// laid out megabytes of glyphs every frame would stall the window.
	maxMessageText = 64 << 10
)

// messageView is one WebSocket message or SSE event: what the list shows, what
// the detail pane inspects and what a session saves. The capture keeps the
// whole message, so a view holds its head and the file that has the rest.
type messageView struct {
	// seq is the message's place in the flow, from 1. A gap in the sequence
	// numbers is a message the capture's queue or the window here dropped.
	seq       int
	direction string // "out" for client to server, "in" for server to client
	kind      string // "text", "binary", a WebSocket control type, or "event"
	data      string // the message, or its head when file holds the rest
	file      string // the spill file the capture wrote for a long message
	size      int    // the message's length in bytes
	kept      int    // how many bytes of it are here, inline or in file
	at        time.Time
	eventID   string // SSE "id"
	eventName string // SSE "event", "message" when it had none
}

// frame is a message as the app keeps it: the table's state holds
// map[int][]frame, one list per WebSocket or SSE flow, which is the name the
// rest of the app already gives a message.
type frame = messageView

// messageViewOf turns a message the capture published into what the pane shows.
func messageViewOf(fr capture.Frame) messageView {
	return messageView{
		seq:       fr.Seq,
		direction: fr.Direction,
		kind:      fr.Kind,
		data:      fr.Data,
		file:      fr.File,
		size:      fr.Size,
		kept:      fr.Kept,
		at:        fr.At,
		eventID:   fr.EventID,
		eventName: fr.EventName,
	}
}

// addFrame files a message the capture published under its flow.
func (a *app) addFrame(ev capture.FrameEvent) {
	if a.frames == nil {
		a.frames = make(map[int][]frame)
	}
	a.frames[ev.FlowID] = appendMessage(a.frames[ev.FlowID], ev)
}

// appendMessage adds a message to a flow's window, keeping the newest
// maxMessages: the ones it drops stay counted, because their sequence numbers
// are gone from the list and the gap says how many.
func appendMessage(list []messageView, ev capture.FrameEvent) []messageView {
	list = append(list, messageViewOf(ev.Frame))
	if over := len(list) - maxMessages; over > 0 {
		list = slices.Delete(list, 0, over)
	}
	return list
}

// messagesDropped counts the messages of a flow that are not in its window: a
// full queue in the capture and the end of the window here both leave gaps in
// the sequence numbers, and a session keeps them.
func messagesDropped(list []messageView) int {
	dropped, prev := 0, 0
	for _, m := range list {
		if m.seq <= 0 {
			continue // a message from a session that numbered none
		}
		if prev == 0 {
			dropped += m.seq - 1
		} else if m.seq > prev+1 {
			dropped += m.seq - prev - 1
		}
		prev = m.seq
	}
	return dropped
}

// messagesNote says what a flow's window holds and what it does not, so a
// bounded capture never hides a loss.
func messagesNote(list []messageView) string {
	if len(list) == 0 {
		return ""
	}
	note := fmt.Sprintf("%d messages", len(list))
	if len(list) == 1 {
		note = "1 message"
	}
	if dropped := messagesDropped(list); dropped > 0 {
		note += fmt.Sprintf(" · %d dropped", dropped)
	}
	return note
}

// note is the header's line about the list: how many of the kept messages the
// search and the filter show, and how many the window does not hold at all.
func (p *messagePanel) note(list, visible []messageView) string {
	note := messagesNote(list)
	if len(visible) == len(list) {
		return note
	}
	return fmt.Sprintf("%d of %s", len(visible), note)
}

// pruneMessages forgets the messages of the flows the table no longer shows.
func pruneMessages(frames map[int][]frame, flows []flow) map[int][]frame {
	live := make(map[int]struct{}, len(flows))
	for _, f := range flows {
		live[f.id] = struct{}{}
	}
	maps.DeleteFunc(frames, func(id int, _ []frame) bool {
		_, ok := live[id]
		return !ok
	})
	return frames
}

// payload is the whole retained message: what the capture kept inline, or what
// it wrote to the spill file when the message was longer than the preview.
func (m messageView) payload() string {
	if m.file == "" {
		return m.data
	}
	data, err := os.ReadFile(m.file)
	if err != nil {
		// The capture is gone and took its spill directory with it: the head
		// is all that is left.
		return m.data
	}
	return string(data)
}

// cut is how many bytes of the message are not here, 0 when all of it is.
func (m messageView) cut() int {
	return max(m.size-max(m.kept, len(m.data)), 0)
}

// binary reports whether the message reads better as bytes than as text.
func (m messageView) binary() bool {
	return m.kind == "binary" || !utf8.ValidString(m.data) || strings.ContainsRune(m.data, 0)
}

// label names the message in the list: its kind, or the name of the SSE event
// it is, which the row's tooltip spells out with the event's id.
func (m messageView) label() string {
	if m.kind == "event" {
		return cmp.Or(m.eventName, "event")
	}
	return m.kind
}

// kindLine names the message for a tooltip: what it is, and the name and id an
// SSE event carries.
func (m messageView) kindLine() string {
	parts := []string{m.kind}
	if m.eventName != "" {
		parts = append(parts, "event "+m.eventName)
	}
	if m.eventID != "" {
		parts = append(parts, "id "+m.eventID)
	}
	return strings.Join(parts, " · ")
}

// arrow points the way the message went.
func (m messageView) arrow() string {
	if m.direction == "in" {
		return "←"
	}
	return "→"
}

// directionLabel says the way the message went in words.
func (m messageView) directionLabel() string {
	if m.direction == "in" {
		return "server → client"
	}
	return "client → server"
}

// stamp is when the message crossed the proxy, to the millisecond.
func (m messageView) stamp() string {
	if m.at.IsZero() {
		return ""
	}
	return m.at.Format("15:04:05.000")
}

// headline is the first line of the detail: which message, which way, how long.
func (m messageView) headline() string {
	place := "message"
	if m.seq > 0 {
		place = fmt.Sprintf("#%d", m.seq)
	}
	parts := []string{place, m.arrow(), m.label(), humanBytes(m.size)}
	if stamp := m.stamp(); stamp != "" {
		parts = append(parts, stamp)
	}
	return strings.Join(parts, " · ")
}

// meta is the line under the headline: the direction, what an SSE event says
// about itself, and how much of a long message is here.
func (m messageView) meta() string {
	parts := []string{m.directionLabel()}
	if m.eventName != "" {
		parts = append(parts, "event "+m.eventName)
	}
	if m.eventID != "" {
		parts = append(parts, "id "+m.eventID)
	}
	if cut := m.cut(); cut > 0 {
		parts = append(parts, fmt.Sprintf("%s kept of %s", humanBytes(m.kept), humanBytes(m.size)))
	}
	if m.file != "" {
		parts = append(parts, "spilled to "+shortenHome(m.file))
	}
	return strings.Join(parts, " · ")
}

// preview is the message on one line, as a row of the list shows it: its text,
// cut at maxPreview characters, or its first bytes in hex when it is binary.
func (m messageView) preview() string {
	if m.binary() {
		return hexPreview(m.data, 32)
	}
	line := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(m.data)
	if runes := []rune(line); len(runes) > maxPreview {
		return string(runes[:maxPreview]) + "…"
	}
	return line
}

// searchText is what a search reads: the head of the message, which is all of
// it unless it was spilled, and the names an SSE event carries.
func (m messageView) searchText() string {
	return m.direction + " " + m.kind + " " + m.eventName + " " + m.eventID + " " + m.data
}

// row draws one message in the list.
func (m messageView) row(c *ui.Context, chosen bool) {
	t := c.Theme()
	color := ui.Hex("#3b82f6")
	if m.direction == "in" {
		color = ui.Hex("#16a34a")
	}
	if chosen {
		color = t.AccentText
	}
	ui.Row(c).Gap(6).Padding(4, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.arrow()).FontSize(12).TextColor(color).Width(12)
		muted := rowColor(chosen, t, t.TextMuted)
		ui.Text(c, m.stamp()).FontSize(10).TextColor(muted).Width(74).SingleLine()
		label := ui.Text(c, m.label()).FontSize(10).TextColor(muted).Width(52).SingleLine()
		label = label.Tooltip(m.kindLine())
		preview := ui.Text(c, m.preview()).FontSize(12).Font("monospace").
			TextColor(rowColor(chosen, t, t.Text)).SingleLine().Grow(1)
		preview.Tooltip(m.preview())
		ui.Text(c, humanBytes(m.size)).FontSize(10).TextColor(muted).Width(42).SingleLine()
	})
}

// detail draws the chosen message in full: what it is, how much of it the
// capture kept, and the message itself as text or as bytes. It takes the app
// and the flow it belongs to, so nothing here reads a field of the app that
// the caller did not pass.
func (m messageView) detail(c *ui.Context, a *app, f flow, p *messagePanel) {
	t := c.Theme()
	payload := p.payload(f, m)
	if p.modeFor != m.seq {
		// The mode follows the message: bytes open as hex, text as text.
		p.modeFor = m.seq
		if m.binary() {
			p.mode = 1
		} else {
			p.mode = 0
		}
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.headline()).FontSize(12).Font("monospace").TextColor(t.Text).SingleLine().Grow(1)
			if ui.Button(c, "Copy").Clicked() {
				c.Services().WriteClipboard(payload)
				a.setMessage("Copied " + m.label() + " message · " + humanBytes(len(payload)))
			}
		})
		ui.Row(c).Padding(0, 12, 6, 12).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Segmented(c, &p.mode, "Text", "Hex").Label("Show as")
			ui.Text(c, m.meta()).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1).Tooltip(m.meta())
		})
		if p.loadedError != "" {
			ui.Text(c, "Payload unavailable; showing inline preview: "+p.loadedError).FontSize(11).TextColor(t.Danger).Padding(0, 12)
		}
		if len(payload) == 0 {
			ui.Text(c, "Empty message").FontSize(12).TextColor(t.TextMuted).Padding(12)
			return
		}
		if p.mode == 1 {
			m.hexView(c, payload)
			return
		}
		m.textView(c, payload)
	})
}

// textView shows the message as text, indented when it is JSON, cut at
// maxMessageText so a huge message cannot stall the window.
func (m messageView) textView(c *ui.Context, payload string) {
	t := c.Theme()
	text, ok := indentJSON(payload)
	if !ok {
		text = payload
	}
	if len(text) > maxMessageText {
		cut := text[:maxMessageText]
		// Cut at a line, so no character is split and no half line shows.
		if i := strings.LastIndexByte(cut, '\n'); i > 0 {
			cut = cut[:i]
		}
		text = cut + fmt.Sprintf("\n\n… showing the first %s of %s", humanBytes(maxMessageText), humanBytes(len(text)))
	}
	ui.Scroll(c).Grow(1).Padding(12).Children(func() {
		ui.Text(c, text).FontSize(12).Font("monospace").TextColor(t.Text)
	})
}

// hexView shows the message as bytes, so a binary message reads without being
// mangled into text.
func (m messageView) hexView(c *ui.Context, payload string) {
	t := c.Theme()
	ui.List(c, nil, (len(payload)+15)/16, func(i int) {
		ui.Text(c, hexLine(payload, i)).FontSize(11).Font("monospace").
			TextColor(t.Text).Padding(2, 12).SingleLine()
	}).Grow(1)
}

// hexPreview is the first bytes of a binary message as hex, as a row shows it.
func hexPreview(data string, limit int) string {
	n := min(len(data), limit)
	parts := make([]string, 0, n+1)
	for i := range n {
		parts = append(parts, fmt.Sprintf("%02x", data[i]))
	}
	if len(data) > n {
		parts = append(parts, "…")
	}
	return strings.Join(parts, " ")
}

// indentJSON indents a message that is JSON, so a payload sent on one line
// reads; it reports false for text that is not JSON.
func indentJSON(text string) (string, bool) {
	out, err := jsontext.AppendFormat(nil, []byte(text), jsontext.WithIndent("  "))
	if err != nil {
		return "", false
	}
	return string(out), true
}

// messagePanel is the Messages tab's state: the search, the direction filter,
// the chosen message and the mode its detail reads in. The app holds one and
// passes it to the pane, so the pane keeps its place without the message
// methods reaching into the app.
type messagePanel struct {
	// flowID is the flow the state belongs to; a new flow starts clean.
	flowID    int
	query     string
	direction int // 0 all, 1 server to client, 2 client to server
	selected  int // the row chosen, an index into the filtered list
	follow    bool
	mode      int // 0 text, 1 hex
	modeFor   int
	split     float32
	list      ui.ListState

	// loaded caches the chosen message's payload, so a spilled one is read
	// once rather than every frame.
	loadedFlow  int
	loadedSeq   int
	loaded      string
	loadedError string

	searchCache   bodyCache
	searchPending map[int]bool
	searchResults chan bodyResult
	searchContext context.Context
	searchCancel  context.CancelFunc
}

// view draws the Messages tab for one flow: the search and direction filter,
// the messages kept, and the chosen one in full.
func (p *messagePanel) view(c *ui.Context, a *app, f flow, list []messageView) {
	t := c.Theme()
	if p.flowID != f.id {
		p.stopSearch()
		p.flowID = f.id
		p.query = ""
		p.direction = 0
		p.follow = true
		p.modeFor = 0
		p.loadedSeq = 0
	}
	if p.split <= 0 {
		p.split = 360
	}
	if len(list) == 0 {
		ui.Text(c, "No messages yet").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	// The search and the direction filter are built first: they change what
	// the list shows, and the line after them reads it.
	var visible []messageView
	ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center).Children(func() {
		search := ui.SearchField(c, &p.query).Placeholder("Search messages…").Label("Search messages").Width(220)
		search.Tooltip("Searches metadata and the full retained payload; spill files are read off the UI thread")
		if search.Changed() {
			p.follow = true
		}
		if ui.Segmented(c, &p.direction, "All", "In", "Out").Label("Direction").Changed() {
			p.follow = true
		}
		p.searchMessages(a, list)
		visible = p.matching(list)
		note := p.note(list, visible)
		if len(p.searchPending) > 0 {
			note += " · searching…"
			c.After(100 * time.Millisecond)
		}
		if n := len(p.searchCache.errs); n > 0 {
			note += fmt.Sprintf(" · %d payloads unreadable", n)
		}
		ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(note)
		ui.Spacer(c)
		if !p.follow && ui.Button(c, "Latest").Clicked() {
			p.follow = true
		}
	})
	if p.follow && len(visible) > 0 {
		p.selected = len(visible) - 1
	}
	p.selected = min(max(p.selected, 0), max(len(visible)-1, 0))
	ui.Split(c, &p.split, func() {
		p.list.Key = func(i int) any {
			if seq := visible[i].seq; seq > 0 {
				return seq
			}
			return i
		}
		p.list.Selected = &p.selected
		p.list.FollowEnd = p.follow
		p.list.Label = func(i int) string { return visible[i].label() + ": " + visible[i].preview() }
		rows := ui.List(c, &p.list, len(visible), func(i int) {
			visible[i].row(c, i == p.selected)
		}).Grow(1)
		if rows.Changed() {
			// A row was chosen: the list stays where the person put it.
			p.follow = false
		}
		rows.Children(func() {
			if len(visible) == 0 {
				ui.Text(c, "No message matches").FontSize(12).TextColor(t.TextMuted).Padding(16)
			}
		})
	}, func() {
		if len(visible) == 0 {
			ui.Text(c, "Choose a message to see it in full").FontSize(12).TextColor(t.TextMuted).Padding(16)
			return
		}
		// List remaps a disappeared selected key to -1 during filtering.
		if p.selected < 0 || p.selected >= len(visible) {
			p.selected = len(visible) - 1
		}
		visible[p.selected].detail(c, a, f, p)
	}).Grow(1)
}

// matching lists the messages the search and the direction filter keep, in the
// order they arrived.
func (p *messagePanel) matching(list []messageView) []messageView {
	query := strings.ToLower(strings.TrimSpace(p.query))
	out := make([]messageView, 0, len(list))
	for _, m := range list {
		switch p.direction {
		case 1:
			if m.direction != "in" {
				continue
			}
		case 2:
			if m.direction != "out" {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(m.searchText()), query) && !(p.searchCache.query == query && p.searchCache.matches[m.seq]) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// payload reads the chosen message once: a long one lives in the spill file
// the capture wrote, and reading it every frame would work the disk for a
// still window.
func (p *messagePanel) payload(f flow, m messageView) string {
	if p.loadedFlow != f.id || p.loadedSeq != m.seq {
		p.loadedFlow, p.loadedSeq, p.loaded = f.id, m.seq, m.payload()
		p.loadedError = ""
		if m.file != "" && len(p.loaded) < m.kept {
			p.loadedError = "the spill file is missing or incomplete"
		}
	}
	return p.loaded
}

func (p *messagePanel) stopSearch() {
	if p.searchCancel != nil {
		p.searchCancel()
	}
	p.searchCancel, p.searchResults, p.searchPending = nil, nil, nil
}

// Search spill files lazily with at most four outstanding reads. Caches stay
// bounded to the visible retention window even on a long-running live stream.
func (p *messagePanel) searchMessages(a *app, list []messageView) {
	query := strings.ToLower(strings.TrimSpace(p.query))
	if p.searchCache.query != query || p.searchResults == nil {
		p.stopSearch()
		p.searchCache = bodyCache{query: query, matches: make(map[int]bool), errs: make(map[int]string)}
		p.searchContext, p.searchCancel = context.WithCancel(context.Background())
		p.searchResults = make(chan bodyResult, bodyWorkers)
		p.searchPending = make(map[int]bool)
	}
	for len(p.searchResults) > 0 {
		r := <-p.searchResults
		delete(p.searchPending, r.id)
		p.searchCache.matches[r.id] = r.matched
		if r.err != "" {
			p.searchCache.errs[r.id] = r.err
		}
	}
	live := make(map[int]bool, len(list))
	for _, m := range list {
		live[m.seq] = true
	}
	maps.DeleteFunc(p.searchCache.matches, func(id int, _ bool) bool { return !live[id] })
	maps.DeleteFunc(p.searchCache.errs, func(id int, _ string) bool { return !live[id] })
	if query == "" {
		return
	}
	for _, m := range list {
		if len(p.searchPending) >= bodyWorkers {
			break
		}
		_, known := p.searchCache.matches[m.seq]
		if m.file == "" || known || p.searchPending[m.seq] || strings.Contains(strings.ToLower(m.searchText()), query) {
			continue
		}
		p.searchPending[m.seq] = true
		ctx, results := p.searchContext, p.searchResults
		go func() {
			matched, err := bodySideContains(ctx, m.data, m.file, m.kept, query, bodyChunk)
			if ctx.Err() != nil {
				return
			}
			r := bodyResult{id: m.seq, matched: matched}
			if err != nil {
				r.err = err.Error()
			}
			results <- r
			a.invalidate()
		}()
	}
}
