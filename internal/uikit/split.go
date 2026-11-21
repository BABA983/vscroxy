package uikit

import "github.com/egoist/mygo/ui"

// MyGo paints a split's divider with the theme's Border, and its hover and
// drag line with the theme's Accent. This app spends Accent on the primary
// button's fill, so an unhushed split draws a near-black rule across the pane
// the moment the pointer touches it. These wrappers give the split a quiet
// accent for its own chrome and hand the full theme back to each pane, so a
// primary button inside one keeps its ink.

// Split is ui.Split with the divider's hover line kept quiet.
func Split(c *ui.Context, size *float32, first, second func()) ui.Element {
	return splitQuiet(c, func(first, second func()) ui.Element {
		return ui.Split(c, size, first, second)
	}, first, second)
}

// SplitVertical is ui.SplitVertical with the divider's hover line kept quiet.
func SplitVertical(c *ui.Context, size *float32, first, second func()) ui.Element {
	return splitQuiet(c, func(first, second func()) ui.Element {
		return ui.SplitVertical(c, size, first, second)
	}, first, second)
}

// SplitPercent is ui.SplitPercent with the divider's hover line kept quiet.
func SplitPercent(c *ui.Context, percent *float32, first, second func()) ui.Element {
	return splitQuiet(c, func(first, second func()) ui.Element {
		return ui.SplitPercent(c, percent, first, second)
	}, first, second)
}

// SplitVerticalPercent is ui.SplitVerticalPercent with the divider's hover
// line kept quiet.
func SplitVerticalPercent(c *ui.Context, percent *float32, first, second func()) ui.Element {
	return splitQuiet(c, func(first, second func()) ui.Element {
		return ui.SplitVerticalPercent(c, percent, first, second)
	}, first, second)
}

func splitQuiet(c *ui.Context, build func(first, second func()) ui.Element, first, second func()) ui.Element {
	t := c.Theme()
	quiet := *t
	quiet.Accent = Colors(t).Faint
	c.SetTheme(&quiet)
	// The split reads the theme before it runs either pane, so the divider
	// takes the quiet accent while the panes draw with the full one.
	e := build(func() { c.SetTheme(t); first() }, func() { c.SetTheme(t); second() })
	c.SetTheme(t)
	return e
}
