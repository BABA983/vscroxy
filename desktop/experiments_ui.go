package main

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

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
	wire                             int
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
	var drafts []experimentDraft
	for _, d := range a.documents {
		baseline := d.baseline
		baseline.Form = slices.Clone(baseline.Form)
		drafts = append(drafts, experimentDraft{Key: d.debugKey, Source: d.origin, Value: d.value(), Baseline: baseline, SavedKey: d.savedKey})
	}
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
	s.sourceComparison, s.wire = captureAnalysis{}, 0
	if r := s.record(id); r != nil {
		s.note = r.Note
	}
}

func (r experimentRecord) compared(ignore string) flow {
	f := flow{id: r.ID, method: r.Draft.Method, url: r.Draft.URL, path: r.Draft.URL}
	if reply := r.Exchange; reply != nil {
		f.method, f.url, f.path, f.status, f.millis = reply.Method, reply.URL, reply.URL, reply.Status, reply.Millis
		f.reqBody, f.reqSize, f.reqError, f.reqHeaders = string(r.RequestBody), reply.RequestBodySize, reply.RequestBodyError, headerBlock(reply.RequestHeader)
		f.resBody, f.resSize, f.resError, f.resHeaders = string(r.ResponseBody), reply.ResponseBodySize, reply.ResponseBodyError, headerBlock(reply.ResponseHeader)
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
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(min(1200, width-80)).Height(height - 100).Gap(10).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "Experiments").FontSize(18).Bold().Grow(1)
				if ui.Button(c, "Clear experiments…").Disabled(s.readOnly || len(s.log.Records) == 0).Clicked() {
					s.clearing = true
				}
				if ui.Button(c, "Close").Clicked() {
					s.open = false
				}
			})
			ui.Text(c, "Private local snapshots may contain credentials. Up to 100 runs / 32 MiB; baseline retained. Payloads keep up to 256 KiB per side. Opening or comparing never sends.").FontSize(11).TextColor(t.TextMuted)
			if s.problem != "" {
				ui.Text(c, s.problem).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Grow(1).Gap(12).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Width(220).Gap(4).Children(func() {
					a.debugDrafts(c)
					if len(s.log.Records) == 0 {
						ui.Text(c, "Send a request to record an experiment.").FontSize(12).TextColor(t.TextMuted)
					}
					for _, r := range s.log.Records {
						if !s.includes(r) {
							continue
						}
						label := fmt.Sprintf("#%d · %s", r.ID, r.At.Local().Format("01-02 15:04:05"))
						if r.ID == s.log.Baseline {
							label += " · Baseline"
						}
						button := ui.Button(c, label).Label(fmt.Sprintf("Experiment #%d", r.ID)).FillWidth().Height(50).Tooltip(r.Draft.Method + " " + r.Draft.URL + "\n" + r.Summary)
						if r.ID == s.picked {
							button.Background(t.Accent).TextColor(t.AccentText)
						}
						if button.Clicked() {
							s.pick(r.ID)
						}
					}
				})
				ui.Column(c).Grow(1).Gap(8).Children(func() {
					r := s.record(s.picked)
					if r == nil {
						message := "Send a request to record an experiment."
						if s.scope != nil {
							message = "No retained experiments for this source. Open a linked draft to continue; opening never sends."
						}
						ui.Text(c, message).FontSize(12)
						if s.scope != nil {
							f := investigationFlow(s.scope.Exchange)
							ui.Text(c, fmt.Sprintf("Source #%d · %s %s · %d", f.id, f.method, f.url, f.status)).FontSize(12).SingleLine().Tooltip(f.url)
							if s.reader == nil {
								s.reader = &editor.State{Buffer: editor.NewBuffer(f.body(true)), ReadOnly: true, Label: "Source snapshot", NoScrollPastEnd: true}
							}
							editor.View(c, s.reader).Grow(1)
						}
						return
					}
					ui.Text(c, fmt.Sprintf("Experiment #%d · %s %s", r.ID, r.Draft.Method, r.Draft.URL)).FontSize(12).Bold().SingleLine().Tooltip(r.Draft.URL)
					ui.Text(c, r.Summary+" · Environment: "+cmp.Or(r.Environment, "none")).FontSize(11).TextColor(t.TextMuted)
					ui.Row(c).Gap(6).Wrap().Children(func() {
						if ui.Button(c, "Open as draft").FontSize(11).Clicked() {
							a.addDocument(r.Draft, "")
							a.setDebugSource(r.Source)
							a.experimentID, a.response, a.composeTests, a.composeResult = r.ID, r.response(), slices.Clone(r.Tests), r.Summary
							s.open = false
						}
						if ui.Button(c, "Trace source").FontSize(11).Disabled(r.Source == nil).Clicked() {
							a.openDebugHistory(r.Source)
						}
						origin := slices.IndexFunc(a.documents, func(d *requestDocument) bool { return r.DraftKey != "" && d.debugKey == r.DraftKey })
						if ui.Button(c, "Origin draft").Label("Open originating draft").FontSize(11).Disabled(origin < 0).Tooltip("Return to the original editable tab without overwriting its current edits: " + r.Draft.Name).Clicked() {
							d := a.documents[origin]
							a.requestDocument, a.requestChoice, a.workspace = d, d.savedKey, 1
							d.revealTab, s.open = true, false
							d.urlInput.Focus()
						}
						if ui.Button(c, "Set baseline").FontSize(11).Clicked() {
							s.log.Baseline, s.left = r.ID, r.ID
							s.persist()
						}
						if ui.Button(c, "Use as left").FontSize(11).Clicked() {
							s.left = r.ID
							s.comparison.diff = nil
						}
						if ui.Button(c, "Compare with left").FontSize(11).Disabled(s.record(cmp.Or(s.left, s.log.Baseline)) == nil || cmp.Or(s.left, s.log.Baseline) == r.ID).Clicked() {
							s.tab = 1
							s.comparison.diff = nil
						}
					})
					ui.Row(c).Gap(6).Children(func() {
						ui.TextInput(c, &s.note).Label("Experiment note").Placeholder("What did this attempt test?").Grow(1)
						if ui.Button(c, "Save note").FontSize(11).Clicked() {
							r.Note = s.note
							s.persist()
						}
					})
					a.debugWire(c, *r)
					ui.Tabs(c, &s.tab, "Recorded result", "Compare experiments", "Compare source")
					if s.tab == 2 {
						s.compareSource(c, *r)
						return
					}
					if s.tab == 1 {
						if ui.TextInput(c, &s.ignore).Label("Ignore JSON keys").Placeholder("Ignore top-level response JSON keys: timestamp, traceId").Changed() {
							s.comparison.diffPart = ""
						}
						ui.Text(c, "Comparison only; original snapshots remain unchanged. Ignores top-level response JSON keys, not headers or nested fields.").FontSize(10).TextColor(t.TextMuted)
						if left := s.record(cmp.Or(s.left, s.log.Baseline)); left != nil && left.ID != r.ID {
							s.comparison.compare(c, left.compared(s.ignore), r.compared(s.ignore), [2]string{fmt.Sprintf("Run #%d", left.ID), fmt.Sprintf("Run #%d", r.ID)})
						} else {
							ui.Text(c, "Choose another experiment as left, or set a baseline first.").TextColor(t.TextMuted)
						}
					} else {
						if s.reader == nil {
							f := r.compared("")
							text := f.resHeaders + "\n\n" + f.body(true)
							for _, check := range r.Tests {
								text += fmt.Sprintf("\nCheck %t: %s", check.Pass, check.Name)
							}
							if len(r.Replies) > 0 {
								text += "\nWebSocket replies:\n" + strings.Join(r.Replies, "\n")
							}
							s.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Experiment result"}
						}
						editor.View(c, s.reader).Grow(1)
					}
				})
			})
		})
	})
	ui.Modal(c, &s.clearing, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "Clear all experiment records?").Bold()
			ui.Text(c, "This removes the retained runs and baseline, not open drafts, captured traffic or saved requests.")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					s.clearing = false
				}
				if ui.PrimaryButton(c, "Clear records").Clicked() {
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
