package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// A dialog's panel carries the title the caller gives Modal, so assistive
// technology announces what opened, not just an unnamed dialog.
func TestModalNamesItsPanel(t *testing.T) {
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		Modal(c, &open, "Delete 3 requests?", func() {
			DialogColumn(c, DialogSmall).Children(func() {
				DialogTitle(c, "Delete 3 requests?")
				DialogFooter(c).Children(func() {
					SecondaryButton(c, "Cancel")
					DestructiveButton(c, "Delete")
				})
			})
		})
	}, 640, 400)
	tt.Frame()
	for _, a := range tt.AccessAll() {
		if a.Role == ui.RoleDialog {
			if a.Name != "Delete 3 requests?" {
				t.Fatalf("dialog name = %q, want its title", a.Name)
			}
			return
		}
	}
	t.Fatal("the modal did not expose a dialog")
}

// A confirmation starts on its primary action, so Enter chooses it; Escape
// cancels. A destructive primary starts on Cancel, as macOS does, and the
// dialog is an alert dialog named by its title and described by its message.
func TestConfirmDialogKeyboardAndDangerFocus(t *testing.T) {
	build := func(open *bool, got *ConfirmResult, o ConfirmOptions) func(*ui.Context) {
		return func(c *ui.Context) {
			c.SetTheme(Theme(false))
			if r := ConfirmDialog(c, open, o); r != ConfirmNone {
				*got, *open = r, false
			}
		}
	}

	t.Run("Enter chooses the primary", func(t *testing.T) {
		open, got := true, ConfirmNone
		o := ConfirmOptions{Title: "Save changes?", Message: "The draft is not on disk.", Primary: "Save", Secondary: "Don't Save", Cancel: "Cancel"}
		tt := ui.NewTester(build(&open, &got, o), 640, 400)
		tt.Frame()
		if !tt.Focused("Save") {
			t.Fatalf("the focus is not on Save; access=%v", tt.AccessAll())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if got != ConfirmPrimary || open {
			t.Fatalf("Enter chose %v (open=%v), want ConfirmPrimary and closed", got, open)
		}
	})

	t.Run("Escape cancels", func(t *testing.T) {
		open, got := true, ConfirmNone
		o := ConfirmOptions{Title: "Save changes?", Message: "The draft is not on disk.", Primary: "Save", Cancel: "Cancel"}
		tt := ui.NewTester(build(&open, &got, o), 640, 400)
		tt.Frame()
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if got != ConfirmCancel || open {
			t.Fatalf("Escape chose %v (open=%v), want ConfirmCancel and closed", got, open)
		}
	})

	t.Run("a destructive primary leaves the focus on Cancel", func(t *testing.T) {
		open, got := true, ConfirmNone
		const title, message = "Delete 3 requests?", "Their files are removed from the library."
		o := ConfirmOptions{Title: title, Message: message, Severity: SeverityDanger, Primary: "Delete", Cancel: "Cancel", PrimaryDanger: true}
		tt := ui.NewTester(build(&open, &got, o), 640, 400)
		tt.Frame()
		if !tt.Focused("Cancel") {
			t.Fatalf("the focus is not on Cancel; access=%v", tt.AccessAll())
		}
		alert, ok := tt.AccessByRole(ui.RoleAlertDialog, title)
		if !ok {
			t.Fatalf("no alert dialog named %q; access=%v", title, tt.AccessAll())
		}
		if alert.Description != message {
			t.Fatalf("alert description = %q, want %q", alert.Description, message)
		}
		// A destructive primary is one button, not a PrimaryButton left
		// behind under the DestructiveButton that replaced it.
		n := 0
		for _, a := range tt.AccessAll() {
			if a.Role == ui.RoleButton && a.Name == o.Primary {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%d buttons named %q, want one", n, o.Primary)
		}
	})
}

// Closing a confirmation gives the focus back to the control that opened
// it, so a keyboard user goes on where they were.
func TestConfirmDialogRestoresTheOpenersFocus(t *testing.T) {
	open := false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(Theme(false))
		if PrimaryButton(c, "Delete").Clicked() {
			open = true
		}
		if ConfirmDialog(c, &open, ConfirmOptions{Title: "Delete it?", Primary: "Delete", Cancel: "Cancel"}) == ConfirmCancel {
			open = false
		}
	}, 640, 400)
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	if !open {
		t.Fatal("the confirmation did not open")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if open {
		t.Fatal("Escape did not close the confirmation")
	}
	if !tt.Focused("Delete") {
		t.Fatalf("the focus did not go back to the opener; access=%v", tt.AccessAll())
	}
}
