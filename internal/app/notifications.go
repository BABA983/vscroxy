package app

import (
	"slices"
	"strconv"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// noticeHistoryLimit is how many notices the center keeps. Like VS Code's
// notification list, it holds the recent past in memory only: a notice older
// than the last fifty is gone, and none of it is written to disk.
const noticeHistoryLimit = 50

var notificationIcon = lucide.Bell()

// noticeCenter holds the queue of notices waiting for a toast, and the list's
// own UI state. Its fields are guarded by the app's mu except expanded,
// which belongs to the UI thread like MyGo's other widget state.
type noticeCenter struct {
	open     bool
	pending  []notice
	nextID   uint64
	expanded map[uint64]*bool
}

// noticeCenter is the app's own notification state. Its fields are read and
// written under a.mu, except expanded, which MyGo's disclosure owns on the UI
// thread.
func (a *app) noticeCenter() *noticeCenter { return &a.noticeUI }

// notificationItems is the history as the list sees it: a copy, so the UI
// never walks a slice a background notice can append to.
func (a *app) notificationItems() []notice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.notices)
}

func (a *app) notificationOpen() bool {
	center := a.noticeCenter()
	a.mu.Lock()
	defer a.mu.Unlock()
	return center.open
}

// noticeUnread counts errors and warnings not yet seen in the center. Info
// and success notices never make a badge: they are news, not something to
// act on later.
func (a *app) noticeUnread() (errors, warnings int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range a.notices {
		if n.read || n.level < noticeWarning {
			continue
		}
		if n.level == noticeError {
			errors++
		} else {
			warnings++
		}
	}
	return errors, warnings
}

// openNotifications shows the list and marks everything in it read. Opening
// the list is what acknowledges a background error, not closing its toast.
func (a *app) openNotifications() {
	center := a.noticeCenter()
	a.mu.Lock()
	for i := range a.notices {
		a.notices[i].read = true
	}
	center.open = true
	a.mu.Unlock()
	a.invalidate()
}

// clearNotifications empties the history and any notices still waiting to be
// shown as toasts. It leaves the last status line intact.
func (a *app) clearNotifications() {
	center := a.noticeCenter()
	a.mu.Lock()
	a.notices = nil
	center.pending = nil
	a.mu.Unlock()
	clear(center.expanded)
	a.invalidate()
}

// dismissNotice removes one entry, and with it anything left to act on.
func (a *app) dismissNotice(id uint64) {
	center := a.noticeCenter()
	a.mu.Lock()
	if i := a.noticeIndex(id); i >= 0 {
		a.notices = slices.Delete(a.notices, i, i+1)
	}
	a.mu.Unlock()
	delete(center.expanded, id)
	a.invalidate()
}

// markNoticeSpent records that a notice's action has been used. The center
// then stops offering it: replaying Undo on the next deletion would undo
// something the person never chose.
func (a *app) markNoticeSpent(id uint64) {
	a.mu.Lock()
	if i := a.noticeIndex(id); i >= 0 {
		a.notices[i].spent = true
	}
	a.mu.Unlock()
	a.invalidate()
}

// notificationAction runs a notice's action from the center, once. The run
// and the history write happen on the UI thread, as a toast's action does.
func (a *app) notificationAction(id uint64) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].spent || a.notices[i].run == nil {
		a.mu.Unlock()
		return
	}
	n := a.notices[i]
	a.notices[i].spent = true
	a.mu.Unlock()
	n.run()
	a.invalidate()
}

// noticeIndex finds a notice by its in-memory id. The caller holds a.mu.
func (a *app) noticeIndex(id uint64) int {
	return slices.IndexFunc(a.notices, func(n notice) bool { return n.id == id })
}

// notificationButton is the status bar's entry to the list. The count is the
// unread errors and warnings, tinted by the worse of the two.
func (a *app) notificationButton(c *ui.Context, t *ui.Theme) {
	center := a.noticeCenter()
	open := a.notificationOpen()
	errors, warnings := a.noticeUnread()
	ink := t.Warning
	if errors > 0 {
		ink = uikit.Colors(t).Danger
	}
	b := uikit.ToolbarButton(c, locales.Notifications(), locales.Notifications(), open).
		Gap(t.Space(uikit.SpaceXS)).Padding(0, t.Space(uikit.SpaceS))
	b.Children(func() {
		ui.Icon(c, notificationIcon).Size(uikit.IconInline, uikit.IconInline)
		if n := errors + warnings; n > 0 {
			ui.Row(c).Shrink(0).Height(uikit.HeightStatus-6).Radius(uikit.RadiusControl).
				Background(ink.Alpha(0.14)).AlignItems(ui.Center).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Caption(c, strconv.Itoa(n)).TextColor(ink).FontFeatures("tnum").SingleLine()
			})
		}
	})
	if b.Clicked() {
		if open {
			a.closeNotifications()
		} else {
			a.openNotifications()
		}
	}
	ui.PopoverBase(c, b, &center.open, func(panel ui.Element) {
		a.notificationList(c, t, b, panel)
	})
}

func (a *app) closeNotifications() {
	a.noticeCenter().open = false
	a.invalidate()
}

// notificationList is the list itself: newest first, each entry a disclosure
// with its detail and action, and one command to clear the whole history.
func (a *app) notificationList(c *ui.Context, t *ui.Theme, anchor, panel ui.Element) {
	items := a.notificationItems()
	width, height := c.Size()
	uikit.PopoverPanel(c, panel).AttachTo(anchor, ui.AnchorBottomRight, ui.AnchorTopRight).
		Margin(t.Space(uikit.SpaceS), 0, 0, 0).Padding(t.Space(uikit.SpaceM)).
		Width(min(420, width-32)).MaxHeight(min(520, height-80)).
		Gap(t.Space(uikit.SpaceXS))

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceS)).Children(func() {
		uikit.BodyStrong(c, locales.Notifications()).Grow(1)
		uikit.SecondaryButton(c, locales.ClearNotifications()).Disabled(len(items) == 0).OnClick(a.clearNotifications)
	})
	center := a.noticeCenter()
	if len(items) == 0 {
		uikit.EmptyState(c, locales.NoNotifications(), "", func() {
			uikit.SecondaryButton(c, locales.Close()).OnClick(a.closeNotifications)
		})
		return
	}
	ui.Scroll(c).FillWidth().MinHeight(0).MaxHeight(min(440, height-160)).Children(func() {
		ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceXS)).Children(func() {
			for i := len(items) - 1; i >= 0; i-- {
				n := items[i]
				ui.Column(c.Key("notification-" + strconv.FormatUint(n.id, 10))).FillWidth().Children(func() {
					a.noticeRow(c, t, center, n)
				})
			}
		})
	})
}

// noticeRow draws one notice. The trigger is the one-line summary; its panel
// holds the detail, the action and the dismiss control.
func (a *app) noticeRow(c *ui.Context, t *ui.Theme, center *noticeCenter, n notice) {
	if center.expanded == nil {
		center.expanded = make(map[uint64]*bool)
	}
	p := uikit.Colors(t)
	icon, ink := infoIcon, p.Info
	switch n.level {
	case noticeSuccess:
		icon, ink = successIcon, p.Success
	case noticeWarning:
		icon, ink = warningIcon, p.Warning
	case noticeError:
		icon, ink = errorIcon, p.Danger
	}
	expanded := center.expanded[n.id]
	if expanded == nil {
		expanded = new(false)
		center.expanded[n.id] = expanded
	}
	col := ui.CollapsibleBase(c, expanded)
	col.Trigger.FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(ink).Shrink(0)
		uikit.Body(c, n.text).Grow(1).MinWidth(0)
		uikit.DataMeta(c, noticeTime(n.at)).Shrink(0)
		ui.Icon(c, chevronIcon).Size(uikit.IconInline, uikit.IconInline).
			TextColor(p.Faint).Rotate(180 * col.Progress()).Shrink(0)
	})
	col.Panel(func() {
		ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceXS)).Padding(0, 0, t.Space(uikit.SpaceS), uikit.IconInline+t.Space(uikit.SpaceXS)).Children(func() {
			if n.detail != "" {
				uikit.Body(c, n.detail).FillWidth().MinWidth(0).Selectable()
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceM)).Children(func() {
				if n.action != "" && n.run != nil && !n.spent && (n.valid == nil || n.valid()) {
					uikit.SecondaryButton(c, n.action).OnClick(func() { a.notificationAction(n.id) })
				}
				ui.Spacer(c)
				uikit.RowIconButton(c, locales.DismissNotification(), locales.DismissNotification(), closeIcon).
					OnClick(func() { a.dismissNotice(n.id) })
			})
		})
	})
}

// noticeTime is the timestamp beside a notice: a clock time today, and the
// date as well once the notice is older.
func noticeTime(at time.Time) string {
	if time.Since(at) < 24*time.Hour {
		return at.Format("15:04")
	}
	return at.Format("2006-01-02 15:04")
}
