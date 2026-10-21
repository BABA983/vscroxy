package main

import (
	"cmp"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// requestDocument owns both the editable draft and the last send. Neither is
// borrowed from the selected capture row or another request tab.
type requestDocument struct {
	id                int
	revealTab         bool
	tabLabel          string
	savedKey          string
	baseline          savedRequest
	sourceFlow        int
	sourceAPI         string
	composeName       string
	composeCollection string
	composeMethod     string
	composeURL        string
	composeHeaders    string
	composeBody       string
	composePre        string
	composePost       string
	composeTab        int
	scriptTab         int
	composeResult     string
	composeWaiting    bool
	composeTests      []testResult
	composeReplies    []string
	response          *capture.Flow
	responseTab       int
	responseEditor    *editor.State
	requestPercent    float32
}

// requestResult is handed back to the UI thread; workers never mutate tabs,
// editor buffers or environments while a frame is reading them.
type requestResult struct {
	document    *requestDocument
	summary     string
	response    *capture.Flow
	tests       []testResult
	replies     []string
	environment string
	varsBefore  map[string]string
	vars        map[string]string
}

func (d *requestDocument) value() savedRequest {
	return savedRequest{
		Name: d.composeName, Collection: d.composeCollection,
		Method: d.composeMethod, URL: d.composeURL, Headers: d.composeHeaders,
		Body: d.composeBody, Pre: d.composePre, Post: d.composePost,
	}
}

func (d *requestDocument) dirty() bool { return d.value() != d.baseline }

func (d *requestDocument) title() string {
	return cmp.Or(d.composeName, "Untitled request")
}

func (a *app) addDocument(r savedRequest, key string) {
	a.nextDocument++
	d := &requestDocument{
		id: a.nextDocument, revealTab: true, savedKey: key, requestPercent: 43,
		composeName: r.Name, composeCollection: r.Collection,
		composeMethod: cmp.Or(r.Method, "GET"), composeURL: r.URL,
		composeHeaders: r.Headers, composeBody: r.Body,
		composePre: r.Pre, composePost: r.Post,
		baseline: savedRequest{Method: "GET", URL: "https://"},
	}
	if key != "" {
		d.baseline = d.value()
	}
	a.documents = append(a.documents, d)
	a.requestDocument = d
	a.requestChoice = key
	a.workspace = 1
}

func (a *app) closeDocument(d *requestDocument) {
	at := slices.Index(a.documents, d)
	if at < 0 {
		return
	}
	a.documents = slices.Delete(a.documents, at, at+1)
	if a.requestDocument == d {
		a.requestDocument = nil
		a.requestChoice = ""
		if len(a.documents) > 0 {
			a.requestDocument = a.documents[min(at, len(a.documents)-1)]
			a.revealTab = true
			a.requestChoice = a.savedKey
		}
	}
}

func (a *app) closeRequestModal(c *ui.Context, t *ui.Theme) {
	d := a.closingDocument
	if d == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(440).Gap(16).Children(func() {
			ui.Text(c, "Save changes?").FontSize(17).Bold()
			ui.Text(c, d.title()+" has unsaved changes. Closing it without saving will discard this draft.").TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.closingDocument = nil
				}
				if ui.Button(c, "Discard").Clicked() {
					a.closeDocument(d)
					a.closingDocument = nil
				}
				if ui.PrimaryButton(c, "Save & close").Clicked() {
					a.requestDocument = d
					if a.saveComposed() {
						a.closeDocument(d)
						a.closingDocument = nil
					}
				}
			})
		})
	})
	if !open {
		a.closingDocument = nil
	}
}

func (a *app) documentTabs(c *ui.Context, t *ui.Theme) {
	var closing *requestDocument
	ui.Row(c).Height(40).Background(t.Surface).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
			selected := slices.Index(a.documents, a.requestDocument)
			tabs := ui.TabsBase(c, &selected, len(a.documents))
			tabs.List.Gap(1).Padding(4, 6, 0, 6).Children(func() {
				for i, d := range a.documents {
					c.Key(d.id)
					tab := tabs.Tab(i).Label("Request tab: "+d.title()).Height(36).Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(6, 6, 0, 0).TextColor(t.TextMuted)
					label := fmt.Sprintf("%s %s %t", d.composeMethod, d.title(), d.dirty())
					if d.revealTab || d == a.requestDocument && d.tabLabel != label {
						tab.ScrollIntoView()
						d.revealTab = false
					}
					d.tabLabel = label
					if d == a.requestDocument {
						tab.Background(t.Background).TextColor(t.Text).Border(1, t.Border).BorderWidth(1, 1, 0, 1)
					}
					tab.Children(func() {
						ui.Text(c, d.composeMethod).FontSize(9).Bold().TextColor(methodColor(d.composeMethod))
						ui.Text(c, d.title()).FontSize(12).SingleLine().MaxWidth(165)
						if d.dirty() {
							ui.Box(c).Size(5, 5).Radius(3).Background(t.Accent).Label("Unsaved changes")
						}
						if ui.ButtonBase(c).Size(20, 20).Radius(4).Label("Close " + d.title()).Tooltip("Close request").Disabled(d.composeWaiting).Children(func() {
							ui.Text(c, "×").FontSize(15).TextColor(t.TextMuted)
						}).Clicked() {
							closing = d
						}
					})
				}
			})
			if tabs.List.Changed() && selected >= 0 && selected < len(a.documents) {
				a.requestDocument = a.documents[selected]
				a.requestChoice = a.savedKey
			}
		})
		if ui.ButtonBase(c).Size(34, 34).Label("Add request tab").Tooltip("New request").Children(func() {
			ui.Icon(c, plusIcon).Size(15, 15).TextColor(t.TextMuted)
		}).Clicked() {
			a.openCompose()
		}
	})
	ui.Divider(c)
	if closing != nil {
		if closing.dirty() {
			a.requestDocument = closing
			a.requestChoice = closing.savedKey
			a.closingDocument = closing
		} else {
			a.closeDocument(closing)
		}
	}
}

func (a *app) composeView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().Children(func() {
		a.documentTabs(c, t)
		d := a.requestDocument
		if d == nil {
			ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
				ui.Icon(c, requestIcon).Size(30, 30).TextColor(t.TextMuted)
				ui.Text(c, "Open a saved request or create a new one").TextColor(t.TextMuted)
				if ui.PrimaryButton(c, "Create request").Clicked() {
					a.openCompose()
				}
			})
			return
		}
		ui.Column(c).Key(d.id).Grow(1).Children(func() {
			ui.Row(c).Height(42).Padding(0, 18).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &d.composeCollection).Width(155).Placeholder("Collection").Label("Collection").FontSize(11)
				ui.Text(c, "/").TextColor(t.TextMuted)
				ui.TextInput(c, &d.composeName).Grow(1).Placeholder("Untitled request").Label("Name").FontSize(11)
				if names := a.envNames(); len(names) > 0 {
					ui.Select(c, &a.activeEnv, names).Width(130).Label("Active environment")
				}
				if ui.Button(c, "Variables…").FontSize(11).Clicked() {
					a.openVars()
				}
				if ui.Button(c, "Save request").FontSize(11).Clicked() {
					a.saveComposed()
				}
			})
			if d.sourceFlow != 0 {
				ui.Row(c).Height(24).Padding(0, 18).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.ButtonBase(c).Label("Source capture").Children(func() {
						ui.Text(c, fmt.Sprintf("Source: Capture #%d", d.sourceFlow)).FontSize(11).TextColor(t.Accent)
					}).Clicked() {
						a.clearFilters()
						for i, at := range a.visible {
							if a.flows[at].id == d.sourceFlow {
								a.picked, a.workspace = i, 0
								return
							}
						}
						a.setMessage("Source capture is no longer in this session")
					}
					ui.Text(c, "· editable draft").FontSize(11).TextColor(t.TextMuted)
				})
			}
			if d.sourceAPI != "" {
				ui.Row(c).Height(24).Padding(0, 18).Children(func() {
					if ui.ButtonBase(c).Label("Source API").Children(func() {
						ui.Text(c, "Source: APIs · review contract").FontSize(11).TextColor(t.Accent)
					}).Clicked() {
						a.apiChoice, a.workspace = d.sourceAPI, 2
					}
				})
			}
			ui.Row(c).Height(56).Padding(8, 18).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &d.composeMethod).Width(86).Height(36).Label("Method").FontSize(12).FontWeight(600).TextColor(methodColor(d.composeMethod))
				ui.TextInput(c, &d.composeURL).Grow(1).Height(36).Label("URL").Placeholder("https://api.example.com/v1/resource").Font("monospace").FontSize(12)
				if ui.Button(c, "Copy cURL").Width(100).Height(36).FontSize(11).Label("Copy as cURL").Tooltip("Copy this draft; variable placeholders are preserved and scripts are not run").Clicked() {
					a.copyCurl(c, flow{method: d.composeMethod, url: d.composeURL, reqHeader: parseHeaderBlock(d.composeHeaders), reqBody: d.composeBody})
				}
				if ui.PrimaryButton(c, "Send").Width(84).Height(36).Disabled(d.composeWaiting).Clicked() {
					a.sendComposed()
				}
			})
			ui.SplitVerticalPercent(c, &d.requestPercent, func() {
				ui.Column(c).Fill().Children(func() {
					ui.Tabs(c, &d.composeTab, "Headers", "Body", "Scripts").Padding(0, 14)
					ui.Divider(c)
					ui.Column(c).Grow(1).Padding(12, 18).Gap(8).Children(func() {
						switch d.composeTab {
						case 0:
							ui.Text(c, "REQUEST HEADERS · one Key: Value per line").FontSize(10).TextColor(t.TextMuted)
							ui.TextArea(c.Key("headers"), &d.composeHeaders).Grow(1).Font("monospace").FontSize(12).Label("Request headers").Placeholder("Accept: application/json")
						case 1:
							ui.Text(c, "RAW BODY · also used as the WebSocket text message").FontSize(10).TextColor(t.TextMuted)
							ui.TextArea(c.Key("body"), &d.composeBody).Grow(1).Font("monospace").FontSize(12).Label("Request body")
						case 2:
							ui.Segmented(c, &d.scriptTab, "Pre-request", "Post-response")
							if d.scriptTab == 0 {
								ui.TextArea(c.Key("pre"), &d.composePre).Grow(1).Font("monospace").FontSize(12).Label("Pre-request script").Placeholder("req.set(\"Accept\", \"application/json\")")
							} else {
								ui.TextArea(c.Key("post"), &d.composePost).Grow(1).Font("monospace").FontSize(12).Label("Post-response script").Placeholder("test(\"status is OK\", res.status === 200)")
							}
						}
					})
				})
			}, func() {
				a.requestResponse(c, t, d)
			}).Grow(1)
		})
	})
}

func (a *app) requestResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(40).Padding(0, 14).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Tabs(c, &d.responseTab, "Response", "Response headers", "Test results")
			ui.Spacer(c)
			if r := d.response; r != nil {
				ui.Text(c, fmt.Sprintf("%d %s", r.Status, http.StatusText(r.Status))).FontSize(11).Bold().TextColor(statusColor(r.Status, t))
				ui.Text(c, fmt.Sprintf("%d ms · %s", r.Millis, humanBytes(r.Bytes))).FontSize(11).TextColor(t.TextMuted)
			}
		})
		ui.Divider(c)
		if d.response == nil && len(d.composeReplies) == 0 {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Text(c, cmp.Or(d.composeResult, "Send a request to see its response")).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "The exchange will also appear in Capture.").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		if strings.HasPrefix(d.composeResult, "Post-response script:") {
			ui.Text(c, d.composeResult).FontSize(11).TextColor(t.Danger).Padding(8, 18)
		}
		switch d.responseTab {
		case 0:
			if d.responseEditor == nil {
				body := strings.Join(d.composeReplies, "\n\n")
				if d.response != nil {
					body = d.response.ResponseBody
				}
				value := jsontext.Value(body)
				isJSON := value.Indent(jsontext.WithIndent("  ")) == nil
				if isJSON {
					body = string(value)
				}
				buf := editor.NewBuffer(body)
				if isJSON {
					buf.SetLanguage(editor.LanguageNamed("json"))
				}
				d.responseEditor = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, Label: "Response body"}
			}
			if d.response != nil && d.response.ResponseBodySize > len(d.response.ResponseBody) {
				ui.Text(c, "Preview limited to 8 MiB; the complete body is in Capture.").FontSize(11).TextColor(t.TextMuted).Padding(6, 18)
			}
			editor.View(c, d.responseEditor).Grow(1)
		case 1:
			ui.Scroll(c).Grow(1).Padding(14, 18).Children(func() {
				if d.response != nil {
					ui.Text(c, headerBlock(d.response.ResponseHeader)).Font("monospace").FontSize(12)
				}
			})
		case 2:
			ui.Scroll(c).Grow(1).Padding(14, 18).Gap(8).Children(func() {
				if len(d.composeTests) == 0 {
					ui.Text(c, "No assertions. Add test(name, pass) in a script.").FontSize(12).TextColor(t.TextMuted)
				}
				for _, result := range d.composeTests {
					mark, color := "✓", t.Success
					if !result.Pass {
						mark, color = "✗", t.Danger
					}
					ui.Text(c, mark+" "+result.Name).FontSize(12).TextColor(color)
				}
			})
		}
	})
}
