package uikit

import (
	"slices"
	"strconv"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// DocumentTab is one tab in a document strip. It is a descriptor, not the
// document: the screen maps its own open documents to these fields.
type DocumentTab struct {
	Title string
	// Method is a request's method, printed in its method ink. A tab with no
	// method uses Icon instead.
	Method  string
	Icon    *ui.SVG
	Dirty   bool
	Preview bool
	Active  bool
	// Fixed tabs sit before the scrolling tabs and never move or close. Live
	// is the fixed tab of the main window.
	Fixed    bool
	Closable bool
	// Pinned tabs stay at the start of the scrolling tabs, as VS Code's do;
	// bulk closes skip them.
	Pinned bool
	// Reveal scrolls this tab into view this frame. The screen sets it when
	// the active tab changed, so the reader can still scroll the strip away.
	Reveal bool
}

// DocumentTabsOptions is a document strip: fixed tabs, then the tabs that
// scroll, a new-tab button, the overflow menu and an optional control of the
// open document at the strip's right end. Callbacks receive the tab's index in
// Tabs.
type DocumentTabsOptions struct {
	Tabs          []DocumentTab
	NewLabel      string
	OverflowLabel string
	// Trailing draws a control that belongs to the open document rather than
	// to the window, such as the environment a request sends in. It keeps its
	// place while the tabs scroll.
	Trailing   func()
	OnActivate func(index int)
	OnClose    func(index int)
	OnPin      func(index int)
	OnNew      func()
	// OnOverflow draws the strip's list button and hands it to the screen; the
	// screen anchors a searchable quick pick to it.
	OnOverflow func(anchor ui.Element)
	// TabMenu builds a tab's own menu when it is right-clicked, or nil for
	// none. The strip stays generic: the screen offers only the actions that
	// tab supports, in its own words.
	TabMenu func(index int) func(*ui.Menu)
}

// DocumentTabs draws a row of document tabs above a content area. The fixed
// tabs stay at the left while the rest scroll, a preview is italic, and a
// dirty tab shows a dot that becomes its close mark under the pointer. A
// middle click closes a tab, as it does in a browser.
//
// The strip is chrome on the shell's surface. The chosen tab is a soft ink
// wash in a rounded box, as Postman draws it, rather than a browser tab that
// joins the page below; no rule separates one tab from the next. The new-tab
// button follows the last tab, as it does in VS Code and Postman; the list of
// open tabs and the optional document control keep the strip's end.
func DocumentTabs(c *ui.Context, o DocumentTabsOptions) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Row(c).Shrink(0).Height(HeightTabs).FillWidth().Background(p.Window).AlignItems(ui.Stretch).Gap(t.Space(SpaceXS)).Children(func() {
		// The rule under the strip. Every tab sits above it, as in Postman;
		// the chosen tab's pill does not join the page.
		ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(HairlineWidth).Background(p.Border).PassThrough()
		for i, item := range o.Tabs {
			if item.Fixed {
				documentTab(c, item, i, o)
			}
		}
		tabStripOn(c, p.Window, func() {
			ui.Row(c).Height(HeightTabs).AlignItems(ui.Stretch).Gap(t.Space(SpaceXS)).Children(func() {
				for i, item := range o.Tabs {
					if !item.Fixed {
						documentTab(c, item, i, o)
					}
				}
				if o.OnNew != nil {
					newDocumentTab(c, o)
				}
			})
		}).Grow(1).FillHeight()
		// With only the fixed Live tab open, the list would repeat the strip.
		if o.OnOverflow != nil && slices.ContainsFunc(o.Tabs, func(tab DocumentTab) bool { return !tab.Fixed }) {
			overflowTabs(c, o)
		}
		if o.Trailing != nil {
			ui.Row(c).Height(HeightTabs).Shrink(0).AlignItems(ui.Center).Padding(0, t.Space(SpaceM)).Children(o.Trailing)
		}
	})
}

func documentTab(c *ui.Context, item DocumentTab, index int, o DocumentTabsOptions) {
	t, p := c.Theme(), Colors(c.Theme())
	minWidth := float32(110)
	if item.Fixed {
		// Live is one short word with its icon: it takes what it needs.
		minWidth = 0
	}
	tab := ui.ButtonBase(c).Height(HeightTabs-2*t.Space(SpaceXS)).MarginY(t.Space(SpaceXS)).
		MinWidth(minWidth).MaxWidth(180).Shrink(0).
		Padding(0, t.Space(SpaceS)).Gap(t.Space(SpaceXS)).AlignItems(ui.Center).Radius(RadiusControl).
		Border(0, ui.Color{}).Label(item.Title)
	switch {
	case item.Active:
		tab.Background(p.Foreground.Alpha(0.05)).TextColor(t.Text)
	case tab.Pressed():
		tab.Background(p.Muted).TextColor(t.Text)
	case tab.Hovered():
		tab.Background(p.Foreground.Alpha(0.03)).TextColor(t.Text)
	default:
		tab.TextColor(t.TextMuted)
	}
	if item.Preview {
		tab.Italic()
	}
	if item.Reveal {
		tab.ScrollIntoView()
	}

	var close ui.Element
	closeSet := false
	tab.Children(func() {
		if item.Method != "" {
			MethodLabel(c, item.Method).FontSize(Scaled(TextMeta)).Shrink(0)
		} else if item.Icon != nil {
			ui.Icon(c, item.Icon).Size(IconInline, IconInline).Shrink(0)
		}
		title := ui.Text(c, item.Title).FontSize(Scaled(TextMeta)).LineHeight(LineSmall).
			SingleLine().MinWidth(0).Grow(1)
		if item.Preview {
			title.Italic()
		}
		if item.Closable {
			close = documentTabClose(c, item, tab)
			// Postman keeps the close mark out of the strip until the tab is
			// under the pointer or keyboard; a dirty tab's dot stays visible.
			if !item.Dirty && !tab.Hovered() && !tab.FocusWithin() {
				close.Opacity(0)
			}
			closeSet = true
		}
	})

	if o.TabMenu != nil {
		if build := o.TabMenu(index); build != nil {
			ContextMenu(c, tab, build)
		}
	}

	if closeSet && close.Clicked() {
		if o.OnClose != nil {
			o.OnClose(index)
		}
		return
	}
	middle := false
	tab.ObserveInput(func(ev ui.InputEvent) {
		if ev.Kind == ui.InputPointerUp && ev.Button == 2 && !ev.Canceled {
			middle = true
		}
	})
	if middle {
		if o.OnClose != nil {
			o.OnClose(index)
		}
		return
	}
	if tab.DoubleClicked() && item.Preview && o.OnPin != nil {
		o.OnPin(index)
		return
	}
	if tab.Clicked() && o.OnActivate != nil {
		o.OnActivate(index)
	}
}

func documentTabClose(c *ui.Context, item DocumentTab, tab ui.Element) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	close := ui.ButtonBase(c).Size(16, 16).Shrink(0).Radius(RadiusInner).
		AlignItems(ui.Center).Justify(ui.Center).Label(locales.CloseTabNamed(item.Title))
	if close.Hovered() {
		close.Background(p.MutedHover)
	}
	close.Children(func() {
		if item.Dirty && !tab.Hovered() {
			ui.Box(c).Size(7, 7).Radius(4).Background(t.Text)
			return
		}
		ui.Icon(c, closeIcon).Size(IconInline, IconInline).TextColor(t.TextMuted)
	})
	return close
}

func newDocumentTab(c *ui.Context, o DocumentTabsOptions) {
	t, p := c.Theme(), Colors(c.Theme())
	label := o.NewLabel
	if label == "" {
		label = locales.NewRequest()
	}
	size := HeightTabs - 2*t.Space(SpaceXS)
	button := ui.ButtonBase(c).Size(size, size).MarginY(t.Space(SpaceXS)).Radius(RadiusControl).Shrink(0).
		AlignItems(ui.Center).Justify(ui.Center).Label(label)
	if button.Hovered() {
		button.Background(p.Foreground.Alpha(0.03))
	}
	button.Children(func() {
		ui.Icon(c, lucide.Plus()).Size(IconInline, IconInline).TextColor(t.TextMuted)
	})
	if button.Clicked() {
		o.OnNew()
	}
	Tip(c, button, label)
}

func overflowTabs(c *ui.Context, o DocumentTabsOptions) {
	t := c.Theme()
	tip := locales.AllOpenTabsCount(len(o.Tabs))
	if o.OverflowLabel != "" {
		tip = o.OverflowLabel + " (" + strconv.Itoa(len(o.Tabs)) + ")"
	}
	button := ui.ButtonBase(c).Label(tip)
	button.Size(HeightControlSmall, HeightControlSmall).MarginY((HeightTabs - HeightControlSmall) / 2).
		Padding(0).Radius(RadiusControl).Shrink(0).AlignItems(ui.Center).Justify(ui.Center)
	if button.Hovered() {
		button.Background(t.Border)
	}
	button.Children(func() {
		ui.Icon(c, chevronIcon).Size(IconLarge, IconLarge).TextColor(t.TextMuted)
	})
	Tip(c, button, tip)
	o.OnOverflow(button)
}
