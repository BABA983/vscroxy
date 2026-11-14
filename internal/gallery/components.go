package gallery

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// The samples the export puts the pointer on and tabs to. The entry and the
// plate name them from the same constant, so a plate that moves a sample
// fails the export instead of quietly photographing the wrong state.
const (
	hoverButtons   = "Hover Buttons"
	focusButtons   = "Focus Buttons"
	hoverQuiet     = "Hover Quiet"
	focusQuiet     = "Focus Quiet"
	hoverInputs    = "Hover Inputs"
	focusInputs    = "Focus Inputs"
	hoverTabs      = "Hover Tabs"
	focusTabs      = "Focus Tabs"
	hoverSegmented = "Hover Segmented"
	focusSegmented = "Focus Segmented"
	hoverMenus     = "Hover Menus"
	focusMenus     = "Focus Menus"
	hoverPanes     = "Hover Panes"
	focusPanes     = "Focus Panes"
	hoverSidebar   = "Hover Sidebar"
	hoverActivity  = "Hover Activity"
	focusActivity  = "Focus Activity"
	hoverStatus    = "Hover Status"
	focusStatus    = "Focus Status"
	hoverPanels    = "Hover Panels"
	focusPanels    = "Focus Panels"
	hoverSettings  = "Hover Settings"
	focusSettings  = "Focus Settings"
	hoverToasts    = "Hover Toasts"
	focusToasts    = "Focus Toasts"
	hoverLists     = "Hover Lists"
)

// components is the state every component plate keeps across frames: the
// values its fields and choices bind to.
type components struct {
	host, empty, search, searchChrome string
	bypass                            string
	format, pick, labels              string
	tab, counted, sub, segment        int
	sidebar                           string
	range_                            uikit.ActivityRange
	open                              bool
	on, off, check                    bool
	toasts                            bool
	icon                              ui.ImageSource
}

// componentsGroup is the kit's public components, one plate per family, each
// listing the states it has. Hover and pressed are drawn by the pointer: the
// window shows them as the pointer crosses them, and the export puts the
// tester's pointer on the named sample.
func (g *Gallery) components() *Group {
	k := &components{host: "api.example.com", bypass: "localhost\n*.internal.test\n10.0.0.0/8", format: "Pretty", pick: "all", labels: "Pretty", sidebar: "projects", range_: uikit.ActivityRange{From: 22, To: 34}}
	k.icon = iconBitmap()
	return &Group{ID: "components", Title: "Components", Entries: []*Entry{
		{ID: "text", Title: "Text", Note: "Seven roles; a text has no states", Height: 420, build: k.textPlate},
		{ID: "buttons", Title: "Buttons", Note: "Primary, secondary, danger and compact, with their faces", Height: 440, Hover: hoverButtons, Focus: focusButtons, build: k.buttonPlate},
		{ID: "buttons-quiet", Title: "Quiet buttons", Note: "Toolbar, footer, icon, chip, row and swatch", Height: 560, Hover: hoverQuiet, Focus: focusQuiet, build: k.quietPlate},
		{ID: "inputs", Title: "Inputs", Note: "Fields, their two faces, search and the two selects", Height: 620, Hover: hoverInputs, Focus: focusInputs, build: k.inputPlate},
		{ID: "tabs", Title: "Tabs", Note: "Tabs, counted tabs, sub-tabs and a scrolling strip", Height: 440, Hover: hoverTabs, Focus: focusTabs, build: k.tabPlate},
		{ID: "segmented", Title: "Segmented", Note: "A recessed groove and a raised thumb", Height: 280, Hover: hoverSegmented, Focus: focusSegmented, build: k.segmentedPlate},
		{ID: "menus", Title: "Menus", Note: "Menu select, the ⋯ buttons and a tooltip", Height: 400, Hover: hoverMenus, Focus: focusMenus, Tip: true, build: k.menuPlate},
		{ID: "panes", Title: "Pane headers", Note: "A title, its tabs and what the pane reports", Height: 380, Hover: hoverPanes, Focus: focusPanes, Tip: true, build: k.panePlate},
		{ID: "sidebar", Title: "Sidebar", Note: "Rows, headings, letters and the action row", Height: 600, Hover: hoverSidebar, build: k.sidebarPlate},
		{ID: "activity", Title: "Activity strip", Note: "Rest and a brushed range; drag the plot to brush", Height: 300, Hover: hoverActivity, Focus: focusActivity, build: k.activityPlate},
		{ID: "status", Title: "Status and methods", Note: "Pills, method inks, response meta, sparkline, chip", Height: 560, Hover: hoverStatus, Focus: focusStatus, build: k.statusPlate},
		{ID: "empty", Title: "Empty state", Note: "One icon, a title, a sentence and the actions", Height: 340, build: k.emptyPlate},
		{ID: "panels", Title: "Panels and dialogs", Note: "Popover, dialog column and footer; the dialog opens", Height: 480, Hover: hoverPanels, Focus: focusPanels, build: k.panelPlate},
		{ID: "settings", Title: "Settings form", Note: "A page, its groups, SettingsRow and its multiline row", Height: 560, Hover: hoverSettings, Focus: focusSettings, build: k.settingsPlate},
		{ID: "toasts", Title: "Toasts", Note: "Three kinds; the buttons add another", Height: 300, Hover: hoverToasts, Focus: focusToasts, build: k.toastPlate},
		{ID: "lists", Title: "List selection", Note: "Selected rows, the marker and a list's theme", Height: 440, Hover: hoverLists, build: k.listPlate},
	}}
}

// stateRow is one state of a plate: its name in the margin, then the sample.
// The sample that is hovered or focused carries the label the export finds,
// so the pointer and the keyboard land on the control itself.
func stateRow(c *ui.Context, state string, sample func()) {
	t := c.Theme()
	ui.Row(c).MinHeight(uikit.HeightControl).Gap(t.Space(3)).AlignItems(ui.Start).Children(func() {
		uikit.Meta(c, state).Width(96).Shrink(0).Padding(0, 0, 0, 0)
		ui.Row(c).Grow(1).Gap(t.Space(2)).AlignItems(ui.Start).Children(sample)
	})
}

// link is a LinkButton with the text it shows: the button carries the
// accessible name, and the caller builds what is inside it, as it does for
// ToolbarButton.
func link(c *ui.Context, label string) ui.Element {
	return uikit.LinkButton(c, label).Children(func() {
		ui.Text(c, label).FontSize(uikit.Scaled(uikit.TextMeta))
	})
}

// labelled names a sample for the export's pointer and keyboard.
func labelled(e ui.Element, label string) ui.Element { return e.Label(label) }

// tipRoom leaves the height a tooltip needs above the row under the pointer,
// so the plate does not hide the row above it.
func tipRoom(c *ui.Context) {
	ui.Box(c).Height(c.Theme().Space(7)).Shrink(0)
}

func caption(c *ui.Context, s string) {
	uikit.Hint(c, s)
}

func (k *components) textPlate(c *ui.Context) {
	rows := []struct {
		role   string
		sample func()
	}{
		{"Heading", func() { uikit.Heading(c, "POST /v1/projects") }},
		{"Body", func() { uikit.Body(c, "The request was replayed with the draft's headers.") }},
		{"Prose", func() {
			uikit.Prose(c, "A paragraph takes the looser leading long sentences and Chinese need; a body label keeps the font's own.")
		}},
		{"Mono", func() { uikit.Mono(c, "https://api.example.com/v1/projects?id=42") }},
		{"Hint", func() { uikit.Hint(c, "Captured traffic appears here once the proxy is on.") }},
		{"Meta", func() { uikit.Meta(c, "12 ms · 4.2 KB · 15:04:05") }},
		{"Label", func() { uikit.Label(c, "Headers") }},
	}
	for _, row := range rows {
		ui.Row(c).MinHeight(40).Gap(c.Theme().Space(4)).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			uikit.Meta(c, row.role).Width(96)
			row.sample()
		})
	}
	caption(c, "No states: a text is a role, not a control.")
}

func (k *components) buttonPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.PrimaryButton(c, "Send")
		uikit.SecondaryButton(c, "Copy as cURL")
		uikit.DangerButton(c, "Delete")
		uikit.CompactButton(c, "Copy")
		link(c, "+ Add property")
	})
	stateRow(c, "Hover", func() { labelled(uikit.PrimaryButton(c, "Send"), hoverButtons) })
	stateRow(c, "Pressed", func() { uikit.PrimaryButton(c, "Send") })
	stateRow(c, "Focus", func() { labelled(uikit.PrimaryButton(c, "Send"), focusButtons) })
	stateRow(c, "Link", func() {
		link(c, "+ Add property")
		link(c, "Reset to the saved value")
	})
	stateRow(c, "Disabled", func() {
		uikit.PrimaryButton(c, "Send").Disabled(true)
		uikit.SecondaryButton(c, "Copy as cURL").Disabled(true)
		uikit.DangerButton(c, "Delete").Disabled(true)
		uikit.CompactButton(c, "Copy").Disabled(true)
		link(c, "+ Add property").Disabled(true)
	})
	caption(c, "Two heights: 28 for forms, toolbars and the one primary action, 22 for inline actions, chips and the footer. A link is text at 22 with no face until the pointer arrives. Hover and pressed show under the pointer; focus needs the keyboard (Tab). No loading state: the kit has none, so a screen disables the button and shows progress beside it.")
}

func (k *components) quietPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.ToolbarButton(c, "Breakpoints", "Pause matching requests", false).Children(func() { ui.Text(c, "Breakpoints") })
		uikit.FooterButton(c, "Clear", "Clear the capture", false)
		uikit.IconButton(c, "Settings", "Settings", lucide.Settings())
		uikit.ChipButton(c, "Commands", "Search every action").Children(func() {
			ui.Icon(c, lucide.Search()).Size(uikit.IconInline, uikit.IconInline).TextColor(c.Theme().TextMuted)
			uikit.Meta(c, "⌘K")
		})
		ui.Column(c).Width(280).Children(func() {
			uikit.RowButton(c, "List projects").Justify(ui.Start).Children(func() { uikit.Body(c, "List projects") })
		})
		uikit.SwatchButton(c, "Mark red", uikit.MarkColors[0].Color, false)
	})
	tipRoom(c)
	stateRow(c, "Hover", func() {
		labelled(uikit.ToolbarButton(c, "Breakpoints", "Pause matching requests", false).Children(func() {
			ui.Text(c, "Breakpoints")
		}), hoverQuiet)
	})
	stateRow(c, "Pressed", func() {
		uikit.ToolbarButton(c, "Breakpoints", "Pause matching requests", false).Children(func() { ui.Text(c, "Breakpoints") })
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.ToolbarButton(c, "Breakpoints", "Pause matching requests", false).Children(func() {
			ui.Text(c, "Breakpoints")
		}), focusQuiet)
	})
	stateRow(c, "On", func() {
		uikit.ToolbarButton(c, "Breakpoints on", "Pause matching requests", true).Children(func() { ui.Text(c, "Breakpoints on") })
		uikit.FooterButton(c, "Auto-follow", "Follow the newest request", true)
		uikit.SidebarButton(c, "Show all domains", "", lucide.ChevronRight(), false)
	})
	stateRow(c, "Selected", func() {
		ui.Column(c).Width(280).Children(func() {
			uikit.RowButton(c, "List projects").Justify(ui.Start).Children(func() { uikit.Body(c, "List projects") })
		})
		uikit.SwatchButton(c, "Mark red", uikit.MarkColors[0].Color, true)
		uikit.SwatchButton(c, "Mark green", uikit.MarkColors[3].Color, false)
	})
	stateRow(c, "Disabled", func() {
		ui.Row(c).Disabled(true).Gap(c.Theme().Space(2)).Children(func() {
			uikit.ToolbarButton(c, "Breakpoints", "Unavailable", false).Children(func() { ui.Text(c, "Breakpoints") })
			uikit.IconButton(c, "Settings", "Unavailable", lucide.Settings())
			uikit.CompactButton(c, "Copy")
		})
	})
	caption(c, "A row button has no face at rest or under the pointer, as a macOS list row: it only shows the press. The swatch shows a check when chosen and an outline under the pointer.")
}

func (k *components) inputPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.Input(c, &k.host).Label("Host").Placeholder("api.example.com").Width(240)
		uikit.SearchField(c, &k.search, "Search host or path", "Search…", false).Width(240)
		uikit.Select(c, &k.format, []string{"Pretty", "Raw", "Preview"}).Width(140)
		uikit.SelectLabels(c, &k.labels, []string{"Pretty", "Raw"}, func(s string) string { return s }).Width(140)
	})
	stateRow(c, "Hover", func() {
		labelled(uikit.Input(c, &k.host).Label("Host").Width(240), hoverInputs)
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.Input(c, &k.host).Label("Host").Width(240), focusInputs)
	})
	stateRow(c, "Empty", func() {
		uikit.Input(c, &k.empty).Label("Empty").Placeholder("Search host or path").Width(240)
	})
	stateRow(c, "Disabled", func() {
		uikit.Input(c, &k.host).Label("Disabled").Width(240).Disabled(true)
	})
	stateRow(c, "On chrome", func() {
		ui.Column(c).Width(280).Padding(c.Theme().Space(2)).Radius(uikit.RadiusPanel).
			Background(uikit.Colors(c.Theme()).Chrome).Children(func() {
			uikit.SearchField(c, &k.searchChrome, "Search collections", "Search…", true)
		})
	})
	caption(c, "A field on the canvas is a well; on chrome it is the canvas with a hairline. Focus draws a neutral edge and an accent tint halo. No hover or error state: a field has no hover face, and the kit reports errors in text under it.")
}

func (k *components) tabPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.Tabs(c, &k.tab, "Request", "Params", "Headers", "Body", "Auth")
	})
	stateRow(c, "Counted", func() {
		uikit.CountedTabs(c, &k.counted, "Response", []string{"Body", "Headers", "Cookies", "Timing"}, []string{"", "5", "", ""})
	})
	stateRow(c, "Sub-tabs", func() {
		uikit.SubTabs(c, &k.sub, "Body", "Pretty", "Raw", "Preview")
	})
	stateRow(c, "Hover", func() {
		labelled(uikit.Tabs(c, &k.tab, "Request", "Params", "Headers", "Body", "Auth"), hoverTabs)
	})
	stateRow(c, "Pressed", func() {
		uikit.Tabs(c, &k.tab, "Request", "Params", "Headers", "Body", "Auth")
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.Tabs(c, &k.tab, "Request", "Params", "Headers", "Body", "Auth"), focusTabs)
	})
	stateRow(c, "Scrolling", func() {
		uikit.TabStrip(c, func() {
			for _, label := range []string{"Body", "Headers", "Cookies", "Timing", "Raw", "Checks", "Console"} {
				uikit.Body(c, label).Padding(0, c.Theme().Space(2))
			}
		}).Width(320)
	})
	caption(c, "The chosen tab is a neutral chip; a sub-tab is accent text only. The strip fades the edge that hides more tabs.")
}

func (k *components) segmentedPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.Segmented(c, &k.segment, "Apps", "Domains")
		uikit.Segmented(c, &k.segment, "All", "HTTP", "HTTPS", "WS", "SSE")
	})
	stateRow(c, "Hover", func() { labelled(uikit.Segmented(c, &k.segment, "Apps", "Domains"), hoverSegmented) })
	stateRow(c, "Pressed", func() { uikit.Segmented(c, &k.segment, "Apps", "Domains") })
	stateRow(c, "Focus", func() { labelled(uikit.Segmented(c, &k.segment, "Apps", "Domains"), focusSegmented) })
	caption(c, "A segmented control chooses a mode; tabs switch what a pane shows.")
}

func (k *components) menuPlate(c *ui.Context) {
	stateRow(c, "Menu select", func() {
		uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) {
			m.Item("Pretty").ID("pretty")
			m.Item("Raw").ID("raw")
			m.Item("Preview").ID("preview")
		}).Width(140)
		uikit.MenuSelect(c, "Local", func(m *ui.Menu) {
			m.Item("Local").ID("local")
			m.Item("Staging").ID("staging")
		}).Width(140)
	})
	stateRow(c, "Hover", func() {
		uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) { m.Item("Pretty").ID("pretty") }).Width(140)
	})
	stateRow(c, "Pressed", func() {
		uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) { m.Item("Pretty").ID("pretty") }).Width(140)
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) { m.Item("Pretty").ID("pretty") }).Width(140), focusMenus)
	})
	stateRow(c, "⋯ buttons", func() {
		uikit.MoreButton(c, "More actions", "More actions", func(m *ui.Menu) { m.Item("Export").ID("export") })
		uikit.MoreMenu(c, "Pane options", func(m *ui.Menu) { m.Item("Copy").ID("copy") })
		uikit.TabMenu(c, "More", "More tabs", func(m *ui.Menu) { m.Item("Raw").ID("raw") })
	})
	tipRoom(c)
	stateRow(c, "Tooltip", func() {
		labelled(uikit.Tip(c, uikit.SecondaryButton(c, "Hover for a tip"), "Copy as cURL · Ctrl+C").Width(180), hoverMenus)
	})
	caption(c, "Menus, popovers and tooltips wear the same panel: canvas, a hairline and the shadow, rising 4 points. A tooltip waits for the pointer; the export advances the clock to show it.")
}

func (k *components) panePlate(c *ui.Context) {
	stateRow(c, "Pane header", func() {
		ui.Column(c).Width(680).Border(1, c.Theme().Border).Radius(uikit.RadiusPanel).Children(func() {
			uikit.PaneHeader(c, func() {
				uikit.PaneTitle(c, "Request", "Request pane", ui.Color{}, c.Theme().Text, nil)
				uikit.CountedTabs(c, &k.tab, "Request pane", []string{"Headers", "Params", "Body", "Auth"}, []string{"5"})
			}, func() {
				uikit.TabMenu(c, "More", "Request pane options", func(m *ui.Menu) { m.Item("Raw").ID("raw") })
			})
			uikit.PaneHeader(c, func() {
				uikit.PaneTitle(c, "500 Internal Server Error", "Response pane", uikit.StatusColor(500, c.Theme()), c.Theme().Danger, nil)
				uikit.CountedTabs(c, &k.counted, "Response pane", []string{"Body", "Headers", "Cookies", "Timing"}, nil)
			}, func() {
				uikit.ResponseMeta(c, "1242 ms", "1.8 KB · JSON", uikit.Timing{Phases: []uikit.TimingPhase{
					{Name: "DNS", Millis: 3, Color: uikit.Colors(c.Theme()).Info},
					{Name: "Connect", Millis: 41, Color: uikit.Colors(c.Theme()).Socket},
					{Name: "Waiting", Millis: 1180, Color: uikit.Colors(c.Theme()).Warning},
					{Name: "Download", Millis: 18, Color: uikit.Colors(c.Theme()).MethodPost},
				}})
			})
		})
	})
	tipRoom(c)
	stateRow(c, "Hover", func() {
		labelled(ui.Column(c).Width(680).Border(1, c.Theme().Border).Radius(uikit.RadiusPanel).Children(func() {
			uikit.PaneHeader(c, func() {
				uikit.PaneTitle(c, "Request", "Request pane", ui.Color{}, c.Theme().Text, func() {})
			}, nil)
		}), hoverPanes)
	})
	stateRow(c, "Focus", func() {
		labelled(ui.Column(c).Width(680).Children(func() {
			uikit.PaneHeader(c, func() {
				uikit.PaneTitle(c, "Request", "Request pane", ui.Color{}, c.Theme().Text, nil)
			}, nil)
		}), focusPanes)
	})
	caption(c, "One row per pane: a title, its tabs, then what it reports at the far end. Hover the title to see its quiet face; a click focuses the pane.")
}

func (k *components) sidebarPlate(c *ui.Context) {
	open := true
	ui.Row(c).Gap(c.Theme().Space(4)).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Width(240).Padding(c.Theme().Space(1)).Gap(0).Height(300).
			Background(uikit.Colors(c.Theme()).Chrome).Children(func() {
			uikit.SidebarList(c, "gallery-sidebar", &k.sidebar, func(string) {}, func() {
				uikit.SidebarHeading(c, "Traffic", &open, nil)
				uikit.SidebarItem(c, "projects", lucide.LayoutList(), "All requests", k.sidebar == "projects", true).Children(func() { uikit.Meta(c, "1,159") })
				uikit.SidebarItem(c, "errors", lucide.CircleAlert(), "Errors", k.sidebar == "errors", true).Children(func() { uikit.Meta(c, "73") })
				uikit.SidebarItem(c, "slow", lucide.Clock(), "Slow", k.sidebar == "slow", true).Children(func() { uikit.Meta(c, "35") })
				uikit.SidebarItemLetter(c, "api", "api.example.com", k.sidebar == "api", true).Children(func() { uikit.Meta(c, "12") })
				uikit.SidebarItemImage(c, "app", k.icon, "Safari", k.sidebar == "app", true)
				uikit.SidebarHeading(c, "Sources", &open, nil)
				uikit.SidebarButton(c, "Show all domains", "", lucide.ChevronRight(), false)
			})
		})
		ui.Column(c).Grow(1).Gap(c.Theme().Space(2)).Children(func() {
			caption(c, "Hover a row to see it take the body color; the chosen row keeps the tint when the window is behind, and the neutral fill when it is not.")
			stateRow(c, "Chosen", func() {
				ui.Column(c).Width(220).Children(func() {
					uikit.SidebarItem(c, "row", lucide.LayoutList(), "All requests", true, false).Children(func() { uikit.Meta(c, "1,159") })
				})
			})
			stateRow(c, "Hover", func() {
				labelled(ui.Column(c).Width(220).Children(func() {
					uikit.SidebarItem(c, "row2", lucide.LayoutList(), "All requests", false, true)
				}), hoverSidebar)
			})
			caption(c, "The list moves with the arrows, Home, End and the first letters of a row; it scrolls with the region around it. No focus state: the list draws no ring, as in the sidebar, and the keyboard shows on the chosen row.")
		})
	})
}

func (k *components) activityPlate(c *ui.Context) {
	buckets := make([]uikit.ActivityBucket, 60)
	for i := range buckets {
		switch {
		case i%17 == 3:
			buckets[i] = uikit.ActivityBucket{2, 0, 1, 1, 0}
		case i%9 == 0:
			buckets[i] = uikit.ActivityBucket{0, 1, 0, 0, 0}
		case i%5 == 0:
			buckets[i] = uikit.ActivityBucket{3, 0, 0, 0, 1}
		case i%3 == 0:
			buckets[i] = uikit.ActivityBucket{5, 0, 0, 0, 0}
		}
	}
	stateRow(c, "Rest", func() {
		ui.Column(c).Width(640).Children(func() {
			var none uikit.ActivityRange
			uikit.ActivityStrip(c, buckets, &none, "20:09:30", func() {})
		})
	})
	stateRow(c, "Brushed", func() {
		ui.Column(c).Width(640).Children(func() {
			uikit.ActivityStrip(c, buckets, &k.range_, "20:09:30", func() {})
		})
	})
	stateRow(c, "Hover", func() {
		labelled(ui.Column(c).Width(640).Children(func() {
			var none uikit.ActivityRange
			uikit.ActivityStrip(c, buckets, &none, "20:09:30", func() {})
		}), hoverActivity)
	})
	stateRow(c, "Focus", func() {
		labelled(ui.Column(c).Width(640).Children(func() {
			var none uikit.ActivityRange
			uikit.ActivityStrip(c, buckets, &none, "20:09:30", func() {})
		}), focusActivity)
	})
	caption(c, "The axis is always the last five minutes. Drag the plot to brush a range, drag a handle to resize it, and press Escape to clear it. The bars stack normal, 3xx, 4xx, 5xx and slow.")
}

func (k *components) statusPlate(c *ui.Context) {
	t := c.Theme()
	stateRow(c, "Status", func() {
		for _, s := range []struct {
			label  string
			status int
		}{{"200 OK", 200}, {"304", 304}, {"302 Found", 302}, {"404 Not Found", 404}, {"500 Error", 500}} {
			uikit.StatusPill(c, s.label, uikit.StatusColor(s.status, t))
		}
	})
	stateRow(c, "Methods", func() {
		for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "WS", "HEAD"} {
			uikit.MethodLabel(c, m)
		}
	})
	stateRow(c, "Response meta", func() {
		ui.Column(c).Width(260).Children(func() { uikit.ResponseMeta(c, "1242 ms", "1.8 KB · JSON", uikit.Timing{}) })
		ui.Column(c).Width(160).Children(func() { uikit.ResponseMeta(c, "47 ms", "83 B", uikit.Timing{}) })
	})
	stateRow(c, "Sparkline", func() {
		uikit.Sparkline(c, []int{2, 5, 3, 8, 4, 6, 9, 3, 7}, t.TextMuted)
		uikit.Sparkline(c, []int{1, 2, 2, 3, 5, 8, 13, 21, 34}, t.Success)
	})
	stateRow(c, "Glyph", func() {
		uikit.Glyph(c, lucide.Search())
		uikit.Glyph(c, lucide.CircleAlert())
		uikit.Body(c, "a quiet mark beside a label")
	})
	stateRow(c, "Chips", func() {
		uikit.QueryChip(c, "host:api.example.com", func() {}, func() {})
		uikit.QueryChip(c, "status:4xx", nil, func() {})
		uikit.QueryChip(c, "method:POST", func() {}, func() {})
	})
	tipRoom(c)
	stateRow(c, "Hover", func() {
		labelled(uikit.QueryChip(c, "host:api.example.com", func() {}, func() {}), hoverStatus)
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.QueryChip(c, "method:POST", func() {}, func() {}), focusStatus)
	})
	caption(c, "Status and method colors only ink text; nothing is filled. A query chip is a neutral face because a filter is not a selection.")
}

func (k *components) emptyPlate(c *ui.Context) {
	ui.Row(c).Children(func() {
		ui.Column(c).Width(520).Height(240).Border(1, c.Theme().Border).Radius(uikit.RadiusPanel).Children(func() {
			uikit.EmptyState(c, "No requests yet", "Enable the system proxy to start capturing.", func() {
				ui.Row(c).Gap(c.Theme().Space(2)).Children(func() {
					uikit.PrimaryButton(c, "Enable system proxy")
					uikit.SecondaryButton(c, "Client setup")
				})
			})
		})
	})
	caption(c, "The icon is 16 points and unframed; the title is a heading, the sentence a hint, and the actions are the screen's own.")
}

func (k *components) panelPlate(c *ui.Context) {
	stateRow(c, "Popover", func() {
		uikit.PopoverPanel(c, ui.Column(c)).Padding(c.Theme().Space(3)).Width(260).Gap(c.Theme().Space(1)).Children(func() {
			uikit.Body(c, "Popover panel")
			uikit.Meta(c, "Menus, popovers and toasts")
		})
	})
	stateRow(c, "Dialog", func() {
		ui.Column(c).Width(420).Padding(c.Theme().Space(4)).Gap(c.Theme().Space(3)).Radius(uikit.RadiusPanel).
			Background(uikit.Colors(c.Theme()).Canvas).InsetShadow(0, 0, 0, 1, uikit.Colors(c.Theme()).Hairline).
			Shadow(0, 20, 56, 0, uikit.Colors(c.Theme()).Shadow).Children(func() {
			uikit.DialogTitle(c, "Rename request")
			uikit.Input(c, &k.host).Label("Name")
			uikit.DialogFooter(c).Children(func() {
				uikit.SecondaryButton(c, "Cancel")
				uikit.PrimaryButton(c, "Rename")
			})
		})
	})
	stateRow(c, "Modal", func() {
		labelled(uikit.SecondaryButton(c, "Open dialog"), hoverPanels).OnClick(func() { k.open = true })
		uikit.Modal(c, &k.open, func() {
			uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
				uikit.DialogTitle(c, "Delete 3 requests?")
				uikit.Hint(c, "They leave this collection; captured traffic is not touched.")
				uikit.DialogFooter(c).Children(func() {
					uikit.SecondaryButton(c, "Cancel")
					uikit.DangerButton(c, "Delete")
				})
			})
		})
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.SecondaryButton(c, "Open dialog"), focusPanels)
	})
	caption(c, "Modal adds the backdrop, the focus trap and Escape; the column, title and footer are the kit's. The dialog opens over the plate.")
}

func (k *components) settingsPlate(c *ui.Context) {
	uikit.SettingsPage(c, "Capture", "What the proxy keeps and what it drops.", func() {
		uikit.SettingsGroup(c, "Storage", func() {
			uikit.SettingsRow(c, "Flow limit", func() ui.Element {
				return labelled(uikit.Input(c, &k.host).Placeholder("20000").Width(160), hoverSettings)
			}, "Rows kept before the oldest spill to disk.")
			uikit.SettingsRow(c, "Memory limit", func() ui.Element {
				return uikit.Input(c, &k.empty).Placeholder("512 MB").Width(160)
			}, "Bodies above this are read from disk.")
		})
		uikit.SettingsGroup(c, "Behavior", func() {
			uikit.SettingsRow(c, "Keep traffic", func() ui.Element {
				return ui.Switch(c, &k.on).Label("Keep traffic")
			}, "")
			uikit.SettingsRowMultiline(c, "Bypass list", func() ui.Element {
				return ui.Column(c).Gap(c.Theme().Space(2)).Children(func() {
					labelled(ui.Checkbox(c, &k.check, "Pause on start"), focusSettings)
					ui.TextArea(c, &k.bypass).Height(54).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Disabled(true)
				})
			}, "One host per line; these connect directly.")
		})
	})
	caption(c, "A settings page keeps a reading width; its groups are headings and rules, not cards. A row lines its label up with the control, and a multiline row with the control's first line.")
}

func (k *components) toastPlate(c *ui.Context) {
	if !k.toasts {
		k.toasts = true
		c.AddToast(ui.Toast{Title: "Copied as cURL", Type: "success"})
		c.AddToast(ui.Toast{Title: "Certificate is not trusted", Description: "Install it to read HTTPS.", Type: "warning"})
		c.AddToast(ui.Toast{Title: "The proxy stopped", Description: "Port 7890 is in use.", Type: "error", Timeout: -1})
	}
	stateRow(c, "Add", func() {
		labelled(uikit.SecondaryButton(c, "Show a toast"), hoverToasts).OnClick(func() {
			c.AddToast(ui.Toast{Title: "Saved to the collection", Type: "success"})
		})
		labelled(uikit.SecondaryButton(c, "Show an error"), focusToasts).OnClick(func() {
			c.AddToast(ui.Toast{Title: "The request failed", Description: "dial tcp: connection refused", Type: "error"})
		})
	})
	caption(c, "A toast is a popover panel above the footer, with an icon in its kind's signal color. Three show at once; the pointer or the keyboard holds them.")
}

func (k *components) listPlate(c *ui.Context) {
	row := func(focused bool, label string) {
		theme := uikit.ListTheme(c.Theme(), focused)
		ui.Themed(c, theme).Background(ui.Transparent).FillWidth().Children(func() {
			box := ui.Row(c).Height(uikit.HeightControl).FillWidth().Padding(0, c.Theme().Space(2)).AlignItems(ui.Center).Gap(c.Theme().Space(2)).
				Background(c.Theme().Accent)
			if label != "" {
				box.Label(label)
			}
			uikit.RowMarker(c, focused)
			box.Children(func() {
				uikit.MethodLabel(c, "GET")
				uikit.Mono(c, "/v1/projects").Grow(1)
				uikit.Meta(c, "200 · 47 ms")
			})
		})
	}
	stateRow(c, "Focused", func() {
		ui.Column(c).Width(560).Children(func() {
			row(true, "")
			ui.Column(c).FillWidth().Background(uikit.Colors(c.Theme()).Canvas).Children(func() {
				uikit.KVRow(c, func() {
					ui.Text(c, "Accept").Font(uikit.FontMono).Width(140).TextColor(c.Theme().TextMuted)
					ui.Text(c, "application/json").Font(uikit.FontMono)
				})
			})
		})
	})
	stateRow(c, "Unfocused", func() {
		ui.Column(c).Width(560).Children(func() { row(false, "") })
	})
	stateRow(c, "Hover", func() {
		ui.Column(c).Width(560).Children(func() { row(true, hoverLists) })
	})
	stateRow(c, "Selected row", func() {
		ui.Column(c).Width(240).Children(func() {
			uikit.Selected(c, uikit.RowButton(c, "Row"), true).Children(func() { uikit.Body(c, "Chosen row") })
			uikit.Selected(c, uikit.RowButton(c, "Row behind"), false).Children(func() { uikit.Body(c, "Chosen, window behind") })
		})
	})
	caption(c, "A data row takes a 2-point marker at its leading edge and the list's tint; a chosen place is a rounded block. Unfocused rows keep the shape and turn neutral.")
}

// iconBitmap is a small app-icon stand-in for SidebarItemImage: a bitmap with
// the app icon's proportions, so the row shows the real code path.
func iconBitmap() ui.ImageSource {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, color.RGBA{R: uint8(20 + x*6), G: uint8(90 + y*4), B: 180, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(fmt.Errorf("gallery icon: %w", err))
	}
	bmp, err := ui.DecodeBitmap(b.Bytes())
	if err != nil {
		panic(fmt.Errorf("gallery icon: %w", err))
	}
	return bmp
}
