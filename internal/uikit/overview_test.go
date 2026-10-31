package uikit

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// TestOverviewSnapshots renders every component in each theme, so the kit
// is reviewed in one image per theme before the screens that use it. It
// only runs when VSCROXY_UIKIT_SNAPSHOTS names an output directory:
//
//	VSCROXY_UIKIT_SNAPSHOTS="$PWD/.amp/in/artifacts/uikit" go test ./internal/uikit -run '^TestOverviewSnapshots$' -count=1
func TestOverviewSnapshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_UIKIT_SNAPSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_UIKIT_SNAPSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []struct {
		name string
		dark bool
	}{{"paper", false}, {"ink", true}} {
		var o overview
		tt := ui.NewTester(func(c *ui.Context) { o.view(c, theme.dark) }, 1280, 860)
		tt.Frame()
		save := func(name string) {
			t.Helper()
			f, err := os.Create(filepath.Join(dir, theme.name+"-"+name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		// The focus ring is drawn for keyboard focus only.
		tt.Key(0, ui.KeyTab)
		save("overview")
		for _, state := range []string{"Primary", "Secondary", "Toolbar"} {
			r, ok := tt.Find("Hover " + state)
			if !ok {
				t.Fatalf("missing %s button", state)
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Press(r.X+r.W/2, r.Y+r.H/2)
			tt.Frame()
			save("pressed-" + state)
			tt.Release(r.X+r.W/2, r.Y+r.H/2)
			tt.Frame()
			save("hover-" + state)
		}
	}
}

type overview struct {
	text, empty, invalid string
	format, side         string
	on, off, check       bool
	segment, tab         int
	open                 bool
}

func (o *overview) view(c *ui.Context, dark bool) {
	c.SetTheme(Theme(dark))
	t := c.Theme()
	if o.format == "" {
		o.format, o.side, o.text, o.invalid, o.on, o.check = "Pretty", "all", "api.example.com", "70000", true, true
	}
	section := func(title string, content func()) {
		ui.Column(c).Gap(t.Space(2)).Children(func() {
			ui.Text(c, title).FontSize(TextMeta).Bold().LetterSpacing(0.5).TextColor(t.TextMuted)
			content()
		})
	}
	row := func(content func()) { ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(content) }
	ui.Row(c).Fill().Background(t.Background).Children(func() {
		ui.Sidebar(c, &o.side, func() {
			SidebarSection(c, "Live", &o.open, func() {})
			SidebarItem(c, "all", lucide.LayoutList(), "All traffic", o.side == "all")
			SidebarItem(c, "errors", lucide.CircleAlert(), "Errors", o.side == "errors")
			SidebarItem(c, "slow", lucide.Clock(), "Slow", o.side == "slow")
		}).Width(200).FillHeight().Background(Colors(t).Panel)
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Padding(t.Space(6)).Gap(t.Space(6)).Children(func() {
			ui.Text(c, "vscroxy uikit").FontSize(TextTitle).Bold()
			section("BUTTONS", func() {
				row(func() {
					PrimaryButton(c, "Send")
					SecondaryButton(c, "Copy as cURL")
					ToolbarButton(c, "Breakpoints", "Pause matching requests", false).Children(func() { ui.Text(c, "Breakpoints") })
					ToolbarButton(c, "Breakpoints on", "Pause matching requests", true).Children(func() { ui.Text(c, "Breakpoints on") })
					IconButton(c, "Settings", "Settings", lucide.Settings())
				})
				row(func() {
					PrimaryButton(c, "Disabled").Disabled(true)
					SecondaryButton(c, "Disabled").Disabled(true)
					ui.Row(c).Disabled(true).Children(func() {
						ToolbarButton(c, "Disabled", "Unavailable", false).Children(func() { ui.Text(c, "Disabled") })
						IconButton(c, "Disabled icon", "Unavailable", lucide.Settings())
					})
				})
				row(func() {
					PrimaryButton(c, "Hover Primary")
					SecondaryButton(c, "Hover Secondary")
					ToolbarButton(c, "Hover Toolbar", "", false).Children(func() { ui.Text(c, "Hover Toolbar") })
				})
			})
			section("TEXT", func() {
				Heading(c, "Heading · POST /v1/orders")
				Body(c, "Body · the request was replayed with the draft's headers.")
				Mono(c, "Mono · https://api.example.com/v1/orders?id=42")
				Hint(c, "Hint · captured traffic appears here once the proxy is on.")
				row(func() {
					Label(c, "Label")
					Meta(c, "Meta · 12 ms · 4.2 KB · 15:04:05")
				})
			})
			section("INPUTS AND TOGGLES", func() {
				row(func() {
					ui.TextInput(c, &o.text).Label("Host").Width(220).Height(HeightControl)
					ui.TextInput(c, &o.empty).Label("Empty").Placeholder("Search host or path").Width(220).Height(HeightControl)
					ui.TextInput(c, &o.invalid).Label("Disabled").Width(120).Height(HeightControl).Disabled(true)
					ui.Select(c, &o.format, []string{"Pretty", "Raw", "Preview"}).Width(140).Height(HeightControl)
				})
				row(func() {
					ui.Checkbox(c, &o.check, "Rule enabled")
					ui.Checkbox(c, &o.off, "Stop on failure")
					ui.Switch(c, &o.on).Label("System proxy")
					ui.Switch(c, &o.off).Label("LAN devices")
					Segmented(c, &o.segment, "All", "HTTP", "HTTPS", "WS", "SSE", "JSON")
				})
				row(func() {
					for i, m := range MarkColors {
						SwatchButton(c, "Mark "+m.Name, m.Color, i == 1)
					}
				})
			})
			section("TABS", func() {
				Tabs(c, &o.tab, "Response", "Body", "Headers", "Cookies", "Timing")
			})
			section("SIGNALS", func() {
				row(func() {
					for _, s := range []struct {
						label  string
						status int
					}{{"200 OK", 200}, {"304", 304}, {"302 Found", 302}, {"404 Not Found", 404}, {"500 Error", 500}} {
						StatusPill(c, s.label, StatusColor(s.status, t))
					}
				})
				row(func() {
					for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "WS", "HEAD"} {
						MethodLabel(c, m)
					}
				})
			})
			section("KEY / VALUE", func() {
				ui.Column(c).Width(520).Border(1, t.Border).Radius(RadiusControl).Children(func() {
					for _, kv := range [][2]string{{"Accept", "application/json"}, {"Content-Type", "application/json"}} {
						KVRow(c, func() {
							ui.Text(c, kv[0]).Font(FontMono).Width(160).TextColor(t.TextMuted)
							ui.Text(c, kv[1]).Font(FontMono)
						})
					}
				})
			})
			section("PANELS", func() {
				row(func() {
					PopoverPanel(c, ui.Column(c)).Padding(t.Space(3)).Width(240).Children(func() {
						ui.Text(c, "Popover panel")
						ui.Text(c, "Menus, popovers and toasts").FontSize(TextMeta).TextColor(t.TextMuted)
					})
					ui.Column(c).Width(400).Height(160).Border(1, t.Border).Radius(RadiusPanel).Children(func() {
						EmptyState(c, "No requests yet", "Enable the system proxy to start capturing.", func() {
							PrimaryButton(c, "Enable system proxy")
						})
					})
					DialogColumn(c, 320).Children(func() {
						DialogTitle(c, "Rename request")
						DialogFooter(c).Children(func() {
							SecondaryButton(c, "Cancel")
							PrimaryButton(c, "Rename")
						})
					})
				})
			})
		})
	})
}
