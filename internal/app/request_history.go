package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// historyWidth is the History / Examples column beside a saved request.
const historyWidth = 240

// historyRecords are the retained runs of the request d edits, newest first:
// those sent from the library request it opened, in any session, and those
// sent from this document before it was saved. Never matched by URL.
func (a *app) historyRecords(d *requestDocument) []experimentRecord {
	var records []experimentRecord
	for _, r := range a.experiments.log.Records {
		if d.assetID != "" && r.Draft.ID == d.assetID || r.DraftKey == d.debugKey {
			records = append(records, r)
		}
	}
	return records
}

// historyColumn lists what a saved request has produced: the retained runs
// it was sent as, or the examples saved with it. Choosing a run shows it
// read-only in place of the response; choosing an example opens it in the
// Examples tab.
func (a *app) historyColumn(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Column(c).Width(historyWidth).FillHeight().Background(uikit.Colors(t).Panel).
		BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Height(uikit.HeightTabs+uikit.HeightCompact/2).Padding(0, t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Segmented(c, &d.historyTab, "History", "Examples").Label("Request history").FillWidth()
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(2), t.Space(2)).Gap(t.Space(0.5)).Children(func() {
			if d.historyTab == 1 {
				a.historyExamples(c, t, d)
				return
			}
			records := a.historyRecords(d)
			if len(records) == 0 {
				uikit.Hint(c, "Send this request to keep its runs here. Opening one never sends.").Padding(t.Space(2))
				return
			}
			for _, r := range records {
				status, millis := 0, 0
				if f := r.Exchange; f != nil {
					status, millis = f.Status, f.Millis
				}
				row := uikit.RowButton(c.Key(strconv.Itoa(r.ID)), fmt.Sprintf("Run #%d", r.ID)).Tooltip(r.Summary)
				if r.ID == d.historyPick {
					uikit.Selected(c, row, true)
				}
				row.Children(func() {
					shown := "—"
					if status != 0 {
						shown = strconv.Itoa(status)
					}
					ui.Text(c, shown).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(uikit.StatusColor(status, t)).Width(28)
					uikit.Body(c, runTime(r.At)).FontFeatures("tnum").SingleLine().Grow(1).MinWidth(0)
					if status != 0 {
						uikit.Meta(c, fmt.Sprintf("%d ms", millis)).FontFeatures("tnum")
					}
				})
				if row.Clicked() {
					d.historyPick, d.historyReader = r.ID, nil
				}
			}
		})
	})
}

func (a *app) historyExamples(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if len(d.examples) == 0 {
		uikit.Hint(c, "Send the request, then save a response as an example.").Padding(t.Space(2))
		return
	}
	for i, e := range d.examples {
		row := uikit.RowButton(c.Key(e.ID), "Open example: "+e.Name)
		if d.composeTab == composeTabExamples && i == d.example {
			uikit.Selected(c, row, true)
		}
		row.Children(func() {
			ui.Text(c, strconv.Itoa(e.Status)).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(uikit.StatusColor(e.Status, t)).Width(28)
			uikit.Body(c, e.Name).SingleLine().Grow(1).MinWidth(0)
		})
		if row.Clicked() {
			d.composeTab, d.example, d.exampleEditor = composeTabExamples, i, nil
		}
	}
}

// historyResponse shows a retained run where the response goes, read-only,
// until the reader goes back to the latest response or sends again.
func (a *app) historyResponse(c *ui.Context, t *ui.Theme, d *requestDocument, r *experimentRecord) {
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			ui.Text(c, fmt.Sprintf("Run #%d · %s", r.ID, runTime(r.At))).FontSize(uikit.TextBody).FontWeight(600).FontFeatures("tnum").SingleLine()
			uikit.Meta(c, "Read-only").Grow(1)
			if f := r.Exchange; f != nil && f.Status != 0 {
				uikit.StatusPill(c, strconv.Itoa(f.Status), uikit.StatusColor(f.Status, t)).FontSize(uikit.TextMeta)
				uikit.Meta(c, fmt.Sprintf("%d ms", f.Millis)).FontFeatures("tnum")
				uikit.Meta(c, humanBytes(f.Bytes)).FontFeatures("tnum")
			}
			if uikit.CompactButton(c, "Open as draft").Tooltip("Open this run as a new draft with its result; never sends").Clicked() {
				a.openRecord(*r)
				return
			}
			if uikit.IconButton(c, "Show latest response", "Back to this request's latest response", closeIcon).Clicked() {
				d.historyPick, d.historyReader = 0, nil
			}
		})
		if r.Outcome != "" || r.Summary != "" {
			uikit.Meta(c, r.Summary).Padding(t.Space(1), t.Space(3)).SingleLine().Tooltip(r.Summary)
		}
		if d.historyReader == nil {
			d.historyReader = &editor.State{Buffer: editor.NewBuffer(r.resultText()), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: fmt.Sprintf("Run #%d result", r.ID)}
		}
		requestCodeEditor(c, d.historyReader)
	})
}

// runTime is when a run happened: the time today, the date before.
func runTime(at time.Time) string {
	at = at.Local()
	if y, m, day := time.Now().Date(); at.Year() == y && at.Month() == m && at.Day() == day {
		return at.Format("15:04:05")
	}
	return at.Format("01-02 15:04")
}
