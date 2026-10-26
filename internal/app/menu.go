package app

import (
	"runtime"

	"github.com/egoist/mygo"
)

// menu uses the platform menu bar, including standard text-editing commands.
func (a *app) menu() *mygo.Menu {
	a.workbench.menuItems = make(map[string]*mygo.MenuItem)
	command := func(id, label string) *mygo.MenuItem {
		item := &mygo.MenuItem{ID: id, Label: label, Disabled: true, Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
			a.workbench.menuAction = id
			a.invalidate()
		}}
		for _, action := range a.desktopActions(nil) {
			if action.id == id {
				item.Accelerator = action.accelerator()
			}
		}
		a.workbench.menuItems[id] = item
		return item
	}
	preferences := command("settings", "Settings…")
	file := &mygo.MenuItem{Label: "File", Submenu: []*mygo.MenuItem{
		command("new-request", "New request"),
		command("import-curl", "Import cURL…"),
		command("save-request", "Save request"),
		command("close-request", "Close request tab"),
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
	items = append(items, file, &mygo.MenuItem{Role: mygo.RoleEditMenu}, &mygo.MenuItem{Label: "Request", Submenu: []*mygo.MenuItem{
		command("edit", "Edit & replay"),
		command("replay", "Replay selected…"),
		command("copy", "Copy as cURL"),
		mygo.Separator(),
		command("send", "Send request"),
		command("compare-source", "Compare with source"),
		command("back", "Back to traffic"),
	}}, &mygo.MenuItem{Label: "View", Submenu: []*mygo.MenuItem{
		command("capture-workspace", "Capture"),
		command("requests-workspace", "Requests"),
		command("apis-workspace", "API contracts"),
		command("investigate-workspace", "Investigations"),
		mygo.Separator(),
		command("palette", "Commands…"),
		command("search", "Find / Search traffic"),
		command("primary", "Focus request list / URL"),
		command("focus", "Focus inspector / Back to list"),
		command("next-tab", "Next debugging tab"),
		command("previous-tab", "Previous debugging tab"),
		mygo.Separator(), {Role: mygo.RoleToggleFullScreen},
	}}, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	return mygo.NewMenu(items)
}
