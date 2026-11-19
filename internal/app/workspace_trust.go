package app

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A project folder is identified by its cleaned absolute path. Symlinks are
// deliberately not resolved: a linked path is a different name the user chose
// and does not silently inherit a decision made for the real path.
func trustPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}

func normalizeTrustPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if key := trustPath(p); key != "" {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// trustLists copies the decisions for Settings and for a settings write. It
// is also safe to call from a worker waiting on an answer.
func (a *app) trustLists() (trusted, untrusted []string) {
	a.trustMu.Lock()
	defer a.trustMu.Unlock()
	return slices.Clone(a.trustedProjects), slices.Clone(a.untrustedProjects)
}

func (a *app) trustDecision(dir string) (allow, decided bool) {
	key := trustPath(dir)
	a.trustMu.Lock()
	defer a.trustMu.Unlock()
	if slices.Contains(a.trustedProjects, key) {
		return true, true
	}
	if slices.Contains(a.untrustedProjects, key) {
		return false, true
	}
	return false, false
}

// activateTrustAskLocked puts the oldest unanswered folder in the prompt.
// trustMu must be held.
func (a *app) activateTrustAskLocked() {
	if len(a.trustQueue) == 0 {
		a.trustAskDir, a.trustAskOpen = "", false
		return
	}
	a.trustAskDir, a.trustAskOpen = a.trustQueue[0], true
	a.trustPrompts.Add(1)
}

// projectScriptPermission is the seam every request-script path uses. A send
// with an untrusted answer still goes out; only its scripts are skipped. The
// wait happens on the sending goroutine, never on the frame that draws the
// dialog.
func (a *app) projectScriptPermission(ctx context.Context, dir string) bool {
	key := trustPath(dir)
	if key == "" {
		return true
	}
	if allow, decided := a.trustDecision(key); decided {
		if allow {
			a.trustChecks.Add(1)
		}
		return allow
	}
	a.trustMu.Lock()
	if ch := a.trustPending[key]; ch != nil {
		a.trustMu.Unlock()
		return a.awaitTrust(ctx, key, ch)
	}
	if a.trustPending == nil {
		a.trustPending = map[string]chan struct{}{}
	}
	// A closed channel releases every send that arrived before the answer.
	answer := make(chan struct{})
	a.trustPending[key] = answer
	a.trustQueue = append(a.trustQueue, key)
	if a.trustAskDir == "" {
		a.activateTrustAskLocked()
	}
	a.trustMu.Unlock()
	a.invalidate()
	return a.awaitTrust(ctx, key, answer)
}

func (a *app) awaitTrust(ctx context.Context, dir string, answered <-chan struct{}) bool {
	select {
	case <-answered:
		allow, _ := a.trustDecision(dir)
		if allow {
			a.trustChecks.Add(1)
		}
		return allow
	case <-ctx.Done():
		// The send itself was cancelled. Its notice still says scripts were
		// skipped; the dialog remains open so the person can decide.
		return false
	}
}

// currentTrustAsk names the folder of the prompt still waiting for an answer.
func (a *app) currentTrustAsk() string {
	a.trustMu.Lock()
	defer a.trustMu.Unlock()
	return a.trustAskDir
}

// hasTrustAsk lets autoDialogs put the prompt in the queue off the UI thread.
func (a *app) hasTrustAsk() bool { return a.currentTrustAsk() != "" }

// answerTrust remembers one decision, releases every send waiting on it and
// writes the settings at once: the next run must see the answer even if the
// app is closed immediately.
func (a *app) answerTrust(allow bool) {
	a.trustMu.Lock()
	dir := a.trustAskDir
	answer := a.trustPending[dir]
	delete(a.trustPending, dir)
	a.trustQueue = slices.DeleteFunc(a.trustQueue, func(p string) bool { return p == dir })
	a.activateTrustAskLocked()
	if dir != "" {
		if allow {
			a.trustedProjects = append(a.trustedProjects, dir)
			a.untrustedProjects = slices.DeleteFunc(a.untrustedProjects, func(p string) bool { return p == dir })
		} else {
			a.untrustedProjects = append(a.untrustedProjects, dir)
			a.trustedProjects = slices.DeleteFunc(a.trustedProjects, func(p string) bool { return p == dir })
		}
		a.trustedProjects, a.untrustedProjects = normalizeTrustPaths(a.trustedProjects), normalizeTrustPaths(a.untrustedProjects)
	}
	a.trustMu.Unlock()
	if answer != nil {
		close(answer)
	}
	if dir != "" {
		a.bypassSettingsSave()
		if err := a.persistSettings(); err != nil {
			a.fail(locales.Settings(), err)
		}
		if allow {
			a.done(locales.WorkspaceTrustTrusted())
		} else {
			a.warn(locales.WorkspaceTrustRestricted())
		}
	}
	a.invalidate()
}

// revokeTrust drops both answers for dir, so the next script run asks again.
func (a *app) revokeTrust(dir string) {
	key := trustPath(dir)
	if key == "" {
		return
	}
	a.trustMu.Lock()
	a.trustedProjects = slices.DeleteFunc(a.trustedProjects, func(p string) bool { return p == key })
	a.untrustedProjects = slices.DeleteFunc(a.untrustedProjects, func(p string) bool { return p == key })
	a.trustMu.Unlock()
	a.bypassSettingsSave()
	if err := a.persistSettings(); err != nil {
		a.fail(locales.Settings(), err)
	}
	a.done(locales.WorkspaceTrustRevoked())
	a.invalidate()
}

// trustPromptModal is queued by autoDialogs. Dismissing or Escape answers
// "Don't trust", so a script is never dropped without a visible decision.
func (a *app) trustPromptModal(c *ui.Context, t *ui.Theme) {
	dir := a.currentTrustAsk()
	if dir == "" {
		a.trustAskOpen = false
		return
	}
	uikit.Modal(c, &a.trustAskOpen, locales.WorkspaceTrustPrompt(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.WorkspaceTrustPrompt())
			uikit.Callout(c, uikit.CalloutWarning, locales.WorkspaceTrustPromptDetail(dir), nil)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.DontTrust()).Clicked() {
					a.answerTrust(false)
				}
				ui.Spacer(c)
				if uikit.PrimaryButton(c, locales.Trust()).Clicked() {
					a.answerTrust(true)
				}
			})
		})
	})
	if !a.trustAskOpen {
		a.answerTrust(false)
	}
}

// openWorkspaceTrust is the workbench.trust.manage command's action.
func (a *app) openWorkspaceTrust() {
	a.trustManageOpen = true
	a.invalidate()
}
