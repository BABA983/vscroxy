package app

import (
	"runtime"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// menu uses the platform menu bar, including standard text-editing commands.
func (a *app) menu() *mygo.Menu {
	a.workbench.menuItems = make(map[string]*mygo.MenuItem)
	command := func(id, label string) *mygo.MenuItem {
		item := &mygo.MenuItem{ID: id, Label: label, Disabled: true, Click: func(_ *mygo.MenuItem, w *mygo.Window) {
			// Every window has the menu, and its shortcuts come before the
			// content's: the Tools window runs the commands it has itself.
			if w != nil && w == a.toolsWin {
				a.toolsMenuAction = id
				a.invalidateTools()
				return
			}
			a.workbench.menuAction = id
			a.invalidate()
		}}
		item.Accelerator = pick(a.actions(nil), id).shortcut.accelerator()
		a.workbench.menuItems[id] = item
		return item
	}
	preferences := command("app.settings", locales.Settings2())
	file := &mygo.MenuItem{Label: locales.File(), Submenu: []*mygo.MenuItem{
		command("request.new", locales.NewRequest()),
		command("request.import-curl", locales.ImportCurl()),
		command("collection.import-folder", locales.ImportCollectionFolder()),
		command("capture.import-session", locales.ImportCaptureSession()),
		command("capture.import-proxyman-folder", locales.ImportProxymanLogFolder()),
		command("request.save", locales.SaveRequest()),
		command("request.close", locales.CloseRequest()),
		mygo.Separator(),
		{Role: mygo.RoleClose, Label: locales.CloseWindow(), Accelerator: "CmdOrCtrl+Shift+W"},
	}}
	var items []*mygo.MenuItem
	if runtime.GOOS == "darwin" {
		items = append(items, &mygo.MenuItem{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, mygo.Separator(), preferences, mygo.Separator(),
			{Role: mygo.RoleServices}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide},
			mygo.Separator(), {Role: mygo.RoleQuit},
		}})
	} else {
		file.Submenu = append(file.Submenu, preferences, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})
	}
	var copyFormats []*mygo.MenuItem
	for _, format := range requestCopyFormats[1:] {
		copyFormats = append(copyFormats, command(format.id, format.label))
	}
	copyFormats = append(copyFormats, command("flow.copy-markdown", locales.MarkdownTable()), command("flow.copy-csv", "CSV"),
		command("flow.copy-raw", locales.RawHttp()), command("flow.copy-har", locales.HarSanitized()), command("flow.copy-har-raw", locales.HarWithSensitiveData()))
	items = append(items, file, &mygo.MenuItem{Role: mygo.RoleEditMenu}, &mygo.MenuItem{Label: locales.Request(), Submenu: []*mygo.MenuItem{
		command("flow.edit", locales.EditRequest()),
		command("request.duplicate", locales.DuplicateAsNewRequest()),
		command("flow.replay", locales.ReplaySelected()),
		command("flow.copy-curl", locales.CopyAsCurl()),
		{Label: locales.CopyAs(), Submenu: copyFormats},
		mygo.Separator(),
		command("request.send", locales.SendRequest()),
		command("request.compare-source", locales.CompareWithSource()),
	}}, &mygo.MenuItem{Label: locales.View(), Submenu: []*mygo.MenuItem{
		command("go.live", locales.LiveTraffic()),
		command("go.collections", locales.Collections()),
		command("go.sessions", locales.Sessions()),
		mygo.Separator(),
		command("view.layout-right", locales.SideBySideLayout()),
		command("view.layout-bottom", locales.StackedLayout()),
		command("view.sidebar", locales.ToggleSidebar()),
		mygo.Separator(),
		command("app.palette", locales.Commands()),
		command("view.search", locales.FindSearchTraffic()),
		command("view.primary", locales.FocusRequestListUrl()),
		command("view.focus-inspector", locales.FocusInspectorBackToList()),
		command("request.next-tab", locales.NextOpenRequest()),
		command("request.previous-tab", locales.PreviousOpenRequest()),
		mygo.Separator(), {Role: mygo.RoleToggleFullScreen},
	}}, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	return mygo.NewMenu(items)
}
