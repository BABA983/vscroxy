package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// timingView draws the Timing tab: how long the request waited for the
// server and how long the response took to arrive, as a bar and as numbers.
func (a *app) timingView(c *ui.Context, f flow) {
	t := c.Theme()
	if f.wait == 0 && f.download == 0 {
		ui.Text(c, "No timing for this flow").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	wait := ui.Hex("#2563eb")
	download := ui.Hex("#22c55e")
	ui.Column(c).Gap(12).Padding(16).Children(func() {
		ui.Row(c).Height(10).Radius(5).Clip().Children(func() {
			ui.Box(c).Grow(float32(max(f.wait, 1))).Height(10).Background(wait)
			ui.Box(c).Grow(float32(max(f.download, 1))).Height(10).Background(download)
		})
		ui.Row(c).Gap(16).Children(func() {
			timingLegend(c, t, wait, "Wait", f.wait)
			timingLegend(c, t, download, "Download", f.download)
			timingLegend(c, t, t.TextMuted, "Total", f.millis)
		})
	})
}

func timingLegend(c *ui.Context, t *ui.Theme, color ui.Color, label string, ms int) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(color)
		ui.Text(c, fmt.Sprintf("%s %d ms", label, ms)).FontSize(12).TextColor(t.TextMuted)
	})
}
