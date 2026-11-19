package app

import (
	"os"
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func queuedModal(c *ui.Context, open *bool, title string) {
	uikit.Modal(c, open, title, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, title)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Done()).Clicked() {
					*open = false
				}
			})
		})
	})
}

func TestAutoDialogsShowOneAtATimeInArrivalOrder(t *testing.T) {
	a := workspaceApp()
	first, second := false, false
	tt := ui.NewTester(func(c *ui.Context) {
		a.dialogs.draw(c, []autoDialog{
			{id: "first", open: &first, draw: func(c *ui.Context) { queuedModal(c, &first, "First queued dialog") }},
			{id: "second", open: &second, draw: func(c *ui.Context) { queuedModal(c, &second, "Second queued dialog") }},
		})
	}, 1000, 700)
	first = true
	a.dialogs.arrive("first")
	tt.Frame()
	if !tt.HasText("First queued dialog") || tt.HasText("Second queued dialog") {
		t.Fatalf("first frame texts = %v", tt.Texts())
	}
	second = true
	a.dialogs.arrive("second")
	tt.Frame()
	if !tt.HasText("First queued dialog") || tt.HasText("Second queued dialog") {
		t.Fatalf("second arrival texts = %v", tt.Texts())
	}
	// Answering the first dialog promotes the second without an idle frame.
	first = false
	tt.Frame()
	if tt.HasText("First queued dialog") || !tt.HasText("Second queued dialog") {
		t.Fatalf("after answer texts = %v", tt.Texts())
	}
}

func breakpointOverSocketApp() *app {
	a := workspaceApp()
	a.socket.open = true
	a.bp = &capture.Breakpoint{
		ID: 1, Phase: capture.PhaseMessage, URL: "wss://api.example.com/socket",
		Direction: "out", MessageType: "text", Done: make(chan struct{}),
	}
	a.bpOpen, a.bpBody = true, "original"
	return a
}

// A breakpoint holds live traffic, so it must draw above a session dialog
// the reader opened earlier. view() draws the queue last for this reason.
func TestQueuedBreakpointDrawsAboveUserModal(t *testing.T) {
	a := breakpointOverSocketApp()
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	texts := tt.Texts()
	session := slices.Index(texts, locales.WebsocketSessionState(locales.NotConnected()))
	pause := slices.Index(texts, locales.BreakpointWebSocketPaused())
	if session < 0 || pause < 0 {
		t.Fatalf("both dialogs should be built: %v", texts)
	}
	if pause < session {
		t.Fatalf("the breakpoint drew below the session dialog: %v", texts)
	}
	if _, ok := tt.Find("Message payload"); !ok {
		t.Fatalf("the queued breakpoint is not actionable: %v", texts)
	}
}

// TestTitleDialogQueueScreenshots captures the breakpoint over the session
// dialog in both themes. Set VSCROXY_TITLE_DIALOG_SCREENSHOTS to a directory.
func TestTitleDialogQueueScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_TITLE_DIALOG_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_TITLE_DIALOG_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := breakpointOverSocketApp()
			tt := ui.NewTester(a.view, 1180, 760)
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			texts := tt.Texts()
			session := slices.Index(texts, locales.WebsocketSessionState(locales.NotConnected()))
			pause := slices.Index(texts, locales.BreakpointWebSocketPaused())
			if session < 0 || pause < session {
				t.Fatalf("breakpoint did not draw over the session: %v", texts)
			}
			writeFeatureScreenshot(t, dir, "queue-above-user-"+appearance+".png", tt.Image())
		})
	}
}
