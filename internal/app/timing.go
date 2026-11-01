package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"strings"

	"github.com/egoist/mygo/ui"
)

// timingView draws the Timing tab: the phases a request went through as rows,
// each with a bar placed where it happened in the exchange.
func (a *app) timingView(c *ui.Context, f flow) {
	t := c.Theme()
	if f.wait == 0 && f.download == 0 {
		uikit.Hint(c, "No timing for this flow").Padding(t.Space(4))
		return
	}
	total := max(f.millis, 1)
	dns := uikit.Colors(t).MethodPatch
	wait := uikit.Colors(t).Info
	download := t.Success
	ui.Column(c).Gap(t.Space(2)).Padding(t.Space(4)).Children(func() {
		if f.conn.dnsFresh && f.conn.resolved != "" {
			waterfallRow(c, t, "DNS lookup", fmt.Sprintf("%s → %s", hostOnly(f.host), f.conn.resolved),
				f.conn.lookupMs, 0, total, dns)
		}
		// The wait covers the dial and the server's own time; a lookup the
		// flow made itself is the part of it that can be named.
		waterfallRow(c, t, "Waiting", "request sent, first byte back", f.wait, 0, total, wait)
		waterfallRow(c, t, "Download", "rest of the response body", f.download, f.wait, total, download)
		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Padding(t.Space(1), 0).Children(func() {
			ui.Text(c, "Total").FontSize(uikit.TextBody).FontWeight(600).TextColor(t.Text).Shrink(0)
			target := f.method + " " + f.host + f.path
			uikit.Meta(c, target).Grow(1).Shrink(1).MinWidth(0).SingleLine().Tooltip(target)
			ui.Text(c, fmt.Sprintf("%d ms", f.millis)).Font(uikit.FontMono).FontSize(uikit.TextMeta).FontFeatures("tnum").FontWeight(600).TextColor(t.Text).Shrink(0)
		})
		uikit.Meta(c, connectionNote(f)).Padding(t.Space(1), 0, 0, 0)
	})
}

// headlineTiming splits a response's time for the hover card on its duration:
// the lookup the request made itself, the rest of the wait, and the download,
// so the phases add up to the time the server took.
func headlineTiming(t *ui.Theme, f flow) uikit.Timing {
	if f.wait == 0 && f.download == 0 {
		return uikit.Timing{}
	}
	var phases []uikit.TimingPhase
	wait := f.wait
	if f.conn.dnsFresh && f.conn.resolved != "" {
		lookup := min(f.conn.lookupMs, wait)
		phases = append(phases, uikit.TimingPhase{Name: "DNS lookup", Millis: lookup, Color: uikit.Colors(t).MethodPatch})
		wait -= lookup
	}
	phases = append(phases,
		uikit.TimingPhase{Name: "Waiting", Millis: wait, Color: uikit.Colors(t).Info},
		uikit.TimingPhase{Name: "Download", Millis: f.download, Color: t.Success})
	note := connectionNote(f)
	if f.conn.dnsFresh && f.conn.resolved != "" {
		// The card lists the lookup on its own row.
		note = strings.TrimSpace(strings.TrimPrefix(note, "The DNS lookup is the first part of the wait."))
	}
	return uikit.Timing{Phases: phases, Note: note}
}

// waterfallRow is one phase: its name, what it was, its time, and a bar at its
// place in the exchange.
func waterfallRow(c *ui.Context, t *ui.Theme, label, detail string, ms, offset, total int, color ui.Color) {
	// The name and milliseconds lead, the detail gives way in a narrow
	// inspector, and the bar takes the full width below them.
	ui.Column(c).FillWidth().Gap(t.Space(1)).Padding(t.Space(1), 0).Children(func() {
		ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(uikit.TextBody).TextColor(t.Text).Shrink(0)
			uikit.Meta(c, detail).Grow(1).Shrink(1).MinWidth(0).SingleLine().Tooltip(detail)
			uikit.Meta(c, fmt.Sprintf("%d ms", ms)).Font(uikit.FontMono).FontFeatures("tnum").Shrink(0)
		})
		ui.Row(c).FillWidth().Height(8).Children(func() {
			if offset > 0 {
				ui.Spacer(c).Grow(float32(offset))
			}
			ui.Box(c).Grow(float32(max(ms, 1))).Height(8).Radius(4).Background(color)
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
