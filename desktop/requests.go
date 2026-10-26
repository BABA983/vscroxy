package main

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// requestDocument owns both the editable draft and the last send. Neither is
// borrowed from the selected capture row or another request tab.
type requestDocument struct {
	id                int
	debugKey          string
	origin            *debugSource
	revealTab         bool
	tabLabel          string
	savedKey          string
	baseline          savedRequest
	sourceFlow        int
	experimentID      int
	source            *flow
	sourceView        *captureView
	comparison        captureAnalysis
	sourceAPI         string
	composeName       string
	composeCollection string
	composeMethod     string
	composeURL        string
	composeHeaders    string
	composeBody       string
	// headerRows is the header table. composeHeaders stays the Key: Value
	// block for scripts, cURL and the bulk editor, written from the enabled
	// rows; headerFor is the block the rows were last built from.
	headerRows []requestField
	headerFor  string
	headerBulk bool
	// headerWarn is the confirmation shown before the block displays
	// credentials in plain text.
	headerWarn bool
	// headerOff are the switched-off rows while the bulk editor shows the
	// block, which has no way to say a row is disabled.
	headerOff      []requestField
	bulkEditor     *editor.State
	bulkFor        string
	auth           requestAuth
	bodyMode       string
	formFields     []requestField
	queryRows      []requestField
	querySource    string
	queryError     string
	composePre     string
	composePost    string
	checks         responseChecks
	composeTab     int
	scriptTab      int
	composeResult  string
	composeWaiting bool
	sendCancel     context.CancelFunc
	sendCancelling bool
	sendOutcome    string
	sentRun        string
	// bodyEditor is the raw request body's editor. bodyFor is the text it
	// last held, so an import or a restored draft resets it while typing
	// never does. The scripts use the same reader, each with its own state.
	bodyEditor     *editor.State
	bodyFor        string
	preEditor      *editor.State
	preFor         string
	postEditor     *editor.State
	postFor        string
	composeTests   []testResult
	composeReplies []string
	response       *capture.Flow
	// responseTab is the response tab the reader chose. Comparing with the
	// source capture is not a tab: it replaces the whole response area and
	// returns to the tab kept here.
	responseTab    int
	comparing      bool
	responseView   int
	responseEditor *editor.State
	responseList   ui.ListState
	// sent is exactly what the last send carried, so the screen can say
	// whether the draft on it is the one that produced the response.
	sent           savedRequest
	sentAt         time.Time
	sentSet        bool
	requestPercent float32
	// splitSeen is the share the editor last laid out, so a divider the reader
	// moved is never moved back by the app.
	splitSeen float32
	// splitTouched is set once the reader drags the divider.
	splitTouched bool
	urlInput     ui.Handle
}

// requestResult is handed back to the UI thread; workers never mutate tabs,
// editor buffers or environments while a frame is reading them.
type requestResult struct {
	document    *requestDocument
	summary     string
	outcome     string
	response    *capture.Flow
	tests       []testResult
	replies     []string
	environment string
	varsBefore  map[string]string
	vars        map[string]string
	history     *historyEntry
	experiment  *experimentRecord
}

func (d *requestDocument) value() savedRequest {
	r := savedRequest{
		Name: d.composeName, Collection: d.composeCollection,
		Method: d.composeMethod, URL: d.composeURL, Headers: d.composeHeaders,
		Body: d.composeBody, Pre: d.composePre, Post: d.composePost,
		Auth: d.auth, BodyMode: d.bodyMode, Form: slices.Clone(d.formFields),
		Checks: d.checks, HeaderList: headerRowsKept(d.headerRows),
	}
	if len(d.headerRows) > 0 {
		r.Headers = headerRowsText(d.headerRows)
	}
	if r.Auth.Type == "None" {
		r.Auth = requestAuth{}
	}
	if r.BodyMode == "Raw" {
		r.BodyMode = ""
	}
	return r
}

func (d *requestDocument) dirty() bool { return !reflect.DeepEqual(d.value(), d.baseline) }

func (d *requestDocument) cancelSend() {
	if d.composeWaiting && d.sendCancel != nil && !d.sendCancelling {
		d.sendCancelling = true
		d.sendCancel()
	}
}

func (d *requestDocument) title() string {
	return cmp.Or(d.composeName, "Untitled request")
}

func (a *app) addDocument(r savedRequest, key string) {
	a.nextDocument++
	d := &requestDocument{
		id: a.nextDocument, debugKey: uuid.New().String(), revealTab: true, savedKey: key, requestPercent: draftPercent,
		composeName: r.Name, composeCollection: r.Collection,
		composeMethod: cmp.Or(r.Method, "GET"), composeURL: r.URL,
		composeHeaders: r.Headers, composeBody: r.Body,
		headerRows: slices.Clone(r.HeaderList),
		composePre: r.Pre, composePost: r.Post,
		checks: r.Checks,
		auth:   r.Auth, bodyMode: cmp.Or(r.BodyMode, "Raw"), formFields: slices.Clone(r.Form), composeTab: composeTabFor(r),
		baseline: savedRequest{Method: "GET", URL: "https://"},
	}
	if d.headerRows == nil && d.composeHeaders != "" {
		d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	}
	d.auth.Type = cmp.Or(d.auth.Type, "None")
	d.auth.Location = cmp.Or(d.auth.Location, "Header")
	if key != "" {
		d.baseline = d.value()
	}
	a.documents = append(a.documents, d)
	a.requestDocument = d
	a.requestChoice = key
	a.workspace, a.requestsSection = 1, 0
	d.urlInput.Focus()
}

// The request editor's tabs, in the order they show.
const (
	composeTabParams = iota
	composeTabHeaders
	composeTabAuth
	composeTabBody
	composeTabScripts
	composeTabChecks
)

// composeTabFor opens the tab a request is usually edited in: its body when
// it has one, its headers otherwise.
func composeTabFor(r savedRequest) int {
	if r.Body != "" || r.BodyMode != "" && r.BodyMode != "Raw" {
		return composeTabBody
	}
	return composeTabHeaders
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
			a.urlInput.Focus()
		} else {
			a.workbench.content.Focus()
		}
	}
}

func (a *app) requestClose(d *requestDocument) {
	if d.composeWaiting {
		return
	}
	if d.dirty() {
		a.requestDocument, a.requestChoice, a.workspace = d, d.savedKey, 1
		a.closingDocument = d
	} else {
		a.closeDocument(d)
	}
}

// Capture and the open drafts are one cycle: Ctrl+Tab steps through the
// request documents and back to the live traffic. Workspace shortcuts stay
// independent, so Cmd/Ctrl+1–4 never unexpectedly select a draft tab.
func (a *app) cycleDocument(step int) {
	index := 0
	if a.workspace == 1 {
		index = slices.Index(a.documents, a.requestDocument) + 1
	}
	index = (index + step + len(a.documents) + 1) % (len(a.documents) + 1)
	if index == 0 {
		a.workspace, a.captureSection = 0, 0
		if view := a.views.activeView(); view != nil {
			view.list.Handle.Focus()
		}
		return
	}
	a.requestDocument = a.documents[index-1]
	a.requestChoice, a.workspace, a.revealTab = a.savedKey, 1, true
	a.urlInput.Focus()
}

func (a *app) closeRequestModal(c *ui.Context, t *ui.Theme) {
	d := a.closingDocument
	if d == nil {
		return
	}
	open := true
	appModal(c, &open, func() {
		ui.Column(c).Width(440).Gap(16).Children(func() {
			dialogTitle(c, "Save changes?")
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

// documentTabs is the Requests workspace's own tab strip. It holds request
// documents only: traffic is the other workspace, and the capture's
// breadcrumb returns to it.
func (a *app) documentTabs(c *ui.Context, t *ui.Theme) {
	var closing *requestDocument
	ui.Row(c).Height(38).Background(t.Surface).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
			selected := max(slices.Index(a.documents, a.requestDocument), 0)
			tabs := ui.TabsBase(c, &selected, len(a.documents))
			tabs.List.Gap(1).Padding(4, 6, 0, 6).Children(func() {
				for i, d := range a.documents {
					c.Key(d.id)
					tab := tabs.Tab(i).Label("Request tab: "+d.title()).Height(32).Padding(0, 8).Gap(7).AlignItems(ui.Center).Radius(5).TextColor(t.TextMuted)
					label := fmt.Sprintf("%s %s %t", d.composeMethod, d.title(), d.dirty())
					if d.revealTab || d == a.requestDocument && d.tabLabel != label {
						tab.ScrollIntoView()
						d.revealTab = false
					}
					d.tabLabel = label
					if d == a.requestDocument {
						tab.Background(t.SurfaceHover).TextColor(t.Text)
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
				a.requestChoice, a.workspace, a.requestsSection = a.savedKey, 1, 0
			}
		})
		if ui.ButtonBase(c).Size(30, 30).Label("Add request tab").Tooltip("New request").Children(func() {
			ui.Icon(c, plusIcon).Size(14, 14).TextColor(t.TextMuted)
		}).Clicked() {
			a.openCompose()
		}
	})
	ui.Divider(c)
	if closing != nil {
		a.requestClose(closing)
	}
}

// Response views are separate from the request editor's tabs: reading what
// came back never moves the draft.
const (
	resViewBody = iota
	resViewHeaders
	resViewCookies
	resViewTiming
	resViewTests
)

// composeView is the debugging screen: the request as it will be sent on top,
// what came back below, and the provenance of both always visible.
func (a *app) composeView(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil {
		ui.Column(c).Fill().Center().Gap(12).Children(func() {
			ui.Icon(c, requestIcon).Size(30, 30).TextColor(t.TextMuted)
			ui.Text(c, "Open a saved request or create a new one").TextColor(t.TextMuted)
			if ui.PrimaryButton(c, "Create request").Clicked() {
				a.openCompose()
			}
		})
		return
	}
	ui.Column(c).Key(d.id).Fill().Children(func() {
		a.requestBreadcrumb(c, t, d)
		a.requestBar(c, t, d)
		if d.comparing {
			ui.Column(c).Grow(1).Children(func() { a.requestResponse(c, t, d) })
			return
		}
		if d.requestPercent == 0 {
			d.requestPercent = draftPercent
		}
		// A share the reader set is theirs; the app only moves the divider it
		// still owns.
		if d.splitSeen != 0 && d.requestPercent != d.splitSeen {
			d.splitTouched = true
		}
		d.splitSeen = d.requestPercent
		ui.SplitVerticalPercent(c.Key("request-editor"), &d.requestPercent, func() {
			a.requestEditor(c, t, d)
		}, func() {
			a.requestResponse(c, t, d)
		}).Grow(1)
	})
}

// responseArrived gives the first response the room it needs, unless the reader
// has set the divider themselves.
func (d *requestDocument) responseArrived() {
	if d.splitTouched {
		return
	}
	d.requestPercent, d.splitSeen = responsePercent, responsePercent
}

// headerWarnModal asks before the Key: Value block puts every credential on
// the screen: the table masks them one by one, the block cannot.
func (a *app) headerWarnModal(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil || !d.headerWarn {
		return
	}
	appModal(c, &d.headerWarn, func() {
		ui.Column(c).Width(460).Gap(12).Children(func() {
			dialogTitle(c, "Bulk edit shows credentials")
			ui.Text(c, "The Key: Value block writes every header value in plain text, including tokens, cookies and passwords. The table masks them one row at a time.").FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "Switching modes does not send a request. All enabled header values will be visible; be careful when sharing your screen.").FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.headerWarn = false
				}
				if ui.PrimaryButton(c, "Show values").Clicked() {
					d.headerWarn = false
					toggleHeaderMode(d)
				}
			})
		})
	})
}

// requestRenameModal edits the document's name and collection, so neither
// needs a permanent input on the breadcrumb.
func (a *app) requestRenameModal(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if !a.renameOpen || d == nil {
		return
	}
	appModal(c, &a.renameOpen, func() {
		ui.Column(c).Width(440).Gap(12).Children(func() {
			dialogTitle(c, "Rename request")
			ui.TextInput(c, &d.composeName).Label("Name").Placeholder("Untitled request").FillWidth()
			ui.TextInput(c, &d.composeCollection).Label("Collection").Placeholder("Collection").FillWidth()
			ui.Text(c, "The collection groups saved requests in the library.").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.renameOpen = false
				}
				if ui.PrimaryButton(c, "Done").Clicked() {
					a.renameOpen = false
				}
			})
		})
	})
}

// requestBreadcrumb ties the draft to the capture it came from and holds the
// environment and the save action.
func (a *app) requestBreadcrumb(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(38).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		label := "Back to traffic"
		if d.sourceFlow != 0 {
			label = fmt.Sprintf("Source #%d", d.sourceFlow)
		}
		back := ui.ButtonBase(c).Label("Back to traffic").Height(26).Padding(0, 6).Gap(6).Radius(5).AlignItems(ui.Center).
			TextColor(t.TextMuted).Tooltip("Return to the capture, keeping its filters, selection and position")
		if back.Hovered() {
			back.TextColor(t.Text)
		}
		if back.Clicked() {
			a.backToTraffic(c)
		}
		back.Children(func() {
			ui.Icon(c, backIcon).Size(13, 13)
			ui.Text(c, label).FontSize(12)
		})
		// The name and the collection are one quiet line: a click renames
		// them, so neither needs a permanent input here. An unnamed draft
		// says so once, not twice: its tab already reads Untitled request.
		name := d.composeName
		if d.composeCollection != "" {
			name = d.composeCollection + " / " + cmp.Or(name, "Untitled request")
		}
		if name == "" {
			name = "Name this request…"
		}
		rename := ui.ButtonBase(c).Label("Rename request").Height(26).Padding(0, 6).Radius(5).AlignItems(ui.Center).
			TextColor(t.TextMuted).Tooltip("Rename this request or move it to another collection")
		if rename.Hovered() {
			rename.TextColor(t.Text)
		}
		if rename.Clicked() {
			a.renameOpen = true
		}
		rename.Children(func() { ui.Text(c, name).FontSize(12).SingleLine().MaxWidth(320) })
		if d.experimentID != 0 && !d.composeWaiting {
			ui.Text(c, fmt.Sprintf("Recorded experiment #%d · retained result", d.experimentID)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
		if d.sourceFlow != 0 && (a.query != "" || a.filter != "all" || a.chip != 0 || a.filtersActive()) {
			ui.Text(c, "Capture filters kept · new traffic may be hidden").FontSize(10).TextColor(t.Warning).SingleLine().
				Tooltip("Returning to Capture restores its filters, selection and position")
		}
		if d.sourceAPI != "" {
			if ui.ButtonBase(c).Label("Source API").Height(24).Padding(0, 6).Radius(4).AlignItems(ui.Center).
				TextColor(t.TextMuted).Children(func() {
				ui.Text(c, "Source: APIs · review contract").FontSize(11).TextColor(t.Accent)
			}).Clicked() {
				a.apiChoice, a.workspace, a.requestsSection = d.sourceAPI, 1, 1
			}
		}
		ui.Spacer(c)
		if names := a.envNames(); len(names) > 0 {
			ui.Select(c, &a.activeEnv, names).Width(140).Label("Active environment").FontSize(11)
		}
		a.desktopButton(c, "save-request", "Save")
		ui.MenuButton(c, "⋯", func(m *ui.Menu) {
			if m.Item("Copy as cURL").Chosen() {
				a.copyCurl(c, flow{method: d.composeMethod, url: d.composeURL, reqHeader: parseHeaderBlock(d.composeHeaders), reqBody: d.composeBody})
			}
			if m.Item("Experiments…").Chosen() {
				a.experiments.scope, a.experiments.draftKey = nil, ""
				a.experiments.open = true
			}
			if m.Item("Send history").Chosen() {
				a.openSendHistory(d)
			}
			m.Separator()
			if m.Item("Share regression…").Chosen() {
				a.openRegressionShare(d.value())
			}
			if m.Item("Import regression…").Chosen() {
				a.importRegression()
			}
			m.Separator()
			if m.Item("Manage environments…").Chosen() {
				a.openVars()
			}
		}).Label("Request options").Height(26).Padding(0, 6).FontSize(14).TextColor(t.TextMuted).Border(0, t.Border)
	})
	ui.Divider(c)
}

// draftPercent is the share of the debugging screen the request editor gets
// while nothing has come back: enough for several header rows and the row
// action, without shrinking the type. A response pulls it to responsePercent.
const (
	draftPercent    = 55
	responsePercent = 35
)

// requestMethods are the methods the editor offers; any other is typed.
var requestMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// requestBar is the line a person edits and sends: method, URL, and the one
// prominent action.
func (a *app) requestBar(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(52).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		// An editable dropdown: a click lists the common methods, and a method
		// the list lacks is typed and kept as it is. The chevron and the hover
		// background say it is a control, not a label.
		control := ui.Row(c).Gap(2).Padding(0, 4).Radius(5).AlignItems(ui.Center)
		if control.Hovered() {
			control.Background(t.SurfaceHover)
		}
		control.Children(func() {
			method := ui.ComboboxBase(c, &d.composeMethod)
			method.Input.Width(78).Height(30).Label("Method").FontSize(12).FontWeight(600).
				TextColor(methodColor(d.composeMethod)).Border(0, t.Border)
			if method.Input.Changed() {
				d.composeMethod = strings.ToUpper(strings.TrimSpace(d.composeMethod))
			}
			method.Popup(func(panel ui.Element) {
				panel.Padding(4).Radius(6).Background(t.Background).Border(1, t.Border)
				query := strings.ToUpper(strings.TrimSpace(d.composeMethod))
				for _, m := range requestMethods {
					if method.Filtering() && query != "" && !strings.HasPrefix(m, query) {
						continue
					}
					item := method.Item(m).Height(26).Padding(0, 10).Radius(4).AlignItems(ui.Center)
					if item.Highlighted() {
						item.Background(t.SurfaceHover).TextColor(t.Text)
					}
					item.Children(func() { ui.Text(c, m).FontSize(12).TextColor(methodColor(m)) })
				}
			})
			if chosen, ok := method.Chosen(); ok {
				d.composeMethod = chosen
			}
			arrow := ui.ButtonBase(c).Size(18, 18).Radius(4).Label("Method list").Tooltip("Common methods").AlignItems(ui.Center)
			if arrow.Hovered() {
				arrow.Background(t.SurfacePressed)
			}
			if arrow.Children(func() { ui.Icon(c, chevronIcon).Size(12, 12).TextColor(t.TextMuted) }).Clicked() {
				method.SetOpen(!method.Open())
				method.Input.Focus()
			}
		})
		ui.TextInput(c, &d.composeURL).Bind(&d.urlInput).Grow(1).Height(34).Label("URL").
			Placeholder("https://api.example.com/v1/resource").Font("monospace").FontSize(12)
		sendLabel := "Send"
		if d.composeWaiting {
			sendLabel = "Sending…"
		}
		a.desktopButton(c, "send", sendLabel).Width(96).Height(34).Background(t.Accent).TextColor(t.AccentText)
		if d.composeWaiting {
			label := "Cancel"
			if d.sendCancelling {
				label = "Cancelling…"
			}
			a.desktopButton(c, "cancel-send", label).Label("Cancel send").Width(96).Height(34).
				Tooltip("Stop waiting; cancelling does not undo server-side changes")
		}
	})
	ui.Divider(c)
}

// requestEditor is the draft's own body: parameters, auth, headers, body and
// scripts, each a tab of the same document.
func (a *app) requestEditor(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(34).Padding(0, 6).Gap(4).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			flatTabs(c, &d.composeTab, "Request editor", "Params", "Headers", "Auth", "Body", "Scripts", "Checks")
			if d.composeTab == composeTabBody {
				// The body's type and formatting belong to the Body tab, so
				// they cost no row of their own.
				ui.Spacer(c)
				ui.Select(c, &d.bodyMode, []string{"Raw", "URL encoded", "Multipart"}).Label("Request body type").Width(132)
				formatted, isJSON := indentJSON(d.composeBody)
				if ui.ButtonBase(c).Label("Format body").Height(24).Padding(0, 7).Gap(5).Radius(4).AlignItems(ui.Center).
					TextColor(t.TextMuted).Disabled(!isJSON || d.bodyMode != "Raw").
					Tooltip("Indent the JSON body; the bytes sent stay what the editor shows").Children(func() {
					ui.Icon(c, formatIcon).Size(12, 12)
					ui.Text(c, "Format").FontSize(11)
				}).Clicked() {
					// Rebuild the editor from the formatted text instead of
					// letting its buffer write the old bytes back.
					d.composeBody, d.bodyFor = formatted, ""
				}
			}
		})
		ui.Column(c).Grow(1).Padding(8, 12).Gap(8).Children(func() {
			switch d.composeTab {
			case composeTabParams:
				a.paramsEditor(c, d, t)
			case composeTabHeaders:
				a.headerEditor(c, d, t)
			case composeTabAuth:
				a.authEditor(c, d, t)
			case composeTabBody:
				a.requestBodyEditor(c, d, t)
			case composeTabScripts:
				ui.Row(c).Children(func() {
					ui.Segmented(c, &d.scriptTab, "Pre-request", "Post-response")
				})
				a.requestScriptText(c, d)
			case composeTabChecks:
				a.checksEditor(c, d, t)
			}
		})
		ui.Row(c).Height(22).Padding(0, 12).AlignItems(ui.Center).Children(func() {
			a.requestSentState(c, t, d)
		})
	})
}

// requestSentState says whether the draft on screen is the one that was
// actually sent, and when.
func (a *app) requestSentState(c *ui.Context, t *ui.Theme, d *requestDocument) {
	switch {
	case d.composeWaiting:
		ui.Text(c, "Sending…").FontSize(11).TextColor(t.TextMuted)
	case !d.sentSet:
		ui.Text(c, "Not sent yet").FontSize(11).TextColor(t.TextMuted)
	case reflect.DeepEqual(d.value(), d.sent):
		ui.Text(c, "Draft matches last attempt · "+d.sentAt.Local().Format("15:04:05")).FontSize(11).TextColor(t.TextMuted).
			Tooltip("The draft snapshot matches; variables, scripts, redirects and rules may change the wire request")
	default:
		ui.Text(c, "Edited since last send · not yet sent").FontSize(11).TextColor(t.Warning).
			Tooltip("The response below belongs to the previous send, not to this draft")
	}
}

// requestResponse is what came back: the result first, then the body with
// its own format, headers, cookies and timing, tied to the send that produced
// it.
func (a *app) requestResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	outcome, detail := d.sendOutcome, d.composeResult
	if r := a.experiments.record(d.experimentID); r != nil && r.RunKey == d.sentRun && r.Outcome == "Network error" {
		outcome, detail = r.Outcome, r.Summary
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).MinHeight(40).Padding(6, 12).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Response").FontSize(12).Bold().TextColor(t.Text)
			if r := d.response; r != nil {
				status := fmt.Sprintf("%d %s", r.Status, http.StatusText(r.Status))
				if outcome == "Network error" {
					status = fmt.Sprintf("%d · proxy response", r.Status)
				}
				ui.Text(c, status).FontSize(12).Bold().TextColor(statusColor(r.Status, t))
				ui.Text(c, "·").FontSize(12).TextColor(t.Border)
				ui.Text(c, fmt.Sprintf("%d ms", r.Millis)).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "·").FontSize(12).TextColor(t.Border)
				ui.Text(c, humanBytes(r.Bytes)).FontSize(12).TextColor(t.TextMuted)
			} else if d.composeWaiting {
				ui.Text(c, "Sending…").FontSize(12).TextColor(t.TextMuted)
			}
			if !d.composeWaiting && outcome != "" && outcome != "Response received" {
				ui.Text(c, outcome).FontSize(11).TextColor(t.Warning)
			}
			if len(d.composeTests) > 0 {
				passed := 0
				for _, check := range d.composeTests {
					if check.Pass {
						passed++
					}
				}
				color := t.Success
				if passed != len(d.composeTests) {
					color = t.Danger
				}
				ui.Text(c, fmt.Sprintf("%d/%d checks passed", passed, len(d.composeTests))).FontSize(11).Bold().TextColor(color)
			}
			ui.Spacer(c)
			if d.source != nil && d.response != nil && !d.comparing {
				if ui.ButtonBase(c).Label("Compare with source").Height(26).Padding(0, 8).Gap(6).Radius(5).AlignItems(ui.Center).
					TextColor(t.TextMuted).Tooltip("Compare this response with the capture it came from").Children(func() {
					ui.Icon(c, compareIcon).Size(13, 13)
					ui.Text(c, "Compare with source").FontSize(11)
				}).Clicked() {
					d.comparing = true
				}
			}
			// Without a retained send there is nothing to read, so the entry
			// waits rather than opening an empty list.
			if ui.ButtonBase(c).Label("Send history").Height(26).Padding(0, 8).Gap(6).Radius(5).AlignItems(ui.Center).
				TextColor(t.TextMuted).Disabled(d.origin == nil && d.experimentID == 0).
				Tooltip("Inspect the exact request and result of each retained attempt; opening never sends").Children(func() {
				ui.Icon(c, sendHistoryIcon).Size(13, 13)
				ui.Text(c, "Send history").FontSize(11)
			}).Clicked() {
				a.openSendHistory(d)
			}
		})
		if d.sentSet {
			ui.Row(c).Height(26).Padding(0, 12).Gap(10).AlignItems(ui.Center).Children(func() {
				label := "Last attempt · " + d.sentAt.Local().Format("15:04:05") + " · " + d.sent.Method + " " + d.sent.URL
				ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1).Tooltip(label + "\nDraft snapshot; inspect Send history for prepared request and wire evidence")
				if !reflect.DeepEqual(d.value(), d.sent) {
					ui.Text(c, "Draft changed · not sent").FontSize(11).TextColor(t.Warning)
				}
			})
		}
		if outcome == "Network error" && d.response != nil || outcome == "Post-response script failed" || outcome == "Cancelled" && d.response != nil || outcome == "Timed out" && d.response != nil {
			ui.Text(c, outcome+": "+detail).FontSize(11).TextColor(t.Danger).Padding(6, 12).Selectable()
		}
		if d.response == nil && len(d.composeReplies) == 0 {
			// Nothing has come back yet: the empty state says what to do, and
			// the tabs wait for something to read.
			a.responseBody(c, t, d)
			return
		}
		if d.comparing {
			a.responseBody(c, t, d)
			a.responseProvenance(c, t, d)
			return
		}
		ui.Row(c).Height(34).Padding(0, 6).Gap(4).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			labels := []string{"Body", "Headers", "Cookies", "Timing", "Tests"}
			if d.responseTab >= len(labels) {
				d.responseTab = resViewBody
			}
			flatTabs(c, &d.responseTab, "Response tab", labels...)
			ui.Spacer(c)
			if d.responseTab == resViewBody && d.response != nil {
				formats := []string{"Pretty", "Raw", "Hex"}
				seg := ui.SegmentedBase(c, &d.responseView, len(formats))
				seg.Track.Padding(2).Radius(6).Border(1, t.Border).Background(t.Background).Children(func() {
					for i, name := range formats {
						s := seg.Segment(i).Label(name).Height(22).Padding(0, 8).Radius(4).AlignItems(ui.Center).TextColor(t.TextMuted)
						if i == d.responseView {
							s.Background(t.SurfaceHover).TextColor(t.Text)
						}
						s.Children(func() { ui.Text(c, name).FontSize(11) })
					}
				})
				if seg.Track.Changed() {
					d.responseEditor = nil
					d.responseList = ui.ListState{}
				}
			}
		})
		a.responseBody(c, t, d)
		a.responseProvenance(c, t, d)
	})
}

// responseProvenance says when the response arrived and whether the capture it
// came from was rewritten by sending it again.
func (a *app) responseProvenance(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil {
		return
	}
	provenance := "Response from " + d.response.At.Local().Format("15:04:05")
	if d.sourceFlow != 0 {
		provenance += fmt.Sprintf(" · Original capture #%d unchanged", d.sourceFlow)
	}
	ui.Text(c, provenance).FontSize(11).TextColor(t.TextMuted).Padding(6, 12).
		Tooltip("Redirects, cookies and proxy rules may change the on-wire exchange; Capture has the wire details")
}

// responseBody shows the chosen view of the last response.
func (a *app) responseBody(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil && len(d.composeReplies) == 0 {
		ui.Scroll(c).Grow(1).FillWidth().Padding(16).Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				if d.composeWaiting {
					ui.Spinner(c).Label("Waiting for request result")
					ui.Text(c, "Waiting for response… · 30 s timeout").FontSize(12).TextColor(t.TextMuted)
				} else if d.sendOutcome != "" {
					ui.Text(c, d.sendOutcome).FontSize(13).TextColor(t.Warning)
					ui.Text(c, d.composeResult).FontSize(12).TextColor(t.Text).Selectable().FillWidth()
					ui.Text(c, "Edit the draft and send again. No automatic retry.").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Text(c, cmp.Or(d.composeResult, "Send a request to see its response")).FontSize(12).TextColor(t.TextMuted)
					ui.Text(c, "The exchange will also appear in Capture.").FontSize(11).TextColor(t.TextMuted)
				}
				if d.composeWaiting || d.sendOutcome == "Cancelled" || d.sendOutcome == "Timed out" {
					ui.Text(c, "Cancelling does not undo server-side changes.").FontSize(11).TextColor(t.TextMuted)
				}
			})
		})
		return
	}
	if d.comparing {
		a.responseCompare(c, t, d)
		return
	}
	switch d.responseTab {
	case resViewBody:
		body := strings.Join(d.composeReplies, "\n\n")
		if d.response != nil {
			body = d.response.ResponseBody
			if d.response.ResponseBodySize > len(d.response.ResponseBody) {
				ui.Text(c, "Preview limited to 8 MiB; the complete body is in Capture.").FontSize(11).TextColor(t.TextMuted).Padding(4, 12)
			}
		}
		switch d.responseView {
		case 1:
			if d.responseEditor == nil {
				buf := editor.NewBuffer(body)
				d.responseEditor = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Response body"}
			}
			editor.View(c, d.responseEditor).Grow(1)
		case 2:
			a.responseHex(c, d, body)
		default:
			if d.responseEditor == nil {
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
			editor.View(c, d.responseEditor).Grow(1)
		}
	case resViewHeaders:
		ui.Scroll(c).Grow(1).Padding(10, 12).Children(func() {
			if d.response != nil {
				ui.Text(c, headerBlock(d.response.ResponseHeader)).Font("monospace").FontSize(12)
			}
		})
	case resViewCookies:
		a.responseCookies(c, t, d)
	case resViewTiming:
		ui.Scroll(c).Grow(1).Padding(10, 12).Gap(6).Children(func() {
			if d.response == nil {
				ui.Text(c, "No response yet.").FontSize(12).TextColor(t.TextMuted)
				return
			}
			ui.Text(c, fmt.Sprintf("Total %d ms · %s", d.response.Millis, humanBytes(d.response.Bytes))).FontSize(12)
			ui.Text(c, "Per-phase timing (DNS, connect, TLS, wait, download) is recorded on the capture in Capture → Timing.").FontSize(11).TextColor(t.TextMuted)
			index := a.sentFlowIndex(d)
			if ui.Button(c, "Inspect timing in Capture").FontSize(11).Disabled(index < 0).Clicked() {
				if index >= 0 {
					a.inspectCapture(a.flows[index].id)
					a.syncDetail()
					a.inspect.pane, a.inspect.ready = inspectorSplit, true
					a.inspect.tab[1] = resTabTiming
				}
			}
			if index < 0 {
				ui.Text(c, "This send's wire capture is not available in this session. Send history may retain its snapshot.").FontSize(11).TextColor(t.TextMuted)
			}
		})
	case resViewTests:
		ui.Scroll(c).Grow(1).Padding(10, 12).Gap(8).Children(func() {
			if len(d.composeTests) == 0 {
				ui.Text(c, "No assertions. Add response Checks or test(name, pass) in a script.").FontSize(12).TextColor(t.TextMuted)
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
}

// responseCompare fills the response area with the last send beside the
// capture it came from, and puts the response tabs back when it closes.
func (a *app) responseCompare(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(30).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.ButtonBase(c).Label("Back to response").Height(24).Padding(0, 6).Gap(5).Radius(4).AlignItems(ui.Center).
			TextColor(t.TextMuted).Children(func() {
			ui.Icon(c, backIcon).Size(12, 12)
			ui.Text(c, "Back to response").FontSize(11)
		}).Clicked() {
			d.comparing = false
		}
		ui.Text(c, "Comparing with the source capture").FontSize(11).TextColor(t.TextMuted)
	})
	if d.source == nil || d.response == nil {
		ui.Text(c, "The source capture is no longer in this session.").FontSize(12).TextColor(t.TextMuted).Padding(12)
		return
	}
	r := d.response
	right := flow{method: r.Method, path: r.URL, url: r.URL, status: r.Status, millis: r.Millis,
		reqBody: r.RequestBody, reqSize: r.RequestBodySize, reqHeaders: headerBlock(r.RequestHeader),
		resBody: r.ResponseBody, resSize: r.ResponseBodySize, resHeaders: headerBlock(r.ResponseHeader)}
	if r.RequestBodySize > len(r.RequestBody) {
		right.reqError = "preview limited to 8 MiB"
	}
	if r.ResponseBodySize > len(r.ResponseBody) {
		right.resError = "preview limited to 8 MiB"
	}
	ui.Text(c, "Last send: initial client request and final response; redirects, cookies and proxy rules may change the on-wire exchange. Inspect Capture for wire details.").FontSize(10).TextColor(t.TextMuted).Padding(4, 12)
	d.comparison.compare(c, *d.source, right, [2]string{fmt.Sprintf("Source #%d", d.sourceFlow), "Last send"})
}

// responseCookies lists what the response asked the client to store.
func (a *app) responseCookies(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil {
		ui.Text(c, "No response yet.").FontSize(12).TextColor(t.TextMuted).Padding(12)
		return
	}
	cookies := d.response.ResponseHeader.Values("Set-Cookie")
	if len(cookies) == 0 {
		ui.Text(c, "This response set no cookies.").FontSize(12).TextColor(t.TextMuted).Padding(12)
		return
	}
	ui.Scroll(c).Grow(1).Padding(10, 12).Gap(4).Children(func() {
		for _, cookie := range cookies {
			ui.Text(c, cookie).FontSize(12).Font("monospace").Selectable().SingleLine().Tooltip(cookie)
		}
	})
}

// responseHex renders a response body as a hex dump.
func (a *app) responseHex(c *ui.Context, d *requestDocument, body string) {
	rows := hexRows(body, "")
	if len(rows) == 0 {
		ui.Text(c, "Empty body").FontSize(12).TextColor(c.Theme().TextMuted).Padding(12)
		return
	}
	ui.List(c, &d.responseList, len(rows), func(i int) {
		ui.Text(c, hexLine(body, rows[i])).FontSize(11).Font("monospace").TextColor(c.Theme().Text).Padding(3, 12).SingleLine()
	}).Grow(1)
}

// Only a run key links this send's final wire hop, never its URL or source ID.
func (a *app) sentFlowIndex(d *requestDocument) int {
	if d.sentRun != "" {
		for i := len(a.flows) - 1; i >= 0; i-- {
			if a.flows[i].debugRun == d.sentRun && a.flows[i].kind != "connect" {
				return i
			}
		}
	}
	return -1
}
