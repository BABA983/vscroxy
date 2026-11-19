package app

import (
	"slices"
	"sync"

	"github.com/egoist/mygo/ui"
)

// The self-opening dialogs that share the queue. Every other modal keeps its
// own place in view(), including the link confirmation that follows a click.
const (
	dialogBreakpoint     = "breakpoint"
	dialogDeviceAsk      = "device-ask"
	dialogMCPConsent     = "mcp-consent"
	dialogSaveConflict   = "save-conflict"
	dialogImportReport   = "import-report"
	dialogWorkspaceTrust = "workspace-trust"
)

// autoDialog points at an existing modal's flag, so the queue never copies
// dialog state and never keeps a View element or a Context from a frame.
type autoDialog struct {
	id   string
	open *bool
	draw func(*ui.Context)
}

type queuedDialog struct {
	id string
}

// dialogQueue lets one self-opening dialog draw at a time. Callers record an
// arrival where the state changes (some from a worker), while view() passes
// the current flags and modal functions on every frame.
type dialogQueue struct {
	mu      sync.Mutex
	waiting []queuedDialog
}

// arrive records when a dialog asked to be shown. Repeated arrivals while it
// is already waiting do not move it back in the order.
func (q *dialogQueue) arrive(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !slices.ContainsFunc(q.waiting, func(d queuedDialog) bool { return d.id == id }) {
		q.waiting = append(q.waiting, queuedDialog{id: id})
	}
}

// draw shows only the oldest dialog whose flag is still open. A dialog closed
// by an answer or Escape drops out here, and the next one draws in this same
// frame. view() draws the queue last: an event-driven prompt is never hidden
// beneath a modal the reader opened. Precedence is ordering alone, with no
// second list of user-modal flags to keep in sync.
func (q *dialogQueue) draw(c *ui.Context, candidates []autoDialog) {
	q.mu.Lock()
	flags := make(map[string]*bool, len(candidates))
	for _, d := range candidates {
		flags[d.id] = d.open
	}
	q.waiting = slices.DeleteFunc(q.waiting, func(d queuedDialog) bool {
		open, ok := flags[d.id]
		return !ok || open == nil || !*open
	})
	for _, d := range candidates {
		if d.open == nil || !*d.open || slices.ContainsFunc(q.waiting, func(w queuedDialog) bool { return w.id == d.id }) {
			continue
		}
		q.waiting = append(q.waiting, queuedDialog{id: d.id})
	}
	var first *autoDialog
	if len(q.waiting) > 0 {
		for i := range candidates {
			if candidates[i].id == q.waiting[0].id {
				first = &candidates[i]
				break
			}
		}
	}
	q.mu.Unlock()
	if first != nil {
		first.draw(c)
	}
}

// autoDialogs is the queue's only render call. The modal functions stay as
// they are; they still own their flags, focus, answers and dismissal.
func (a *app) autoDialogs(c *ui.Context) {
	// The gateway sets the pending request off the UI thread; bring the
	// flag onto this frame before the queue reads it.
	a.deviceAskMu.Lock()
	devicePending := a.deviceAskPending != nil
	a.deviceAskMu.Unlock()
	if devicePending {
		a.deviceAskOpen = true
		a.dialogs.arrive(dialogDeviceAsk)
	}
	// A worker starts the trust prompt; bring its arrival onto this frame
	// before the queue reads the flags.
	if a.hasTrustAsk() {
		a.trustAskOpen = true
		a.dialogs.arrive(dialogWorkspaceTrust)
	}
	var conflict *bool
	if a.conflict != nil {
		conflict = &a.conflict.open
	}
	a.dialogs.draw(c, []autoDialog{
		{id: dialogBreakpoint, open: &a.bpOpen, draw: func(c *ui.Context) { a.breakpointModal(c, c.Theme()) }},
		{id: dialogDeviceAsk, open: &a.deviceAskOpen, draw: func(c *ui.Context) { a.deviceAskModal(c, c.Theme()) }},
		{id: dialogMCPConsent, open: &a.mcpConsent.open, draw: func(c *ui.Context) { a.mcpConsentModal(c, c.Theme()) }},
		{id: dialogSaveConflict, open: conflict, draw: func(c *ui.Context) { a.saveConflictModal(c, c.Theme()) }},
		{id: dialogImportReport, open: &a.importReport.open, draw: func(c *ui.Context) { a.importReportModal(c, c.Theme()) }},
		{id: dialogWorkspaceTrust, open: &a.trustAskOpen, draw: func(c *ui.Context) { a.trustPromptModal(c, c.Theme()) }},
	})
}
