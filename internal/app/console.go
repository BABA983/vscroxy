package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// consoleEntry is one row of the request workspace's Console: a sent request,
// or a line its pre-request or post-response script printed. A request row
// keeps the headers and sizes its disclosure shows, never a body.
type consoleEntry struct {
	id int
	at time.Time
	// request marks the one row per send; the rest carry kind and text.
	request         bool
	method, url     string
	status, millis  int
	outcome         string
	kind, text      string
	requestHeaders  string
	responseHeaders string
	requestSize     int
	responseSize    int
}

// consolePanel is the Console's place in the window. It is memory only: open,
// the divider's share and the chosen level last for this run, and the entries
// are capped so a long session cannot grow without bound.
type consolePanel struct {
	open     bool
	split    float32
	level    string
	seq      int
	entries  []consoleEntry
	expanded map[int]bool
}

const consoleEntryLimit = 1000

// consoleLevels are the level filter's values in menu order. An empty value is
// All logs.
var consoleLevels = []string{"", "log", "info", "warning", "error"}

func consoleLevelLabel(level string) string {
	switch level {
	case "":
		return locales.AllLogs()
	case "log":
		return locales.LogLevelLog()
	case "info":
		return locales.LogLevelInfo()
	case "warning":
		return locales.LogLevelWarning()
	default:
		return locales.LogLevelError()
	}
}

// toggleConsole opens or closes the request workspace's Console. The footer
// item and the view.console command both come here.
func (a *app) toggleConsole() {
	p := &a.requestConsole
	p.open = !p.open
	if p.split == 0 {
		p.split = 70
	}
	a.invalidate()
}

// consoleSend adds one send to the Console: its pre-request lines, the request
// row, then its post-response lines, in that order. Script variables are not
// console lines; the request row and its messages are all that is kept.
func (a *app) consoleSend(d *requestDocument, result requestResult) {
	if d == nil {
		return
	}
	p := &a.requestConsole
	now := time.Now()
	start := d.sentAt
	if start.IsZero() {
		start = now
	}
	// Script lines are stamped by when they ran around the request, not when
	// the UI happened to drain the result: a pre-request line comes at the
	// send's start, and a post-response line once the response has elapsed.
	completed := now
	if r := result.response; r != nil {
		completed = start.Add(time.Duration(max(r.Millis, 0)) * time.Millisecond)
	}
	appendMessage := func(m request.ConsoleMessage, at time.Time) {
		if m.Kind == "variable" || m.Kind == "" {
			return
		}
		p.seq++
		p.entries = append(p.entries, consoleEntry{id: p.seq, at: at, kind: m.Kind, text: m.Text})
	}
	var post []request.ConsoleMessage
	for _, m := range result.console {
		if strings.HasPrefix(m.Phase, "pre-request") {
			appendMessage(m, start)
		} else {
			post = append(post, m)
		}
	}
	entry := consoleEntry{request: true, at: d.sentAt, method: d.sent.Method, url: d.sent.URL, outcome: result.outcome}
	if entry.at.IsZero() {
		entry.at = now
	}
	if r := result.response; r != nil {
		entry.status, entry.millis = r.Status, r.Millis
		entry.requestHeaders = request.HeaderBlock(r.RequestHeader)
		entry.responseHeaders = request.HeaderBlock(r.ResponseHeader)
		entry.requestSize, entry.responseSize = r.RequestBodySize, max(r.Bytes, r.ResponseBodySize)
	} else {
		entry.requestHeaders = request.HeaderBlock(request.ParseHeaderBlock(d.sent.Headers))
		entry.requestSize = len(d.sent.Body)
	}
	p.seq++
	entry.id = p.seq
	p.entries = append(p.entries, entry)
	for _, m := range post {
		appendMessage(m, completed)
	}
	if len(p.entries) > consoleEntryLimit {
		p.entries = slices.Delete(p.entries, 0, len(p.entries)-consoleEntryLimit)
	}
}

// visibleConsoleEntries applies the level filter. Request rows are part of All
// logs; a level shows only the messages that level names.
func (a *app) visibleConsoleEntries() []consoleEntry {
	p := &a.requestConsole
	if p.level == "" {
		return p.entries
	}
	out := make([]consoleEntry, 0, len(p.entries))
	for _, e := range p.entries {
		if !e.request && e.kind == p.level {
			out = append(out, e)
		}
	}
	return out
}

// consolePanelView is the bottom Console: one compact header and its rows.
func (a *app) consolePanelView(c *ui.Context, t *ui.Theme) {
	p := &a.requestConsole
	entries := a.visibleConsoleEntries()
	ui.Column(c).Fill().FillWidth().MinHeight(0).Background(t.Background).
		Border(0, t.Border).BorderWidth(1, 0, 0, 0).Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Shrink(0).Padding(0, t.Space(uikit.SpaceL)).
			Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
			uikit.BodyStrong(c, locales.Console())
			ui.Spacer(c)
			uikit.ToolbarSelect(c, consoleLevelLabel(p.level), func(m *ui.Menu) {
				for _, level := range consoleLevels {
					if m.Item(consoleLevelLabel(level)).Checked(level == p.level).Chosen() {
						p.level = level
					}
				}
			}).Label(locales.ConsoleLevelFilter())
			if uikit.CompactButton(c, locales.Clear()).Disabled(len(p.entries) == 0).Clicked() {
				p.entries, p.expanded = nil, nil
			}
			copy := uikit.RowIconButton(c, locales.CopyAllLogs(), locales.CopyAllLogs(), lucide.Copy()).
				Disabled(len(p.entries) == 0)
			if uikit.Tip(c, copy, locales.CopyAllLogs()).Clicked() {
				c.Services().WriteClipboard(a.consoleText())
				a.done(locales.ConsoleCopied())
			}
			if uikit.Tip(c, uikit.RowIconButton(c, locales.Close(), locales.Close(), lucide.X()), locales.Close()).Clicked() {
				p.open = false
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			if len(entries) == 0 {
				message := locales.ConsoleEmpty()
				if p.level != "" {
					message = locales.ConsoleNoLevelEntries()
				}
				uikit.Hint(c, message).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
				return
			}
			for _, entry := range entries {
				if entry.request {
					a.consoleRequestRow(c, t, entry)
				} else {
					consoleMessageRow(c, t, entry)
				}
			}
		})
	})
}

// consoleRequestRow is one sent request: its disclosure, method, URL, status
// and duration on one line, and its headers and sizes when open.
func (a *app) consoleRequestRow(c *ui.Context, t *ui.Theme, entry consoleEntry) {
	p := &a.requestConsole
	open := p.expanded[entry.id]
	row := ui.Row(c).Key(fmt.Sprintf("console-request-%d", entry.id)).FillWidth().MinHeight(uikit.HeightRow).
		Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	toggle := func() {
		if p.expanded == nil {
			p.expanded = map[int]bool{}
		}
		p.expanded[entry.id] = !open
	}
	arrow := lucide.ChevronRight()
	if open {
		arrow = lucide.ChevronDown()
	}
	row.Children(func() {
		if uikit.Tip(c, uikit.RowIconButton(c, locales.ConsoleRequestDetails(),
			locales.ConsoleRequestDetails(), arrow), locales.ConsoleRequestDetails()).Clicked() {
			toggle()
		}
		consoleStamp(c, entry.at)
		uikit.MethodLabel(c, entry.method).Width(46).Shrink(0)
		uikit.Mono(c, entry.url).FontSize(uikit.Scaled(uikit.TextData)).Grow(1).MinWidth(0).SingleLine()
		status := strconv.Itoa(entry.status)
		ink := consoleStatusColor(entry.status, t)
		if entry.status == 0 {
			status = locales.Failed()
			ink = t.Danger
		}
		ui.Row(c).Width(58).Shrink(0).Justify(ui.End).Children(func() {
			uikit.Caption(c, status).FontFeatures("tnum").TextColor(ink).SingleLine()
		})
		ui.Row(c).Width(88).Shrink(0).Justify(ui.End).Children(func() {
			if entry.millis > 0 {
				uikit.Meta(c, fmt.Sprintf("%d ms", entry.millis)).Font(uikit.FontMono).FontFeatures("tnum").SingleLine()
			}
		})
	})
	if !open {
		return
	}
	ui.Column(c).FillWidth().Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).
		PaddingStart(t.Space(uikit.SpaceL) + 20 + 92 + t.Space(uikit.SpaceS)*2).
		Gap(t.Space(uikit.SpaceXS)).Children(func() {
		consoleHeaderBlock(c, t, locales.ConsoleRequestHeaders(), entry.requestHeaders)
		consoleHeaderBlock(c, t, locales.ConsoleResponseHeaders(), entry.responseHeaders)
		ui.Row(c).Gap(t.Space(uikit.SpaceL)).Children(func() {
			uikit.Meta(c, locales.RequestSize()+" · "+humanBytes(entry.requestSize))
			uikit.Meta(c, locales.ConsoleResponseSize()+" · "+humanBytes(entry.responseSize))
		})
	})
}

// consoleMessageRow is one script line: time, level icon, and the message,
// which wraps rather than losing its end.
func consoleMessageRow(c *ui.Context, t *ui.Theme, entry consoleEntry) {
	ink := t.Text
	var icon *ui.SVG
	switch entry.kind {
	case "warning":
		ink, icon = t.Warning, lucide.TriangleAlert()
	case "error":
		ink, icon = t.Danger, lucide.CircleAlert()
	}
	ui.Row(c).FillWidth().MinHeight(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceL)).
		Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Start).Children(func() {
		// The request row's disclosure occupies this gutter, so every row's
		// timestamp starts at the same x whether or not it expands.
		ui.Box(c).Size(uikit.HeightRowAction, uikit.HeightRowAction).Shrink(0)
		consoleStamp(c, entry.at)
		ui.Row(c).Width(14).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if icon != nil {
				ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(ink)
			}
		})
		ui.Text(c, entry.text).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextData)).
			TextColor(ink).Selectable().Grow(1).MinWidth(0)
	})
}

// consoleStamp is the same-width time at every row's start.
func consoleStamp(c *ui.Context, at time.Time) {
	uikit.Meta(c, at.Local().Format("15:04:05.000")).Font(uikit.FontMono).FontFeatures("tnum").
		Width(92).Shrink(0).SingleLine()
}

func consoleHeaderBlock(c *ui.Context, t *ui.Theme, label, block string) {
	ui.Row(c).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Start).Children(func() {
		uikit.Meta(c, label).Width(112).Shrink(0).SingleLine()
		if strings.TrimSpace(block) == "" {
			uikit.Meta(c, "—")
			return
		}
		uikit.Mono(c, strings.TrimRight(block, "\n")).FontSize(uikit.Scaled(uikit.TextMeta)).
			TextColor(t.TextMuted).Selectable().Grow(1).MinWidth(0)
	})
}

func consoleStatusColor(status int, t *ui.Theme) ui.Color {
	switch {
	case status >= 200 && status < 300:
		return t.Success
	case status >= 300 && status < 500:
		return t.Warning
	case status >= 500:
		return t.Danger
	default:
		return uikit.Colors(t).MutedForeground
	}
}

// consoleText is the copy-all text: every entry, not only the filtered ones.
func (a *app) consoleText() string {
	var b strings.Builder
	for _, e := range a.requestConsole.entries {
		stamp := e.at.Local().Format("15:04:05.000")
		if e.request {
			status := strconv.Itoa(e.status)
			if e.status == 0 {
				status = "—"
			}
			fmt.Fprintf(&b, "%s %s %s %s %d ms\n", stamp, e.method, e.url, status, e.millis)
			continue
		}
		fmt.Fprintf(&b, "%s [%s] %s\n", stamp, e.kind, e.text)
	}
	return b.String()
}
