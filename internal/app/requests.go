package app

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
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
	id       int
	debugKey string
	// The History / Examples column of a saved request: which list shows,
	// the retained run shown read-only in place of the response (0 for the
	// document's own response), and its reader.
	historyTab        int
	historyPick       int
	historyReader     *editor.State
	origin            *debugSource
	assetID           string
	savedKey          string
	baseline          savedRequest
	sourceFlow        int
	experimentID      int
	source            *flow
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
	d := a.newDocument(r, key)
	a.documents = append(a.documents, d)
	a.requestDocument = d
	a.requestChoice = key
	a.page = pageRequest
	d.urlInput.Focus()
}

// newDocument is a document for r that no tab or capture holds yet.
func (a *app) newDocument(r savedRequest, key string) *requestDocument {
	a.nextDocument++
	d := &requestDocument{
		id: a.nextDocument, debugKey: uuid.New().String(), requestPercent: draftPercent,
		composeTab: composeTabFor(r), baseline: savedRequest{Method: "GET", URL: "https://"},
	}
	d.load(r, key)
	return d
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
	// composeTabOriginal reads the captured request a draft came from, in
	// the capture's readers. Only a captured request has it.
	composeTabOriginal
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
		a.requestDocument, a.requestChoice, a.page = d, d.savedKey, pageRequest
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
	if !a.page.capture() {
		index = slices.Index(a.documents, a.requestDocument) + 1
	}
	index = (index + step + len(a.documents) + 1) % (len(a.documents) + 1)
	if index == 0 {
		a.page = pageLive
		if view := a.views.activeView(); view != nil {
			view.list.Handle.Focus()
		}
		return
	}
	a.requestDocument = a.documents[index-1]
	a.requestChoice, a.page = a.savedKey, pageRequest
	a.urlInput.Focus()
}

func (a *app) closeRequestModal(c *ui.Context, t *ui.Theme) {
	d := a.closingDocument
	if d == nil {
		return
	}
	open := true
	uikit.Modal(c, &open, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Save changes?")
			uikit.Hint(c, d.title()+" has unsaved changes. Closing it without saving will discard this draft.")
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.closingDocument = nil
				}
				if uikit.SecondaryButton(c, "Discard").Clicked() {
					a.closeDocument(d)
					a.closingDocument = nil
				}
				if uikit.PrimaryButton(c, "Save & close").Clicked() {
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

// documentHeader names the one request on screen as a breadcrumb — its
// collection, then its name, which renames or moves it — with the
// environment it sends in and its actions. Other open requests are in the
// sidebar: saved ones in their collection, new ones under Unsaved.
func (a *app) documentHeader(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(2)).Gap(t.Space(1)).AlignItems(ui.Center).Background(uikit.Colors(t).Panel).Children(func() {
		if d != nil {
			crumb := uikit.ToolbarButton(c, "Rename request", "Rename or move to another collection", false).Height(uikit.HeightCompact).
				Padding(0, t.Space(1)).Gap(t.Space(1)).MinWidth(0)
			crumb.Children(func() {
				if d.composeCollection != "" {
					uikit.Meta(c, strings.ReplaceAll(d.composeCollection, "/", " / ")+" /").SingleLine().MinWidth(0)
				}
				uikit.Body(c, d.title()).FontWeight(600).SingleLine().MinWidth(0)
				if d.dirty() {
					unsavedDot(c)
				}
			})
			if crumb.Clicked() {
				a.renameOpen, a.saveAfterRename = true, false
			}
		}
		ui.Spacer(c)
		if names := a.envNames(); len(names) > 0 {
			env := ui.SelectBase(c.Key("active-environment"), &a.activeEnv)
			env.Trigger.Label("Active environment").Tooltip("Active environment").Width(132).Height(uikit.HeightControl).
				Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(uikit.RadiusControl).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(t.Accent)
				uikit.Body(c, cmp.Or(a.activeEnv, "No environment")).SingleLine().Grow(1)
				uikit.Glyph(c, chevronIcon)
			})
			env.Popup(func(panel ui.Element) {
				uikit.PopoverPanel(c, panel).Padding(t.Space(1))
				for _, name := range names {
					item := env.Item(name).Label(name).Height(uikit.HeightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
					if item.Highlighted() {
						item.Background(t.SurfaceHover)
					}
					item.Children(func() {
						color := t.TextMuted
						if name == a.activeEnv {
							color = t.Accent
						}
						ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(color)
						uikit.Body(c, name)
					})
				}
			})
		} else {
			uikit.ToolbarButton(c, "Manage environments", "Add an environment for request variables", false).
				Children(func() { ui.Text(c, "No environment") }).OnClick(a.openVars)
		}
		if d != nil {
			a.requestDocumentActions(c, t, d)
			// A send in flight finishes before its request closes.
			ui.Row(c).Disabled(d.composeWaiting).Children(func() {
				if uikit.IconButton(c, "Close request", "Close this request · Ctrl/Cmd+W", closeIcon).Clicked() {
					a.requestClose(d)
				}
			})
		}
	})
	ui.Divider(c)
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
		uikit.EmptyState(c, "Open a saved request or create a new one", "Keep drafts, variables and responses together in one request tab.", func() {
			if uikit.PrimaryButton(c, "Create request").Clicked() {
				a.openCompose()
			}
		})
		return
	}
	ui.Column(c).Key(d.id).Fill().Children(func() {
		a.requestBar(c, t, d, nil)
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
			a.requestEditor(c, t, d, nil)
		}, func() {
			if r := a.experiments.record(d.historyPick); r != nil {
				a.historyResponse(c, t, d, r)
				return
			}
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
	uikit.Modal(c, &d.headerWarn, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Bulk edit shows credentials")
			uikit.Hint(c, "The Key: Value block writes every header value in plain text, including tokens, cookies and passwords. The table masks them one row at a time.")
			uikit.Hint(c, "Switching modes does not send a request. All enabled header values will be visible; be careful when sharing your screen.")
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					d.headerWarn = false
				}
				if uikit.PrimaryButton(c, "Show values").Clicked() {
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
	uikit.Modal(c, &a.renameOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			title := "Rename request"
			if a.saveAfterRename {
				title = "Save to collection"
			}
			uikit.DialogTitle(c, title)
			uikit.Input(c, &d.composeName).Label("Name").Placeholder("Untitled request").FillWidth().
				Height(uikit.HeightControl).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).FontSize(uikit.TextBody)
			uikit.Input(c, &d.composeCollection).Label("Collection").Placeholder("Collection").FillWidth().
				Height(uikit.HeightControl).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).FontSize(uikit.TextBody)
			uikit.Meta(c, "The collection groups saved requests in the library.")
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.renameOpen = false
				}
				if a.saveAfterRename {
					if uikit.PrimaryButton(c, "Save").Clicked() && a.saveComposed() {
						a.renameOpen = false
					}
					return
				}
				if uikit.PrimaryButton(c, "Done").Clicked() {
					a.renameOpen = false
				}
			})
		})
	})
}

// requestDocumentActions never scrolls away with a long document tab strip.
func (a *app) requestDocumentActions(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Key(d.id).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
		if d.sourceAPI != "" {
			if uikit.ToolbarButton(c, "Source API", "Open the API this request came from", false).Height(uikit.HeightCompact).Children(func() {
				ui.Text(c, "Source API").TextColor(t.Accent)
			}).Clicked() {
				a.apiChoice, a.page = d.sourceAPI, pageAPI
			}
		}
		a.desktopButton(c, "save-request", "Save")
		uikit.MoreMenu(c, "Request options", func(m *ui.Menu) {
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
		}).Height(uikit.HeightControl).FontSize(uikit.TextTitle)
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
	ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.CompactButton(c, fmt.Sprintf("Variables · %d unresolved", missing)).Label("Request variables").Clicked() {
			d.variablesOpen = !d.variablesOpen
		}
		uikit.Meta(c, "Target before scripts: "+target).SingleLine().Grow(1)
	})
	if !d.variablesOpen {
		return
	}
	ui.Column(c).Height(160).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(uikit.Colors(t).Panel).Children(func() {
		ui.Row(c).Gap(t.Space(2)).Children(func() {
			uikit.Meta(c, "Draft context · pre-script may generate or replace values").Grow(1)
			if uikit.CompactButton(c, "Edit environments").Clicked() {
				a.openVars()
			}
		})
		variableRows(c, rows, &d.variablesReveal)
	})
	ui.Divider(c)
}

func variableRows(c *ui.Context, rows []request.ResolvedVariable, reveal *bool) {
	t := c.Theme()
	ui.Checkbox(c, reveal, "Reveal values (plaintext)").FontSize(uikit.TextMeta)
	ui.Scroll(c).Grow(1).FillWidth().Gap(t.Space(2)).Children(func() {
		if len(rows) == 0 {
			uikit.Meta(c, "No referenced variables in this snapshot.")
		}
		for _, row := range rows {
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				ui.Text(c, row.Name).Width(140).Font(uikit.FontMono).FontSize(uikit.TextMeta)
				value, color := "••••••", t.Text
				if row.Missing {
					value, color = "Unresolved", t.Warning
				} else if *reveal {
					value = row.Value
					if value == "" {
						value = "(empty value)"
					}
				}
				ui.Text(c, value).FontSize(uikit.TextMeta).TextColor(color).Grow(1).Selectable()
				uikit.Meta(c, row.Source).Width(180)
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
// requestBar is the method, URL and Send; more adds what follows Send.
func (a *app) requestBar(c *ui.Context, t *ui.Theme, d *requestDocument, more func()) {
	ui.Row(c).Height(48).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Grow(1).Height(uikit.HeightLarge).Border(1, t.Border).Radius(uikit.RadiusControl).AlignItems(ui.Center).Children(func() {
			// An editable dropdown: a click lists the common methods, and a method
			// the list lacks is typed and kept as it is. The chevron and the hover
			// background say it is a control, not a label.
			control := ui.Row(c).Gap(t.Space(0.5)).Padding(0, t.Space(1)).AlignItems(ui.Center)
			if control.Hovered() {
				control.Background(t.SurfaceHover)
			}
			control.Children(func() {
				method := ui.ComboboxBase(c, &d.composeMethod)
				method.Input.Width(72).Height(uikit.HeightControl).Padding(0, t.Space(1)).Label("Method").FontSize(uikit.TextBody).FontWeight(600).
					TextColor(uikit.MethodColor(d.composeMethod, t)).Border(0, t.Border)
				if method.Input.Changed() {
					d.composeMethod = strings.ToUpper(strings.TrimSpace(d.composeMethod))
				}
				method.Popup(func(panel ui.Element) {
					uikit.PopoverPanel(c, panel).Padding(t.Space(1))
					query := strings.ToUpper(strings.TrimSpace(d.composeMethod))
					for _, m := range requestMethods {
						if method.Filtering() && query != "" && !strings.HasPrefix(m, query) {
							continue
						}
						item := method.Item(m).Height(uikit.HeightControl).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
						if item.Highlighted() {
							item.Background(t.SurfaceHover).TextColor(t.Text)
						}
						item.Children(func() { ui.Text(c, m).FontSize(uikit.TextBody).TextColor(uikit.MethodColor(m, t)) })
					}
				})
				if chosen, ok := method.Chosen(); ok {
					d.composeMethod = chosen
				}
				if uikit.IconButton(c, "Method list", "Common methods", chevronIcon).Size(18, 18).Clicked() {
					method.SetOpen(!method.Open())
					method.Input.Focus()
				}
			})
			ui.Box(c).Width(1).Height(18).Background(t.Border)
			input := ui.TextInputBase(c, &d.composeURL).Bind(&d.urlInput).Grow(1).Height(uikit.HeightControl).Padding(0, t.Space(2)).Label("URL").
				Placeholder("https://api.example.com/v1/resource").Font(uikit.FontMono).FontSize(uikit.TextBody)
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
		// A capture's inspector beside the list keeps its width for the URL:
		// a narrower Send, and while sending only Cancel in its place (the
		// response says it is sending).
		narrow := a.page.capture() && a.inspectorWidth(c) < 600
		sendWidth, cancelWidth := float32(80), float32(96)
		if narrow {
			sendWidth, cancelWidth = 64, 76
		}
		if !narrow || !d.composeWaiting {
			a.desktopButton(c, "send", sendLabel).Label(sendLabel).Width(sendWidth).Height(uikit.HeightLarge).Radius(uikit.RadiusControl).FontSize(uikit.TextBody).Background(t.Accent).TextColor(t.AccentText)
		}
		if d.composeWaiting {
			label := "Cancel"
			if d.sendCancelling && !narrow {
				label = "Cancelling…"
			}
			a.desktopButton(c, "cancel-send", label).Label("Cancel send").Width(cancelWidth).Height(uikit.HeightLarge).
				Tooltip("Stop waiting; cancelling does not undo server-side changes")
		}
		if more != nil {
			more()
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
			spans = append(spans, ui.Span{Text: raw, Font: uikit.FontMono, Size: uikit.TextBody, Color: t.Text})
			break
		}
		end := strings.Index(raw[start+2:], "}}")
		if end < 0 {
			spans = append(spans, ui.Span{Text: raw, Font: uikit.FontMono, Size: uikit.TextBody, Color: t.Text})
			break
		}
		if start > 0 {
			spans = append(spans, ui.Span{Text: raw[:start], Font: uikit.FontMono, Size: uikit.TextBody, Color: t.Text})
		}
		end += start + 4
		spans = append(spans, ui.Span{Text: raw[start:end], Font: uikit.FontMono, Size: uikit.TextBody, Color: t.Accent, Weight: 600, Background: t.Accent.Alpha(0.12), Radius: uikit.RadiusControl})
		raw = raw[end:]
	}
	return spans
}

// requestEditor is the draft's own body: parameters, auth, headers, body and
// scripts, each a tab of the same document.
// requestEditor edits the request's parts; original, when set, reads the
// captured request it came from in its own tab.
func (a *app) requestEditor(c *ui.Context, t *ui.Theme, d *requestDocument, original func()) {
	labels := []string{"Params", "Headers", "Auth", "Body", "Scripts", "Checks", "Examples"}
	if original != nil {
		labels = append(labels, "Original")
	} else if d.composeTab == composeTabOriginal {
		d.composeTab = composeTabParams
	}
	// Beside a capture's response the editor has half the width: its body
	// tools take their own row there.
	wide := a.contentWidth(c) >= 900 && original == nil
	bodyTools := func() {
		uikit.Select(c, &d.bodyMode, []string{"Raw", "URL encoded", "Multipart"}).Label("Request body type").Width(132).FontSize(uikit.TextMeta)
		formatted, isJSON := indentJSON(d.composeBody)
		ui.Row(c).Disabled(!isJSON || d.bodyMode != "Raw").Children(func() {
			if uikit.ToolbarButton(c, "Format body", "Indent the JSON body; the bytes sent stay what the editor shows", false).Children(func() {
				ui.Icon(c, formatIcon).Size(uikit.IconInline, uikit.IconInline)
				ui.Text(c, "Format").FontSize(uikit.TextMeta)
			}).Clicked() {
				// Rebuild the editor rather than writing its old buffer back.
				d.composeBody, d.bodyFor = formatted, ""
			}
		})
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			counts := make([]string, len(labels))
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
				if original == nil {
					uikit.CountedTabs(c, &d.composeTab, "Request editor", labels, counts)
					return
				}
				// A capture reads first: Original leads its tabs, so the tab
				// it opens on is the one in view, and the arrows follow the
				// order shown.
				n := len(labels)
				shown := (d.composeTab + 1) % n
				uikit.CountedTabs(c, &shown, "Request editor", slices.Concat(labels[n-1:], labels[:n-1]), slices.Concat(counts[n-1:], counts[:n-1]))
				d.composeTab = (shown + n - 1) % n
			})
			if wide && d.composeTab == composeTabBody {
				bodyTools()
			}
			if d.composeTab == composeTabHeaders {
				a.headerModeButton(c, d, t)
			}
		})
		if !wide && d.composeTab == composeTabBody {
			ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
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
					uikit.Segmented(c, &d.scriptTab, "Pre-request", "Post-response")
				})
				a.requestScriptText(c, d)
			case composeTabChecks:
				a.checksEditor(c, d, t)
			case composeTabExamples:
				a.examplesEditor(c, d, t)
			case composeTabOriginal:
				original()
			}
		})
		// A visible response already names the last attempt and draft changes.
		if d.composeWaiting || d.sentSet && d.response == nil {
			ui.Row(c).Height(uikit.HeightCompact).Padding(0, t.Space(3)).AlignItems(ui.Center).Children(func() {
				a.requestSentState(c, t, d)
			})
		}
	})
}

// requestSentState says whether the draft on screen is the one that was
// actually sent, and when.
func (a *app) requestSentState(c *ui.Context, t *ui.Theme, d *requestDocument) {
	switch {
	case d.composeWaiting:
		uikit.Meta(c, "Sending…")
	case !d.sentSet:
		uikit.Meta(c, "Not sent yet")
	case reflect.DeepEqual(d.draft(), d.sent):
		uikit.Meta(c, "Draft matches last attempt · "+d.sentAt.Local().Format("15:04:05")).
			Tooltip("The draft snapshot matches; variables, scripts, redirects and rules may change the wire request")
	default:
		ui.Text(c, "Edited since last send · not yet sent").FontSize(uikit.TextMeta).TextColor(t.Warning).
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
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			if !d.comparing && (d.response != nil || len(d.composeReplies) > 0 || len(d.console) > 0) {
				labels := []string{"Body", "Headers", "Cookies", "Timing", "Checks", "Console"}
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
					uikit.CountedTabs(c, &d.responseTab, "Response tab", labels, counts)
				})
				if d.responseTab == resViewBody && d.response != nil {
					formats := []string{"Pretty", "Raw", "Hex"}
					ui.PopoverMenuButton(c, formats[d.responseView], func(m *ui.Menu) {
						for i, name := range formats {
							if m.Item(name).Checked(i == d.responseView).Chosen() {
								d.responseView, d.responseEditor, d.responseList = i, nil, ui.ListState{}
							}
						}
					}).Label("Response format").Tooltip("Response body format").Height(uikit.HeightControl).FontSize(uikit.TextMeta).Padding(0, t.Space(2))
				}
			} else {
				ui.Text(c, "Response").FontSize(uikit.TextBody).FontWeight(600).Grow(1)
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
				uikit.StatusPill(c, shown, uikit.StatusColor(r.Status, t)).Label(status).Tooltip(status).FontSize(uikit.TextMeta).Padding(t.Space(1), t.Space(2))
				uikit.Meta(c, fmt.Sprintf("%d ms", r.Millis)).FontFeatures("tnum")
				uikit.Meta(c, humanBytes(r.Bytes)).FontFeatures("tnum")
			} else if d.composeWaiting {
				uikit.Hint(c, "Sending…")
			}
			if d.source != nil && d.response != nil && !d.comparing {
				if uikit.IconButton(c, "Compare with source", "Compare this response with the capture it came from", compareIcon).
					Size(uikit.HeightControl, uikit.HeightControl).Clicked() {
					d.comparing = true
				}
			}
			if d.response != nil && !d.composeWaiting && outcome != "Network error" && d.sentSet {
				if uikit.IconButton(c, "Save as example", "Keep this response with the request, as a Postman example", exampleIcon).
					Size(uikit.HeightControl, uikit.HeightControl).Clicked() {
					a.saveExample(d)
				}
			}
			// Without a retained send there is nothing to read, so the entry
			// waits rather than opening an empty list.
			ui.Row(c).Disabled(d.origin == nil && d.experimentID == 0).Children(func() {
				if uikit.IconButton(c, "Send history", "Inspect the exact request and result of each retained attempt; opening never sends", sendHistoryIcon).
					Size(uikit.HeightControl, uikit.HeightControl).Clicked() {
					a.openSendHistory(d)
				}
			})
		})
		if d.experimentID != 0 && !d.composeWaiting {
			uikit.Meta(c, fmt.Sprintf("Recorded experiment #%d", d.experimentID)).Padding(t.Space(1), t.Space(3)).
				Tooltip("Retained result from this experiment; opening it does not send the draft")
		}
		if !d.composeWaiting && outcome != "" && outcome != "Response received" {
			ui.Text(c, outcome).FontSize(uikit.TextMeta).TextColor(t.Warning).Padding(t.Space(1), t.Space(3))
		}
		if d.sentSet {
			ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				path := d.sent.URL
				if u, err := url.Parse(path); err == nil && u.Host != "" {
					path = cmp.Or(u.EscapedPath(), "/")
				}
				label := "Last attempt · " + d.sentAt.Local().Format("15:04:05") + " · " + d.sent.Method + " " + path
				if uikit.ToolbarButton(c, "Last send details", "Show the full draft URL and response time", false).Grow(1).MinWidth(0).
					Justify(ui.Start).Children(func() {
					arrow := "›"
					if d.lastAttemptOpen {
						arrow = "⌄"
					}
					ui.Text(c, arrow).FontSize(uikit.TextTitle)
					ui.Text(c, label).FontSize(uikit.TextMeta).SingleLine()
				}).Clicked() {
					d.lastAttemptOpen = !d.lastAttemptOpen
				}
				if !reflect.DeepEqual(d.draft(), d.sent) {
					ui.Text(c, "Draft changed · not sent").FontSize(uikit.TextMeta).TextColor(t.Warning)
				} else if !d.composeWaiting {
					uikit.Meta(c, "Draft matches last attempt").
						Tooltip("The draft snapshot matches; variables, scripts, redirects and rules may change the wire request")
				}
			})
			if d.lastAttemptOpen {
				ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(uikit.Colors(t).Panel).Children(func() {
					uikit.Body(c, d.sent.Method+" "+d.sent.URL).Label("Last sent draft URL").Selectable()
					uikit.Meta(c, "Draft snapshot · variables, scripts, redirects and proxy rules may change the wire request. Inspect Send history for retained wire evidence.")
					if d.response != nil {
						uikit.Meta(c, "Response from "+d.response.At.Local().Format("15:04:05"))
					}
				})
			}
		}
		if outcome == "Cancelled" && (d.response != nil || len(d.composeReplies) > 0 || d.responseTab == resViewConsole) {
			ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() { a.cancelledResult(c, t, d) })
		} else if outcome == "Network error" && d.response != nil || outcome == "Post-response script failed" || outcome == "Timed out" && d.response != nil {
			ui.Text(c, outcome+": "+detail).FontSize(uikit.TextMeta).TextColor(t.Danger).Padding(t.Space(2), t.Space(3)).Selectable()
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
	ui.Text(c, "Waiting cancelled. The server may still be processing; check whether sending again would repeat the operation.").FontSize(uikit.TextBody).TextColor(t.Text)
	ui.Collapsible(c, "Technical details", &d.sendDetails, func() {
		uikit.Meta(c, d.composeResult).Selectable().FillWidth()
	}).FontSize(uikit.TextBody)
}

// responseProvenance says when the response arrived and whether the capture it
// came from was rewritten by sending it again.
func (a *app) responseProvenance(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil || d.sentSet && !d.comparing {
		return
	}
	provenance := "Response from " + d.response.At.Local().Format("15:04:05")
	uikit.Meta(c, provenance).Padding(t.Space(2), t.Space(3)).
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
					uikit.Hint(c, "Waiting for response… · 30 s timeout")
				} else if d.sendOutcome == "Cancelled" {
					a.cancelledResult(c, t, d)
				} else if d.sendOutcome != "" {
					ui.Text(c, d.sendOutcome).FontSize(uikit.TextBody).TextColor(t.Warning)
					ui.Text(c, d.composeResult).FontSize(uikit.TextBody).TextColor(t.Text).Selectable().FillWidth()
					uikit.Meta(c, "Edit the draft and send again. No automatic retry.")
				} else {
					uikit.Hint(c, cmp.Or(d.composeResult, "Send a request to see its response"))
					uikit.Meta(c, "The exchange will also appear in Capture.")
				}
				if d.composeWaiting || d.sendOutcome == "Timed out" {
					uikit.Meta(c, "Cancelling does not undo server-side changes.")
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
				uikit.Meta(c, "Preview limited to 8 MiB; the complete body is in Capture.").Padding(t.Space(1), t.Space(3))
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
			d.responseEditor.Theme = uikit.EditorTheme(t)
			editor.View(c, d.responseEditor).Grow(1)
		}
	case resViewHeaders:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Children(func() {
			if d.response != nil {
				uikit.Mono(c, request.HeaderBlock(d.response.ResponseHeader))
			}
		})
	case resViewCookies:
		a.responseCookies(c, t, d)
	case resViewTiming:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			if d.response == nil {
				uikit.Hint(c, "No response yet.")
				return
			}
			uikit.Body(c, fmt.Sprintf("Total %d ms · %s", d.response.Millis, humanBytes(d.response.Bytes)))
			uikit.Meta(c, "Per-phase timing (DNS, connect, TLS, wait, download) is recorded on the capture in Capture → Timing.")
			index := a.sentFlowIndex(d)
			if uikit.CompactButton(c, "Inspect timing in Capture").Disabled(index < 0).Clicked() {
				if index >= 0 {
					a.inspectCapture(a.flows[index].id)
					a.syncDetail()
					a.inspect.pane, a.inspect.ready = inspectorSplit, true
					a.inspect.tab[1] = resTabTiming
				}
			}
			if index < 0 {
				uikit.Meta(c, "This send's wire capture is not available in this session. Send history may retain its snapshot.")
			}
		})
	case resViewTests:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			if len(d.composeTests) == 0 {
				uikit.Hint(c, "No assertions. Add response Checks, or pm.test(name, fn) in a script.")
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
		ui.Text(c, name).FontSize(uikit.TextBody).TextColor(color)
		if result.Error != "" {
			uikit.Meta(c, result.Error).Padding(0, 0, 0, t.Space(3)).Selectable()
		}
	})
}

func runConsole(c *ui.Context, entries []request.ConsoleMessage) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
		if len(entries) == 0 {
			uikit.Hint(c, "No output. Call console.log(...) in a script to print here.")
		}
		for _, entry := range entries {
			color := t.Text
			if entry.Kind == "error" {
				color = t.Danger
			}
			ui.Text(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).Font(uikit.FontMono).FontSize(uikit.TextBody).TextColor(color).Selectable().FillWidth()
		}
	})
}

// responseCompare fills the response area with the last send beside the
// capture it came from, and puts the response tabs back when it closes.
func (a *app) responseCompare(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.ToolbarButton(c, "Back to response", "Back to the response", false).Height(uikit.HeightCompact).Gap(t.Space(1)).Children(func() {
			ui.Icon(c, backIcon).Size(uikit.IconInline, uikit.IconInline)
			ui.Text(c, "Back to response").FontSize(uikit.TextMeta)
		}).Clicked() {
			d.comparing = false
		}
		uikit.Meta(c, "Comparing with the source capture")
	})
	if d.source == nil || d.response == nil {
		uikit.Hint(c, "The source capture is no longer in this session.").Padding(t.Space(3))
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
	uikit.Meta(c, "Last send: client request and final response. Capture has the wire exchange.").Padding(t.Space(1), t.Space(3))
	d.comparison.compare(c, *d.source, right, [2]string{fmt.Sprintf("Source #%d", d.sourceFlow), "Last send"})
}

// responseCookies lists what the response asked the client to store.
func (a *app) responseCookies(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil {
		uikit.Hint(c, "No response yet.").Padding(t.Space(3))
		return
	}
	cookies := d.response.ResponseHeader.Values("Set-Cookie")
	if len(cookies) == 0 {
		uikit.Hint(c, "This response set no cookies.").Padding(t.Space(3))
		return
	}
	ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(1)).Children(func() {
		for _, cookie := range cookies {
			uikit.Mono(c, cookie).Selectable().SingleLine().Tooltip(cookie)
		}
	})
}

// responseHex renders a response body as a hex dump.
func (a *app) responseHex(c *ui.Context, d *requestDocument, body string) {
	rows := hexRows(body, "")
	if len(rows) == 0 {
		uikit.Hint(c, "Empty body").Padding(c.Theme().Space(3))
		return
	}
	ui.List(c, &d.responseList, len(rows), func(i int) {
		ui.Text(c, hexLine(body, rows[i])).FontSize(uikit.TextMeta).Font(uikit.FontMono).TextColor(c.Theme().Text).Padding(c.Theme().Space(1), c.Theme().Space(3)).SingleLine()
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
