package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// historyWidth is the Runs / Examples column at a saved request's response.
const historyWidth = 220

// historyResponseMin is the room the response keeps beside the runs column:
// its four tabs, the status and its numbers, so the column never takes what
// the response says about itself.
const historyResponseMin = 480

// historyColumnFits reports whether d's response has room for its runs
// column; a narrower one opens the runs on their own page instead.
func (a *app) historyColumnFits(c *ui.Context, d *requestDocument) bool {
	if d == nil || d.assetID == "" || d.comparing {
		return false
	}
	return a.editorPaneWidthAt(c, &a.editorRespArea, 100-draftPercent) >= historyWidth+historyResponseMin
}

// historyColumnShown reports whether the runs column is beside d's response.
func (a *app) historyColumnShown(c *ui.Context, d *requestDocument) bool {
	return !d.historyHidden && a.historyColumnFits(c, d)
}

var historyStatuses = []string{"All statuses", "2xx", "3xx", "4xx", "5xx", "No response"}
var historySources = []string{"All sources", "Capture-linked", "Authored"}

func (r experimentRecord) matchesHistory(status, source string) bool {
	if source == "Capture-linked" && r.Source == nil || source == "Authored" && r.Source != nil {
		return false
	}
	code := 0
	if r.Exchange != nil {
		code = r.Exchange.Status
	}
	if status == "No response" {
		return code == 0
	}
	return len(status) != 3 || status[1:] != "xx" || code/100 == int(status[0]-'0')
}

func historyFilters(c *ui.Context, status, source *string) {
	if *status == "" {
		*status = historyStatuses[0]
	}
	if *source == "" {
		*source = historySources[0]
	}
	uikit.SelectLabels(c, status, historyStatuses, optionLabel).Label(locales.HistoryStatus()).FillWidth().Shrink(1).MinWidth(0)
	uikit.SelectLabels(c, source, historySources, optionLabel).Label(locales.HistorySource()).FillWidth().Shrink(1).MinWidth(0)
}

// historyRecords are the retained runs of the request d edits, newest first:
// those sent from the library request it opened, in any session, and those
// sent from this document before it was saved. Never matched by URL.
func (a *app) historyRecords(d *requestDocument) []experimentRecord {
	var records []experimentRecord
	for _, r := range a.experiments.log.Records {
		if (d.assetID != "" && r.Draft.ID == d.assetID || r.DraftKey == d.debugKey) && r.matchesHistory(d.historyStatus, d.historySource) {
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
	// The column is part of the response, not a panel of its own: canvas,
	// told apart from it by one rule that runs the response's full height,
	// from the rule under the URL bar to the panel's edge.
	ui.Column(c).Width(historyWidth).Shrink(0).FillHeight().
		BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		// The same row as the response's own header beside it, so the two
		// rows share one centre line.
		uikit.PaneHeader(c, func() {
			uikit.SubTabs(c, &d.historyTab, locales.RequestHistory(), locales.History(), locales.Examples())
		}, nil)
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceM), t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceHair)).Children(func() {
			if d.historyTab == 1 {
				a.historyExamples(c, t, d)
				return
			}
			records := a.historyRecords(d)
			// The filters are only worth their room once there are runs to
			// narrow, and they stay while a filter is set: a filter that
			// hides everything must stay in reach to be undone.
			if len(records) > 0 || d.historyStatus != "" || d.historySource != "" {
				historyFilters(c, &d.historyStatus, &d.historySource)
			}
			if len(records) == 0 {
				// Nothing to narrow yet. The response beside it already says
				// how to send, so the column only says it is empty.
				ui.Column(c).FillWidth().Center().Padding(t.Space(uikit.SpaceXL), 0).Children(func() {
					uikit.Hint(c, locales.NotSentYet())
				})
				return
			}
			for _, r := range records {
				status, millis := 0, 0
				if f := r.Exchange; f != nil {
					status, millis = f.Status, f.Millis
				}
				row := uikit.Tip(c, uikit.RowButton(c.Key(strconv.Itoa(r.ID)), locales.RunNumberLabel(r.ID)), r.Summary).
					PopoverMenu(func(m *ui.Menu) { actionMenu(m, a.runCompareActions(runTarget(r))) })
				if r.ID == d.historyPick {
					uikit.Selected(c, row, !a.windowBehind)
				}
				row.Children(func() {
					shown := "—"
					if status != 0 {
						shown = strconv.Itoa(status)
					}
					uikit.DataMeta(c, shown).TextColor(uikit.StatusColor(status, t)).Width(28)
					uikit.Body(c, runTime(r.At)).FontFeatures("tnum").SingleLine().Grow(1).MinWidth(0)
					if r.ID == a.experiments.log.Baseline {
						uikit.Tip(c, uikit.Glyph(c, pinIcon), locales.Baseline())
					}
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
		uikit.Hint(c, locales.SendTheRequestThenSave()).Padding(t.Space(uikit.SpaceM))
		return
	}
	for i, e := range d.examples {
		row := uikit.RowButton(c.Key(e.ID), locales.OpenExampleNamed(e.Name))
		if d.composeTab == composeTabExamples && i == d.example {
			uikit.Selected(c, row, !a.windowBehind)
		}
		row.Children(func() {
			uikit.DataMeta(c, strconv.Itoa(e.Status)).TextColor(uikit.StatusColor(e.Status, t)).Width(28)
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
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			uikit.Body(c, locales.RunNumber(r.ID, runTime(r.At))).FontWeight(500).FontFeatures("tnum").SingleLine()
			uikit.Meta(c, locales.ReadOnly()).Grow(1)
			if f := r.Exchange; f != nil && f.Status != 0 {
				uikit.StatusPill(c, strconv.Itoa(f.Status), uikit.StatusColor(f.Status, t))
				uikit.Meta(c, fmt.Sprintf("%d ms", f.Millis)).FontFeatures("tnum")
				uikit.Meta(c, humanBytes(f.Bytes)).FontFeatures("tnum")
			}
			if uikit.Tip(c, uikit.CompactButton(c, locales.OpenAsDraft()), locales.OpenThisRunAsA()).Clicked() {
				a.openRecord(*r)
				return
			}
			if uikit.RowIconButton(c, locales.ShowLatestResponse(), locales.BackToThisRequestS(), closeIcon).Clicked() {
				d.historyPick, d.historyReader = 0, nil
			}
		})
		ui.Row(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Children(func() {
			if uikit.Tip(c, uikit.CompactButton(c, locales.RunDetails()), locales.NotesBaselineAndComparisonIn()).Clicked() {
				a.openSendHistory(d)
				a.experiments.pick(r.ID)
			}
		})
		if r.Outcome != "" || r.Summary != "" {
			uikit.Tip(c, uikit.Meta(c, r.Summary).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).SingleLine(), r.Summary)
		}
		if d.historyReader == nil {
			d.historyReader = &editor.State{Buffer: editor.NewBuffer(r.resultText()), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.RunNumberResult(r.ID)}
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
