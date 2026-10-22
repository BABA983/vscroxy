package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

// menu uses the platform menu bar, including standard text-editing commands.
func (a *app) menu() *mygo.Menu {
	preferences := &mygo.MenuItem{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
		a.settingsOpen = true
		a.invalidate()
	}}
	file := &mygo.MenuItem{Label: "File", Submenu: []*mygo.MenuItem{
		{Label: "New request", Accelerator: "CmdOrCtrl+N", Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
			a.openCompose()
			a.invalidate()
		}},
		{Label: "Import cURL…", Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
			a.openCurlImport()
			a.invalidate()
		}},
		mygo.Separator(),
		{Role: mygo.RoleClose},
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
	items = append(items, file, &mygo.MenuItem{Role: mygo.RoleEditMenu}, &mygo.MenuItem{Label: "View", Submenu: []*mygo.MenuItem{
		{Label: "Capture", Accelerator: "CmdOrCtrl+1", Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
			a.workspace = 0
			a.invalidate()
		}},
		{Label: "Requests", Accelerator: "CmdOrCtrl+2", Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
			if a.requestDocument == nil {
				a.openCompose()
			}
			a.workspace = 1
			a.invalidate()
		}},
		{Label: "APIs", Accelerator: "CmdOrCtrl+3", Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
			a.workspace = 2
			a.invalidate()
		}},
		mygo.Separator(), {Role: mygo.RoleToggleFullScreen},
	}}, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	return mygo.NewMenu(items)
}
