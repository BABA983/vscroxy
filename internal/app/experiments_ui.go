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
	log, err := loadExperiments(path)
	if err != nil {
		s.problem, s.readOnly = "Experiments: "+err.Error(), true
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
		a.requestDocument = a.documents[min(max(log.Active, 0), len(a.documents)-1)]
		a.requestChoice = a.savedKey
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
					err = saveExperiments(s.path, state)
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
			s.problem = "Experiment checkpoint failed: " + err.Error()
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
	a.sendOutcome = cmp.Or(r.Outcome, "Retained attempt")
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
	if reply := r.Exchange; reply != nil {
		f.method, f.url, f.path, f.status, f.millis = reply.Method, reply.URL, reply.URL, reply.Status, reply.Millis
		f.reqBody, f.reqSize, f.reqError, f.reqHeaders = string(r.RequestBody), reply.RequestBodySize, reply.RequestBodyError, request.HeaderBlock(reply.RequestHeader)
		f.resBody, f.resSize, f.resError, f.resHeaders = string(r.ResponseBody), reply.ResponseBodySize, reply.ResponseBodyError, request.HeaderBlock(reply.ResponseHeader)
	}
	if r.Exchange == nil || r.Exchange.Status == 0 {
		f.resError = "No HTTP response: " + r.Summary
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
			f.resError += "\nIgnore keys not applied: requires a complete JSON object"
		}
	}
	return f
}

func (a *app) experimentsModal(c *ui.Context, t *ui.Theme) {
	s := &a.experiments
	if !s.open {
		return
	}
	uikit.Modal(c, &s.open, func() { a.experimentsView(c, t, false) })
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
	panel := ui.Column(c).Gap(t.Space(2))
	if inline {
		panel.Fill().Padding(t.Space(3))
	} else {
		panel.Width(min(1200, width-80)).Height(height - 100)
	}
	panel.Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			title := "History"
			if s.scope != nil {
				title += " · Source"
			} else if s.draftKey != "" {
				title += " · Draft"
			}
			uikit.DialogTitle(c, title).Grow(1)
			if uikit.SecondaryButton(c, locales.ClearHistory()).Disabled(s.readOnly || len(s.log.Records) == 0).Clicked() {
				s.clearing = true
			}
			if uikit.SecondaryButton(c, locales.Close()).Clicked() {
				s.open = false
				if inline {
					a.page = pageRequest
				}
			}
		})
		uikit.Meta(c, locales.PrivateLocalSnapshotsMayContain())
		if s.problem != "" {
			ui.Text(c, s.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
		}
		ui.Row(c).Gap(t.Space(2)).Children(func() {
			historyFilters(c, &s.status, &s.sourceFilter)
		})
		ui.Row(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(uikit.RadiusControl).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c).Width(220).Padding(t.Space(1)).Children(func() {
				a.debugDrafts(c)
				if len(s.log.Records) == 0 {
					uikit.Hint(c, locales.SendARequestToRecord()).Padding(t.Space(2))
				}
				for _, r := range s.log.Records {
					if !s.includes(r) {
						continue
					}
					label := fmt.Sprintf("#%d · %s", r.ID, r.At.Local().Format("01-02 15:04:05"))
					if r.ID == s.log.Baseline {
						label += " · Baseline"
					}
					button := uikit.ToolbarButton(c, locales.ExperimentNumber(r.ID), r.Draft.Method+" "+r.Draft.URL+"\n"+r.Summary, r.ID == s.picked).
						FillWidth().FontFeatures("tnum").Children(func() {
						ui.Text(c, label).FontSize(uikit.TextMeta).FontFeatures("tnum").SingleLine()
					})
					if button.Clicked() {
						s.pick(r.ID)
					}
				}
			})
			ui.Divider(c)
			ui.Column(c).Grow(1).MinWidth(0).Padding(t.Space(2)).Gap(t.Space(2)).Children(func() {
				r := s.record(s.picked)
				if r == nil {
					message := "Send a request to record an experiment."
					if s.scope != nil {
						message = "No retained experiments for this source. Open a linked draft to continue; opening never sends."
					} else if s.draftKey != "" {
						message = "No retained sends for this draft. Opening history never sends."
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
				uikit.Tip(c, ui.Text(c, locales.RunDraft(r.ID, r.Draft.Method, r.Draft.URL)).FontSize(uikit.TextBody).FontFeatures("tnum").FontWeight(500).SingleLine(), r.Draft.URL)
				uikit.Meta(c, r.Summary+" · Environment: "+cmp.Or(r.Environment, "none")).FontFeatures("tnum")
				if r.Outcome != "" {
					uikit.Meta(c, r.Outcome)
				}
				ui.Row(c).Gap(t.Space(1)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, locales.OpenAsDraft()).Clicked() {
						a.openRecord(*r)
						s.open = false
					}
					if uikit.SecondaryButton(c, locales.TraceSource()).Disabled(r.Source == nil).Clicked() {
						a.openDebugHistory(r.Source)
					}
					origin := slices.IndexFunc(a.documents, func(d *requestDocument) bool { return r.DraftKey != "" && d.debugKey == r.DraftKey })
					if uikit.Tip(c, uikit.SecondaryButton(c, locales.OriginDraft()).Label(locales.OpenOriginatingDraft()).Disabled(origin < 0), "Return to the original editable tab without overwriting its current edits: "+r.Draft.Name).Clicked() {
						d := a.documents[origin]
						a.requestDocument, a.requestChoice, a.page = d, d.savedKey, pageRequest
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
				ui.Row(c).Gap(t.Space(2)).Children(func() {
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
						ui.Text(c, locales.NoVariableSnapshotRetainedOlder()).FontSize(uikit.TextMeta)
					}
					variableRows(c, r.Variables, &s.revealVars)
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
						s.comparison.compare(c, left.compared(s.ignore), r.compared(s.ignore), [2]string{fmt.Sprintf("Run #%d", left.ID), fmt.Sprintf("Run #%d", r.ID)}, &a.diffSplit)
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
	uikit.Modal(c, &s.clearing, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.ClearHistory2())
			uikit.Hint(c, locales.ThisRemovesTheRetainedRuns())
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					s.clearing = false
				}
				if uikit.PrimaryButton(c, locales.ClearRecords()).Clicked() {
					s.log.Records = nil
					s.log.Baseline = 0
					s.picked = 0
					s.left = 0
					s.reader = nil
					s.clearing = false
					s.persist()
				}
			})
		})
	})
}
