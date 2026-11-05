package app

import (
	"runtime"

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
	preferences := command("app.settings", "Settings…")
	file := &mygo.MenuItem{Label: "File", Submenu: []*mygo.MenuItem{
		command("request.new", "New request"),
		command("request.import-curl", "Import cURL…"),
		command("collection.import-folder", "Import Bruno collection…"),
		command("request.save", "Save request"),
		command("request.close", "Close request"),
		mygo.Separator(),
		{Role: mygo.RoleClose, Label: "Close window", Accelerator: "CmdOrCtrl+Shift+W"},
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
	copyFormats = append(copyFormats, command("flow.copy-har", "HAR (sanitized)"), command("flow.copy-har-raw", "HAR (with sensitive data)"))
	items = append(items, file, &mygo.MenuItem{Role: mygo.RoleEditMenu}, &mygo.MenuItem{Label: "Request", Submenu: []*mygo.MenuItem{
		command("flow.edit", "Edit request"),
		command("flow.replay", "Replay selected…"),
		command("flow.copy-curl", "Copy as cURL"),
		{Label: "Copy as…", Submenu: copyFormats},
		mygo.Separator(),
		command("request.send", "Send request"),
		command("request.compare-source", "Compare with source"),
	}}, &mygo.MenuItem{Label: "View", Submenu: []*mygo.MenuItem{
		command("go.live", "Live traffic"),
		command("go.collections", "Collections"),
		command("go.sessions", "Sessions"),
		mygo.Separator(),
		command("view.layout-right", "Side by side layout"),
		command("view.layout-bottom", "Stacked layout"),
		command("view.sidebar", "Toggle sidebar"),
		mygo.Separator(),
		command("app.palette", "Commands…"),
		command("view.search", "Find / Search traffic"),
		command("view.primary", "Focus request list / URL"),
		command("view.focus-inspector", "Focus inspector / Back to list"),
		command("request.next-tab", "Next open request"),
		command("request.previous-tab", "Previous open request"),
		mygo.Separator(), {Role: mygo.RoleToggleFullScreen},
	}}, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	return mygo.NewMenu(items)
}
