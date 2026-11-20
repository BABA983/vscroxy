package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// openInspectorWindow moves the capture inspector into its own window. The
// inspector keeps the same state as the docked one, so the table's selection,
// its tabs and the body readers stay in step. Without a main window (headless
// tests) the detached state is enough to exercise the layout.
func (a *app) openInspectorWindow() {
	a.openInspectorWindowAt(mygo.Rectangle{})
}

// openInspectorWindowAt restores the given frame as well as the inspector.
// The frame comes from inspector-windows.json, not MyGo's StateKey: that key
// would restore the geometry even when the flow the window showed is gone.
func (a *app) openInspectorWindowAt(bounds mygo.Rectangle) {
	if a.inspectorWin != nil {
		a.inspectorWin.Focus()
		return
	}
	a.inspectorDetached = true
	a.workbench.focused = false
	if a.win == nil {
		a.invalidate()
		return
	}
	opts := mygo.WindowOptions{
		Title:           locales.RequestInspector(),
		Width:           inspectorWindowWidth,
		Height:          inspectorWindowHeight,
		MinWidth:        inspectorWindowMinWidth,
		MinHeight:       inspectorWindowMinHeight,
		AutoHideMenuBar: true,
		Content:         ui.View(a.inspectorWindowView),
	}
	if bounds.Width > 0 && bounds.Height > 0 {
		opts.X, opts.Y, opts.Width, opts.Height = bounds.X, bounds.Y, bounds.Width, bounds.Height
	}
	w := mygo.NewWindow(opts)
	a.inspectorWin, a.inspectorFocused = w, true
	w.OnFocus(func() { w.Update(func() { a.inspectorFocused = true }) })
	w.OnBlur(func() { w.Update(func() { a.inspectorFocused = false }) })
	w.OnClosed(func() {
		if a.win != nil {
			a.win.Update(func() {
				if a.inspectorWin == w {
					a.dockInspector()
				}
			})
			return
		}
		if a.inspectorWin == w {
			a.dockInspector()
		}
	})
	a.invalidate()
}

// closeInspectorWindow is the Dock action: the inspector returns to the main
// window, and its window closes through OnClosed.
func (a *app) closeInspectorWindow() {
	if a.inspectorWin != nil {
		a.inspectorWin.Close()
		return
	}
	a.dockInspector()
}

func (a *app) dockInspector() {
	a.inspectorWin, a.inspectorDetached, a.inspectorFocused = nil, false, false
	a.invalidate()
}

// inspectorWindowView is the detached inspector: the same panes as the docked
// one, with its own Dock action in the header.
func (a *app) inspectorWindowView(c *ui.Context) {
	dark := a.appearance == "dark" || a.appearance == "" && c.Theme().Dark
	c.SetTheme(uikit.Theme(dark))
	c.SetMenuPresentation(ui.PopoverMenus)
	t := c.Theme()
	a.inspectorInWindow = true
	defer func() { a.inspectorInWindow = false }()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(uikit.HeightBar).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Glyph(c, detachIcon)
			uikit.Body(c, locales.RequestInspector()).Grow(1)
			if uikit.Tip(c, uikit.SecondaryButton(c, locales.DockInspector()).Label(locales.DockInspector()), locales.DockInspectorHint()).Clicked() {
				a.closeInspectorWindow()
			}
		})
		ui.Divider(c)
		a.detailInWindow(c)
	})
}
