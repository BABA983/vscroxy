package app

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestNoticeHistoryKeepsTheNewestFifty(t *testing.T) {
	a := &app{}
	for i := range 60 {
		a.notify(notice{level: noticeInfo, text: fmt.Sprintf("notice %d", i)})
	}
	if len(a.notices) != noticeHistoryLimit {
		t.Fatalf("history has %d notices, want %d", len(a.notices), noticeHistoryLimit)
	}
	if got := a.notices[0].text; got != "notice 10" {
		t.Errorf("oldest kept = %q, want notice 10", got)
	}
	if got := a.notices[len(a.notices)-1].text; got != "notice 59" {
		t.Errorf("newest kept = %q, want notice 59", got)
	}
	// Showing the toasts must not empty the history: the notifications
	// center has the notices after their toasts are gone.
	tt := ui.NewTester(func(c *ui.Context) { a.showNotices(c) }, 800, 600)
	tt.Frame()
	if len(a.notices) != noticeHistoryLimit {
		t.Errorf("history after toasts = %d, want %d", len(a.notices), noticeHistoryLimit)
	}
}

func TestOpeningNotificationsClearsUnread(t *testing.T) {
	a := &app{}
	a.inform("Saved the collection")
	a.warn("Project changed on disk")
	a.fail("Import", errors.New("unsupported format"))
	if errs, warns := a.noticeUnread(); errs != 1 || warns != 1 {
		t.Fatalf("unread = %d errors, %d warnings; want 1 of each", errs, warns)
	}
	a.openNotifications()
	if errs, warns := a.noticeUnread(); errs != 0 || warns != 0 {
		t.Errorf("unread after opening = %d errors, %d warnings; want none", errs, warns)
	}
	if !a.noticeCenter().open || len(a.notices) != 3 {
		t.Errorf("center open = %t, history = %d; want an open list of three", a.noticeCenter().open, len(a.notices))
	}
}

func TestNotificationsActionRunsFromTheCenter(t *testing.T) {
	a := &app{}
	runs := 0
	a.notify(notice{text: "Import failed", detail: "unsupported format", action: "Retry", run: func() { runs++ }})
	a.openNotifications()
	tt := notificationTester(t, a, false)
	row, ok := tt.AccessByRole(ui.RoleDisclosure, "")
	if !ok {
		t.Fatalf("notice not listed: %v", tt.Texts())
	}
	box := row.Bounds
	tt.ClickAt(box.X+box.W-20, box.Y+box.H/2) // expand the detail and action
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Retry"); err != nil {
		t.Fatalf("click Retry: %v (texts %v)", err, tt.Texts())
	}
	if runs != 1 {
		t.Errorf("action ran %d times, want 1", runs)
	}
	tt.Frame()
	if tt.HasText("Retry") {
		t.Error("a used one-shot action is still offered")
	}
}

func TestClearNotificationsEmptiesTheCenter(t *testing.T) {
	a := &app{}
	a.warn("Project changed on disk")
	a.fail("Import", errors.New("unsupported format"))
	a.openNotifications()
	tt := notificationTester(t, a, false)
	if !tt.HasText("Import") || !tt.HasText("Project changed on disk") {
		t.Fatalf("notices missing before clear: %v", tt.Texts())
	}
	if err := tt.Click(locales.ClearNotifications()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.notices) != 0 || len(a.noticeCenter().pending) != 0 {
		t.Errorf("history = %d, pending = %d; want both empty", len(a.notices), len(a.noticeCenter().pending))
	}
	if errs, warns := a.noticeUnread(); errs != 0 || warns != 0 {
		t.Errorf("unread after clear = %d errors, %d warnings; want none", errs, warns)
	}
	if !tt.HasText(locales.NoNotifications()) {
		t.Errorf("empty state not shown: %v", tt.Texts())
	}
}

// notificationTester draws the status bar and its list without the rest of
// the workbench, at the same theme the app gives that chrome.
func notificationTester(t *testing.T, a *app, dark bool) *ui.Tester {
	t.Helper()
	th := uikit.Theme(dark)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(th)
		ui.Column(c).Fill().Background(th.Background).Children(func() {
			a.shell(c, th)
		})
	}, 1200, 640)
	tt.SetLocale("en-US")
	tt.Frame()
	return tt
}
