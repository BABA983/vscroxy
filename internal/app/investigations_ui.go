package app

import (
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// All workbench state belongs to the UI thread. Workers receive immutable
// snapshots and publish one bounded result. There is no background persistence.
type investigationWorkbench struct {
	open, busy, closed, comparing bool
	sources                       []EvidenceSource
	kept                          int
	query, problem, feedback      string
	matches                       [][]int
	panes                         [2]int
	lists                         [2]ui.ListState
	picks                         []investigationPick
	comparison                    captureAnalysis
	title, notes, preview         string
	reviewing, reviewed           bool
	origin                        string
	reviewCode                    *editor.State
	reviewVersion                 uint64
	result                        chan investigationResult
}

type investigationResult struct {
	sources  []EvidenceSource
	kept     int
	preview  string
	feedback string
	err      error
}

// show only reveals the native reader. It deliberately does not launch an OS
// dialog; file access is a separate explicit Open files action inside it.
func (s *investigationWorkbench) show() {
	if !s.closed {
		s.open = true
		s.origin = ""
	}
}

// close abandons any pending result without waiting for a native dialog on the
// main thread. A finite worker can finish into its buffered channel safely.
func (s *investigationWorkbench) close() {
	s.closed, s.open = true, false
	s.origin = ""
	s.sources, s.matches, s.picks = nil, nil, nil
	s.preview, s.notes, s.title = "", "", ""
	s.reviewCode = nil
	s.comparison = captureAnalysis{}
	s.result = nil
}

func (s *investigationWorkbench) work(a *app, fn func() investigationResult) {
	if s.busy || s.closed {
		return
	}
	s.busy, s.problem, s.feedback = true, "", ""
	ch := make(chan investigationResult, 1)
	s.result = ch
	notify := a.invalidate
	go func() {
		ch <- fn()
		notify()
	}()
}

func (s *investigationWorkbench) poll() {
	select {
	case r := <-s.result:
		s.busy, s.result = false, nil
		if r.err != nil {
			s.problem = r.err.Error()
			return
		}
		if len(r.sources) > 0 {
			s.sources = append(s.sources, r.sources...)
			s.kept += r.kept
			if len(s.sources) > 1 {
				s.panes[1] = len(s.sources) - 1
			}
			s.refreshSearch()
		}
		if r.preview != "" {
			s.preview, s.reviewing, s.reviewed = r.preview, true, false
			s.reviewCode = nil
		}
		s.feedback = r.feedback
	default:
	}
}

func (s *investigationWorkbench) refreshSearch() {
	s.matches = make([][]int, len(s.sources))
	for i, src := range s.sources {
		for j, f := range src.Session.Flows {
			if investigationMatches(f, src.Notes, s.query) {
				s.matches[i] = append(s.matches[i], j)
			}
		}
	}
	s.lists = [2]ui.ListState{}
}

// loadInvestigationFiles is also the callable non-dialog import path used by
// tests. The batch is all-or-nothing and respects the already retained budget.
func loadInvestigationFiles(paths []string, count, kept int) investigationResult {
	r := investigationResult{}
	if len(paths) > investigationSources-count {
		r.err = fmt.Errorf("keep at most %d sources; forget opened files first", investigationSources)
		return r
	}
	for _, path := range paths {
		b, err := ReadInvestigationFile(path)
		if err != nil {
			r.err = fmt.Errorf("%s: %w", filepath.Base(path), err)
			return r
		}
		if count+len(r.sources)+len(b.Sources) > investigationSources {
			r.err = fmt.Errorf("keep at most %d sources; forget opened files first", investigationSources)
			return r
		}
		for _, src := range b.Sources {
			if b.Notes != "" {
				src.Notes = strings.TrimSpace(b.Notes + "\n" + src.Notes)
			}
			if len(src.Notes) > 64<<10 {
				r.err = fmt.Errorf("combined source notes exceed 64 KiB")
				return r
			}
			// Count the canonical wire size, including decoded binary payloads'
			// base64 representation. File and object-count limits also apply.
			data, err := json.Marshal(src)
			if err != nil {
				r.err = err
				return r
			}
			r.kept += len(data)
			if kept+r.kept > investigationKeepLimit {
				r.err = fmt.Errorf("opened sources exceed 48 MiB; forget opened files first")
				return r
			}
			r.sources = append(r.sources, src)
		}
	}
	r.feedback = fmt.Sprintf("Opened %d historical sources; live traffic unchanged", len(r.sources))
	return r
}

func (s *investigationWorkbench) openFiles(a *app) {
	count, kept := len(s.sources), s.kept
	s.work(a, func() investigationResult {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title: "Open historical sessions or evidence", Multiple: true,
			Filters: []mygo.FileFilter{{Name: "Session / evidence JSON", Extensions: []string{"json"}}},
		})
		if err != nil {
			return investigationResult{err: err}
		}
		if len(paths) == 0 {
			return investigationResult{}
		}
		return loadInvestigationFiles(paths, count, kept)
	})
}

func (s *investigationWorkbench) pick(p investigationPick, selected bool) {
	if selected {
		if len(s.picks) >= evidenceSelectionLimit {
			s.problem = fmt.Sprintf("select at most %d exchanges", evidenceSelectionLimit)
			return
		}
		if !slices.Contains(s.picks, p) {
			s.picks = append(s.picks, p)
		}
	} else {
		s.picks = slices.DeleteFunc(s.picks, func(other investigationPick) bool { return other == p })
	}
	s.comparison = captureAnalysis{}
}

func (s *investigationWorkbench) label(p investigationPick) string {
	src := s.sources[p.source]
	return fmt.Sprintf("%d · %s · #%d", p.source+1, src.Label, src.Session.Flows[p.row].ID)
}

func (s *investigationWorkbench) pane(c *ui.Context, side int) {
	t := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("investigation-pane-%d", side))).Grow(1).Basis(0).FillHeight().Gap(t.Space(2)).Children(func() {
		if len(s.sources) == 0 {
			emptyState(c, "No opened sources", "Open saved session or evidence files to inspect them here.", nil)
			return
		}
		options := make([]string, len(s.sources))
		for i, src := range s.sources {
			options[i] = fmt.Sprintf("%d · %s", i+1, src.Label)
		}
		choice := options[s.panes[side]]
		if ui.Select(c, &choice, options).Label(fmt.Sprintf("Historical source %d", side+1)).FillWidth().
			Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).FontSize(textBody).Changed() {
			s.panes[side] = slices.Index(options, choice)
			s.lists[side] = ui.ListState{}
		}
		index := s.panes[side]
		src, rows := s.sources[index], s.matches[index]
		ui.Text(c, fmt.Sprintf("Historical / read-only · %d of %d exchanges", len(rows), len(src.Session.Flows))).FontSize(textMeta).TextColor(t.TextMuted)
		if src.Notes != "" {
			ui.Text(c, "Source notes: "+investigationSummary(src.Notes)).FontSize(textMeta).Tooltip(src.Notes)
		}
		ui.Divider(c).Background(t.Border)
		ui.List(c, &s.lists[side], len(rows), func(i int) {
			row := rows[i]
			f := src.Session.Flows[row]
			p := investigationPick{index, row}
			chosen := slices.Contains(s.picks, p)
			ui.Column(c).MinHeight(heightControl).Padding(t.Space(1), t.Space(2)).Gap(t.Space(1)).Children(func() {
				label := fmt.Sprintf("#%d · %s · %d · %s", f.ID, investigationSummary(f.Method), f.Status, investigationSummary(f.URL))
				if ui.Checkbox(c, &chosen, label).Label(fmt.Sprintf("Select source %d exchange %d", index+1, f.ID)).Tooltip(f.URL).FontSize(textBody).Changed() {
					s.pick(p, chosen)
				}
				if f.Note != "" {
					ui.Text(c, "Note: "+investigationSummary(f.Note)).FontSize(textMeta).TextColor(t.TextMuted)
				}
				if problem := strings.Join([]string{f.ConnectionError, f.ReqError, f.ResError}, " "); strings.TrimSpace(problem) != "" {
					ui.Text(c, "Error: "+investigationSummary(problem)).FontSize(textMeta).TextColor(t.Danger)
				}
			})
		}).Grow(1).Gap(t.Space(1)).Dividers(1, t.Border).Children(func() {
			if len(rows) == 0 {
				emptyState(c, "No matching exchanges", "Try another URL, status, note or error.", nil)
			}
		})
	})
}

func investigationSummary(s string) string {
	// Bound row labels, not retained data or search. Full values remain in the
	// editable evidence review and the comparison's URL tooltips.
	n := 0
	for i := range s {
		if n == 160 {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

func (s *investigationWorkbench) prepareReview(a *app) {
	sources, picks := slices.Clone(s.sources), slices.Clone(s.picks)
	title, notes := s.title, s.notes
	s.work(a, func() investigationResult {
		data, err := buildEvidence(sources, picks, title, notes)
		return investigationResult{preview: string(data), err: err}
	})
}

func (s *investigationWorkbench) exportReviewed(a *app) {
	if !s.reviewed {
		return
	}
	data := []byte(s.reviewCode.Buffer.String())
	s.work(a, func() investigationResult {
		if _, err := decodeEvidence(data); err != nil {
			return investigationResult{err: err}
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "Export reviewed raw evidence", DefaultPath: "vscroxy-evidence.json",
			Filters: []mygo.FileFilter{{Name: "Evidence JSON", Extensions: []string{"json"}}}})
		if err != nil || path == "" {
			return investigationResult{err: err}
		}
		if err := WriteEvidenceBundle(path, data, true); err != nil {
			return investigationResult{err: err}
		}
		return investigationResult{feedback: "Exported reviewed evidence · " + filepath.Base(path)}
	})
}

// showReview opens the evidence review on a bundle the investigation board
// built. Consent and the editable raw JSON stay in one place.
func (s *investigationWorkbench) showReview(data []byte) {
	if s.closed {
		return
	}
	s.open, s.reviewing, s.reviewed = true, true, false
	s.preview, s.reviewCode = string(data), nil
	s.problem, s.feedback = "", ""
	s.origin = "board"
}

// modal must be called every frame, including when hidden, to collect worker
// results. Parent integration needs only a field, show(), modal(c,a), and close().
func (s *investigationWorkbench) modal(c *ui.Context, a *app) {
	s.poll()
	if !s.open || s.closed {
		return
	}
	w, h := c.Size()
	t := c.Theme()
	appModal(c, &s.open, func() {
		ui.Column(c).Width(min(1120, w-80)).Height(min(800, h-100)).Gap(t.Space(2)).Children(func() {
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				dialogTitle(c, "Historical sessions & evidence").Grow(1)
				if secondaryButton(c, "Close").Clicked() {
					s.open = false
				}
			})
			ui.Divider(c).Background(t.Border)
			ui.Text(c, "Read-only local files. Never sends requests, runs scripts, changes rules or replaces live traffic.").FontSize(textMeta).TextColor(t.TextMuted)
			if s.busy {
				ui.Text(c, "Working…").FontSize(textMeta)
			}
			if s.problem != "" {
				ui.Text(c, s.problem).FontSize(textBody).TextColor(t.Danger)
			}
			if s.feedback != "" {
				ui.Text(c, s.feedback).FontSize(textMeta).TextColor(t.Success)
			}
			if s.reviewing {
				s.review(c, a)
				return
			}
			if s.comparing && len(s.picks) == 2 {
				if secondaryButton(c, "Back to sources").Clicked() {
					s.comparing = false
				}
				left, right := s.picks[0], s.picks[1]
				lf := investigationFlow(s.sources[left.source].Session.Flows[left.row])
				rf := investigationFlow(s.sources[right.source].Session.Flows[right.row])
				// Session IDs can collide; this private diff has stable local IDs.
				lf.id, rf.id = 1, 2
				s.comparison.compare(c, lf, rf, [2]string{s.label(left), s.label(right)})
				return
			}
			s.browse(c, a)
		})
	})
}

func (s *investigationWorkbench) browse(c *ui.Context, a *app) {
	t := c.Theme()
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		if secondaryButton(c, "Open files…").Disabled(s.busy).Clicked() {
			s.openFiles(a)
		}
		if secondaryButton(c, "Forget opened files").Disabled(s.busy || len(s.sources) == 0).Clicked() {
			s.sources, s.picks, s.matches = nil, nil, nil
			s.kept, s.panes = 0, [2]int{}
			s.comparison, s.problem, s.feedback = captureAnalysis{}, "", ""
		}
		ui.Text(c, fmt.Sprintf("%d / 8 sources · %.1f / 48 MiB", len(s.sources), float64(s.kept)/(1<<20))).FontSize(textMeta)
	})
	ui.Text(c, "Search scope: opened sources only · URL, numeric status, exchange/source notes and errors · literal, case-insensitive · not headers, bodies or messages").FontSize(textMeta).TextColor(t.TextMuted)
	if ui.TextInput(c, &s.query).Label("Search historical sources").Placeholder("Search URL / status / note / error").
		Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).FontSize(textBody).Changed() {
		s.refreshSearch()
	}
	ui.Row(c).Grow(1).Gap(t.Space(3)).Dividers(1, t.Border).AlignItems(ui.Stretch).Children(func() {
		s.pane(c, 0)
		s.pane(c, 1)
	})
	ui.Divider(c).Background(t.Border)
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		ui.Text(c, fmt.Sprintf("%d / 64 selected across all sources (including hidden results)", len(s.picks))).FontSize(textMeta).Grow(1)
		if secondaryButton(c, "Clear selection").Clicked() {
			s.picks, s.comparison = nil, captureAnalysis{}
		}
		if secondaryButton(c, "Compare selected two").Disabled(len(s.picks) != 2).Clicked() {
			s.comparing = true
		}
	})
	ui.TextInput(c, &s.title).Label("Evidence title").Placeholder("Evidence title (required for export)").
		Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).FontSize(textBody)
	ui.TextInput(c, &s.notes).Label("Evidence notes").Placeholder("Notes for this evidence bundle (optional)").
		Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).FontSize(textBody)
	if primaryButton(c, "Review selected evidence…").Disabled(s.busy || len(s.picks) == 0).Clicked() {
		s.prepareReview(a)
	}
}

func (s *investigationWorkbench) review(c *ui.Context, a *app) {
	ui.Text(c, "RAW EVIDENCE · NOT AUTOMATICALLY SANITIZED").FontSize(13).Bold().TextColor(c.Theme().Warning)
	ui.Text(c, "May include credentials, cookies, URL secrets, bodies, binary/base64 messages, trailers and private notes. Review ALL JSON before sharing. Edit title, notes, source labels and captured fields below; remove secrets yourself. No automatic redaction is performed.").FontSize(12)
	if s.reviewCode == nil {
		buf := editor.NewBuffer(s.preview)
		buf.SetLanguage(editor.LanguageNamed("json"))
		s.reviewCode = &editor.State{Buffer: buf, Label: "Editable raw evidence JSON", NoScrollPastEnd: true}
		s.reviewVersion = buf.Version()
		s.preview = ""
	}
	s.reviewCode.ReadOnly = s.busy
	editor.View(c, s.reviewCode).Grow(1).Disabled(s.busy)
	if version := s.reviewCode.Buffer.Version(); version != s.reviewVersion {
		s.reviewVersion = version
		s.reviewed, s.problem, s.feedback = false, "", ""
	}
	ui.Checkbox(c, &s.reviewed, "I reviewed the complete raw evidence and accept responsibility for sharing it").Label("Confirm full evidence review").Disabled(s.busy)
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		back := "Back without exporting"
		if s.origin == "board" {
			back = "Close without exporting"
		}
		if ui.Button(c, back).Disabled(s.busy).Clicked() {
			s.reviewing, s.reviewed, s.preview = false, false, ""
			s.reviewCode = nil
			if s.origin == "board" {
				s.open, s.origin = false, ""
			}
		}
		if ui.PrimaryButton(c, "Export reviewed evidence…").Disabled(s.busy || !s.reviewed).Clicked() {
			s.exportReviewed(a)
		}
	})
}
