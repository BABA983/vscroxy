package app

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type experimentWorkbench struct {
	// limits are the configured history caps, shared by the writer.
	limits                           experimentLimits
	log                              experimentLog
	path, problem                    string
	open, clearing, readOnly, closed bool
	picked, left, tab                int
	note, ignore                     string
	reader                           *editor.State
	comparison                       captureAnalysis
	sourceComparison                 captureAnalysis
	scope                            *debugSource
	draftKey                         string
	assetID, status, sourceFilter    string
	wire                             int
	revealVars                       bool
	records                          ui.ListState
	changed                          time.Time
	jobs                             chan experimentLog
	results                          chan error
	writer                           sync.WaitGroup
	notify                           func()
}

func (a *app) loadExperiments(path string) {
	s := &a.experiments
	s.path = path
	s.notify = a.invalidate
	s.limits = a.experimentLimits()
	log, err := loadExperiments(path, a.experimentLimits())
	if err != nil {
		s.problem, s.readOnly = locales.ExperimentsProblem(err.Error()), true
		return
	}
	s.log = log
	for _, draft := range log.Drafts {
		draft = restoreExperimentDraft(draft)
		a.addDocument(draft.Value, draft.SavedKey)
		a.baseline = draft.Baseline
		if draft.Key != "" {
			a.debugKey = draft.Key
		}
		a.setDebugSource(draft.Source)
	}
	if len(a.documents) > 0 {
		a.activateDocument(a.documents[min(max(log.Active, 0), len(a.documents)-1)])
	}
}

// One ordered writer prevents an older checkpoint from replacing a newer one.
// Record payloads are immutable; callers replace notes and copy the outer slices.
func (s *experimentWorkbench) persist() {
	if s.path == "" || s.readOnly || s.closed {
		return
	}
	if s.jobs == nil {
		s.jobs, s.results = make(chan experimentLog, 1), make(chan error, 1)
		s.writer.Go(func() {
			for state := range s.jobs {
				err := os.MkdirAll(filepath.Dir(s.path), 0o700)
				if err == nil {
					err = saveExperiments(s.path, state, s.limits)
				}
				select {
				case <-s.results:
				default:
				}
				s.results <- err
				if s.notify != nil {
					s.notify()
				}
			}
		})
	}
	state := s.log
	state.Records, state.Drafts = slices.Clone(state.Records), slices.Clone(state.Drafts)
	select {
	case <-s.jobs:
	default:
	}
	s.jobs <- state
}

func (s *experimentWorkbench) close() {
	if s.closed {
		return
	}
	s.persist()
	s.closed = true
	if s.jobs != nil {
		close(s.jobs)
		s.writer.Wait()
	}
}

func (a *app) checkpointExperiments(c *ui.Context) {
	s := &a.experiments
	if s.path == "" || s.readOnly || s.closed {
		return
	}
	select {
	case err := <-s.results:
		if err != nil {
			s.problem = locales.ExperimentCheckpointFailed(err.Error())
		} else {
			s.problem = ""
		}
	default:
	}
	drafts := a.experimentDrafts()
	active := max(0, slices.Index(a.documents, a.requestDocument))
	if !reflect.DeepEqual(s.log.Drafts, drafts) || s.log.Active != active {
		s.log.Drafts, s.log.Active = drafts, active
		s.changed = c.Now()
	}
	if !s.changed.IsZero() {
		if c.Now().Sub(s.changed) >= 500*time.Millisecond {
			s.persist()
			s.changed = time.Time{}
		} else {
			c.After(500 * time.Millisecond)
		}
	}
}

// experimentDrafts snapshots the open documents for the log. A schema that
// the draft and its baseline share is stored once, in Value; SchemaShared
// tells loadExperiments to put it back into the baseline.
func (a *app) experimentDrafts() []experimentDraft {
	var drafts []experimentDraft
	for _, d := range a.documents {
		value := d.value()
		baseline := d.baseline
		baseline.Form = slices.Clone(baseline.Form)
		shared := value.GraphQL.Schema != "" &&
			baseline.GraphQL.Schema == value.GraphQL.Schema &&
			baseline.GraphQL.SchemaSource == value.GraphQL.SchemaSource
		if shared {
			baseline.GraphQL.Schema, baseline.GraphQL.SchemaSource = "", ""
		}
		drafts = append(drafts, experimentDraft{Key: d.debugKey, Source: d.origin, Value: value, Baseline: baseline, SavedKey: d.savedKey, SchemaShared: shared})
	}
	return drafts
}

func restoreExperimentDraft(draft experimentDraft) experimentDraft {
	if draft.SchemaShared {
		draft.Baseline.GraphQL.Schema = draft.Value.GraphQL.Schema
		draft.Baseline.GraphQL.SchemaSource = draft.Value.GraphQL.SchemaSource
	}
	return draft
}

func (s *experimentWorkbench) record(id int) *experimentRecord {
	for i := range s.log.Records {
		if s.log.Records[i].ID == id {
			return &s.log.Records[i]
		}
	}
	return nil
}

func (s *experimentWorkbench) pick(id int) {
	s.picked, s.reader, s.comparison.diff = id, nil, nil
	s.revealVars = false
	s.sourceComparison, s.wire = captureAnalysis{}, 0
	if r := s.record(id); r != nil {
		s.note = r.Note
	}
}

// openRecord opens a retained attempt as a new draft with its result,
// never sending it again.
func (a *app) openRecord(r experimentRecord) {
	draft := r.Draft
	draft.ID = "" // Restoring an attempt creates a copy, not a library update.
	a.addDocument(draft, "")
	a.setDebugSource(r.Source)
	a.experimentID, a.response, a.composeTests, a.composeResult = r.ID, r.response(), slices.Clone(r.Tests), r.Summary
	a.composeReplies = slices.Clone(r.Replies)
	a.console = slices.Clone(r.Console)
	a.sent, a.sentAt, a.sentSet, a.sentRun = a.draft(), r.At, true, r.RunKey
	a.sendOutcome = cmp.Or(r.Outcome, locales.RetainedAttempt())
	if a.response != nil {
		a.responseArrived()
	}
}

// resultText is a retained run's response as one read-only text: headers,
// body, checks and WebSocket replies.
func (r experimentRecord) resultText() string {
	f := r.compared("")
	text := f.resHeaders + "\n\n" + f.body(true)
	for _, check := range r.Tests {
		text += fmt.Sprintf("\nCheck %t: %s", check.Pass, check.Name)
		if check.Error != "" {
			text += " — " + check.Error
		}
	}
	if len(r.Replies) > 0 {
		text += "\nWebSocket replies:\n" + strings.Join(r.Replies, "\n")
	}
	return text
}

func (r experimentRecord) compared(ignore string) flow {
	f := flow{id: r.ID, method: r.Draft.Method, url: r.Draft.URL, path: r.Draft.URL}
	// Bodies may have been spilled to disk when the history size cap was hit.
	f.reqBody, f.resBody = string(r.requestBody()), string(r.responseBody())
	if reply := r.Exchange; reply != nil {
		f.method, f.url, f.path, f.status, f.millis = reply.Method, reply.URL, reply.URL, reply.Status, reply.Millis
		f.reqSize, f.reqError, f.reqHeaders = reply.RequestBodySize, reply.RequestBodyError, request.HeaderBlock(reply.RequestHeader)
		f.resSize, f.resError, f.resHeaders = reply.ResponseBodySize, reply.ResponseBodyError, request.HeaderBlock(reply.ResponseHeader)
	} else {
		f.reqSize, f.resSize = len(f.reqBody), len(f.resBody)
	}
	if (r.Exchange == nil || r.Exchange.Status == 0) && f.resBody == "" {
		f.resError = locales.NoHTTPResponseWith(r.Summary)
	}
	if strings.TrimSpace(ignore) != "" {
		var object map[string]jsontext.Value
		if json.Unmarshal([]byte(f.resBody), &object) == nil && object != nil && f.resError == "" {
			for key := range strings.SplitSeq(ignore, ",") {
				delete(object, strings.TrimSpace(key))
			}
			if data, err := json.Marshal(object, json.Deterministic(true)); err == nil {
				f.resBody = string(data)
				f.resSize = len(data)
			}
		} else {
			f.resError += locales.IgnoreKeysNotApplied()
		}
	}
	return f
}

func (a *app) experimentsModal(c *ui.Context, t *ui.Theme) {
	s := &a.experiments
	if !s.open {
		return
	}
	title := locales.RunsPage()
	if s.scope != nil {
		title = locales.TitleSource(title)
	} else if s.draftKey != "" {
		title = locales.TitleDraft(title)
	}
	uikit.Modal(c, &s.open, title, func() { a.experimentsView(c, t, false) })
}

// The retained-run reader is shared by the resource page and scoped history.
func (a *app) experimentsView(c *ui.Context, t *ui.Theme, inline bool) {
	s := &a.experiments
	if selected := s.record(s.picked); selected == nil || !s.includes(*selected) {
		s.picked = 0
		for _, r := range s.log.Records {
			if s.includes(r) {
				s.pick(r.ID)
				break
			}
		}
	}
	width, height := c.Size()
	panel := ui.Column(c).Gap(t.Space(uikit.SpaceM))
	if inline {
		panel.Grow(1).FillWidth().MinHeight(0).Padding(t.Space(uikit.SpaceL))
	} else {
		panel.Width(min(1200, width-80)).Height(height - 100)
	}
	panel.Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			title := locales.RunsPage()
			if s.scope != nil {
				title = locales.TitleSource(title)
			} else if s.draftKey != "" {
				title = locales.TitleDraft(title)
			}
			uikit.DialogTitle(c, title).Grow(1)
			uikit.Tip(c, uikit.Glyph(c, infoIcon).TextColor(t.TextMuted), locales.PrivateLocalSnapshotsMayContain())
			if uikit.SecondaryButton(c, locales.ClearHistory()).Disabled(s.readOnly || len(s.log.Records) == 0).Clicked() {
				s.clearing = true
			}
			if uikit.SecondaryButton(c, locales.Close()).Clicked() {
				s.open = false
				if inline {
					a.showRequestWorkspace()
				}
			}
		})
		if s.problem != "" {
			uikit.Callout(c, uikit.CalloutDanger, s.problem, nil)
		}
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
			historyFilters(c, &s.status, &s.sourceFilter)
		})
		visible := make([]experimentRecord, 0, len(s.log.Records))
		for _, r := range s.log.Records {
			if s.includes(r) {
				visible = append(visible, r)
			}
		}
		selected := 0
		for i, r := range visible {
			if r.ID == s.picked {
				selected = i
				break
			}
		}
		s.records.Selected = &selected
		s.records.Key = func(i int) any { return visible[i].ID }
		s.records.Label = func(i int) string { return locales.ExperimentNumber(visible[i].ID) }
		ui.Row(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(uikit.RadiusControl).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c).Width(220).MinHeight(0).Padding(t.Space(uikit.SpaceXS)).Gap(t.Space(uikit.SpaceS)).Children(func() {
				a.debugDrafts(c)
				if len(visible) == 0 {
					uikit.Hint(c, locales.SendARequestToRecord())
				}
				uikit.DataList(c, &s.records, len(visible), func(row ui.ListRow) {
					r := visible[row.Index]
					label := fmt.Sprintf("#%d · %s", r.ID, r.At.Local().Format("01-02 15:04:05"))
					if r.ID == s.log.Baseline {
						label += " · " + locales.Baseline()
					}
					uikit.Tip(c, ui.Column(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Children(func() {
						uikit.Caption(c, label).FontFeatures("tnum").SingleLine()
						uikit.Meta(c, r.Summary).SingleLine()
					}), r.Draft.Method+" "+r.Draft.URL+"\n"+r.Summary).
						PopoverMenu(func(m *ui.Menu) { actionMenu(m, a.runCompareActions(runTarget(r))) })
				}).FillWidth()
				if selected >= 0 && selected < len(visible) && visible[selected].ID != s.picked {
					s.pick(visible[selected].ID)
				}
			})
			ui.Divider(c)
			ui.Column(c).Grow(1).MinWidth(0).Padding(t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).Children(func() {
				r := s.record(s.picked)
				if r == nil {
					message := locales.SendRequestForExperiment()
					if s.scope != nil {
						message = locales.ExperimentsNoRetainedSource()
					} else if s.draftKey != "" {
						message = locales.ExperimentsNoRetainedDraft()
					}
					if s.scope == nil {
						uikit.EmptyState(c, locales.NoRetainedRuns(), message, nil)
					} else {
						uikit.Body(c, message)
					}
					if s.scope != nil {
						f := s.scope.Exchange.flow()
						uikit.Tip(c, uikit.Body(c, locales.SourceNumber(f.id, f.method, f.url, f.status)).FontFeatures("tnum").SingleLine(), f.url)
						if s.reader == nil {
							s.reader = &editor.State{Buffer: editor.NewBuffer(f.body(true)), ReadOnly: true, Label: locales.SourceSnapshotLabel(), NoScrollPastEnd: true}
						}
						requestCodeEditor(c, s.reader)
					}
					return
				}
				uikit.Tip(c, uikit.Body(c, locales.RunDraft(r.ID, r.Draft.Method, r.Draft.URL)).FontFeatures("tnum").FontWeight(500).SingleLine(), r.Draft.URL)
				uikit.Meta(c, locales.ExperimentEnvironment(r.Summary, cmp.Or(r.Environment, locales.NoneLower()))).FontFeatures("tnum")
				if r.Outcome != "" {
					uikit.Meta(c, r.Outcome)
				}
				ui.Row(c).Gap(t.Space(uikit.SpaceXS)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, locales.OpenAsDraft()).Clicked() {
						a.openRecord(*r)
						s.open = false
					}
					if uikit.SecondaryButton(c, locales.TraceSource()).Disabled(r.Source == nil).Clicked() {
						a.openDebugHistory(r.Source)
					}
					origin := slices.IndexFunc(a.documents, func(d *requestDocument) bool { return r.DraftKey != "" && d.debugKey == r.DraftKey })
					if uikit.Tip(c, uikit.SecondaryButton(c, locales.OriginDraft()).Label(locales.OpenOriginatingDraft()).Disabled(origin < 0), locales.ReturnToOriginalTab(r.Draft.Name)).Clicked() {
						d := a.documents[origin]
						a.activateDocument(d)
						s.open = false
						d.urlInput.Focus()
					}
					if uikit.SecondaryButton(c, locales.SetBaseline()).Clicked() {
						s.log.Baseline, s.left = r.ID, r.ID
						s.persist()
					}
					if uikit.SecondaryButton(c, locales.UseAsLeft()).Clicked() {
						s.left = r.ID
						s.comparison.diff = nil
					}
					if uikit.SecondaryButton(c, locales.CompareWithLeft()).Disabled(s.record(cmp.Or(s.left, s.log.Baseline)) == nil || cmp.Or(s.left, s.log.Baseline) == r.ID).Clicked() {
						s.tab = 1
						s.comparison.diff = nil
					}
				})
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
					uikit.Input(c, &s.note).Label(locales.ExperimentNote()).Placeholder(locales.WhatDidThisAttemptTest()).Grow(1)
					if uikit.SecondaryButton(c, locales.SaveNote()).Clicked() {
						r.Note = s.note
						s.persist()
					}
				})
				a.debugWire(c, *r)
				uikit.TabStrip(c, func() {
					if uikit.Tabs(c, &s.tab, "", locales.RecordedResult(), locales.CompareRuns(), locales.CompareSource(), locales.SentRequest(), locales.Console(), locales.Variables()).Gap(0).Changed() {
						s.reader = nil
					}
				}).Height(uikit.HeightTabs).FillWidth()
				if s.tab == 5 {
					uikit.Meta(c, locales.ReferencedValuesAfterPreScript())
					if r.Variables == nil {
						uikit.Caption(c, locales.NoVariableSnapshotRetainedOlder())
					}
					a.variableRows(c, r.Variables, &s.revealVars, nil)
					return
				}
				if s.tab == 4 {
					runConsole(c, r.Console)
					return
				}
				if s.tab == 3 {
					uikit.Meta(c, locales.InitialClientRequestAfterVariables())
					if s.reader == nil {
						text := "Request was not prepared. The editable draft snapshot is retained."
						if f := r.Exchange; f != nil {
							text = f.Method + " " + f.URL + "\n" + request.HeaderBlock(f.RequestHeader) + "\n\n" + string(r.RequestBody)
							if f.RequestBodyError != "" {
								text += "\n\n" + f.RequestBodyError
							}
						}
						s.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.SentRequestSnapshot()}
					}
					requestCodeEditor(c, s.reader)
					return
				}
				if s.tab == 2 {
					s.compareSource(c, *r, &a.diffSplit)
					return
				}
				if s.tab == 1 {
					if uikit.Input(c, &s.ignore).Label(locales.IgnoreJsonKeys()).Placeholder(locales.IgnoreTopLevelResponseJson()).Changed() {
						s.comparison.diffPart = ""
					}
					uikit.Meta(c, locales.ComparisonOnlyOriginalSnapshotsRemain())
					if left := s.record(cmp.Or(s.left, s.log.Baseline)); left != nil && left.ID != r.ID {
						s.comparison.compare(c, left.compared(s.ignore), r.compared(s.ignore), [2]string{fmt.Sprintf("Run #%d", left.ID), fmt.Sprintf("Run #%d", r.ID)}, &a.diffSplit, nil)
					} else {
						uikit.EmptyState(c, locales.ChooseABaseline(), locales.ChooseAnotherRunAsLeft(), nil)
					}
				} else {
					if s.reader == nil {
						s.reader = &editor.State{Buffer: editor.NewBuffer(r.resultText()), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.ExperimentResult()}
					}
					requestCodeEditor(c, s.reader)
				}
			})
		})
	})
	if result := uikit.ConfirmDialog(c, &s.clearing, uikit.ConfirmOptions{
		Title:         locales.ClearHistory2(),
		Message:       locales.ThisRemovesTheRetainedRuns(),
		Severity:      uikit.SeverityDanger,
		Primary:       locales.ClearRecords(),
		Cancel:        locales.Cancel(),
		PrimaryDanger: true,
	}); result != uikit.ConfirmNone {
		s.clearing = false
		if result == uikit.ConfirmPrimary {
			s.log.clearSpill()
			s.log.Records = nil
			s.log.Baseline = 0
			s.picked = 0
			s.left = 0
			s.reader = nil
			if a.compare.selected.kind == "run" {
				a.compare.selected = compareTarget{}
			}
			s.persist()
		}
	}
}
