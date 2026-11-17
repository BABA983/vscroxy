package gallery

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"

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
	hoverTooltip   = "Hover Tooltip"
	hoverCard      = "Hover Card"
	hoverChecks    = "Hover Checks"
	focusChecks    = "Focus Checks"
)

// components is the state every component plate keeps across frames: the
// values its fields and choices bind to.
type components struct {
	host, empty, search, searchChrome string
	invalid                           string
	bypass                            string
	format, pick, labels              string
	query, flow                       string
	notes, port, badPort              string
	disabledNotes, combo              string
	portValue, badPortValue           float64
	tab, counted, sub, segment        int
	sidebar                           string
	range_                            uikit.ActivityRange
	open                              bool
	on, off, check, hovered, focused  bool
	choice                            string
	icon                              ui.ImageSource
}

// componentsGroup is the kit's public components, one plate per family, each
// listing the states it has. Hover and pressed are drawn by the pointer: the
// window shows them as the pointer crosses them, and the export puts the
// tester's pointer on the named sample.
func (g *Gallery) components() *Group {
	k := &components{host: "api.example.com", choice: "Medium", bypass: "localhost\n*.internal.test\n10.0.0.0/8", format: "Pretty", pick: "all", labels: "Pretty", query: "", flow: "20000", notes: "api.example.com\nlocalhost:3000", disabledNotes: "localhost:3000", combo: "Medium", port: "8080", portValue: 8080, badPort: "80", badPortValue: 80, on: true, sidebar: "projects", range_: uikit.ActivityRange{From: 22, To: 34}}
	k.icon = iconBitmap()
	return &Group{ID: "components", Title: "Components", Entries: []*Entry{
		{ID: "text", Title: "Text", Note: "Nine roles, including the error sentence and multi-line data; a text has no states", Height: 520, build: k.textPlate},
		{ID: "buttons", Title: "Buttons", Note: "Primary, secondary, destructive and compact, with their faces", Height: 440, Hover: hoverButtons, Focus: focusButtons, build: k.buttonPlate},
		{ID: "buttons-quiet", Title: "Quiet buttons", Note: "Toolbar, footer, icon, chip, row and swatch", Height: 560, Hover: hoverQuiet, Focus: focusQuiet, build: k.quietPlate},
		{ID: "inputs", Title: "Inputs", Note: "Fields, focus, invalid, search and the two selects", Height: 680, Hover: hoverInputs, Focus: focusInputs, build: k.inputPlate},
		{ID: "tabs", Title: "Tabs", Note: "Tabs, counted tabs, sub-tabs and a scrolling strip", Height: 440, Hover: hoverTabs, Focus: focusTabs, build: k.tabPlate},
		{ID: "segmented", Title: "Segmented", Note: "A recessed groove and a raised thumb", Height: 280, Hover: hoverSegmented, Focus: focusSegmented, build: k.segmentedPlate},
		{ID: "menus", Title: "Menus", Note: "A 28-point row, the menu shadow and keycaps", Height: 560, Hover: hoverMenus, Focus: focusMenus, Tip: true, build: k.menuPlate},
		{ID: "checks", Title: "Checkbox and radio", Note: "Boxes wear a control face: rest, hover, checked, focus and disabled", Height: 420, Hover: hoverChecks, Focus: focusChecks, build: k.checkPlate},
		{ID: "feedback", Title: "Feedback", Note: "Spinner, progress, callouts, badges and a primary button while it is busy", Height: 600, build: k.feedbackPlate},
		{ID: "kbd", Title: "Kbd", Note: "Shortcuts as 4-point keycaps on a hairline", Height: 280, build: k.kbdPlate},
		{ID: "tooltip", Title: "Tooltip", Note: "The one tip face: small text, a shortcut as Kbd, a long URL", Height: 380, Hover: hoverTooltip, Tip: true, build: k.tooltipPlate},
		{ID: "hover-card", Title: "Hover card", Note: "A title, one meta line and key/value rows over a capture row", Height: 340, Hover: hoverCard, Tip: true, build: k.hoverCardPlate},
		{ID: "panes", Title: "Pane headers", Note: "A title, its tabs and what the pane reports", Height: 380, Hover: hoverPanes, Focus: focusPanes, Tip: true, build: k.panePlate},
		{ID: "sidebar", Title: "Sidebar", Note: "Rows, headings, letters and the action row", Height: 600, Hover: hoverSidebar, build: k.sidebarPlate},
		{ID: "activity", Title: "Activity strip", Note: "Rest and a brushed range; drag the plot to brush", Height: 300, Hover: hoverActivity, Focus: focusActivity, build: k.activityPlate},
		{ID: "status", Title: "Status and methods", Note: "Pills, method inks, response meta, sparkline, chip", Height: 560, Hover: hoverStatus, Focus: focusStatus, build: k.statusPlate},
		{ID: "empty", Title: "Empty state", Note: "One icon, a title, a sentence and the actions", Height: 340, build: k.emptyPlate},
		{ID: "panels", Title: "Panels and dialogs", Note: "Popover, 12/16-point panels and the dialog scrim", Height: 520, Hover: hoverPanels, Focus: focusPanels, build: k.panelPlate},
		{ID: "dialog", Title: "Dialog", Note: "The modal open: title, scrim and the dialog shadow", Height: 520, Click: "Open dialog", build: k.dialogPlate},
		{ID: "command-palette-loose", Title: "Command palette", Note: "720 wide, a 48-point input and 40-point result rows", Height: 620, build: k.commandPalettePlate},
		{ID: "settings", Title: "Settings form", Note: "A page, its groups, SettingsRow and its multiline row", Height: 560, Hover: hoverSettings, Focus: focusSettings, build: k.settingsPlate},
		{ID: "settings-cards", Title: "Settings cards", Note: "The 620-wide card skeleton with 44-point rows", Height: 640, build: k.settingsCardPlate},
		{ID: "toasts", Title: "Toasts", Note: "16-point corners, the popover shadow and a live action", Height: 320, Hover: hoverToasts, Focus: focusToasts, build: k.toastPlate},
		{ID: "lists", Title: "List selection", Note: "Selected rows, the marker and a list's theme", Height: 440, Hover: hoverLists, build: k.listPlate},
	}}
}

// stateRow is one state of a plate: its name in the margin, then the sample.
// The sample that is hovered or focused carries the label the export finds,
// so the pointer and the keyboard land on the control itself.
func stateRow(c *ui.Context, state string, sample func()) {
	t := c.Theme()
	// A row reserves a control's height and a gap below it, so the controls of
	// one state never overrun the next.
	ui.Row(c).MinHeight(uikit.HeightControl).Padding(0, 0, t.Space(uikit.SpaceS), 0).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Start).Children(func() {
		uikit.Meta(c, state).Width(96).Shrink(0).Padding(0, 0, 0, 0)
		ui.Row(c).Grow(1).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Start).Children(sample)
	})
}

// link is the kit's LinkButton, which paints its own label.
func link(c *ui.Context, label string) ui.Element {
	return uikit.LinkButton(c, label)
}

// addLink is the LinkButton with the same plus a table's add row uses.
func addLink(c *ui.Context, label string) ui.Element {
	return uikit.LinkButton(c, label, lucide.Plus())
}

// labelled names a sample for the export's pointer and keyboard.
func labelled(e ui.Element, label string) ui.Element { return e.Label(label) }

// tipRoom leaves the height a tooltip needs above the row under the pointer,
// so the plate does not hide the row above it.
func tipRoom(c *ui.Context) {
	ui.Box(c).Height(uikit.HeightControl).Shrink(0)
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
		{"ErrorText", func() { uikit.ErrorText(c, "The proxy could not bind 127.0.0.1:7890.") }},
		{"Code", func() { uikit.Code(c, "Content-Type: application/json\nX-Request-Id: 7f3a9c") }},
	}
	for _, row := range rows {
		ui.Row(c).MinHeight(40).Gap(c.Theme().Space(uikit.SpaceXL)).AlignItems(ui.Center).
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
		uikit.CompactButton(c, "Copy")
		addLink(c, "Add property")
	})
	// The plate draws the pointer faces itself, so they are visible before the
	// pointer arrives; the export still puts the pointer on Hover.
	stateRow(c, "Hover", func() { labelled(uikit.PrimaryButtonState(c, "Send", uikit.ButtonHover), hoverButtons) })
	stateRow(c, "Pressed", func() { uikit.PrimaryButtonState(c, "Send", uikit.ButtonPressed) })
	stateRow(c, "Focus", func() { labelled(uikit.PrimaryButton(c, "Send"), focusButtons) })
	stateRow(c, "Link", func() {
		addLink(c, "Add property")
		link(c, "Reset to the saved value")
	})
	stateRow(c, "Disabled", func() {
		uikit.PrimaryButton(c, "Send").Disabled(true)
		uikit.SecondaryButton(c, "Copy as cURL").Disabled(true)
		uikit.CompactButton(c, "Copy").Disabled(true)
		addLink(c, "Add property").Disabled(true)
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
		ui.Row(c).Disabled(true).Gap(c.Theme().Space(uikit.SpaceM)).Children(func() {
			uikit.ToolbarButton(c, "Breakpoints", "Unavailable", false).Children(func() { ui.Text(c, "Breakpoints") })
			uikit.IconButton(c, "Settings", "Unavailable", lucide.Settings())
			uikit.CompactButton(c, "Copy")
		})
	})
	caption(c, "A row button has no face at rest or under the pointer, as a macOS list row: it only shows the press. The swatch shows a check when chosen and an outline under the pointer.")
}

func (k *components) inputPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.Input(c, &k.host).Label("Host").Placeholder("api.example.com").Width(uikit.WidthWide)
		uikit.SearchField(c, &k.search, "Search host or path", "Search…", false).Width(uikit.WidthWide)
		uikit.Select(c, &k.format, []string{"Pretty", "Raw", "Preview"}).Width(uikit.WidthPicker)
		uikit.SelectLabels(c, &k.labels, []string{"Pretty", "Raw"}, func(s string) string { return s }).Width(uikit.WidthPicker)
	})
	stateRow(c, "Hover", func() {
		labelled(uikit.Input(c, &k.host).Label("Host").Width(uikit.WidthWide), hoverInputs)
	})
	stateRow(c, "Focus", func() {
		// A field's focus face is uikit's, drawn from the field's own state:
		// force it here so the plate shows the edge and halo without the
		// keyboard, and Tab shows the same face when it reaches the field.
		labelled(uikit.FieldFace(c, uikit.InputControl(c, &k.host).Label("Host"), true).Width(uikit.WidthWide), focusInputs)
	})
	stateRow(c, "Empty", func() {
		uikit.Input(c, &k.empty).Label("Empty").Placeholder("Search host or path").Width(uikit.WidthWide)
	})
	stateRow(c, "Invalid", func() {
		ui.Form(c, func() {
			uikit.FormRow(c, "Port", "Blank is automatic; 1024–65535.", "Port must be 1024–65535.", func() {
				uikit.InputState(c, &k.invalid, true).Width(uikit.WidthWide)
			})
		})
	})
	stateRow(c, "Disabled", func() {
		uikit.Input(c, &k.host).Label("Disabled").Width(uikit.WidthWide).Disabled(true)
		uikit.Combobox(c, &k.combo, []string{"Medium", "High"}).Label("Disabled combobox").Width(uikit.WidthWide).Disabled(true)
		uikit.TextArea(c, &k.disabledNotes).Label("Disabled notes").Height(64).Width(uikit.WidthWide).Disabled(true)
	})
	stateRow(c, "Text area", func() {
		uikit.TextArea(c, &k.notes).Label("Notes").Placeholder("One host per line").Height(64).Width(uikit.WidthWide)
	})
	stateRow(c, "Number", func() {
		uikit.NumberField(c, &k.port, &k.portValue, 1024, 65535, "", "Port", "", nil)
	})
	stateRow(c, "Number invalid", func() {
		uikit.NumberField(c, &k.badPort, &k.badPortValue, 1024, 65535, "", "Port", "", nil)
	})
	stateRow(c, "On chrome", func() {
		ui.Column(c).Width(280).Padding(c.Theme().Space(uikit.SpaceM)).Radius(uikit.RadiusPanel).
			Background(uikit.Colors(c.Theme()).Chrome).Children(func() {
			uikit.SearchField(c, &k.searchChrome, "Search collections", "Search…", true)
		})
	})
	caption(c, "A field on the canvas is a well; on chrome it is the canvas with a hairline. Focus draws the ring edge and a ring halo; an invalid field draws the danger edge, and FormRow binds its description and error to the control. A field has no hover face.")
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
				uikit.Body(c, label).Padding(0, c.Theme().Space(uikit.SpaceM))
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
		}).Width(uikit.WidthPicker)
		uikit.MenuSelect(c, "Local", func(m *ui.Menu) {
			m.Item("Local").ID("local")
			m.Item("Staging").ID("staging")
		}).Width(uikit.WidthPicker)
	})
	stateRow(c, "Open menu", func() {
		uikit.MenuPanel(c, ui.Column(c)).Width(236).Padding(c.Theme().Space(uikit.SpaceXS)).Gap(0).Children(func() {
			menuRow(c, "Copy as cURL", "⌘C", false, false)
			menuRow(c, "Copy as fetch", "⇧⌘C", false, false)
			menuRow(c, "Replay", "⌘R", true, false)
			menuRow(c, "Delete", "⌫", false, true)
		})
	})
	stateRow(c, "Hover", func() {
		uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) { m.Item("Pretty").ID("pretty") }).Width(uikit.WidthPicker)
	})
	stateRow(c, "Pressed", func() {
		uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) { m.Item("Pretty").ID("pretty") }).Width(uikit.WidthPicker)
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.MenuSelect(c, "Pretty", func(m *ui.Menu) { m.Item("Pretty").ID("pretty") }).Width(uikit.WidthPicker), focusMenus)
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
	caption(c, "A menu is a 10-point canvas panel on the menu shadow; its rows are 28 points, and the shortcut is a Kbd, not plain muted text. Tooltips keep their inverse face and wait for the pointer.")
}

// menuRow is a drawn menu's row in the gallery: the real menus are MyGo's,
// and this is the face their items take when the app draws them.
func menuRow(c *ui.Context, label, shortcut string, selected, danger bool) {
	t, p := c.Theme(), uikit.Colors(c.Theme())
	row := ui.Row(c).Height(uikit.MenuRowHeight).FillWidth().Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).
		Radius(uikit.RadiusControl).AlignItems(ui.Center)
	color := t.Text
	if danger {
		color = p.Danger
	}
	if selected {
		row.Background(p.Accent)
		color = t.AccentText
	}
	row.Children(func() {
		ui.Text(c, label).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(color).Grow(1).SingleLine()
		uikit.Kbd(c, shortcut)
	})
}

func (k *components) kbdPlate(c *ui.Context) {
	t, p := c.Theme(), uikit.Colors(c.Theme())
	stateRow(c, "On canvas", func() {
		for _, key := range []string{"⌘K", "⇧⌘Z", "↩", "⌥←", "esc"} {
			uikit.Kbd(c, key)
		}
	})
	stateRow(c, "On chrome", func() {
		ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Radius(uikit.RadiusControl).Background(p.Chrome).Gap(t.Space(uikit.SpaceM)).Children(func() {
			for _, key := range []string{"⌘K", "⇧⌘Z", "⌥←"} {
				uikit.Kbd(c, key)
			}
		})
	})
	stateRow(c, "In a row", func() {
		ui.Column(c).Width(300).Children(func() {
			menuRow(c, "Open command palette", "⌘K", false, false)
			menuRow(c, "Copy as cURL", "⌘C", true, false)
		})
	})
	caption(c, "A Kbd is 18 points high with 4-point corners, a 1-point hairline and an 11-point label. It sits at the trailing edge of a menu or command row, never inside prose.")
}

func (k *components) tooltipPlate(c *ui.Context) {
	tipRoom(c)
	stateRow(c, "Text", func() {
		uikit.Tip(c, uikit.SecondaryButton(c, "Hover for a tip"), "Captured traffic appears here once the proxy is on.").Width(uikit.WidthWide)
	})
	stateRow(c, "Shortcut", func() {
		labelled(uikit.TipKeys(c, uikit.SecondaryButton(c, "Hide sidebar"), "Hide sidebar", "⌘", "B"), hoverTooltip)
	})
	stateRow(c, "Long URL", func() {
		uikit.Tip(c, uikit.SecondaryButton(c, "Hover a long URL"), "https://api.example.com/v1/projects?include=members&page=2&per_page=50").Width(uikit.WidthWide)
	})
	caption(c, "A tip is the canvas on the popover shadow, a 1-point hairline, 8-point corners and 6×10 of padding: 12-point text at the normal ink, up to 360 wide, with a shortcut as a Kbd cap. It waits 500ms and does not wait again when the pointer moves to its neighbour.")
}

func (k *components) hoverCardPlate(c *ui.Context) {
	tipRoom(c)
	stateRow(c, "Capture row", func() {
		row := ui.Row(c).Height(uikit.HeightRow).Padding(0, c.Theme().Space(uikit.SpaceM)).Gap(c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).
			Radius(uikit.RadiusControl).Background(uikit.Colors(c.Theme()).Fill).Children(func() {
			uikit.MethodLabel(c, "GET")
			uikit.Body(c, "List projects").SingleLine()
		})
		labelled(uikit.CardTip(c, row, uikit.HoverCard{
			Title: "GET /v1/projects",
			Meta:  "200 · 47 ms · 1.2 KB",
			Rows: []uikit.HoverCardRow{
				{Key: "URL", Value: "https://api.example.com/v1/projects?include=members&page=2"},
				{Key: "Host", Value: "api.example.com"},
				{Key: "Client", Value: "Safari"},
			},
		}), hoverCard)
	})
	// The card hangs below the row; leave it room so the caption stays clear.
	ui.Box(c).Height(uikit.WidthField).Shrink(0)
	caption(c, "A rich card adds a title, one meta line and key/value rows to the same surface. It is for what a row's width cannot show: the full URL, method, status and client of a captured request, or a saved request's address.")
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
	ui.Row(c).Gap(c.Theme().Space(uikit.SpaceXL)).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Width(uikit.WidthWide).Padding(c.Theme().Space(uikit.SpaceXS)).Gap(0).Height(300).
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
		ui.Column(c).Grow(1).Gap(c.Theme().Space(uikit.SpaceM)).Children(func() {
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
		ui.Column(c).Width(uikit.WidthField).Children(func() { uikit.ResponseMeta(c, "47 ms", "83 B", uikit.Timing{}) })
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
				ui.Row(c).Gap(c.Theme().Space(uikit.SpaceM)).Children(func() {
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
		uikit.PopoverPanel(c, ui.Column(c)).Padding(uikit.PopoverPadding).Width(260).Gap(c.Theme().Space(uikit.SpaceXS)).Children(func() {
			uikit.Body(c, "Popover panel")
			uikit.Small(c, "12-point corners and a 16-point inset")
		})
	})
	stateRow(c, "Dialog", func() {
		p := uikit.Colors(c.Theme())
		panel := ui.Column(c).Width(420).Padding(uikit.DialogPadding).Gap(uikit.DialogGap).Radius(uikit.RadiusDialog).
			Background(p.Canvas).InsetShadow(0, 0, 0, 1, p.Hairline)
		uikit.ShadowDialog.Apply(panel, c.Theme().Dark).Children(func() {
			uikit.DialogTitle(c, "Delete 3 requests?")
			uikit.Hint(c, "They leave this collection; captured traffic is not touched.")
			uikit.DialogFooter(c).Children(func() {
				uikit.SecondaryButton(c, "Cancel")
				uikit.DestructiveButton(c, "Delete")
			})
		})
	})
	stateRow(c, "Modal", func() {
		labelled(uikit.SecondaryButton(c, "Open dialog"), hoverPanels).OnClick(func() { k.open = true })
		uikit.Modal(c, &k.open, "Delete 3 requests?", func() {
			uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
				uikit.DialogTitle(c, "Delete 3 requests?")
				uikit.Hint(c, "They leave this collection; captured traffic is not touched.")
				uikit.DialogFooter(c).Children(func() {
					uikit.SecondaryButton(c, "Cancel")
				})
			})
		})
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.SecondaryButton(c, "Open dialog"), focusPanels)
	})
	caption(c, "Modal adds the 35% scrim, the focus trap and Escape; the column, title and footer are the kit's. The dialog is 16-point cornered on the dialog shadow.")
}

func (k *components) dialogPlate(c *ui.Context) {
	// The open state belongs to this tester, not to the plate's shared
	// components: the light export must not leave the dark one with a
	// dialog already open.
	open := ui.Local(c.Root(), "dialog-open", func() bool { return false })
	stateRow(c, "Modal", func() {
		labelled(uikit.SecondaryButton(c, "Open dialog"), "Open dialog").OnClick(func() { *open = true })
		uikit.Modal(c, open, "Delete 3 requests?", func() {
			uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
				uikit.DialogTitle(c, "Delete 3 requests?")
				uikit.Hint(c, "They leave this collection; captured traffic is not touched.")
				uikit.DialogFooter(c).Children(func() {
					uikit.SecondaryButton(c, "Cancel")
					uikit.DestructiveButton(c, "Delete")
				})
			})
		})
	})
	caption(c, "A destructive confirmation is the only place DestructiveButton appears: solid danger, one primary action, Cancel beside it.")
}

func (k *components) commandPalettePlate(c *ui.Context) {
	t, p := c.Theme(), uikit.Colors(c.Theme())
	key := func(key string) string {
		if runtime.GOOS == "darwin" {
			return "⌘" + key
		}
		return "Ctrl+" + key
	}
	enter, open := "Ctrl+Enter", "Enter"
	if runtime.GOOS == "darwin" {
		enter, open = "⌘↩", "↩"
	}
	ui.Row(c).Children(func() {
		uikit.PopoverPanel(c, ui.Column(c)).Width(uikit.CommandPaletteWidth).Padding(0).Gap(0).Children(func() {
			ui.Row(c).Height(uikit.CommandPaletteInputRow).Padding(0, t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, lucide.Search()).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.TextMuted)
				ui.TextInput(c, &k.query).Height(uikit.CommandPaletteInputRow).Padding(0).FontSize(uikit.Scaled(uikit.CommandPaletteInputSize)).
					Grow(1).MinWidth(0).FocusRing(false).Background(ui.Color{}).Border(0, ui.Color{}).
					Label("Find a command").Placeholder("Search commands, requests and environments")
				uikit.Kbd(c, "esc")
			})
			ui.Divider(c)
			ui.Column(c).Padding(uikit.CommandPaletteRowInset).Gap(0).Children(func() {
				for i, item := range []struct {
					label, detail, key string
				}{
					{"Send the draft", "Request editor", enter},
					{"Open Settings", "Appearance, proxy, certificates", key(",")},
					{"Copy as cURL", "The chosen capture, with its headers", key("C")},
					{"Replay 3 requests", "The selection in the capture table", key("R")},
				} {
					row := ui.Row(c).Height(uikit.CommandPaletteRow).FillWidth().Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).
						Radius(uikit.RadiusControl).AlignItems(ui.Center)
					if i == 0 {
						row.Background(p.AccentTint)
					}
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
							ui.Text(c, item.label).FontSize(uikit.Scaled(uikit.CommandPaletteRowSize)).
								LineHeight(20.0 / 14.0).TextColor(t.Text).SingleLine()
							uikit.Small(c, item.detail).TextColor(t.TextMuted).SingleLine()
						})
						uikit.Kbd(c, item.key)
					})
				}
			})
			ui.Divider(c)
			ui.Row(c).Height(40).Padding(0, t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.Kbd(c, "↑")
				uikit.Kbd(c, "↓")
				uikit.Small(c, "Choose")
				uikit.Kbd(c, open)
				uikit.Small(c, "Open")
				uikit.Kbd(c, "esc")
				uikit.Small(c, "Close")
			})
		})
	})
	caption(c, "The command palette is 720 points wide: a 48-point input at 16 points, then 40-point rows at 14. The chosen row is inset 4 points and takes RadiusControl, so the highlight never touches the panel edge.")
}

func (k *components) settingsCardPlate(c *ui.Context) {
	uikit.SettingsCardPage(c, "Capture", "What the proxy keeps and what it drops.", func() {
		uikit.SettingsCard(c, "Storage", func() {
			uikit.SettingsCardRow(c, "Flow limit", "Rows kept before the oldest spill to disk.", func() {
				uikit.Input(c, &k.flow).Placeholder("20000").Width(uikit.WidthField)
			})
			uikit.SettingsCardRow(c, "Memory limit", "Bodies above this are read from disk.", func() {
				uikit.Input(c, &k.empty).Placeholder("512 MB").Width(uikit.WidthField)
			})
		})
		uikit.SettingsCard(c, "Behavior", func() {
			uikit.SettingsCardRow(c, "Keep traffic", "Captured requests stay in this window.", func() {
				uikit.SettingsSwitch(c, &k.on, "Keep traffic")
			})
			uikit.SettingsCardRow(c, "Pause on start", "Open with the proxy stopped until Send.", func() {
				uikit.SettingsSwitch(c, &k.off, "Pause on start")
			})
		})
	})
	caption(c, "The Settings skeleton is 620 points wide. Each group is one canvas card with a hairline, and each row is at least 44 points: title and one description line on the left, control on the right.")
}

func (k *components) settingsPlate(c *ui.Context) {
	uikit.SettingsPage(c, "Capture", "What the proxy keeps and what it drops.", func() {
		uikit.SettingsGroup(c, "Storage", func() {
			uikit.SettingsRow(c, "Flow limit", func() ui.Element {
				return labelled(uikit.Input(c, &k.flow).Placeholder("20000").Width(uikit.WidthField), hoverSettings)
			}, "Rows kept before the oldest spill to disk.")
			uikit.SettingsRow(c, "Memory limit", func() ui.Element {
				return uikit.Input(c, &k.empty).Placeholder("512 MB").Width(uikit.WidthField)
			}, "Bodies above this are read from disk.")
		})
		uikit.SettingsGroup(c, "Behavior", func() {
			uikit.SettingsRow(c, "Keep traffic", func() ui.Element {
				return uikit.Switch(c, &k.on).Label("Keep traffic")
			}, "")
			uikit.SettingsRowMultiline(c, "Bypass list", func() ui.Element {
				return ui.Column(c).Gap(c.Theme().Space(uikit.SpaceM)).Children(func() {
					labelled(ui.Checkbox(c, &k.check, "Pause on start"), focusSettings)
					uikit.TextArea(c, &k.bypass).Height(54).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Disabled(true)
				})
			}, "One host per line; these connect directly.")
		})
	})
	caption(c, "A settings page keeps a reading width; its groups are headings and rules, not cards. A row lines its label up with the control, and a multiline row with the control's first line.")
}

func (k *components) toastPlate(c *ui.Context) {
	// Toasts belong to a window, not to the plate builder: the light and dark
	// exports each get their own root and their own three toasts.
	added := ui.Local(c.Root(), "gallery-toasts", func() bool { return false })
	if !*added {
		*added = true
		c.AddToast(ui.Toast{Title: "Request deleted", Action: "Undo", Type: "success", Timeout: -1})
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
	caption(c, "A toast is a popover panel above the footer, with an icon in its kind's signal color. An action is a quiet link on the message's row; three show at once, and the pointer or the keyboard holds them.")
}

func (k *components) listPlate(c *ui.Context) {
	row := func(focused bool, label string) {
		theme := uikit.ListTheme(c.Theme(), focused)
		ui.Themed(c, theme).Background(ui.Transparent).FillWidth().Children(func() {
			box := ui.Row(c).Height(uikit.HeightRow).FillWidth().Padding(0, c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).Gap(c.Theme().Space(uikit.SpaceM)).
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
					ui.Text(c, "Accept").Font(uikit.FontMono).Width(uikit.WidthPicker).TextColor(c.Theme().TextMuted)
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
		ui.Column(c).Width(uikit.WidthWide).Children(func() {
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

func (k *components) checkPlate(c *ui.Context) {
	stateRow(c, "Rest", func() {
		uikit.Checkbox(c, &k.off, "Sync across devices")
		uikit.Checkbox(c, &k.on, "Checked")
	})
	stateRow(c, "Hover", func() {
		labelled(uikit.Checkbox(c, &k.hovered, "Hover"), hoverChecks)
	})
	stateRow(c, "Focus", func() {
		labelled(uikit.Checkbox(c, &k.focused, "Focus"), focusChecks)
	})
	stateRow(c, "Disabled", func() {
		uikit.Checkbox(c, &k.off, "Disabled").Disabled(true)
		uikit.Checkbox(c, &k.on, "Disabled and checked").Disabled(true)
	})
	stateRow(c, "Radio", func() {
		uikit.RadioGroup(c, func() {
			for _, size := range []string{"Small", "Medium", "Large"} {
				uikit.Radio(c, &k.choice, size, size)
			}
		}).Row().Gap(c.Theme().Space(uikit.SpaceL))
	})
	caption(c, "A box is a control face, not an outline: unchecked, hovered and pressed are the text color laid thinly, checked is the ink.")
}

func (k *components) feedbackPlate(c *ui.Context) {
	t := c.Theme()
	stateRow(c, "Spinner", func() {
		uikit.Spinner(c, "Loading messages")
		uikit.Hint(c, "Work of unknown length")
	})
	stateRow(c, "Progress", func() {
		ui.Column(c).Gap(t.Space(uikit.SpaceM)).Width(uikit.WidthWide + uikit.WidthField).Children(func() {
			for _, bar := range []struct {
				name  string
				value float64
			}{{"Uploading", 0.4}, {"Connecting", -1}} {
				ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
					uikit.Meta(c, bar.name).Width(uikit.WidthNarrow)
					uikit.ProgressBar(c, bar.value, bar.name).Grow(1)
				})
			}
		})
	})
	stateRow(c, "Callouts", func() {
		ui.Column(c).Gap(t.Space(uikit.SpaceM)).Width(uikit.WidthWide + uikit.WidthField).Children(func() {
			uikit.Callout(c, uikit.CalloutInfo, "Capture is paused; new requests are not recorded.", nil)
			uikit.Callout(c, uikit.CalloutSuccess, "The certificate is trusted by this system.", nil)
			uikit.Callout(c, uikit.CalloutWarning, "The export contains 3 unredacted headers.", nil)
			uikit.Callout(c, uikit.CalloutDanger, "The proxy could not bind 127.0.0.1:7890.", func() {
				uikit.LinkButton(c, "Try again")
			})
		})
	})
	stateRow(c, "Badges", func() {
		uikit.Badge(c, "12", t.TextMuted)
		uikit.Badge(c, "2xx", uikit.Colors(t).Success)
		uikit.Badge(c, "5xx", uikit.Colors(t).Danger)
	})
	stateRow(c, "Busy", func() {
		uikit.PrimaryButton(c, "Save")
		uikit.PrimaryButtonBusy(c, "Save", true)
	})
}
