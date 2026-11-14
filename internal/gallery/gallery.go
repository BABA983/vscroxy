// Package gallery is vscroxy's design gallery: every foundation token, every
// uikit component in every state it has, and every app screen, drawn from the
// real code with fake data. cmd/vscroxy-gallery shows it in a window; the
// export test writes one plate per entry and theme to docs/design.
package gallery

import (
	"fmt"
	"os"

	"github.com/baba983/vscroxy/internal/app"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// Group is one section of the catalog.
type Group struct {
	ID      string
	Title   string
	Entries []*Entry
}

// Entry is one plate: a foundation, a component with its states, or a screen.
type Entry struct {
	Group string
	ID    string
	Title string
	// Note says what the plate shows, or what it cannot.
	Note string
	// Height is the plate's height in the export. Zero takes the group's
	// default.
	Height int
	// Width overrides the export width for a plate that needs more room, as
	// a screen does.
	Width int
	// Hover names the sample the exporter puts the pointer on, and Focus the
	// one it tabs to, so the plate shows those states as the window does.
	// Tip lets the pointer rest long enough for a tooltip, for the plates
	// whose point is the tip.
	Hover string
	Focus string
	Tip   bool

	build func(*ui.Context)
	close func()
}

// Gallery is the window: a catalog on the left, the chosen plate on the
// right, and the appearance and text size above the plate.
type Gallery struct {
	groups  []*Group
	entries []*Entry
	choice  string
	dark    bool
	scale   float32
	// catalogOpen shows the catalog as a drawer over a screen. A screen is
	// the app's own window, and the app lays it out at the window's size, so
	// the catalog cannot sit beside it: the app would draw a column of its
	// own where the catalog is, and size every pane as if that column were
	// the window's.
	catalogOpen bool

	screens map[string]*app.Gallery
}

// New builds the catalog. Nothing is drawn until View runs.
func New() *Gallery {
	g := &Gallery{choice: "palette", scale: 1, screens: make(map[string]*app.Gallery)}
	g.groups = []*Group{
		g.foundations(),
		g.components(),
		g.screensGroup(),
	}
	for _, group := range g.groups {
		for _, e := range group.Entries {
			e.Group = group.ID
			g.entries = append(g.entries, e)
		}
	}
	// VSCROXY_GALLERY_ENTRY opens a plate by id, for a screenshot or a link
	// straight to the screen under review.
	if id := os.Getenv("VSCROXY_GALLERY_ENTRY"); id != "" {
		if g.Entry(id) != nil {
			g.choice = id
		}
	}
	// VSCROXY_GALLERY_DARK opens in the dark appearance, as
	// VSCROXY_GALLERY_ENTRY opens a plate.
	g.dark = os.Getenv("VSCROXY_GALLERY_DARK") != ""
	return g
}

// Entry finds a plate by its id.
func (g *Gallery) Entry(id string) *Entry {
	for _, e := range g.entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// Entries is every plate, in catalog order.
func (g *Gallery) Entries() []*Entry { return g.entries }

// Choose shows a plate and closes the drawer: the plate takes the window
// back, and a screen has the catalog only when it is asked for.
func (g *Gallery) Choose(id string) {
	g.choice = id
	g.catalogOpen = false
}

// SetDark chooses the appearance.
func (g *Gallery) SetDark(dark bool) { g.dark = dark }

// Dark reports the appearance.
func (g *Gallery) Dark() bool { return g.dark }

// SetTextScale chooses the text size the plates build at: 1 or 1.35.
func (g *Gallery) SetTextScale(scale float32) {
	if scale <= 0 {
		scale = 1
	}
	g.scale = scale
}

// TextScale is the text size the plates build at.
func (g *Gallery) TextScale() float32 { return g.scale }

// Close stops every screen the gallery built.
func (g *Gallery) Close() {
	for _, s := range g.screens {
		s.Close()
	}
	clear(g.screens)
}

// View draws the window.
func (g *Gallery) View(c *ui.Context) {
	uikit.SetTextScale(g.scale)
	c.SetTheme(uikit.Theme(g.dark))
	c.SetMenuPresentation(ui.PopoverMenus)
	pal := uikit.Colors(c.Theme())
	entry := g.Entry(g.choice)
	screen := entry != nil && entry.Group == "screens"
	if screen {
		// The app's view is the screen, at the window's size: the bar above
		// it takes height, and the catalog comes over it as a drawer.
		ui.Column(c).Fill().Background(pal.Chrome).Children(func() {
			g.bar(c)
			g.canvas(c)
		})
		if g.catalogOpen {
			g.drawer(c)
		}
	} else {
		ui.Row(c).Fill().Background(pal.Chrome).Children(func() {
			g.catalog(c, false)
			ui.Divider(c)
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
				g.bar(c)
				g.canvas(c)
			})
		})
	}
	uikit.Toasts(c)
}

// drawer is the catalog over a screen: the same rows, on the chrome, with a
// shadow and a Close button, so the screen can have the whole window.
func (g *Gallery) drawer(c *ui.Context) {
	pal := uikit.Colors(c.Theme())
	ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(268).Shadow(8, 0, 28, 0, pal.Shadow).
		Background(pal.Chrome).Children(func() { g.catalog(c, true) })
}

// catalog is the left column: the three groups and their plates. In the
// drawer it takes the full width and offers its own Close button.
func (g *Gallery) catalog(c *ui.Context, inDrawer bool) {
	t := c.Theme()
	width := float32(248)
	if inDrawer {
		width = 268
	}
	ui.Column(c).Width(width).Shrink(0).FillHeight().Background(uikit.Colors(t).Chrome).Children(func() {
		ui.Row(c).Padding(t.Space(4), t.Space(3), t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(t.Space(0.5)).Children(func() {
				ui.Text(c, "vscroxy").FontSize(uikit.Scaled(uikit.TextTitle)).FontWeight(500)
				uikit.Meta(c, "design gallery")
			})
			if inDrawer {
				ui.Spacer(c)
				if uikit.SecondaryButton(c, "Close").Clicked() {
					g.catalogOpen = false
				}
			}
		})
		ui.Scroll(c).Grow(1).Padding(0, t.Space(2), t.Space(3), t.Space(2)).Children(func() {
			uikit.SidebarList(c, "gallery", &g.choice, g.Choose, func() {
				for _, group := range g.groups {
					uikit.SidebarHeading(c, group.Title, nil, func() { uikit.Meta(c, fmt.Sprint(len(group.Entries))) })
					for _, e := range group.Entries {
						uikit.SidebarItem(c, e.ID, nil, e.Title, g.choice == e.ID, true)
					}
				}
			})
		})
	})
}

// bar is the strip above the plate: what it is, and how it is drawn.
func (g *Gallery) bar(c *ui.Context) {
	t := c.Theme()
	entry := g.Entry(g.choice)
	title, note := "", ""
	if entry != nil {
		title, note = entry.Title, entry.Note
	}
	ui.Row(c).Height(48).Shrink(0).Padding(0, t.Space(4)).Gap(t.Space(3)).AlignItems(ui.Center).
		Background(uikit.Colors(t).Chrome).Children(func() {
		ui.Column(c).Gap(0).MinWidth(0).Children(func() {
			ui.Text(c, title).FontSize(uikit.Scaled(uikit.TextBody)).FontWeight(500).SingleLine()
			if note != "" {
				uikit.Meta(c, note).SingleLine()
			}
		})
		if entry != nil && entry.Group == "screens" {
			label := "Catalog"
			if g.catalogOpen {
				label = "Hide catalog"
			}
			if uikit.SecondaryButton(c, label).Clicked() {
				g.catalogOpen = !g.catalogOpen
			}
		}
		ui.Spacer(c)
		uikit.Label(c, "Appearance")
		dark := 0
		if g.dark {
			dark = 1
		}
		uikit.Segmented(c, &dark, "Light", "Dark").Width(120).Label("Appearance")
		if (dark == 1) != g.dark {
			g.SetDark(dark == 1)
			g.syncScreens()
		}
		uikit.Label(c, "Text")
		scale := 0
		if g.scale > 1 {
			scale = 1
		}
		uikit.Segmented(c, &scale, "1.0×", "1.35×").Width(112).Label("Text size")
		if (scale == 1) != (g.scale > 1) {
			if scale == 1 {
				g.SetTextScale(1.35)
			} else {
				g.SetTextScale(1)
			}
		}
	})
}

// canvas is the plate itself. A screen is the app's own view, drawn live; a
// foundation or a component scrolls on the gallery's canvas.
func (g *Gallery) canvas(c *ui.Context) {
	pal := uikit.Colors(c.Theme())
	entry := g.Entry(g.choice)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(pal.Canvas).Children(func() {
		if entry == nil {
			return
		}
		if entry.Group == "screens" {
			screen := g.screen(entry.ID)
			if screen == nil {
				return
			}
			screen.SetDark(g.dark)
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() { screen.View(c) })
			return
		}
		if entry.build == nil {
			ui.Box(c).Fill()
			return
		}
		ui.Scroll(c).Grow(1).Padding(32).Children(func() {
			ui.Column(c).MaxWidth(1120).Gap(c.Theme().Space(6)).Children(func() { entry.build(c) })
		})
	})
}

// screen is the app screen for a plate, built the first time it is shown.
func (g *Gallery) screen(id string) *app.Gallery {
	if s, ok := g.screens[id]; ok {
		return s
	}
	s := app.NewGallery(id)
	if s != nil {
		g.screens[id] = s
	}
	return s
}

// syncScreens hands the chosen appearance to the screens already built.
func (g *Gallery) syncScreens() {
	for _, s := range g.screens {
		s.SetDark(g.dark)
	}
}
