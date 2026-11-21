package uikit

import (
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
	// Reveal scrolls this tab into view this frame. The screen sets it when
	// the active tab changed, so the reader can still scroll the strip away.
	Reveal bool
}

// DocumentTabsOptions is a document strip: fixed tabs, then the tabs that
// scroll, a new-tab button and the overflow menu. Callbacks receive the tab's
// index in Tabs.
type DocumentTabsOptions struct {
	Tabs          []DocumentTab
	NewLabel      string
	OverflowLabel string
	OverflowTip   string
	OnActivate    func(index int)
	OnClose       func(index int)
	OnPin         func(index int)
	OnNew         func()
	OnOverflow    func(*ui.Menu)
}

// DocumentTabs draws a row of document tabs above a content area. The fixed
// tabs stay at the left while the rest scroll, a preview is italic, and a
// dirty tab shows a dot that becomes its close mark under the pointer. A
// middle click closes a tab, as it does in a browser.
func DocumentTabs(c *ui.Context, o DocumentTabsOptions) ui.Element {
	p := Colors(c.Theme())
	return ui.Column(c).Shrink(0).Children(func() {
		ui.Row(c).Height(HeightTabs).FillWidth().Background(p.Background).AlignItems(ui.Stretch).Children(func() {
			for i, item := range o.Tabs {
				if item.Fixed {
					documentTab(c, item, i, o)
				}
			}
			TabStrip(c, func() {
				ui.Row(c).Height(HeightTabs).AlignItems(ui.Stretch).Children(func() {
					for i, item := range o.Tabs {
						if !item.Fixed {
							documentTab(c, item, i, o)
						}
					}
				})
			}).Grow(1).FillHeight()
			if o.OnNew != nil {
				newDocumentTab(c, o)
			}
			if o.OnOverflow != nil {
				ui.Box(c).Width(HairlineWidth).Shrink(0).FillHeight().Background(p.Border)
				overflowTabs(c, o)
			}
		})
		ui.Divider(c)
	})
}

func documentTab(c *ui.Context, item DocumentTab, index int, o DocumentTabsOptions) {
	t, p := c.Theme(), Colors(c.Theme())
	tab := ui.ButtonBase(c).Height(HeightTabs).MinWidth(110).MaxWidth(180).Shrink(0).
		Padding(0, t.Space(SpaceM)).Gap(t.Space(SpaceXS)).AlignItems(ui.Center).Radius(0).
		Border(0, ui.Color{}).Label(item.Title)
	switch {
	case item.Active:
		tab.Background(p.Window).TextColor(t.Text)
	case tab.Hovered():
		tab.Background(p.MutedHover).TextColor(t.Text)
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
			MethodLabel(c, item.Method).Shrink(0)
		} else if item.Icon != nil {
			ui.Icon(c, item.Icon).Size(IconInline, IconInline).Shrink(0)
		}
		title := Body(c, item.Title).SingleLine().MinWidth(0).Grow(1)
		if item.Preview {
			title.Italic()
		}
		if item.Closable {
			close = documentTabClose(c, item, tab)
			closeSet = true
		}
		// The hairline between neighbours belongs to the tab, so an active
		// tab keeps its edge while the row scrolls.
		ui.Box(c).Absolute().Right(0).Top(0).Bottom(0).Width(HairlineWidth).Background(p.Border)
	})

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
	button := ui.ButtonBase(c).Size(HeightTabs, HeightTabs).Shrink(0).
		AlignItems(ui.Center).Justify(ui.Center).Label(label)
	if button.Hovered() {
		button.Background(p.MutedHover)
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
	t, p := c.Theme(), Colors(c.Theme())
	label := o.OverflowLabel
	if label == "" {
		label = locales.AllOpenTabs()
	}
	var button ui.Element
	MenuFace(c, func() {
		button = ui.ButtonBase(c).Label(label+" ("+strconv.Itoa(len(o.Tabs))+")").
			PopoverMenuButton(o.OnOverflow, MenuButtonOptions(c))
	})
	button.Height(HeightTabs).Padding(0, t.Space(SpaceM)).Gap(t.Space(SpaceXS)).
		Shrink(0).AlignItems(ui.Center)
	if button.Hovered() {
		button.Background(p.MutedHover)
	}
	button.Children(func() {
		ui.Icon(c, chevronIcon).Size(IconInline, IconInline).TextColor(t.TextMuted)
		Meta(c, strconv.Itoa(len(o.Tabs)))
	})
	if o.OverflowTip != "" {
		Tip(c, button, o.OverflowTip)
	}
}
