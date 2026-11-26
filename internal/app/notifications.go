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
		if n.read || n.level < noticeWarning || n.level == noticeCancelled {
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

// noticeDismissed reports whether the stable id of a neverShowAgain notice
// was suppressed. The saved value is an id, never the localized text.
func (a *app) noticeDismissed(id string) bool {
	if id == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.dismissedNotices, id)
}

// suppressNotice removes one notice forever, under its stable id, and saves
// that id for the next launch. Settings can reset the choice later.
func (a *app) suppressNotice(id uint64) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].neverShowAgain == "" {
		a.mu.Unlock()
		return
	}
	key := a.notices[i].neverShowAgain
	a.notices = slices.Delete(a.notices, i, i+1)
	if !slices.Contains(a.dismissedNotices, key) {
		a.dismissedNotices = append(slices.Clone(a.dismissedNotices), key)
		slices.Sort(a.dismissedNotices)
	}
	center := a.noticeCenter()
	center.pending = slices.DeleteFunc(center.pending, func(n notice) bool { return n.id == id })
	delete(center.expanded, id)
	a.mu.Unlock()
	a.bypassSettingsSave()
	_ = a.persistSettings()
	a.invalidate()
}

// resetDismissedNotices makes every neverShowAgain notice available again.
func (a *app) resetDismissedNotices() {
	a.mu.Lock()
	if len(a.dismissedNotices) == 0 {
		a.mu.Unlock()
		return
	}
	a.dismissedNotices = nil
	a.mu.Unlock()
	a.bypassSettingsSave()
	_ = a.persistSettings()
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

// notificationAction runs a notice's action from the center, once. The run,
// the link and the history write happen on the UI thread, as a toast's action
// does.
func (a *app) notificationAction(c *ui.Context, id uint64) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].spent || a.notices[i].action == "" ||
		(a.notices[i].run == nil && a.notices[i].openURL == "") ||
		a.notices[i].valid != nil && !a.notices[i].valid() {
		a.mu.Unlock()
		return
	}
	n := a.notices[i]
	if !n.persistent {
		a.notices[i].spent = true
	}
	a.mu.Unlock()
	if n.run != nil {
		n.run()
	}
	if n.openURL != "" {
		c.Services().OpenURL(n.openURL)
	}
	a.invalidate()
}

// notificationAlternate runs a notice's second choice, as Overwrite beside
// Compare on a save conflict. Like the primary action it runs on the UI
// thread, and the conflict's choices stay until the save is resolved.
func (a *app) notificationAlternate(id uint64) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].spent || a.notices[i].alternateRun == nil || a.notices[i].valid != nil && !a.notices[i].valid() {
		a.mu.Unlock()
		return
	}
	n := a.notices[i]
	if !n.persistent {
		a.notices[i].spent = true
	}
	a.mu.Unlock()
	n.alternateRun()
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

// notificationList is the list itself, after VS Code's notification center:
// a title with icon-only Clear all and Hide, then each notice as its own card
// with the level icon, the message across the card, its time under it and its
// choices at the card's bottom right.
func (a *app) notificationList(c *ui.Context, t *ui.Theme, anchor, panel ui.Element) {
	items := a.notificationItems()
	width, height := c.Size()
	// The bell is at the footer's end, so the list opens above it.
	panelWidth := min(420, width-32)
	uikit.PopoverPanel(c, panel).AttachTo(anchor, ui.AnchorTopRight, ui.AnchorBottomRight).
		Margin(0, 0, t.Space(uikit.SpaceS), 0).Padding(t.Space(uikit.SpaceM)).
		Width(panelWidth).MaxHeight(min(520, height-80)).
		Gap(t.Space(uikit.SpaceXS))

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceS)).Children(func() {
		uikit.BodyStrong(c, locales.Notifications()).Grow(1)
		uikit.RowIconButton(c, locales.ClearAllNotifications(), locales.ClearAllNotifications(), lucide.BrushCleaning()).
			Disabled(len(items) == 0).OnClick(a.clearNotifications)
		uikit.RowIconButton(c, locales.HideNotifications(), locales.HideNotifications(), lucide.ChevronDown()).
			OnClick(a.closeNotifications)
	})
	center := a.noticeCenter()
	if len(items) == 0 {
		uikit.Hint(c, locales.NoNewNotifications()).FillWidth().Padding(t.Space(uikit.SpaceM), 0)
		return
	}
	ui.Scroll(c).FillWidth().MinHeight(0).MaxHeight(min(440, height-160)).Children(func() {
		ui.Column(c).FillWidth().Gap(c.Theme().Space(uikit.SpaceM)).Children(func() {
			for i := len(items) - 1; i >= 0; i-- {
				a.noticeCard(c, t, center, items[i])
			}
		})
	})
}

// noticeCard draws one notice as a card: a hairline of the card's own, the
// level icon level with the first line of the message, the message across the
// card, then its time and its choices. A notice with more to read opens it
// under the message, aligned with the message's own left edge.
func (a *app) noticeCard(c *ui.Context, t *ui.Theme, center *noticeCenter, n notice) {
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
	case noticeCancelled:
		icon, ink = infoIcon, p.Faint
	}
	expanded := center.expanded[n.id]
	if expanded == nil {
		expanded = new(false)
		center.expanded[n.id] = expanded
	}
	// The message starts after the level icon; the time, the detail and the
	// choices line up with its left edge below it.
	inset := uikit.IconInline + t.Space(uikit.SpaceXS)
	// The dismiss mark keeps its slot at the end of the first line whether
	// or not the pointer shows it, so the chevron and the message do not
	// move on hover.
	dismissSlot := uikit.HeightRowAction + t.Space(uikit.SpaceXS)
	pad := t.Space(uikit.SpaceL)
	card := ui.Column(c.Key("notification-"+strconv.FormatUint(n.id, 10))).FillWidth().
		Padding(pad).Gap(t.Space(uikit.SpaceM)).
		Radius(uikit.RadiusControl).Border(uikit.HairlineWidth, p.Border)
	card.Children(func() {
		// The first line carries the level icon and, at its end, the
		// disclosure and dismiss controls. They share the line's 20 points
		// and are centered on it, as VS Code puts a card's controls on its
		// title line.
		summary := func(chevron float32) {
			// The level icon sits on the first line of the message, not at
			// the middle of a tall notice.
			ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(ink).Shrink(0).
				Margin(t.Space(uikit.SpaceXS)-uikit.HairlineWidth, 0, 0, 0)
			uikit.Body(c, n.text).Grow(1).MinWidth(0)
			if n.detail != "" {
				ui.Icon(c, chevronIcon).Size(uikit.IconInline, uikit.IconInline).
					TextColor(p.Faint).Rotate(180*chevron).Shrink(0).
					Margin(t.Space(uikit.SpaceXS)-uikit.HairlineWidth, 0, 0, 0)
			}
		}
		if n.detail != "" {
			col := ui.CollapsibleBase(c, expanded)
			col.Trigger.FillWidth().MinWidth(0).PaddingEnd(dismissSlot).
				Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Start).Children(func() {
				summary(col.Progress())
			})
			col.Panel(func() {
				uikit.Small(c, n.detail).TextColor(t.TextMuted).FillWidth().MinWidth(0).Selectable().
					PaddingStart(inset)
			})
		} else {
			ui.Row(c).FillWidth().MinWidth(0).PaddingEnd(dismissSlot).
				Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Start).Children(func() {
				summary(-1)
			})
		}
		uikit.Caption(c, noticeTime(n.at)).TextColor(t.TextMuted).PaddingStart(inset)
		a.noticeActions(c, n)
		// Absolute children are placed from the card's border edge, so the
		// padding puts the mark on the first line's center (20 points high)
		// and inside the card. It stays outside the disclosure so assistive
		// technology sees a button of its own.
		dismiss := uikit.RowIconButton(c, locales.DismissNotification(), locales.DismissNotification(), closeIcon)
		dismiss.Absolute().Top(pad).Right(pad)
		if !card.Hovered() && !card.FocusWithin() {
			dismiss.Opacity(0)
		}
		dismiss.OnClick(func() { a.dismissNotice(n.id) })
	})
}

// noticeActions draws the notice's choices at the card's bottom right, as
// VS Code ends a notification: the primary choice last, and only it takes the
// primary button.
func (a *app) noticeActions(c *ui.Context, n notice) {
	hasAction := n.action != "" && (n.run != nil || n.openURL != "") && !n.spent && (n.valid == nil || n.valid())
	hasAlternate := n.alternate != "" && n.alternateRun != nil && !n.spent && (n.valid == nil || n.valid())
	if !hasAction && !hasAlternate && n.neverShowAgain == "" {
		return
	}
	ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).Gap(c.Theme().Space(uikit.SpaceM)).Children(func() {
		if hasAlternate {
			uikit.SecondaryButton(c, n.alternate).OnClick(func() { a.notificationAlternate(n.id) })
		}
		if n.neverShowAgain != "" {
			uikit.SecondaryButton(c, locales.DontShowAgain()).OnClick(func() { a.suppressNotice(n.id) })
		}
		if hasAction {
			uikit.PrimaryButton(c, n.action).OnClick(func() { a.notificationAction(c, n.id) })
		}
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
