package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// timingView draws the Timing tab: the phases a request went through as rows,
// each with a bar placed where it happened in the exchange.
func (a *app) timingView(c *ui.Context, f flow) {
	if f.timing != nil {
		timingWaterfall(c, f.timing)
		return
	}
	t := c.Theme()
	if f.wait == 0 && f.download == 0 {
		uikit.Hint(c, "No timing for this flow").Padding(t.Space(4))
		return
	}
	total := max(f.millis, 1)
	dns := uikit.Colors(t).MethodPatch
	wait := uikit.Colors(t).Info
	download := t.Success
	ui.Scroll(c).Grow(1).FillWidth().Gap(t.Space(2)).Padding(t.Space(4)).Children(func() {
		uikit.Hint(c, "Aggregate timing only · no request-local trace was recorded")
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

// headlineTiming summarizes the duration without adding overlapping phases.
// Captures without a trace retain their aggregate wait/download breakdown.
func headlineTiming(t *ui.Theme, f flow) uikit.Timing {
	if f.timing != nil {
		return uikit.Timing{Phases: []uikit.TimingPhase{{Name: "Proxy-observed", Millis: int(f.timing.TotalNS / 1e6), Color: uikit.Colors(t).Info}},
			Note: "Open Timing for actual phase offsets. Phases may overlap; setup before request headers and downstream delivery are excluded."}
	}
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

// timingWaterfall keeps all bars on the same request-local axis. Acquisition
// and individual dial attempts can overlap, as can a duplex send and receive.
// Geometry uses the recorded offsets, never a cumulative sum of durations.
func timingWaterfall(c *ui.Context, timing *capture.Timing) {
	t := c.Theme()
	valid := timing.TotalNS >= 0 && len(timing.Phases) <= 129
	for _, p := range timing.Phases {
		valid = valid && p.StartNS >= 0 && p.StartNS <= timing.TotalNS && p.DurationNS >= 0 && p.DurationNS <= timing.TotalNS-p.StartNS && len(p.Name) <= 64 && len(p.Detail) <= 1024
	}
	if !valid {
		uikit.Hint(c, "Invalid timing data · recorded offsets are outside the timeline").Padding(t.Space(4))
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Padding(t.Space(3)).Gap(t.Space(1)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(2)).Children(func() {
			uikit.Body(c, "Timing waterfall").Grow(1)
			uikit.Meta(c, timingDuration(timing.TotalNS)).Font(uikit.FontMono).FontFeatures("tnum")
		})
		uikit.Meta(c, "Request headers → capture publication · offsets from 0")
		ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
			for i := range 5 {
				offset := timing.TotalNS/4*int64(i) + timing.TotalNS%4*int64(i)/4
				uikit.Meta(c, timingDuration(offset)).Font(uikit.FontMono).MinWidth(0).Shrink(1).SingleLine()
			}
		})
		for i, p := range timing.Phases {
			c.Key(i)
			color := uikit.Colors(t).Info
			switch p.Name {
			case "Proxy", "Connection":
				color = t.TextMuted
			case "DNS", "TLS":
				color = uikit.Colors(t).MethodPatch
			case "TCP", "Send":
				color = uikit.Colors(t).Warning
			case "Receive":
				color = t.Success
			}
			status := ""
			if p.Incomplete {
				status = " · unfinished at capture"
			}
			if p.Failed {
				color, status = t.Danger, status+" · failed"
			}
			interval := fmt.Sprintf("Start %s · End %s%s", timingDuration(p.StartNS), timingDuration(p.StartNS+p.DurationNS), status)
			description := fmt.Sprintf("%s · %s · %s; exact offset %s, duration %s", p.Name, p.Detail, interval, time.Duration(p.StartNS), time.Duration(p.DurationNS))
			ui.Column(c).FillWidth().Gap(t.Space(1)).Padding(t.Space(1), 0).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(2)).Children(func() {
					uikit.Body(c, p.Name).Grow(1).MinWidth(0).Shrink(1).SingleLine()
					if p.Failed {
						uikit.Meta(c, "Failed").TextColor(t.Danger)
					}
					if p.Incomplete {
						uikit.Meta(c, "Open").Tooltip("Unfinished at capture")
					}
					uikit.Meta(c, timingDuration(p.DurationNS)).Font(uikit.FontMono).FontFeatures("tnum")
				})
				ui.Box(c).FillWidth().Height(8).Radius(4).Background(t.Border.Alpha(0.3)).
					Label(fmt.Sprintf("Timing phase %d: %s", i+1, p.Name)).Description(description).Tooltip(description).
					Draw(func(painter *ui.Painter, r ui.Rect) {
						total := float64(max(timing.TotalNS, 1))
						for tick := 1; tick < 4; tick++ {
							x := r.X + r.W*float32(tick)/4
							painter.Line(x, r.Y, x, r.Y+r.H, 1, t.Border)
						}
						x := r.X + r.W*float32(float64(p.StartNS)/total)
						width := r.W * float32(float64(p.DurationNS)/total)
						if width < 1 {
							// A hairline marks a zero/subpixel observation, not an
							// invented minimum duration. Text keeps the exact time.
							x = min(x, r.X+r.W-1)
							width = 1
						}
						painter.Fill(ui.Rect{X: x, Y: r.Y, W: width, H: r.H}, color, 0)
						if p.Incomplete {
							painter.Line(x+width-1, r.Y, x+width-1, r.Y+r.H, 2, t.Text)
						}
					})
			})
		}
		ui.Divider(c)
		if timing.Reused {
			uikit.Meta(c, "Upstream connection reused · previous setup is not charged again")
		}
		if timing.Truncated {
			uikit.Hint(c, "Trace limit reached · some observations were not retained")
		}
		uikit.Meta(c, "Phases may overlap; do not add their durations. Connection acquisition can include DNS/TCP/TLS. Unobserved phases are omitted, not zero.")
		uikit.Meta(c, "Proxy-observed only: pre-request MITM setup is in the connection log. Downstream delivery and estimated throttling are not measured here. Streaming timelines stop at this capture snapshot.")
	})
}

func timingDuration(ns int64) string {
	if ns > 0 && ns < 10_000 {
		return "<0.01 ms"
	}
	return fmt.Sprintf("%.2f ms", float64(ns)/1e6)
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
