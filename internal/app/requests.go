package app

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
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
	assetID           string
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
	headerRows []request.Field
	headerFor  string
	headerBulk bool
	// headerWarn is the confirmation shown before the block displays
	// credentials in plain text.
	headerWarn bool
	// headerOff are the switched-off rows while the bulk editor shows the
	// block, which has no way to say a row is disabled.
	headerOff   []request.Field
	bulkEditor  *editor.State
	bulkFor     string
	auth        request.Auth
	bodyMode    string
	formFields  []request.Field
	queryRows   []request.Field
	querySource string
	queryError  string
	composePre  string
	composePost string
	checks      request.Checks
	// examples are the draft's kept responses; example is the one shown,
	// exampleView its Body, Headers or Request view.
	examples        []request.Example
	example         int
	exampleView     int
	exampleEditor   *editor.State
	composeTab      int
	scriptTab       int
	composeResult   string
	composeWaiting  bool
	sendCancel      context.CancelFunc
	sendCancelling  bool
	sendOutcome     string
	sendDetails     bool
	sentRun         string
	variablesOpen   bool
	variablesReveal bool
	// bodyEditor is the raw request body's editor. bodyFor is the text it
	// last held, so an import or a restored draft resets it while typing
	// never does. The scripts use the same reader, each with its own state.
	bodyEditor     *editor.State
	bodyFor        string
	preEditor      *editor.State
	preFor         string
	postEditor     *editor.State
	postFor        string
	composeTests   []request.TestResult
	composeReplies []string
	console        []request.ConsoleMessage
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
	sent            savedRequest
	sentAt          time.Time
	sentSet         bool
	lastAttemptOpen bool
	requestPercent  float32
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
	tests       []request.TestResult
	replies     []string
	console     []request.ConsoleMessage
	environment string
	varsBefore  map[string]string
	vars        map[string]string
	writes      []request.Write
	history     *historyEntry
	experiment  *experimentRecord
}

func (d *requestDocument) value() savedRequest {
	r := savedRequest{
		ID: d.assetID, Source: d.origin,
		Name: d.composeName, Collection: d.composeCollection,
		Method: d.composeMethod, URL: d.composeURL, Headers: d.composeHeaders,
		Body: d.composeBody, Pre: d.composePre, Post: d.composePost,
		Auth: d.auth, BodyMode: d.bodyMode, Form: slices.Clone(d.formFields),
		Checks: d.checks, HeaderList: headerRowsKept(d.headerRows),
		Examples: slices.Clone(d.examples),
	}
	if len(d.headerRows) > 0 {
		r.Headers = headerRowsText(d.headerRows)
	}
	if r.Auth.Type == "None" {
		r.Auth = request.Auth{}
	}
	if r.BodyMode == "Raw" {
		r.BodyMode = ""
	}
	return r
}

// draft is what a send carries: the request without its examples, which
// stay with the request rather than with every attempt.
func (d *requestDocument) draft() savedRequest {
	r := d.value()
	r.Examples = nil
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
		id: a.nextDocument, debugKey: uuid.New().String(), revealTab: true, requestPercent: draftPercent,
		composeTab: composeTabFor(r), baseline: savedRequest{Method: "GET", URL: "https://"},
	}
	d.load(r, key)
	a.documents = append(a.documents, d)
	a.requestDocument = d
	a.requestChoice = key
	a.workspace, a.requestsSection = 1, 0
	a.requestSide = 0
	d.urlInput.Focus()
}

// load puts r in the editor; a key says it is that saved asset, which is
// then the baseline its unsaved changes are measured against.
func (d *requestDocument) load(r savedRequest, key string) {
	d.savedKey, d.assetID = key, r.ID
	d.composeName, d.composeCollection = r.Name, r.Collection
	d.composeMethod, d.composeURL = cmp.Or(r.Method, "GET"), r.URL
	d.composeHeaders, d.composeBody = r.Headers, r.Body
	d.headerRows, d.headerFor = slices.Clone(r.HeaderList), ""
	d.composePre, d.composePost, d.checks = r.Pre, r.Post, r.Checks
	d.examples, d.example, d.exampleEditor = slices.Clone(r.Examples), 0, nil
	d.auth, d.bodyMode, d.formFields = r.Auth, cmp.Or(r.BodyMode, "Raw"), slices.Clone(r.Form)
	if d.headerRows == nil && d.composeHeaders != "" {
		d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	}
	d.auth.Type = cmp.Or(d.auth.Type, "None")
	d.auth.Location = cmp.Or(d.auth.Location, "Header")
	d.setDebugSource(r.Source)
	if key != "" {
		d.baseline = d.value()
	}
}

// The request editor's tabs, in the order they show.
const (
	composeTabParams = iota
	composeTabHeaders
	composeTabAuth
	composeTabBody
	composeTabScripts
	composeTabChecks
	composeTabExamples
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
		dialogColumn(c, dialogSmall).Children(func() {
			dialogTitle(c, "Save changes?")
			ui.Text(c, d.title()+" has unsaved changes. Closing it without saving will discard this draft.").FontSize(textBody).TextColor(t.TextMuted)
			dialogFooter(c).Children(func() {
				if secondaryButton(c, "Cancel").Clicked() {
					a.closingDocument = nil
				}
				if secondaryButton(c, "Discard").Clicked() {
					a.closeDocument(d)
					a.closingDocument = nil
				}
				if primaryButton(c, "Save & close").Clicked() {
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

// documentTabs keeps documents, source navigation and save actions together.
// Only the documents scroll; the active draft's actions remain visible.
func (a *app) documentTabs(c *ui.Context, t *ui.Theme) {
	var closing *requestDocument
	ui.Row(c).Height(heightLarge).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
			selected := max(slices.Index(a.documents, a.requestDocument), 0)
			tabs := ui.TabsBase(c, &selected, len(a.documents))
			tabs.List.Gap(t.Space(0.5)).Padding(t.Space(1), t.Space(2), 0, t.Space(2)).Children(func() {
				for i, d := range a.documents {
					c.Key(d.id)
					tab := tabs.Tab(i).Label("Request tab: "+d.title()).Height(heightTabs).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(radiusControl).TextColor(t.TextMuted)
					label := fmt.Sprintf("%s %s %t", d.composeMethod, d.title(), d.dirty())
					if d.revealTab || d == a.requestDocument && d.tabLabel != label {
						tab.ScrollIntoView()
						d.revealTab = false
					}
					d.tabLabel = label
					if d == a.requestDocument {
						tab.Background(t.Background).TextColor(t.Text).Border(0, t.Accent).BorderWidth(0, 0, 2, 0)
					}
					tab.Children(func() {
						methodLabel(c, d.composeMethod).FontSize(textMeta)
						ui.Text(c, d.title()).FontSize(textBody).SingleLine().MaxWidth(165)
						if d.dirty() {
							ui.Box(c).Size(5, 5).Radius(radiusControl).Background(t.Accent).Label("Unsaved changes")
						}
						if ui.ButtonBase(c).Size(20, 20).Radius(radiusControl).Label("Close " + d.title()).Tooltip("Close request").Disabled(d.composeWaiting).Children(func() {
							ui.Text(c, "×").FontSize(textTitle).TextColor(t.TextMuted)
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
		if ui.ButtonBase(c).Size(heightControl, heightControl).Label("Add request tab").Tooltip("New request").Children(func() {
			ui.Icon(c, plusIcon).Size(iconInline, iconInline).TextColor(t.TextMuted)
		}).Clicked() {
			a.openCompose()
		}
		if names := a.envNames(); len(names) > 0 {
			env := ui.SelectBase(c.Key("active-environment"), &a.activeEnv)
			env.Trigger.Label("Active environment").Tooltip("Active environment").Width(132).Height(heightControl).
				Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(radiusControl).Background(t.Accent)
				ui.Text(c, cmp.Or(a.activeEnv, "No environment")).FontSize(textBody).SingleLine().Grow(1)
				ui.Icon(c, chevronIcon).Size(iconInline, iconInline).TextColor(t.TextMuted)
			})
			env.Popup(func(panel ui.Element) {
				popoverPanel(c, panel).Padding(t.Space(1))
				for _, name := range names {
					item := env.Item(name).Label(name).Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center)
					if item.Highlighted() {
						item.Background(t.SurfaceHover)
					}
					item.Children(func() {
						color := t.TextMuted
						if name == a.activeEnv {
							color = t.Accent
						}
						ui.Box(c).Size(6, 6).Radius(radiusControl).Background(color)
						ui.Text(c, name).FontSize(textBody)
					})
				}
			})
		} else {
			ui.ButtonBase(c).Label("Manage environments").Tooltip("Add an environment for request variables").Height(heightControl).Padding(0, t.Space(2)).
				Children(func() { ui.Text(c, "No environment").FontSize(textBody).TextColor(t.TextMuted) }).OnClick(a.openVars)
		}
		if d := a.requestDocument; d != nil {
			a.requestDocumentActions(c, t, d)
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
	resViewConsole
)

// composeView is the debugging screen: the request as it will be sent on top,
// what came back below, and the provenance of both always visible.
func (a *app) composeView(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil {
		emptyState(c, "Open a saved request or create a new one", "Keep drafts, variables and responses together in one request tab.", func() {
			if primaryButton(c, "Create request").Clicked() {
				a.openCompose()
			}
		})
		return
	}
	ui.Column(c).Key(d.id).Fill().Children(func() {
		a.requestBar(c, t, d)
		a.requestVariablesPanel(c, t, d)
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
		dialogColumn(c, dialogSmall).Children(func() {
			dialogTitle(c, "Bulk edit shows credentials")
			ui.Text(c, "The Key: Value block writes every header value in plain text, including tokens, cookies and passwords. The table masks them one row at a time.").FontSize(textBody).TextColor(t.TextMuted)
			ui.Text(c, "Switching modes does not send a request. All enabled header values will be visible; be careful when sharing your screen.").FontSize(textBody).TextColor(t.TextMuted)
			dialogFooter(c).Children(func() {
				if secondaryButton(c, "Cancel").Clicked() {
					d.headerWarn = false
				}
				if primaryButton(c, "Show values").Clicked() {
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
		dialogColumn(c, dialogSmall).Children(func() {
			dialogTitle(c, "Rename request")
			ui.TextInput(c, &d.composeName).Label("Name").Placeholder("Untitled request").FillWidth().
				Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).FontSize(textBody)
			ui.TextInput(c, &d.composeCollection).Label("Collection").Placeholder("Collection").FillWidth().
				Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).FontSize(textBody)
			ui.Text(c, "The collection groups saved requests in the library.").FontSize(textMeta).TextColor(t.TextMuted)
			dialogFooter(c).Children(func() {
				if secondaryButton(c, "Cancel").Clicked() {
					a.renameOpen = false
				}
				if primaryButton(c, "Done").Clicked() {
					a.renameOpen = false
				}
			})
		})
	})
}

// requestDocumentActions never scrolls away with a long document tab strip.
func (a *app) requestDocumentActions(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Key(d.id).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
		label := "Back to traffic"
		if d.sourceFlow != 0 {
			label = fmt.Sprintf("Source #%d", d.sourceFlow)
		}
		back := ui.ButtonBase(c).Label("Back to traffic").Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).
			TextColor(t.TextMuted).Tooltip("Return to the capture, keeping its filters, selection and position")
		if back.Hovered() {
			back.TextColor(t.Text)
		}
		if back.Clicked() {
			a.backToTraffic(c)
		}
		back.Children(func() {
			ui.Icon(c, backIcon).Size(iconInline, iconInline)
			if d.sourceFlow != 0 {
				ui.Text(c, label).FontSize(textBody)
			}
		})
		// The tab already names the request. Keep its full collection path
		// available at the rename action without reserving another row.
		name := d.composeName
		if d.composeCollection != "" {
			name = d.composeCollection + " / " + cmp.Or(name, "Untitled request")
		}
		if name == "" {
			name = "Name this request…"
		}
		rename := ui.ButtonBase(c).Label("Rename request").Size(heightControl, heightControl).Radius(radiusControl).Center().
			TextColor(t.TextMuted).Tooltip(name + "\nRename or move to another collection")
		if rename.Hovered() {
			rename.TextColor(t.Text)
		}
		if rename.Clicked() {
			a.renameOpen = true
		}
		rename.Children(func() { ui.Icon(c, requestIcon).Size(iconInline, iconInline) })
		if d.sourceFlow != 0 && (a.query != "" || a.filter != "all" || a.chip != 0 || a.filtersActive()) {
			ui.Icon(c, filterIcon).Size(iconInline, iconInline).Label("Capture filters kept · new traffic may be hidden").TextColor(t.Warning).
				Tooltip("Capture filters kept · new traffic may be hidden. Returning restores filters, selection and position.")
		}
		if d.sourceAPI != "" {
			if ui.ButtonBase(c).Label("Source API").Height(heightCompact).Padding(0, t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).
				TextColor(t.TextMuted).Children(func() {
				ui.Text(c, "Source API").FontSize(textBody).TextColor(t.Accent)
			}).Clicked() {
				a.apiChoice, a.workspace, a.requestsSection = d.sourceAPI, 1, 1
				a.requestSide = 3
			}
		}
		a.desktopButton(c, "save-request", "Save")
		ui.PopoverMenuButton(c, "⋯", func(m *ui.Menu) {
			if m.Item("Duplicate as new request").Chosen() {
				r := d.value()
				r.ID, r.Name = "", uniqueRequestName(a.requests, r.Name+" copy")
				a.addDocument(r, "")
			}
			if m.Item("Copy as cURL").Chosen() {
				a.copyDraftCurl(c, d)
			}
			if m.Item("Generate code…").Chosen() {
				a.openCode()
			}
			if m.Item("Run records…").Chosen() {
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
			if m.Item(settingsLabel(d.composeCollection)).Disabled(d.composeCollection == "").Chosen() {
				a.openFolder(d.composeCollection)
			}
			if m.Item("Manage environments…").Chosen() {
				a.openVars()
			}
			if m.Item("Request variables").Chosen() {
				d.variablesOpen = !d.variablesOpen
			}
			if m.Item("Vault…").Chosen() {
				a.openVault()
			}
		}).
			Label("Request options").Tooltip("Request options").Height(heightControl).Padding(0, t.Space(2)).FontSize(textTitle).TextColor(t.TextMuted).Border(0, t.Border)
	})
}

func (a *app) requestVariablesPanel(c *ui.Context, t *ui.Theme, d *requestDocument) {
	vars := a.activeVars()
	r := d.value()
	in := a.inheritance(r.Collection)
	rows := r.ResolveVariables(in, vars, nil)
	missing := 0
	for i := range rows {
		switch rows[i].Source {
		case "Missing":
			missing++
		case "Run input":
			rows[i].Source = "Environment: " + cmp.Or(a.activeEnv, "none")
		}
	}
	if len(rows) == 0 && !d.variablesOpen {
		return
	}
	target := "unresolved (review variables)"
	if u, err := url.Parse(request.ExpandVars(d.composeURL, in.Values(vars))); err == nil && u.Hostname() != "" {
		target = u.Scheme + "://" + u.Host // Do not reveal query tokens or userinfo.
	}
	ui.Row(c).Height(heightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, fmt.Sprintf("Variables · %d unresolved", missing)).Label("Request variables").FontSize(textMeta).Clicked() {
			d.variablesOpen = !d.variablesOpen
		}
		ui.Text(c, "Target before scripts: "+target).FontSize(textMeta).TextColor(t.TextMuted).SingleLine().Grow(1)
	})
	if !d.variablesOpen {
		return
	}
	ui.Column(c).Height(160).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(t.Space(2)).Children(func() {
			ui.Text(c, "Draft context · pre-script may generate or replace values").FontSize(textMeta).TextColor(t.TextMuted).Grow(1)
			if ui.Button(c, "Edit environments").FontSize(textMeta).Clicked() {
				a.openVars()
			}
		})
		variableRows(c, rows, &d.variablesReveal)
	})
	ui.Divider(c)
}

func variableRows(c *ui.Context, rows []request.ResolvedVariable, reveal *bool) {
	t := c.Theme()
	ui.Checkbox(c, reveal, "Reveal values (plaintext)").FontSize(textMeta)
	ui.Scroll(c).Grow(1).FillWidth().Gap(t.Space(2)).Children(func() {
		if len(rows) == 0 {
			ui.Text(c, "No referenced variables in this snapshot.").FontSize(textMeta).TextColor(t.TextMuted)
		}
		for _, row := range rows {
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				ui.Text(c, row.Name).Width(140).Font(fontMono).FontSize(textMeta)
				value, color := "••••••", t.Text
				if row.Missing {
					value, color = "Unresolved", t.Warning
				} else if *reveal {
					value = row.Value
					if value == "" {
						value = "(empty value)"
					}
				}
				ui.Text(c, value).FontSize(textMeta).TextColor(color).Grow(1).Selectable()
				ui.Text(c, row.Source).Width(180).FontSize(textMeta).TextColor(t.TextMuted)
			})
		}
	})
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
	ui.Row(c).Height(48).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Grow(1).Height(heightLarge).Border(1, t.Border).Radius(radiusControl).AlignItems(ui.Center).Children(func() {
			// An editable dropdown: a click lists the common methods, and a method
			// the list lacks is typed and kept as it is. The chevron and the hover
			// background say it is a control, not a label.
			control := ui.Row(c).Gap(t.Space(0.5)).Padding(0, t.Space(1)).AlignItems(ui.Center)
			if control.Hovered() {
				control.Background(t.SurfaceHover)
			}
			control.Children(func() {
				method := ui.ComboboxBase(c, &d.composeMethod)
				method.Input.Width(72).Height(heightControl).Padding(0, t.Space(1)).Label("Method").FontSize(textBody).FontWeight(600).
					TextColor(methodColor(d.composeMethod, t)).Border(0, t.Border)
				if method.Input.Changed() {
					d.composeMethod = strings.ToUpper(strings.TrimSpace(d.composeMethod))
				}
				method.Popup(func(panel ui.Element) {
					popoverPanel(c, panel).Padding(t.Space(1))
					query := strings.ToUpper(strings.TrimSpace(d.composeMethod))
					for _, m := range requestMethods {
						if method.Filtering() && query != "" && !strings.HasPrefix(m, query) {
							continue
						}
						item := method.Item(m).Height(heightControl).Padding(0, t.Space(2)).Radius(radiusControl).AlignItems(ui.Center)
						if item.Highlighted() {
							item.Background(t.SurfaceHover).TextColor(t.Text)
						}
						item.Children(func() { ui.Text(c, m).FontSize(textBody).TextColor(methodColor(m, t)) })
					}
				})
				if chosen, ok := method.Chosen(); ok {
					d.composeMethod = chosen
				}
				arrow := ui.ButtonBase(c).Size(18, 18).Radius(radiusControl).Label("Method list").Tooltip("Common methods").AlignItems(ui.Center)
				if arrow.Hovered() {
					arrow.Background(t.SurfacePressed)
				}
				if arrow.Children(func() { ui.Icon(c, chevronIcon).Size(iconInline, iconInline).TextColor(t.TextMuted) }).Clicked() {
					method.SetOpen(!method.Open())
					method.Input.Focus()
				}
			})
			ui.Box(c).Width(1).Height(18).Background(t.Border)
			input := ui.TextInputBase(c, &d.composeURL).Bind(&d.urlInput).Grow(1).Height(heightControl).Padding(0, t.Space(2)).Label("URL").
				Placeholder("https://api.example.com/v1/resource").Font(fontMono).FontSize(textBody)
			spans := urlSpans(d.composeURL, t)
			at := 0
			hasVariables := false
			for _, span := range spans {
				end := at + utf8.RuneCountInString(span.Text)
				if span.Background.A > 0 {
					hasVariables = true
					input.TextRanges(ui.TextRange{Start: at, End: end, Color: t.Accent, Weight: 600})
				}
				at = end
			}
			if !input.Focused() && hasVariables {
				// Paint chips only at rest. The same native input always owns
				// selection, undo, clipboard and IME; editing shows styled text.
				input.DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Fill(r, t.Background, 0)
					r.X, r.W = r.X+8, max(0, r.W-16)
					p.Clip(r, 0, func() {
						_, h := p.MeasureText(0, spans...)
						p.RichText(r.X, r.Y+(r.H-h)/2, 0, spans...)
					})
				})
			}
		})
		sendLabel := "Send"
		if d.composeWaiting {
			sendLabel = "Sending…"
		}
		a.desktopButton(c, "send", sendLabel).Label(sendLabel).Width(80).Height(heightLarge).Radius(radiusControl).FontSize(textBody).Background(t.Accent).TextColor(t.AccentText)
		if d.composeWaiting {
			label := "Cancel"
			if d.sendCancelling {
				label = "Cancelling…"
			}
			a.desktopButton(c, "cancel-send", label).Label("Cancel send").Width(96).Height(heightLarge).
				Tooltip("Stop waiting; cancelling does not undo server-side changes")
		}
	})
	ui.Divider(c)
}

// urlSpans styles references, never expands them or reads their secret values.
func urlSpans(raw string, t *ui.Theme) []ui.Span {
	var spans []ui.Span
	for raw != "" {
		start := strings.Index(raw, "{{")
		if start < 0 {
			spans = append(spans, ui.Span{Text: raw, Font: fontMono, Size: textBody, Color: t.Text})
			break
		}
		end := strings.Index(raw[start+2:], "}}")
		if end < 0 {
			spans = append(spans, ui.Span{Text: raw, Font: fontMono, Size: textBody, Color: t.Text})
			break
		}
		if start > 0 {
			spans = append(spans, ui.Span{Text: raw[:start], Font: fontMono, Size: textBody, Color: t.Text})
		}
		end += start + 4
		spans = append(spans, ui.Span{Text: raw[start:end], Font: fontMono, Size: textBody, Color: t.Accent, Weight: 600, Background: t.Accent.Alpha(0.12), Radius: radiusControl})
		raw = raw[end:]
	}
	return spans
}

// requestEditor is the draft's own body: parameters, auth, headers, body and
// scripts, each a tab of the same document.
func (a *app) requestEditor(c *ui.Context, t *ui.Theme, d *requestDocument) {
	wide := a.contentWidth(c) >= 900
	bodyTools := func() {
		ui.Select(c, &d.bodyMode, []string{"Raw", "URL encoded", "Multipart"}).Label("Request body type").Width(132).Height(heightControl).FontSize(textMeta)
		formatted, isJSON := indentJSON(d.composeBody)
		if ui.ButtonBase(c).Label("Format body").Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).
			TextColor(t.TextMuted).Disabled(!isJSON || d.bodyMode != "Raw").
			Tooltip("Indent the JSON body; the bytes sent stay what the editor shows").Children(func() {
			ui.Icon(c, formatIcon).Size(iconInline, iconInline)
			ui.Text(c, "Format").FontSize(textMeta)
		}).Clicked() {
			// Rebuild the editor rather than writing its old buffer back.
			d.composeBody, d.bodyFor = formatted, ""
		}
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(heightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			counts := make([]string, 7)
			if u, err := url.Parse(d.composeURL); err == nil {
				n := 0
				for _, values := range u.Query() {
					n += len(values)
				}
				counts[composeTabParams] = fmt.Sprint(n)
			}
			headers := d.headerRows
			if d.headerBulk || headers == nil {
				headers = parseHeaderRows(d.composeHeaders)
			}
			n := 0
			for _, row := range headers {
				if !row.Disabled && row.Name != "" {
					n++
				}
			}
			counts[composeTabHeaders] = fmt.Sprint(n)
			n = 0
			if strings.TrimSpace(d.checks.Status) != "" {
				n++
			}
			for _, block := range []string{d.checks.Headers, d.checks.JSON} {
				for line := range strings.SplitSeq(block, "\n") {
					if strings.TrimSpace(line) != "" {
						n++
					}
				}
			}
			counts[composeTabChecks], counts[composeTabExamples] = fmt.Sprint(n), fmt.Sprint(len(d.examples))
			ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
				requestTabs(c, &d.composeTab, "Request editor", []string{"Params", "Headers", "Auth", "Body", "Scripts", "Checks", "Examples"}, counts)
			})
			if wide && d.composeTab == composeTabBody {
				bodyTools()
			}
			if d.composeTab == composeTabHeaders {
				a.headerModeButton(c, d, t)
			}
		})
		if !wide && d.composeTab == composeTabBody {
			ui.Row(c).Height(heightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				bodyTools()
			})
		}
		ui.Column(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
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
			case composeTabExamples:
				a.examplesEditor(c, d, t)
			}
		})
		// A visible response already names the last attempt and draft changes.
		if d.composeWaiting || d.sentSet && d.response == nil {
			ui.Row(c).Height(heightCompact).Padding(0, t.Space(3)).AlignItems(ui.Center).Children(func() {
				a.requestSentState(c, t, d)
			})
		}
	})
}

// Counts are presentation, not part of each tab's stable accessible name.
func requestTabs(c *ui.Context, selected *int, prefix string, labels, counts []string) {
	t := c.Theme()
	group := ui.TabsBase(c, selected, len(labels))
	group.List.Gap(t.Space(0.5)).Children(func() {
		for i, label := range labels {
			tab := group.Tab(i).Label(prefix+": "+label).Height(heightTabs).Padding(0, t.Space(1)).Gap(t.Space(1)).Radius(0).
				AlignItems(ui.Center).TextColor(t.TextMuted)
			if i == *selected {
				tab.TextColor(t.Text).Border(0, t.Accent).BorderWidth(0, 0, 2, 0)
			}
			if tab.Pressed() {
				tab.Background(t.SurfacePressed)
			} else if tab.Hovered() {
				tab.Background(t.SurfaceHover)
			}
			tab.Children(func() {
				shown := label
				if label == "Tests" {
					shown = "Checks"
				}
				text := ui.Text(c, shown).FontSize(textBody)
				if i == *selected {
					text.FontWeight(600)
				}
				if counts[i] != "" && counts[i] != "0" {
					ui.Text(c, counts[i]).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted)
				}
			})
		}
	})
}

// requestSentState says whether the draft on screen is the one that was
// actually sent, and when.
func (a *app) requestSentState(c *ui.Context, t *ui.Theme, d *requestDocument) {
	switch {
	case d.composeWaiting:
		ui.Text(c, "Sending…").FontSize(textMeta).TextColor(t.TextMuted)
	case !d.sentSet:
		ui.Text(c, "Not sent yet").FontSize(textMeta).TextColor(t.TextMuted)
	case reflect.DeepEqual(d.draft(), d.sent):
		ui.Text(c, "Draft matches last attempt · "+d.sentAt.Local().Format("15:04:05")).FontSize(textMeta).TextColor(t.TextMuted).
			Tooltip("The draft snapshot matches; variables, scripts, redirects and rules may change the wire request")
	default:
		ui.Text(c, "Edited since last send · not yet sent").FontSize(textMeta).TextColor(t.Warning).
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
		ui.Row(c).Height(heightTabs).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			if !d.comparing && (d.response != nil || len(d.composeReplies) > 0 || len(d.console) > 0) {
				labels := []string{"Body", "Headers", "Cookies", "Timing", "Tests", "Console"}
				counts := make([]string, len(labels))
				if r := d.response; r != nil {
					n := 0
					for _, values := range r.ResponseHeader {
						n += len(values)
					}
					counts[resViewHeaders] = fmt.Sprint(n)
				}
				if len(d.composeTests) > 0 {
					passed := 0
					for _, check := range d.composeTests {
						if check.Pass {
							passed++
						}
					}
					counts[resViewTests] = fmt.Sprintf("%d/%d", passed, len(d.composeTests))
				}
				if d.responseTab >= len(labels) {
					d.responseTab = resViewBody
				}
				ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
					requestTabs(c, &d.responseTab, "Response tab", labels, counts)
				})
				if d.responseTab == resViewBody && d.response != nil {
					formats := []string{"Pretty", "Raw", "Hex"}
					ui.PopoverMenuButton(c, formats[d.responseView], func(m *ui.Menu) {
						for i, name := range formats {
							if m.Item(name).Checked(i == d.responseView).Chosen() {
								d.responseView, d.responseEditor, d.responseList = i, nil, ui.ListState{}
							}
						}
					}).Label("Response format").Tooltip("Response body format").Height(heightControl).FontSize(textMeta).Padding(0, t.Space(2))
				}
			} else {
				ui.Text(c, "Response").FontSize(textBody).FontWeight(600).Grow(1)
			}
			if r := d.response; r != nil {
				status := fmt.Sprintf("%d %s", r.Status, http.StatusText(r.Status))
				if outcome == "Network error" {
					status = fmt.Sprintf("%d · proxy response", r.Status)
				}
				shown := status
				if a.contentWidth(c) < 800 {
					shown = fmt.Sprint(r.Status)
					if outcome == "Network error" {
						shown += " · proxy"
					}
				}
				statusPill(c, shown, statusColor(r.Status, t)).Label(status).Tooltip(status).FontSize(textMeta).Padding(t.Space(1), t.Space(2))
				ui.Text(c, fmt.Sprintf("%d ms", r.Millis)).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted)
				ui.Text(c, humanBytes(r.Bytes)).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted)
			} else if d.composeWaiting {
				ui.Text(c, "Sending…").FontSize(textBody).TextColor(t.TextMuted)
			}
			if d.source != nil && d.response != nil && !d.comparing {
				if ui.ButtonBase(c).Label("Compare with source").Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).
					TextColor(t.TextMuted).Tooltip("Compare this response with the capture it came from").Children(func() {
					ui.Icon(c, compareIcon).Size(iconInline, iconInline)
				}).Clicked() {
					d.comparing = true
				}
			}
			if d.response != nil && !d.composeWaiting && outcome != "Network error" && d.sentSet {
				if ui.ButtonBase(c).Label("Save as example").Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).
					TextColor(t.TextMuted).Tooltip("Keep this response with the request, as a Postman example").Children(func() {
					ui.Icon(c, exampleIcon).Size(iconInline, iconInline)
				}).Clicked() {
					a.saveExample(d)
				}
			}
			// Without a retained send there is nothing to read, so the entry
			// waits rather than opening an empty list.
			if ui.ButtonBase(c).Label("Send history").Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).
				TextColor(t.TextMuted).Disabled(d.origin == nil && d.experimentID == 0).
				Tooltip("Inspect the exact request and result of each retained attempt; opening never sends").Children(func() {
				ui.Icon(c, sendHistoryIcon).Size(iconInline, iconInline)
			}).Clicked() {
				a.openSendHistory(d)
			}
		})
		if d.experimentID != 0 && !d.composeWaiting {
			ui.Text(c, fmt.Sprintf("Recorded experiment #%d", d.experimentID)).FontSize(textMeta).TextColor(t.TextMuted).Padding(t.Space(1), t.Space(3)).
				Tooltip("Retained result from this experiment; opening it does not send the draft")
		}
		if !d.composeWaiting && outcome != "" && outcome != "Response received" {
			ui.Text(c, outcome).FontSize(textMeta).TextColor(t.Warning).Padding(t.Space(1), t.Space(3))
		}
		if d.sentSet {
			ui.Row(c).Height(heightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				path := d.sent.URL
				if u, err := url.Parse(path); err == nil && u.Host != "" {
					path = cmp.Or(u.EscapedPath(), "/")
				}
				label := "Last attempt · " + d.sentAt.Local().Format("15:04:05") + " · " + d.sent.Method + " " + path
				if ui.ButtonBase(c).Label("Last send details").Grow(1).MinWidth(0).Height(heightControl).Gap(t.Space(2)).
					AlignItems(ui.Center).Justify(ui.Start).TextColor(t.TextMuted).Tooltip("Show the full draft URL and response time").Children(func() {
					arrow := "›"
					if d.lastAttemptOpen {
						arrow = "⌄"
					}
					ui.Text(c, arrow).FontSize(textTitle)
					ui.Text(c, label).FontSize(textMeta).SingleLine()
				}).Clicked() {
					d.lastAttemptOpen = !d.lastAttemptOpen
				}
				if !reflect.DeepEqual(d.draft(), d.sent) {
					ui.Text(c, "Draft changed · not sent").FontSize(textMeta).TextColor(t.Warning)
				} else if !d.composeWaiting {
					ui.Text(c, "Draft matches last attempt").FontSize(textMeta).TextColor(t.TextMuted).
						Tooltip("The draft snapshot matches; variables, scripts, redirects and rules may change the wire request")
				}
			})
			if d.lastAttemptOpen {
				ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(t.Surface).Children(func() {
					ui.Text(c, d.sent.Method+" "+d.sent.URL).Label("Last sent draft URL").FontSize(textBody).Selectable()
					ui.Text(c, "Draft snapshot · variables, scripts, redirects and proxy rules may change the wire request. Inspect Send history for retained wire evidence.").FontSize(textMeta).TextColor(t.TextMuted)
					if d.response != nil {
						ui.Text(c, "Response from "+d.response.At.Local().Format("15:04:05")).FontSize(textMeta).TextColor(t.TextMuted)
					}
				})
			}
		}
		if outcome == "Cancelled" && (d.response != nil || len(d.composeReplies) > 0 || d.responseTab == resViewConsole) {
			ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() { a.cancelledResult(c, t, d) })
		} else if outcome == "Network error" && d.response != nil || outcome == "Post-response script failed" || outcome == "Timed out" && d.response != nil {
			ui.Text(c, outcome+": "+detail).FontSize(textMeta).TextColor(t.Danger).Padding(t.Space(2), t.Space(3)).Selectable()
		}
		if d.response == nil && len(d.composeReplies) == 0 && len(d.console) == 0 {
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
		a.responseBody(c, t, d)
		a.responseProvenance(c, t, d)
	})
}

func (a *app) cancelledResult(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Text(c, "Waiting cancelled. The server may still be processing; check whether sending again would repeat the operation.").FontSize(textBody).TextColor(t.Text)
	ui.Collapsible(c, "Technical details", &d.sendDetails, func() {
		ui.Text(c, d.composeResult).FontSize(textMeta).TextColor(t.TextMuted).Selectable().FillWidth()
	}).FontSize(textBody)
}

// responseProvenance says when the response arrived and whether the capture it
// came from was rewritten by sending it again.
func (a *app) responseProvenance(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil || d.sentSet && !d.comparing {
		return
	}
	provenance := "Response from " + d.response.At.Local().Format("15:04:05")
	ui.Text(c, provenance).FontSize(textMeta).TextColor(t.TextMuted).Padding(t.Space(2), t.Space(3)).
		Tooltip("Redirects, cookies and proxy rules may change the on-wire exchange; Capture has the wire details")
}

// responseBody shows the chosen view of the last response.
func (a *app) responseBody(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.responseTab == resViewConsole {
		runConsole(c, d.console)
		return
	}
	if d.response == nil && len(d.composeReplies) == 0 {
		ui.Scroll(c).Grow(1).FillWidth().Padding(t.Space(4)).Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(2)).Children(func() {
				if d.composeWaiting {
					ui.Spinner(c).Label("Waiting for request result")
					ui.Text(c, "Waiting for response… · 30 s timeout").FontSize(textBody).TextColor(t.TextMuted)
				} else if d.sendOutcome == "Cancelled" {
					a.cancelledResult(c, t, d)
				} else if d.sendOutcome != "" {
					ui.Text(c, d.sendOutcome).FontSize(textBody).TextColor(t.Warning)
					ui.Text(c, d.composeResult).FontSize(textBody).TextColor(t.Text).Selectable().FillWidth()
					ui.Text(c, "Edit the draft and send again. No automatic retry.").FontSize(textMeta).TextColor(t.TextMuted)
				} else {
					ui.Text(c, cmp.Or(d.composeResult, "Send a request to see its response")).FontSize(textBody).TextColor(t.TextMuted)
					ui.Text(c, "The exchange will also appear in Capture.").FontSize(textMeta).TextColor(t.TextMuted)
				}
				if d.composeWaiting || d.sendOutcome == "Timed out" {
					ui.Text(c, "Cancelling does not undo server-side changes.").FontSize(textMeta).TextColor(t.TextMuted)
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
				ui.Text(c, "Preview limited to 8 MiB; the complete body is in Capture.").FontSize(textMeta).TextColor(t.TextMuted).Padding(t.Space(1), t.Space(3))
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
			d.responseEditor.Theme = bodyEditorTheme(t)
			editor.View(c, d.responseEditor).Grow(1)
		}
	case resViewHeaders:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Children(func() {
			if d.response != nil {
				ui.Text(c, request.HeaderBlock(d.response.ResponseHeader)).Font(fontMono).FontSize(textBody)
			}
		})
	case resViewCookies:
		a.responseCookies(c, t, d)
	case resViewTiming:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			if d.response == nil {
				ui.Text(c, "No response yet.").FontSize(textBody).TextColor(t.TextMuted)
				return
			}
			ui.Text(c, fmt.Sprintf("Total %d ms · %s", d.response.Millis, humanBytes(d.response.Bytes))).FontSize(textBody)
			ui.Text(c, "Per-phase timing (DNS, connect, TLS, wait, download) is recorded on the capture in Capture → Timing.").FontSize(textMeta).TextColor(t.TextMuted)
			index := a.sentFlowIndex(d)
			if ui.Button(c, "Inspect timing in Capture").FontSize(textMeta).Disabled(index < 0).Clicked() {
				if index >= 0 {
					a.inspectCapture(a.flows[index].id)
					a.syncDetail()
					a.inspect.pane, a.inspect.ready = inspectorSplit, true
					a.inspect.tab[1] = resTabTiming
				}
			}
			if index < 0 {
				ui.Text(c, "This send's wire capture is not available in this session. Send history may retain its snapshot.").FontSize(textMeta).TextColor(t.TextMuted)
			}
		})
	case resViewTests:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			if len(d.composeTests) == 0 {
				ui.Text(c, "No assertions. Add response Checks, or pm.test(name, fn) in a script.").FontSize(textBody).TextColor(t.TextMuted)
			}
			for _, result := range d.composeTests {
				testResultRow(c, result)
			}
		})
	}
}

// testResultRow is one assertion: its mark and name, and why a failed one
// failed beneath.
func testResultRow(c *ui.Context, result request.TestResult) {
	t := c.Theme()
	mark, color := "✓", t.Success
	switch {
	case result.Skipped:
		mark, color = "–", t.TextMuted
	case !result.Pass:
		mark, color = "✗", t.Danger
	}
	name := mark + " " + result.Name
	if result.Skipped {
		name += " · skipped"
	}
	ui.Column(c).Gap(t.Space(0.5)).Children(func() {
		ui.Text(c, name).FontSize(textBody).TextColor(color)
		if result.Error != "" {
			ui.Text(c, result.Error).FontSize(textMeta).TextColor(t.TextMuted).Padding(0, 0, 0, t.Space(3)).Selectable()
		}
	})
}

func runConsole(c *ui.Context, entries []request.ConsoleMessage) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
		if len(entries) == 0 {
			ui.Text(c, "No output. Call console.log(...) in a script to print here.").FontSize(textBody).TextColor(t.TextMuted)
		}
		for _, entry := range entries {
			color := t.Text
			if entry.Kind == "error" {
				color = t.Danger
			}
			ui.Text(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).Font(fontMono).FontSize(textBody).TextColor(color).Selectable().FillWidth()
		}
	})
}

// responseCompare fills the response area with the last send beside the
// capture it came from, and puts the response tabs back when it closes.
func (a *app) responseCompare(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(heightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if ui.ButtonBase(c).Label("Back to response").Height(heightCompact).Padding(0, t.Space(2)).Gap(t.Space(1)).Radius(radiusControl).AlignItems(ui.Center).
			TextColor(t.TextMuted).Children(func() {
			ui.Icon(c, backIcon).Size(iconInline, iconInline)
			ui.Text(c, "Back to response").FontSize(textMeta)
		}).Clicked() {
			d.comparing = false
		}
		ui.Text(c, "Comparing with the source capture").FontSize(textMeta).TextColor(t.TextMuted)
	})
	if d.source == nil || d.response == nil {
		ui.Text(c, "The source capture is no longer in this session.").FontSize(textBody).TextColor(t.TextMuted).Padding(t.Space(3))
		return
	}
	r := d.response
	right := flow{method: r.Method, path: r.URL, url: r.URL, status: r.Status, millis: r.Millis,
		reqBody: r.RequestBody, reqSize: r.RequestBodySize, reqHeaders: request.HeaderBlock(r.RequestHeader),
		resBody: r.ResponseBody, resSize: r.ResponseBodySize, resHeaders: request.HeaderBlock(r.ResponseHeader)}
	if r.RequestBodySize > len(r.RequestBody) {
		right.reqError = "preview limited to 8 MiB"
	}
	if r.ResponseBodySize > len(r.ResponseBody) {
		right.resError = "preview limited to 8 MiB"
	}
	ui.Text(c, "Last send: client request and final response. Capture has the wire exchange.").FontSize(textMeta).TextColor(t.TextMuted).Padding(t.Space(1), t.Space(3))
	d.comparison.compare(c, *d.source, right, [2]string{fmt.Sprintf("Source #%d", d.sourceFlow), "Last send"})
}

// responseCookies lists what the response asked the client to store.
func (a *app) responseCookies(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil {
		ui.Text(c, "No response yet.").FontSize(textBody).TextColor(t.TextMuted).Padding(t.Space(3))
		return
	}
	cookies := d.response.ResponseHeader.Values("Set-Cookie")
	if len(cookies) == 0 {
		ui.Text(c, "This response set no cookies.").FontSize(textBody).TextColor(t.TextMuted).Padding(t.Space(3))
		return
	}
	ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(1)).Children(func() {
		for _, cookie := range cookies {
			ui.Text(c, cookie).FontSize(textBody).Font(fontMono).Selectable().SingleLine().Tooltip(cookie)
		}
	})
}

// responseHex renders a response body as a hex dump.
func (a *app) responseHex(c *ui.Context, d *requestDocument, body string) {
	rows := hexRows(body, "")
	if len(rows) == 0 {
		ui.Text(c, "Empty body").FontSize(textBody).TextColor(c.Theme().TextMuted).Padding(c.Theme().Space(3))
		return
	}
	ui.List(c, &d.responseList, len(rows), func(i int) {
		ui.Text(c, hexLine(body, rows[i])).FontSize(textMeta).Font(fontMono).TextColor(c.Theme().Text).Padding(c.Theme().Space(1), c.Theme().Space(3)).SingleLine()
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
