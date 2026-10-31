package app

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

	"github.com/baba983/vscroxy/internal/request"
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
		if draft.Value.ID == "" && draft.SavedKey != "" {
			for _, asset := range a.requests {
				if draft.SavedKey == "saved:"+asset.Collection+"\x00"+asset.Name {
					draft.Value.ID, draft.Baseline.ID, draft.SavedKey = asset.ID, asset.ID, asset.key()
					break
				}
			}
		}
		a.addDocument(draft.Value, draft.SavedKey)
		a.baseline = draft.Baseline
		if draft.Key != "" {
			a.debugKey = draft.Key
		}
		a.setDebugSource(draft.Source)
		// Older checkpoints kept provenance beside the request, not inside it.
		// Restoring that metadata must not make an otherwise unchanged draft dirty.
		a.baseline.Source = draft.Source
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
	s.revealVars = false
	s.sourceComparison, s.wire = captureAnalysis{}, 0
	if r := s.record(id); r != nil {
		s.note = r.Note
	}
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
	appModal(c, &s.open, func() { a.experimentsView(c, t, false) })
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
		ui.Row(c).Height(heightTabs).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			title := "Run records"
			if s.scope != nil {
				title += " · Source"
			} else if s.draftKey != "" {
				title += " · Draft"
			}
			dialogTitle(c, title).Grow(1)
			if secondaryButton(c, "Clear experiments…").Disabled(s.readOnly || len(s.log.Records) == 0).Clicked() {
				s.clearing = true
			}
			if secondaryButton(c, "Close").Clicked() {
				s.open = false
				if inline {
					a.requestsSection, a.requestSide = 0, 0
				}
			}
		})
		ui.Text(c, "Private local snapshots may contain credentials. Up to 100 runs / 32 MiB; baseline retained. Payloads keep up to 256 KiB per side. Opening or comparing never sends.").FontSize(textMeta).TextColor(t.TextMuted)
		if s.problem != "" {
			ui.Text(c, s.problem).FontSize(textBody).TextColor(t.Danger)
		}
		ui.Row(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(radiusControl).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c).Width(220).Padding(t.Space(1)).Children(func() {
				a.debugDrafts(c)
				if len(s.log.Records) == 0 {
					ui.Text(c, "Send a request to record an experiment.").Padding(t.Space(2)).FontSize(textBody).TextColor(t.TextMuted)
				}
				for _, r := range s.log.Records {
					if !s.includes(r) {
						continue
					}
					label := fmt.Sprintf("#%d · %s", r.ID, r.At.Local().Format("01-02 15:04:05"))
					if r.ID == s.log.Baseline {
						label += " · Baseline"
					}
					button := toolbarButton(c, fmt.Sprintf("Experiment #%d", r.ID), r.Draft.Method+" "+r.Draft.URL+"\n"+r.Summary, r.ID == s.picked).
						FillWidth().FontFeatures("tnum").Children(func() {
						ui.Text(c, label).FontSize(textMeta).FontFeatures("tnum").SingleLine()
					})
					if button.Clicked() {
						s.pick(r.ID)
					}
				}
				if inline && len(a.history) > 0 {
					ui.Divider(c)
					ui.Text(c, "Recent requests").Bold()
					ui.Text(c, "Unsent copies · deduplicated by method + URL").FontSize(textMeta).TextColor(t.TextMuted)
					ui.TextInput(c, &a.requestQuery).Label("Search history").Placeholder("Search history…").FontSize(textMeta)
					query := strings.ToLower(strings.TrimSpace(a.requestQuery))
					for i, entry := range a.history {
						if query != "" && !strings.Contains(strings.ToLower(entry.Method+" "+entry.URL), query) {
							continue
						}
						ui.Button(c, entry.label()).FillWidth().FontSize(textMeta).OnClick(func() { a.openHistory(i) })
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
						emptyState(c, "No retained runs", message, nil)
					} else {
						ui.Text(c, message).FontSize(textBody)
					}
					if s.scope != nil {
						f := investigationFlow(s.scope.Exchange)
						ui.Text(c, fmt.Sprintf("Source #%d · %s %s · %d", f.id, f.method, f.url, f.status)).FontSize(textBody).FontFeatures("tnum").SingleLine().Tooltip(f.url)
						if s.reader == nil {
							s.reader = &editor.State{Buffer: editor.NewBuffer(f.body(true)), ReadOnly: true, Label: "Source snapshot", NoScrollPastEnd: true}
						}
						requestCodeEditor(c, s.reader)
					}
					return
				}
				ui.Text(c, fmt.Sprintf("Experiment #%d · %s %s", r.ID, r.Draft.Method, r.Draft.URL)).FontSize(textBody).FontFeatures("tnum").Bold().SingleLine().Tooltip(r.Draft.URL)
				ui.Text(c, r.Summary+" · Environment: "+cmp.Or(r.Environment, "none")).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted)
				if r.Outcome != "" {
					ui.Text(c, r.Outcome).FontSize(textMeta).TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(t.Space(1)).Wrap().Children(func() {
					if secondaryButton(c, "Open as draft").Clicked() {
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
						s.open = false
					}
					if secondaryButton(c, "Trace source").Disabled(r.Source == nil).Clicked() {
						a.openDebugHistory(r.Source)
					}
					origin := slices.IndexFunc(a.documents, func(d *requestDocument) bool { return r.DraftKey != "" && d.debugKey == r.DraftKey })
					if secondaryButton(c, "Origin draft").Label("Open originating draft").Disabled(origin < 0).Tooltip("Return to the original editable tab without overwriting its current edits: " + r.Draft.Name).Clicked() {
						d := a.documents[origin]
						a.requestDocument, a.requestChoice, a.workspace = d, d.savedKey, 1
						a.requestsSection, a.requestSide = 0, 0
						d.revealTab, s.open = true, false
						d.urlInput.Focus()
					}
					if secondaryButton(c, "Set baseline").Clicked() {
						s.log.Baseline, s.left = r.ID, r.ID
						s.persist()
					}
					if secondaryButton(c, "Use as left").Clicked() {
						s.left = r.ID
						s.comparison.diff = nil
					}
					if secondaryButton(c, "Compare with left").Disabled(s.record(cmp.Or(s.left, s.log.Baseline)) == nil || cmp.Or(s.left, s.log.Baseline) == r.ID).Clicked() {
						s.tab = 1
						s.comparison.diff = nil
					}
				})
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					ui.TextInput(c, &s.note).Label("Experiment note").Placeholder("What did this attempt test?").Grow(1).Height(heightControl).FontSize(textBody).Radius(radiusControl)
					if secondaryButton(c, "Save note").Clicked() {
						r.Note = s.note
						s.persist()
					}
				})
				a.debugWire(c, *r)
				ui.ScrollHorizontal(c).Height(heightTabs).FillWidth().Children(func() {
					if flatTabs(c, &s.tab, "", "Recorded result", "Compare experiments", "Compare source", "Sent request", "Console", "Variables").Gap(0).Changed() {
						s.reader = nil
					}
				})
				if s.tab == 5 {
					ui.Text(c, "Referenced values after pre-script, before expansion · run input = environment snapshot or preceding Runner step · post-script changes do not rewrite this snapshot.").FontSize(textMeta).TextColor(t.TextMuted)
					if r.Variables == nil {
						ui.Text(c, "No variable snapshot retained (older records may not include it).").FontSize(textMeta)
					}
					variableRows(c, r.Variables, &s.revealVars)
					return
				}
				if s.tab == 4 {
					runConsole(c, r.Console)
					return
				}
				if s.tab == 3 {
					ui.Text(c, "Initial client request after variables and pre-request script; redirect hops and proxy interventions are separate wire exchanges.").FontSize(textMeta).TextColor(t.TextMuted)
					if s.reader == nil {
						text := "Request was not prepared. The editable draft snapshot is retained."
						if f := r.Exchange; f != nil {
							text = f.Method + " " + f.URL + "\n" + request.HeaderBlock(f.RequestHeader) + "\n\n" + string(r.RequestBody)
							if f.RequestBodyError != "" {
								text += "\n\n" + f.RequestBodyError
							}
						}
						s.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Sent request snapshot"}
					}
					requestCodeEditor(c, s.reader)
					return
				}
				if s.tab == 2 {
					s.compareSource(c, *r)
					return
				}
				if s.tab == 1 {
					if ui.TextInput(c, &s.ignore).Label("Ignore JSON keys").Placeholder("Ignore top-level response JSON keys: timestamp, traceId").Height(heightControl).FontSize(textBody).Radius(radiusControl).Changed() {
						s.comparison.diffPart = ""
					}
					ui.Text(c, "Comparison only; original snapshots remain unchanged. Ignores top-level response JSON keys, not headers or nested fields.").FontSize(textMeta).TextColor(t.TextMuted)
					if left := s.record(cmp.Or(s.left, s.log.Baseline)); left != nil && left.ID != r.ID {
						s.comparison.compare(c, left.compared(s.ignore), r.compared(s.ignore), [2]string{fmt.Sprintf("Run #%d", left.ID), fmt.Sprintf("Run #%d", r.ID)})
					} else {
						emptyState(c, "Choose a baseline", "Choose another experiment as left, or set a baseline first.", nil)
					}
				} else {
					if s.reader == nil {
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
						s.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Experiment result"}
					}
					requestCodeEditor(c, s.reader)
				}
			})
		})
	})
	appModal(c, &s.clearing, func() {
		dialogColumn(c, dialogSmall).Children(func() {
			dialogTitle(c, "Clear all experiment records?")
			ui.Text(c, "This removes the retained runs and baseline, not open drafts, captured traffic or saved requests.").FontSize(textBody).TextColor(t.TextMuted)
			dialogFooter(c).Children(func() {
				if secondaryButton(c, "Cancel").Clicked() {
					s.clearing = false
				}
				if primaryButton(c, "Clear records").Clicked() {
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
