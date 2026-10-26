package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// timingView draws the Timing tab: the phases a request went through as rows,
// each with a bar placed where it happened in the exchange.
func (a *app) timingView(c *ui.Context, f flow) {
	t := c.Theme()
	if f.wait == 0 && f.download == 0 {
		ui.Text(c, "No timing for this flow").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	total := max(f.millis, 1)
	dns := ui.Hex("#a855f7")
	wait := ui.Hex("#2563eb")
	download := ui.Hex("#22c55e")
	ui.Column(c).Gap(6).Padding(16).Children(func() {
		if f.conn.dnsFresh && f.conn.resolved != "" {
			waterfallRow(c, t, "DNS lookup", fmt.Sprintf("%s → %s", hostOnly(f.host), f.conn.resolved),
				f.conn.lookupMs, 0, total, dns)
		}
		// The wait covers the dial and the server's own time; a lookup the
		// flow made itself is the part of it that can be named.
		waterfallRow(c, t, "Waiting", "request sent, first byte back", f.wait, 0, total, wait)
		waterfallRow(c, t, "Download", "rest of the response body", f.download, f.wait, total, download)
		ui.Divider(c)
		ui.Row(c).Gap(10).Padding(4, 0).Children(func() {
			ui.Text(c, "Total").FontSize(12).Bold().TextColor(t.Text).Width(90)
			ui.Text(c, f.method+" "+f.host+f.path).FontSize(11).TextColor(t.TextMuted).Width(260).SingleLine()
			ui.Text(c, fmt.Sprintf("%d ms", f.millis)).FontSize(11).Bold().TextColor(t.Text).Width(64)
		})
		ui.Text(c, connectionNote(f)).FontSize(11).TextColor(t.TextMuted).Padding(4, 0, 0, 0)
	})
}

// waterfallRow is one phase: its name, what it was, its time, and a bar at its
// place in the exchange.
func waterfallRow(c *ui.Context, t *ui.Theme, label, detail string, ms, offset, total int, color ui.Color) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(3, 0).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.Text).Width(90)
		ui.Text(c, detail).FontSize(11).TextColor(t.TextMuted).Width(260).SingleLine().Tooltip(detail)
		ui.Text(c, fmt.Sprintf("%d ms", ms)).FontSize(11).TextColor(t.TextMuted).Width(64)
		ui.Row(c).Grow(1).Height(10).Children(func() {
			if offset > 0 {
				ui.Spacer(c).Grow(float32(offset))
			}
			ui.Box(c).Grow(float32(max(ms, 1))).Height(10).Radius(3).Background(color)
			if rest := total - offset - ms; rest > 0 {
				ui.Spacer(c).Grow(float32(rest))
			}
		})
	})
}

// connectionNote says what the wait covers besides the server's own time: the
// lookup and the connection, when this request paid for them.
func connectionNote(f flow) string {
	var parts []string
	if f.conn.dnsFresh && f.conn.resolved != "" {
		parts = append(parts, "The DNS lookup is the first part of the wait.")
	}
	switch {
	case f.conn.flows > 1:
		parts = append(parts, fmt.Sprintf("Reused connection: request %d on it, so no connect or TLS in the wait.", f.conn.flows))
	case f.conn.flows == 1 && f.conn.tls != "":
		parts = append(parts, "New connection: connect and TLS are inside the wait.")
	case f.conn.flows == 1:
		parts = append(parts, "New connection: the connect is inside the wait.")
	}
	return strings.Join(parts, " ")
}
