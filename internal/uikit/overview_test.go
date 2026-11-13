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
		tt := ui.NewTester(func(c *ui.Context) { o.view(c, theme.dark) }, 1280, 1400)
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
	segment, tab, subtab int
	reqTab, resTab       int
	sources              int
	open, sections       bool
}

func (o *overview) view(c *ui.Context, dark bool) {
	c.SetTheme(Theme(dark))
	t := c.Theme()
	if o.format == "" {
		o.format, o.side, o.text, o.invalid, o.on, o.check = "Pretty", "all", "api.example.com", "70000", true, true
		o.open, o.sections = true, true
	}
	section := func(title string, content func()) {
		ui.Column(c).Gap(t.Space(2)).Children(func() {
			Label(c, title)
			content()
		})
	}
	row := func(content func()) { ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(content) }
	ui.Row(c).Fill().Background(t.Background).Children(func() {
		ui.Sidebar(c, &o.side, func() {
			SidebarHeading(c, "Traffic", &o.open, nil)
			SidebarItem(c, "all", lucide.LayoutList(), "All requests", o.side == "all", true).Children(func() { Meta(c, "1,159") })
			SidebarItem(c, "errors", lucide.CircleAlert(), "Errors", o.side == "errors", true).Children(func() { Meta(c, "73") })
			SidebarItem(c, "slow", lucide.Clock(), "Slow", o.side == "slow", true).Children(func() { Meta(c, "1,035") })
			// The same selection in a window behind others.
			SidebarItem(c, "behind", lucide.Clock(), "Chosen, window behind", true, false)
			// A row without an icon keeps the slot, so its label lines up.
			SidebarItem(c, "host", nil, "api.example.com", false, true).Children(func() { Meta(c, "12") })
			SidebarHeading(c, "Sources", &o.sections, func() { Segmented(c, &o.sources, "Apps", "Domains").Height(t.Space(5)) })
			SidebarButton(c, "Show all domains", "", lucide.ChevronRight(), false).TextColor(t.TextMuted)
		}).Padding(t.Space(1), 0).Gap(0).Width(200).FillHeight().Background(Colors(t).Chrome)
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Padding(t.Space(6)).Gap(t.Space(6)).Children(func() {
			ui.Text(c, "vscroxy uikit").FontSize(Scaled(TextTitle)).FontWeight(500)
			section("Surfaces", func() {
				// One control reads the same on chrome, canvas and well; a
				// field is a well on the canvas and the canvas with a
				// hairline on chrome.
				row(func() {
					for _, s := range []struct {
						name   string
						fill   ui.Color
						chrome bool
					}{{"Chrome", Colors(t).Chrome, true}, {"Canvas", Colors(t).Canvas, false}, {"Well", Colors(t).Well, false}} {
						ui.Column(c).Width(230).Padding(t.Space(3)).Gap(t.Space(2)).Radius(RadiusPanel).
							Background(s.fill).Children(func() {
							Label(c, s.name)
							Body(c, "Body · the draft was replayed")
							Meta(c, "Meta · 12 ms · 4.2 KB")
							row(func() {
								SecondaryButton(c, "Save")
								PrimaryButton(c, "Send")
							})
							field := ui.Row(c).Height(HeightControl).AlignItems(ui.Center).Padding(0, t.Space(2))
							if s.chrome {
								ChromeFieldFace(c, field, false)
							} else {
								FieldFace(c, field, false)
							}
							field.Children(func() { Meta(c, "Search") })
						})
					}
				})
			})
			section("Buttons", func() {
				row(func() {
					PrimaryButton(c, "Send")
					SecondaryButton(c, "Copy as cURL")
					DangerButton(c, "Delete")
					CompactButton(c, "Copy")
					ChipButton(c, "Commands", "Search every action · Ctrl/Cmd+K").Children(func() {
						ui.Icon(c, lucide.Search()).Size(IconInline, IconInline).TextColor(t.TextMuted)
						Meta(c, "⌘K")
					})
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
			section("Text", func() {
				Heading(c, "Heading · POST /v1/orders")
				Body(c, "Body · the request was replayed with the draft's headers.")
				Mono(c, "Mono · https://api.example.com/v1/orders?id=42")
				Hint(c, "Hint · captured traffic appears here once the proxy is on.")
				row(func() {
					Label(c, "Label")
					Meta(c, "Meta · 12 ms · 4.2 KB · 15:04:05")
				})
			})
			// Chinese has no bundled face: these lines exercise the system
			// fallback and the prose leading in both themes.
			section("Chinese", func() {
				Body(c, "Body · 抓取到的请求和响应都在这里。")
				Mono(c, "Mono · 订单 42 / 中文数据")
				Hint(c, "Hint · 开启代理后，抓到的流量会显示在这里。这是一段较长的中文说明，用来检查行高和换行。")
				Meta(c, "Meta · 12 毫秒 · 4.2 KB · 15:04:05")
			})
			section("Inputs and toggles", func() {
				// The two kinds of field there are: a field on chrome is the
				// canvas with a hairline, a field on the canvas is a well.
				row(func() {
					ui.Field(c, "On chrome", func() {
						SearchField(c, &o.empty, "Search collections", "Search…", true)
					}).Width(240)
					ui.Field(c, "On the canvas", func() {
						SearchField(c, &o.empty, "Search host or path", "Search…", false)
					}).Width(240)
				})
				row(func() {
					Input(c, &o.text).Label("Host").Width(220)
					Input(c, &o.empty).Label("Empty").Placeholder("Search host or path").Width(220)
					Input(c, &o.invalid).Label("Disabled").Width(120).Disabled(true)
					Select(c, &o.format, []string{"Pretty", "Raw", "Preview"}).Width(140)
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
			section("Tabs", func() {
				CountedTabs(c, &o.tab, "Request", []string{"Params", "Headers", "Auth", "Body", "Scripts"}, []string{"", "2"})
				SubTabs(c, &o.subtab, "Response", "Body", "Headers", "Cookies", "Timing")
			})
			section("Signals", func() {
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
			section("Activity", func() {
				// One row: its summary, the stacked bars and the latest time.
				var range_ ActivityRange
				buckets := make([]ActivityBucket, 60)
				for i := range buckets {
					switch {
					case i%17 == 3:
						buckets[i] = ActivityBucket{2, 0, 1, 1, 0}
					case i%9 == 0:
						buckets[i] = ActivityBucket{0, 1, 0, 0, 0}
					case i%5 == 0:
						buckets[i] = ActivityBucket{3, 0, 0, 0, 1}
					case i%3 == 0:
						buckets[i] = ActivityBucket{5, 0, 0, 0, 0}
					}
				}
				ui.Column(c).Width(620).Children(func() {
					ActivityStrip(c, buckets, &range_, "20:09:30", func() {})
				})
			})
			section("Pane headers", func() {
				// One row per pane: a title, its tabs, and what it reports.
				ui.Column(c).Width(620).Border(1, t.Border).Radius(RadiusPanel).Children(func() {
					PaneHeader(c, func() {
						PaneTitle(c, "Request", "Request pane", ui.Color{}, t.Text, nil)
						CountedTabs(c, &o.reqTab, "Request pane", []string{"Headers", "Params", "Body", "Auth"}, []string{"5"})
					}, func() {
						TabMenu(c, "More", "Request pane options", func(*ui.Menu) {})
					})
					PaneHeader(c, func() {
						PaneTitle(c, "500 Internal Server Error", "Response pane", StatusColor(500, t), t.Danger, nil)
						CountedTabs(c, &o.resTab, "Response pane", []string{"Body", "Headers", "Cookies", "Timing"}, nil)
					}, func() {
						ResponseMeta(c, "1242 ms", "1.8 KB", Timing{})
					})
				})
			})
			section("Key / value", func() {
				ui.Column(c).Width(520).Border(1, t.Border).Radius(RadiusControl).Children(func() {
					for _, kv := range [][2]string{{"Accept", "application/json"}, {"Content-Type", "application/json"}} {
						KVRow(c, func() {
							ui.Text(c, kv[0]).Font(FontMono).Width(160).TextColor(t.TextMuted)
							ui.Text(c, kv[1]).Font(FontMono)
						})
					}
				})
			})
			section("Panels", func() {
				row(func() {
					PopoverPanel(c, ui.Column(c)).Padding(t.Space(3)).Width(240).Children(func() {
						ui.Text(c, "Popover panel")
						ui.Text(c, "Menus, popovers and toasts").FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
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
