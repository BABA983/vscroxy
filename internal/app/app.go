package app

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/baba983/vscroxy/internal/appicon"
	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/async"
	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/datadir"
	"github.com/baba983/vscroxy/internal/lifecycle"
	"github.com/baba983/vscroxy/internal/linkedmap"
	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/internal/undo"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/internal/watchdog"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"golang.org/x/net/http/httpguts"
)

// page is what the content area shows for the sidebar's one choice.
type page int

const (
	pageLive        page = iota // live traffic, under a view or quick filter
	pageSession                 // historical session tabs
	pageRequest                 // a request document from the collections
	pageAPI                     // an API contract
	pageRuns                    // the run records, which take the window
	pageDiagnostics             // connection diagnostics
	pageOverview                // session or selection overview
	pageSearch                  // one query across requests, variables and traffic
)

// capture reports whether the page reads captured traffic.
func (p page) capture() bool { return p == pageLive }

// app is the state the window shows; view reads it and the controls write it.
type app struct {
	// changed is a test hook: workers pulse it when they ask the UI to redraw,
	// so tests can wait for the state change instead of polling the clock.
	changed chan struct{}

	flows   []flow
	visible []int
	domains []domain
	clients []domain
	// clientApps are each app's executables. tally keeps what all of these
	// are listed from (tally.go), so a build never walks every flow.
	clientApps map[string][]capture.SSLApp
	tally      tally

	// query is the search field: free host/path text and query syntax not
	// yet turned into chips (query.go). queryProblem says why a token was
	// not taken.
	query         string
	queryProblem  string
	searchProblem string
	// queryCaret is where the caret goes in the field once it shows a
	// text set from elsewhere: a completion or a chip turned back into text.
	queryCaret *int
	// queryOpen is whether the field's autocomplete showed in the last
	// build, to tell Down opening it from Down meant for the requests.
	queryOpen bool
	// filterHistory holds the applied traffic filters, newest last.
	filterHistory inputHistory
	filterLast    string // the text Enter or blur last put in the history
	searchFocused bool   // the search field's focus in the previous build
	// guide shows the setup guide in place of the traffic list (guide.go).
	guide  bool
	filter string
	chip   int
	search ui.Handle
	// searchAll is the Search view: one query over saved requests,
	// environments and captured flows. Its worker never holds a UI element.
	searchAll globalSearch
	clearOpen bool

	// Capture filters and the request library have independent selections.
	// Each workspace has two sections, chosen in its sidebar: live traffic or
	// an investigation, and request documents or API contracts.
	page          page
	sidebarChoice string
	requestChoice string
	requestQuery  string
	// requestGen changes with the library, so the command palette rebuilds
	// its request index only when the requests do.
	requestGen   int
	requestIndex requestSearchIndex
	// pendingMove is the drop a tree row queued during the view pass; the
	// next pass applies it, as a menu action that removes rows does.
	pendingMove *libraryMove
	moveTo      moveToState
	// paletteAssets caches scored and sorted resource candidates for one
	// query/library/variables/history key. Repeated palette frames with an
	// unchanged query are lookups, not rescans of the request library.
	paletteAssets *linkedmap.LRUCache[paletteAssetCacheKey, paletteAssetResult]
	// fuzzyLists holds scored name-list hits by owner, query and generation.
	fuzzyLists *linkedmap.LRUCache[fuzzyListKey, fuzzyListCacheValue]
	renameOpen bool
	// saveAfterRename makes the rename dialog Save to collection.
	saveAfterRename bool
	settingsOpen    bool
	settingsTab     int
	settingsQuery   string
	// settingsGen changes when a setting is saved; settingsIndex caches the
	// localized search rows until the language or this generation changes.
	settingsGen          int
	settingsIndex        settingsSearchIndex
	settingsSearchHandle ui.Handle
	// dismissedNotices are the stable ids of notices the person chose not
	// to see again. They are local and saved by id, never by text.
	dismissedNotices []string
	// settingsFocusSearch is a request token: zero leaves focus alone, each
	// increment asks the search field for focus again.
	settingsFocusSearch uint64
	// logLevel is the live setting that currentSettings saves. It stays
	// separate from saved.LogLevel until the write succeeds.
	logLevel string
	// logLevelPickerOpen is the palette's Set Log Level dialog; the level
	// is a draft until Done, then it changes the open log and settings.json.
	logLevelPickerOpen bool
	logLevelPicker     string
	// settingsChoice is the category list's id for settingsTab, which the list
	// chooses by.
	settingsChoice      string
	appearanceChoice    int
	appearanceChoiceSet bool
	// settingsWin is the Settings window while it is open, and
	// settingsFocused whether it is in front; both only on the main thread.
	settingsWin     *mygo.Window
	settingsFocused bool
	// inspectorWin is the detached inspector window while it is open;
	// inspectorDetached keeps the main window from drawing the same panes.
	// inspectorInWindow is true only while that window draws them, so the
	// inspector measures its own width rather than the main window's.
	inspectorWin      *mygo.Window
	inspectorDetached bool
	inspectorFocused  bool
	inspectorInWindow bool
	// toolsWin is the Tools window while it is open, toolsOpen whether Tools
	// shows (as a dialog without a main window), and toolsFocused whether
	// its window is in front.
	toolsWin                *mygo.Window
	toolsOpen, toolsFocused bool
	// toolsPage is the category Tools shows: "rules" or "hosts".
	toolsPage string
	// toolsMenuAction is a menu command chosen while Tools was in front.
	toolsMenuAction string
	// launchPort is the saved setting this run started with: a different
	// saved value waits for a restart.
	launchPort       int
	curlOpen         bool
	curlInput        string
	curlProblem      string
	urlImportOpen    bool
	urlImportText    string
	urlImportProblem string
	urlImportBusy    bool
	// progressDialog is the Dialog location's modal. The id keeps a finishing
	// task from closing a newer dialog's modal.
	progressDialogOpen bool
	progressDialogID   uint64
	// docsOpen shows a collection's or folder's read-only documentation;
	// docsPath is what it covers. docsMarkdown keeps the view's parse.
	docsOpen       bool
	docsPath       string
	docsMarkdown   markdown.Options
	docsSampleLang map[string]int
	contractCache  *linkedmap.LRUCache[string, contractResult]
	// aboutOpen shows the About window; aboutTab picks its licenses page.
	// aboutLicense and aboutNotices are the texts the package ships.
	aboutOpen     bool
	aboutTab      int
	aboutLicense  string
	aboutNotices  string
	aboutMarkdown markdown.Options
	deviceUI      deviceSetup
	clientUI      clientSetup
	diagnostics   captureDiagnostics

	apis            []*apiDocument
	apisPath        string
	apiChoice       string
	apiGenerateOpen bool
	apiCollection   string

	// chosen are the rows chosen for a bulk action, by flow id.
	chosen ui.Selection[int]
	// compare holds the two-command selection and the read-only diff view,
	// independent of the traffic table's own selection and diffs.
	compare          compareState
	overviewScope    overviewScope
	overviewCache    overviewCache
	deviceAskMu      sync.Mutex
	deviceAskPending *deviceAskRequest
	deviceAskOpen    bool
	// picked bridges the table's row-index API; pickedID survives row rebuilds.
	picked   int
	pickedID int
	// undo is the in-memory undo/redo service. Every operation pushes an
	// element there, and a notice keeps the element's handle so its Undo acts
	// on that operation. Nothing here is persisted; a restart starts empty.
	undoService    *undo.Service
	undoSuppressed int
	// undoApplyCount counts state applications by undo elements. A grouped
	// operation applies its whole state once, not once per resource.
	undoApplyCount int
	// pendingFlowDelete waits for the next view pass: the row menu is built
	// while the table is building, so removing rows in its action would leave
	// the table drawing rows that no longer exist.
	pendingFlowDelete []flow
	tab               int

	// sort is the column the table is sorted by, and sortApplied what the
	// rows were sorted with, so a header click sorts on the next frame.
	sort        ui.SortOrder
	sortApplied ui.SortOrder

	sidebarWidth         float32
	tablePercent         float32
	protobufTablePercent float32
	workbench            desktopWorkbench

	totalBytes int64

	// sourcesApps lists the capture's apps in the sidebar's Sources rather
	// than its hosts, which a fresh app lists first; sourcesAll shows the
	// list whole rather than its five busiest.
	sourcesApps bool
	sourcesAll  bool
	// regions are the sidebar's foldable regions and whether each is open,
	// built on first use from shutRegions, which settings carries.
	regions     map[string]*bool
	shutRegions []string
	// paneHeights are the sidebar's middle regions' body heights, built on
	// first use from savedHeights, which settings carries.
	paneHeights  map[string]*float32
	savedHeights map[string]float32
	// windowWidth is the window's width in the last frame: the sidebar gives
	// way when there is no room beside it.
	windowWidth float32
	// paneViewport is the middle's height as the last frame laid it out:
	// the pane stack fills it, and scrolls once its minimums do not fit.
	paneViewport      float32
	paneViewportAsked bool
	// The four panes remember the width they were laid out at, so a pane's
	// row can decide what fits instead of guessing a share of the window.
	inspectorReqPane, inspectorRespPane ui.Handle
	editorReqPane, editorRespPane       ui.Handle
	// Captured requests as documents (captured_document.go): the drafts of
	// edited or sent captures by flow ID, the inspected capture's unchanged
	// document (or why there is none), and the tab's document to come back
	// to from LIVE.
	flowDrafts      map[int]*requestDocument
	live            *requestDocument
	liveFor         int
	liveProblem     string
	liveLoad        bool
	libraryDocument *requestDocument
	// collectionOpen keeps each saved collection's section open or shut,
	// by name, the empty name for the requests outside one.
	collectionOpen map[string]*bool
	// conflict holds a refused library save: the disk version, the draft,
	// and the editor buffer the draft side of the diff keeps editing.
	conflict *saveConflict
	// variableRename is the name prompt and the bulk-edit review; variableRefs
	// is the References view. Neither holds a build-time UI element.
	variableRename variableRenameState
	variableRefs   variableReferencesState
	// history is the local history of one request or folder file.
	history historyView

	// icons caches the app icon per process name; a nil entry means the
	// process has none.
	icons map[string]ui.ImageSource
	// iconLookup asks the desktop for an app's icon; nil is the desktop's own.
	iconLookup func(string) ([]byte, bool)

	// detailFor is the flow the detail panes were built from, so the text and
	// the diff buffers are rebuilt only when the choice changes.
	detailFor    int
	body         string
	reqBody      string
	diff         *editor.DiffState
	diffSplit    float32
	diffBaseline bool
	// testDiff holds the failed assertion's two sides while its read-only
	// comparison is open, independent of a captured response's diff.
	testDiff           *editor.DiffState
	testDiffOpen       bool
	testDiffName       string
	testDiffSplit      float32
	analysis           captureAnalysis
	replayUI           replaySetup
	bulkSaveUI         collectionSave // saves the chosen rows into a collection
	collectionUI       collectionWizard
	recordCollectionUI collectionRecordingEditor
	tokenLinks         *tokenLinkPreview
	matchUI            requestMatchState
	frames             map[int][]frame
	messageUI          messagePanel
	frameLost          atomic.Int64

	// inspect is the inspector's own view, so the request and the response
	// keep independent tabs and body representations while both are on
	// screen, and the exchange-wide views stay one click away.
	inspect                     inspectorState
	decoder                     quickDecoder
	jsonQuery                   jsonQueryInspector
	treeQuery                   jsonQueryInspector
	queries                     []savedQuery
	queriesPath, queriesProblem string

	// The body panes are indexed the way the rest of the inspector is: 0 is
	// the response, 1 the request. Readers survive leaving Capture to edit or
	// compare a request.
	bodyMode [2]int
	// bodyFormatByType remembers an explicit format per media type, so the
	// next body of the same type opens the way this one was read.
	bodyFormatByType map[string]string
	bodyQuery        [2]string
	headerScroll     [2]ui.ScrollState
	bodyText         [2]*editor.State
	// bodyDisplayCache holds the one decoded view of each side's raw body.
	bodyDisplayCache [2]bodyDisplayCache
	// bodyPrettyFor is the body the pretty reader holds, so a draft's changed
	// bytes never show in an older reader.
	bodyPrettyFor [2]string
	bodyReader    [2]ui.Handle
	bodyFind      [2]bodyFindState
	// jsonCrumbs holds the path at each pane's caret. The answer is reused
	// while neither the caret nor the reader's text version changes.
	jsonCrumbs     [2]jsonBreadcrumbs
	activeJSONPath activeJSONPath
	// responseCrumbs caches the tree the sent response's Outline reads; its
	// breadcrumb bar is not drawn, but the parse belongs to this one editor.
	responseCrumbs jsonBreadcrumbs
	outline        outlineUI
	bodyNotice     [2]string
	jsonScroll     [2]ui.ListState
	hexScroll      [2]ui.ListState
	// The wire text and its reader are built once per exchange, not per frame.
	rawText   [2]string
	rawEditor [2]*editor.State

	// The parsed and decoded previews are cached per side, so two visible
	// panes do not rebuild each other's tree on every frame.
	htmlDoc [2]*richtext.Document
	htmlFor [2]string
	// Formatted code, Markdown and merged streams are built once per body.
	formattedCode [2]*editor.State
	codeFor       [2]string
	markdownDoc   [2]*richtext.Document
	markdownFor   [2]string
	mergedText    [2]*editor.State
	mergedFor     [2]string
	imageBmp      [2]*ui.Bitmap
	imageFor      [2]string
	imageErr      [2]string
	jsonTrees     [2]jsonTreeResult
	jsonExpanded  [2]map[nodeID]bool
	jsonReader    [2]jsonReader
	protoFor      [2]string
	protoOK       [2]bool
	// protoOwner is the side whose shared protobuf reader is live this frame.
	protoOwner                        int
	protobuf                          protoInspector
	protoTypes                        []protoTypeRule
	protoTypesPath, protoTypesProblem string
	structured                        [2]structuredInspector

	// newRows counts traffic that arrived while the list was paused, so the
	// footer can offer to resume following without moving the reader.
	newRows          int
	autoSelect       bool
	autoSelecting    bool
	lastPicked       int
	inspectorEditing bool

	// recordingPaused keeps the proxy forwarding without keeping ambient
	// traffic; unrecorded holds what it let pass, so their messages are not
	// kept either. rate counts what was kept, and lastFlow is when capture
	// last delivered anything, for the curve's clock.
	recordingPaused     bool
	unrecorded          map[int]bool
	rate                rateWindow
	lastFlow            atomic.Int64
	collectionRecording *collectionRecording

	srv              *capture.Server
	collecting       bool
	captureReadyOnce sync.Once
	win              *mygo.Window
	// titleWindow is the main window through a seam tests can count; the
	// title and the edited flag are published only when they change.
	titleWindow  titleWindow
	windowTitle  string
	windowEdited bool
	titleSynced  bool
	dialogs      dialogQueue
	control      *desktopControl
	caPath       string
	certificate  certificateSetup
	// certBannerOff puts away the capture screen's certificate banner for
	// this run: it is a statement of the state, not a dialog to answer.
	certBannerOff bool
	systemProxy   proxySetup
	// sleep holds one idle-sleep assertion for active runs; resumeWatch
	// rechecks the system proxy when the machine wakes.
	sleep       sleepGuard
	resumeWatch resumeWatch
	// lifecycle owns graceful cleanup. The hooks let tests drive quit without
	// starting MyGo's native event loop; nil means use the real MyGo calls.
	lifecycle *lifecycle.Lifecycle
	// startupLifetime owns the watchdog started by the first frame and is
	// released by the shutdown joiner in installShutdown.
	startupLifetime *lifecycle.DisposableStore
	uiWatchdog      *watchdog.Watchdog
	firstFrameOnce  sync.Once
	watchdogArmed   bool
	startup         *startupRunner
	// sessionStore owns what belongs to one loaded capture: its body
	// searches, message search and protobuf inspector. replaceSession
	// disposes the whole store, then starts the next one.
	sessionStore *lifecycle.DisposableStore
	quitOnMain   func(func())
	quitApp      func()
	quitError    func(error)
	rulesPath    string
	files        map[string]*store.File // see file
	scripts      scriptStore
	addons       scriptStore
	addonsShown  bool // the Scripts page shows the addons
	rules        []capture.Rule
	ruleUI       ruleEditor
	configImport configImport
	scenarioUI   scenarioUI
	throttle     capture.Throttle
	network      networkWorkbench
	upstream     string // the active configuration's summary, for the chip
	// upstreamCfg is the active configuration without passwords; the sealed
	// passwords are in upstreamSecrets. upstreamUI is the Settings draft.
	upstreamCfg     upstream.Settings
	upstreamSecrets map[string]string
	upstreamProblem string
	upstreamLoading bool
	upstreamPending chan upstreamLoadResult
	upstreamEpoch   atomic.Uint64
	upstreamRetry   upstreamRetry
	upstreamUI      upstreamEditor
	errText         string
	dropped         int64
	scriptErrs      int64

	// ssl keeps the active domain/executable rules and an isolated editor
	// draft. Cancelling the editor never changes active inspection.
	sslPath           string
	sslAll            bool
	sslHosts          string
	sslExclude        string
	sslUntrusted      string
	sslApps           []capture.SSLApp
	sslOpen           bool
	sslDraft          *capture.SSLConfig
	sslDraftHosts     string
	sslDraftExclude   string
	sslDraftUntrusted string
	sslList           ui.ListState

	// appearance is "light", "dark", or empty to follow the system; dark is
	// what it resolved to for this frame.
	appearance string
	dark       bool
	// language is "en" or "zh-Hans", or empty to follow the computer's
	// languages (see language.go).
	language string
	// macChrome is macOS's window layout: the sidebar full height with
	// the traffic lights at its top (see view).
	macChrome bool
	// windowBehind is whether another window is in front of this one;
	// its selections then turn gray.
	windowBehind bool

	// dataDir is the root of every private file this app owns, including
	// the log. It comes from the shared resolver, never from each caller's
	// idea of home.
	dataDir string
	// settings are what the window keeps between launches, besides its
	// bounds, which MyGo saves with the window's StateKey.
	settingsPath     string
	androidProxyPath string
	// backupDir keeps the last copies of the files the app replaces, so an
	// upgrade or a mistaken save never loses what was there.
	backupDir string
	// trustedLinkHosts are local approvals to open links from content
	// without asking again. Keys name one scheme, host and port.
	trustedLinkHosts []string
	// project trust is a local decision per project folder. A worker can be
	// waiting for an answer while frames keep drawing, so the pending asks
	// and the saved lists share one lock.
	trustMu           sync.Mutex
	trustedProjects   []string
	untrustedProjects []string
	trustPending      map[string]chan struct{}
	trustQueue        []string // undecided folders, in the order they asked
	trustAskDir       string   // the folder the current prompt is about
	trustAskOpen      bool
	trustManageOpen   bool
	// trustChecks counts sends whose project scripts were allowed and
	// trustPrompts the times a project had to be asked; tests use them as
	// seams for which scripts ran and how often a person was interrupted.
	trustChecks  atomic.Int64
	trustPrompts atomic.Int64
	// linkPrompt is the pending confirmation for one untrusted destination.
	linkPrompt linkPrompt
	// updateCheck allows the silent launch-time background check. An
	// explicit check still runs when this is off. It never downloads or
	// installs anything.
	updateCheck bool
	// lastRunVersion is the last version this settings file saw at startup;
	// empty on a first install. It decides whether to post a what's-new
	// notice, and is saved with the rest of settings after the post.
	lastRunVersion string
	// settingsReadable says settings.json parsed at startup, so recording
	// the version cannot replace a file we could not read.
	settingsReadable bool
	// manualCheckInFlight keeps a second Check for updates click from starting
	// another release lookup while the first one is still running.
	manualCheckInFlight atomic.Bool
	// attention plays the accessibility signals for pauses, prompts and
	// finished runs; its mode is the saved "Attention signals" setting.
	attention *attentionService
	// mcpConsents are local approvals for stdio commands, keyed by a hash of
	// the document and its expanded command shape. They never enter a
	// collection file.
	mcpConsentsPath    string
	mcpConsentsKeyPath string
	mcpConsentsKey     []byte
	mcpConsents        map[string]mcpConsentApproval
	mcpConsent         mcpConsentState
	// mcpClosing holds sessions from closed tabs or replaced protocol
	// documents until their bounded background close finishes. Quit waits for
	// them as well as for the documents that are still open.
	mcpClosingMu     sync.Mutex
	mcpClosing       map[*mcpSession]struct{}
	saved            settings
	columnsDirty     bool // the app changed the table's layout itself
	builds           int  // view passes, which tests count to see a hover
	lastSave         time.Time
	settingsSaver    *async.RunOnceScheduler
	settingsOnce     sync.Once
	settingsClock    async.Clock // tests inject a fake clock here
	clock            async.Clock // injectable clock for the app's timers; nil means real time
	columns          ui.TableLayout
	proxyPort        int
	proxyPortInput   string
	proxyPortProblem string
	// systemProxyBypass is written when the system proxy is enabled. Set is
	// false when the OS list should be kept instead.
	systemProxyBypass     []string
	systemProxyBypassSet  bool
	systemProxyBypassText string
	// certificates are the client certificates and extra roots shared by
	// sends and captured upstream TLS; certificateUI is the Settings draft.
	certificates  certs.Settings
	certificateUI certificateEditor
	certProblem   string
	// SOCKS5 is configured for the next launch, like the HTTP port. The
	// draft fields are what the settings page edits; the persisted fields
	// are what currentSettings writes and the launch snapshot compares.
	socksOn        bool
	socksPort      int
	socksAuth      bool
	socksUsername  string
	socksPassword  string
	socksProblem   string
	socksOnDraft   bool
	socksPortInput string
	socksAuthDraft bool
	socksUserDraft string
	socksPassDraft string
	launchSOCKS    socksEntry
	// reverses are the saved reverse proxy entries; reverseDraft is the
	// add or edit dialog.
	reverses       []reverseProxySetting
	reverseDraft   reverseProxyDraft
	reverseProblem string

	// ignore keeps noise out of the table without stopping it at the proxy.
	ignorePath string
	ignoreText string
	ignoreOpen bool

	// profiles are the Host profiles: hosts the proxy dials elsewhere, so a
	// device's traffic reaches another machine as if the name pointed there.
	profiles hostsState

	// dnsRule is the dialog a flow opens to map its host: the app that sent
	// it is known there, so nobody has to spell an app's name.
	dnsRuleOpen    bool
	dnsRuleHost    string
	dnsRuleAddr    string
	dnsRuleScope   string
	dnsRuleApps    []string
	dnsRuleProfile string

	// keyLogPath is the file the TLS secrets go to, which Wireshark reads.
	keyLogPath  string
	keyLogOpen  bool
	keyLogInput string

	// projectDir is the project folder whose .vscroxy directory holds the
	// shared files, and projectInput is its Settings field. The watch polls
	// that folder; projectPending says a change waits on unsaved edits.
	projectDir       string
	projectInput     string
	projectWatchStop chan struct{}
	projectPending   atomic.Bool
	// git is Tools → Git: the project folder's working tree, the file whose
	// diff shows and the commit draft.
	git gitPanel

	// Connection overlays belong to the inspected request, not the list.
	connOpen bool

	// customColumns are the columns the user added to the table,
	// hiddenColumns the default ones they took out and shownColumns the
	// optional ones they put in; the filter fields narrow what it shows
	// beyond the search box.
	customColumns []customColumn
	columnRead    columnReader
	hiddenColumns []string
	shownColumns  []string
	filterStatus  string
	filterHeader  string
	filterBody    string
	filterTriage  captureTriage
	// textExprs holds the compiled text conditions of the filters, by field and text.
	textExprs    *linkedmap.LRUCache[string, textExpr]
	filterAny    bool
	activity     activityRange
	activityRows []int // matching rows before the time brush, for its full context
	// hadFlows is sticky for the session: the activity strip is a timeline of
	// what was captured, so it keeps its place after Clear rather than
	// appearing and disappearing as rows come and go.
	hadFlows bool
	// activityCounts caches the strip's buckets until the rows, its end or
	// the brush change.
	activityCounts activityCache
	views          captureViews
	columnOpen     bool
	columnDraft    customColumn

	// mark edits what a flow is marked with: a color and a note.
	markOpen   bool
	markID     int
	markColor  string
	markNote   string
	markStrike bool

	// ignoreApps does the same by app, for a browser's background traffic.
	ignoreAppsPath string
	ignoreAppsText string
	ignoreAppsOpen bool

	// allow narrows the table to listed hosts and apps while allowOn,
	// without stopping the rest at the proxy.
	allowOn                  bool
	allowPath, allowAppsPath string
	allowText, allowAppsText string
	allowOpen, allowAppsOpen bool

	// historyLimit and historySizeMB (MB) bound the retained send history;
	// zero uses the defaults. flowLimit, memoryLimit (MB) and diskLimit (GB)
	// bound what the table
	// keeps, zero for the defaults.
	flowLimit, memoryLimit, diskLimit int
	historyLimit, historySizeMB       int

	// requests are the saved requests, the Compose direction's kept half,
	// and history what Compose sent.
	requests        []savedRequest
	library         libraryState
	requestsProblem string
	// responseRefs holds the latest response of each request in this session,
	// for {{response "..."}} expansion. It is never written to disk.
	responseRefs *request.Responses
	// folders are the collections' and folders' own settings, by path.
	folders     []request.Folder
	folderUI    folderEditor
	deleting    deletion
	code        codeDialog
	vault       vaultState
	experiments experimentWorkbench
	socket      websocketWorkbench
	sse         sseWorkbench
	sessions    sessionWorkspace

	// The active document is embedded so the editor's fields always belong
	// to that tab. Workers return results to their originating document.
	*requestDocument
	documents    []*requestDocument
	nextDocument int
	// markers is the VS Code-style MarkerService behind the Problems panel.
	markers  *markers.Service
	problems problemsState
	// recentlyClosed is the in-memory Reopen Closed Editor stack, keyed by
	// document id and ordered from oldest to newest. documentRecency is the
	// MRU order for Ctrl/Cmd+Tab; nil is the LIVE traffic view.
	recentlyClosed  *linkedmap.LinkedMap[int, closedDocument]
	documentRecency *linkedmap.LinkedMap[*requestDocument, struct{}]
	// mruPending is the quick switch's current pick, held until Ctrl is
	// released or something else happens; repeated Ctrl+Tabs then walk the
	// same order instead of reordering it on every Tab.
	mruPending      bool
	closingDocument *requestDocument
	closingQueue    []*requestDocument
	closingReturnTo *requestDocument
	requestResults  []requestResult
	importReport    importReport
	// urlPaste is a cURL command pasted into the URL field, applied on the
	// next build; urlPasteUndo is the accepted action, restored on the build
	// after it. urlPasteSelection is the input's selection at paste time.
	urlPaste          *urlPasteEdit
	urlPasteUndo      *urlPasteEdit
	urlPasteSelection [2]int
	urlHistory        inputHistory
	// tokens are the OAuth 2.0 access tokens, kept sealed by oauthTokens.
	tokens            request.Tokens
	oauthTokens       oauthTokenStore
	oauth             oauthState
	runner            collectionRunner
	loadRunner        collectionLoadRunner
	mocks             map[string]*collectionMock // running collection mock servers, by path
	cookies           *cookieStore
	cookiesPath       string
	cookiePersistence bool
	// localControl is the saved choice to run the local automation endpoint;
	// controlPath is its discovery file in the data root.
	localControl bool
	controlPath  string
	cookieUI     cookieEditor
	exportUI     exportReview

	// envs are the variables a request uses as {{name}}, one set in use.
	envsPath       string
	envsProblem    string
	envs           []environment
	activeEnv      string
	globals        map[string]string
	globalsCurrent map[string]string
	// folderCurrent holds collection and folder Current values by path and
	// name; their Initial values live in the library. folderCredentials marks
	// the ones that came from a credential slot.
	folderCurrent     map[string]map[string]string
	folderCredentials map[string]map[string]bool
	varsGlobals       bool // the Variables dialog edits the globals
	varsOpen          bool
	varsName          string
	varsChoice        string
	varsRows          []variableRow
	varsProblem       string
	varsComplete      variableCompletion
	// variableHandles gives every authoring field a persistent identity for
	// the focus-scoped Cmd/Ctrl+. quick fix, keyed by its bound string.
	variableHandles map[*string]*ui.Handle

	breakOn   bool
	bp        *capture.Breakpoint
	bpOpen    bool
	bpMethod  string
	bpURL     string
	bpStatus  string
	bpBody    string
	bpHeaders string
	bpAbort   string
	// The templates saved at breakpoints; bpTemplate names the one applied
	// at this pause, bpSaving shows the name field, bpNote what went wrong.
	bpTemplatesPath string
	bpTemplates     []bpTemplate
	bpTemplate      string
	bpSaving        bool
	bpName          string
	bpNote          string
	// bpQuery and bpForm are the query and a form body as rows, the form
	// shown as rows when bpFormRows. bpRawMode shows the message as text in
	// bpRaw; bpRawNote says why that text does not read yet.
	bpQuery    []request.Field
	bpForm     []request.Field
	bpFormRows bool
	bpRawMode  bool
	bpRaw      string
	bpRawNote  string
	// bpWait is the label the wait select shows; breakWaitSeconds is how long
	// an unanswered pause lasts, zero for the default.
	bpWait           string
	breakWaitSeconds int

	// mu guards what the capture goroutine hands to the view: the flows not
	// yet drained, and a startup error.
	mu            sync.Mutex
	pending       []capture.Flow
	pendingFrames []capture.FrameEvent
	bps           []capture.Breakpoint
	err           error
	notices       []notice
	// noticeUI is the notification center's in-memory state, guarded by mu.
	noticeUI      noticeCenter
	message       string // the last notice as one line
	replayResults []replayResult
}

type replayResult struct {
	source flow
	reply  capture.Flow
}

type domain struct {
	name string
	n    int
}

func newApp() *app {
	a := &app{
		macChrome:      runtime.GOOS == "darwin",
		responseRefs:   &request.Responses{},
		filter:         "all",
		sidebarChoice:  "all",
		icons:          make(map[string]ui.ImageSource),
		sidebarWidth:   220,
		tablePercent:   44,
		collectionOpen: make(map[string]*bool),
		// A flow is numbered when it starts and arrives when it ends, so
		// arrival order puts 6 above 5. The number is the start order: the
		// table reads by it, and the # column says so with its arrow.
		sort:        ui.SortOrder{Column: "id"},
		detailFor:   -1,
		frames:      make(map[int][]frame),
		throttle:    capture.Presets[0],
		autoSelect:  true,
		updateCheck: true,
		workbench:   desktopWorkbench{layout: "right"},
		startup:     newStartupRunner(),
	}
	a.attention = newAttentionService(a, liveAttentionChannels{win: func() *mygo.Window { return a.win }})
	// The engine reads SSLKEYLOGFILE on its first handshake, so a key log the
	// settings ask for has to be in the environment before it starts.
	if root, err := datadir.Root(); err == nil {
		a.dataDir = root
		a.settingsPath = filepath.Join(root, "settings.json")
		a.androidProxyPath = filepath.Join(root, "android-proxy.json")
		a.backupDir = filepath.Join(root, "backups")
		// A first launch. The log opened before this already made the
		// directory, so the settings file is what tells the two apart.
		if firstLaunch(a.settingsPath) {
			a.guide = true
		}
		a.caPath = capture.CACertPath(root)
		a.rulesPath = filepath.Join(root, "rules.json")
		a.network.path = filepath.Join(root, "network-presets.json")
		a.bpTemplatesPath = filepath.Join(root, "breakpoint-templates.json")
		a.sslPath = filepath.Join(root, "ssl.json")
		a.library.root = filepath.Join(root, "collections")
		a.library.backupDir = a.backupDir
		a.ignorePath = filepath.Join(root, "ignore.txt")
		a.ignoreAppsPath = filepath.Join(root, "ignore-apps.txt")
		a.allowPath = filepath.Join(root, "allow.txt")
		a.allowAppsPath = filepath.Join(root, "allow-apps.txt")
		a.profiles.dir = filepath.Join(root, "tools", "hosts")
		a.scripts.dir = filepath.Join(root, "tools", "scripts")
		a.addons.dir, a.addons.addon = filepath.Join(root, "tools", "addons"), true
		a.envsPath = filepath.Join(root, "env.json")
		a.cookiesPath = filepath.Join(root, "cookies.json")
		a.mcpConsentsPath = filepath.Join(root, "mcp-consents.json")
		a.mcpConsentsKeyPath = filepath.Join(root, "mcp-consents.key")
		a.mcpConsentsKey, _ = loadOrCreateMCPConsentKey(a.mcpConsentsKeyPath)
		if len(a.mcpConsentsKey) != mcpConsentKeySize {
			a.mcpConsentsKey = newMCPConsentKey()
		}
		a.mcpConsents, _ = loadMCPConsents(a.mcpConsentsPath)
		a.vault.path = filepath.Join(root, "vault.json")
		a.controlPath = filepath.Join(root, "control.json")
		a.vault.keyPath = filepath.Join(root, "vault.key")
		a.oauthTokens.path = filepath.Join(root, "oauth-tokens.json")
		a.tokens.SetSave(a.saveOAuthTokens)
		if saved, err := loadSettings(a.settingsPath); err != nil {
			a.errText = err.Error()
		} else {
			a.saved, a.proxyPort, a.vault.scripts = saved, saved.ProxyPort, saved.VaultScripts
			a.settingsReadable = true
			a.logLevel = saved.LogLevel
			a.urlHistory.load(saved.URLHistory)
			a.filterHistory.load(saved.FilterHistory)
			a.trustedLinkHosts = slices.Clone(saved.TrustedLinkHosts)
			a.trustedProjects, a.untrustedProjects = normalizeTrustPaths(saved.TrustedProjects), normalizeTrustPaths(saved.UntrustedProjects)
			a.attention.setMode(saved.AttentionSignals)
			a.updateCheck = !saved.DisableUpdateCheck
			a.lastRunVersion = saved.LastRunVersion
			a.dismissedNotices = slices.Clone(saved.DismissedNotices)
			a.language = saved.Language
			a.certificates = saved.Certificates.Normalize()
			a.saved.Certificates = a.certificates
			a.upstreamCfg = saved.Upstream.Normalize()
			a.saved.Upstream = a.upstreamCfg
			a.systemProxyBypass = slices.Clone(saved.SystemProxyBypass)
			a.systemProxyBypassSet = saved.SystemProxyBypassSet
			a.systemProxyBypassText = strings.Join(a.systemProxyBypass, "\n")
			a.cookiePersistence = saved.CookiePersistence
			a.localControl = saved.LocalControl
			a.launchPort = saved.ProxyPort
			a.socksOn, a.socksPort, a.socksAuth = saved.SOCKSOn, saved.SOCKSPort, saved.SOCKSAuth
			a.socksUsername, a.socksPassword = saved.SOCKSUsername, saved.SOCKSPassword
			a.resetSOCKSDrafts()
			a.launchSOCKS = a.socksEntry()
			a.reverses = slices.Clone(saved.ReverseProxies)
			a.allowOn = saved.AllowList
			a.flowLimit, a.memoryLimit, a.diskLimit = saved.FlowLimit, saved.MemoryLimit, saved.DiskLimit
			a.historyLimit, a.historySizeMB = saved.HistoryLimit, saved.HistorySizeMB
			a.guide = a.guide || saved.Guide
			if saved.ProjectDir != "" {
				a.projectDir, a.projectInput = saved.ProjectDir, saved.ProjectDir
				a.library.root = filepath.Join(saved.ProjectDir, ".vscroxy", "collections")
			}
			if saved.ProxyPort != 0 {
				a.proxyPortInput = strconv.Itoa(saved.ProxyPort)
			}
			if saved.KeyLogPath != "" {
				_ = os.Setenv("SSLKEYLOGFILE", saved.KeyLogPath)
				a.keyLogPath = saved.KeyLogPath
			}
		}
		markStartup(markSettingsLoaded)
	}
	if a.settingsPath != "" {
		var problems []string
		// Sealed passphrases must be available before validation: a saved
		// PKCS#12 certificate cannot be checked with an empty password.
		if secrets, err := certs.SecretStoreFor(a.settingsPath).Load(); err != nil {
			problems = append(problems, err.Error())
			a.certificates.SecretsProblem = err.Error()
		} else {
			a.certificates.Secrets = secrets
		}
		if err := a.certificates.Validate(); err != nil {
			problems = append(problems, err.Error())
		}
		if len(problems) > 0 {
			a.certProblem = strings.Join(problems, "; ")
		}
		if secrets, err := upstream.SecretStoreFor(a.settingsPath).Load(); err != nil {
			a.upstreamProblem = err.Error()
		} else {
			a.upstreamSecrets = secrets
		}
		markStartup(markSecretsLoaded)
	}
	addr, err := a.saved.proxyAddr()
	var srv *capture.Server
	if err == nil {
		// The CA directory is the parent of the public certificate the rest
		// of the app trusts and serves to clients.
		caDir := ""
		if a.caPath != "" {
			caDir = filepath.Dir(a.caPath)
		}
		srv, err = capture.New(addr, caDir)
	}
	if err != nil {
		// Keep local assets usable, but never present examples as captured data.
		a.errText = err.Error()
	} else {
		a.srv = srv
		srv.SetCertificates(a.certificates)
		if err := a.applySigningCA(); err != nil {
			a.certProblem = err.Error()
		}
		srv.SetBreakWait(a.breakWait())
		a.upstreamPending = make(chan upstreamLoadResult, 1)
		if a.upstreamCfg.Enabled() {
			// A PAC URL may be slow or unreachable. Requests fail closed while
			// it loads, and the result reaches the UI through the next frame.
			a.upstreamProblem = locales.UpstreamConfigurationIsLoading()
			a.upstreamLoading = true
			placeholder := srv.SetUpstreamUnavailable(a.upstreamCfg, errors.New("upstream configuration is loading"))
			cfg, secrets := a.upstreamCfg.Clone(), maps.Clone(a.upstreamSecrets)
			epoch := a.upstreamEpoch.Load()
			go guarded(func() {
				applied, err := srv.SetUpstreamConfigFailClosed(placeholder, cfg, secrets)
				a.upstreamPending <- upstreamLoadResult{epoch: epoch, applied: applied, err: err, settings: cfg, secrets: secrets}
			})
		} else if err := srv.SetUpstreamConfig(a.upstreamCfg, a.upstreamSecrets); err != nil {
			a.upstreamProblem = err.Error()
		} else {
			a.upstream = a.upstreamCfg.Summary()
		}
		if cfg, err := a.socksConfig(); err != nil {
			a.socksProblem = err.Error()
		} else if err := srv.SetSOCKS(cfg); err != nil {
			a.socksProblem = err.Error()
		}
		if err := srv.SetReverseProxies(a.reverseConfigs()); err != nil {
			a.reverseProblem = err.Error()
		}
		a.reloadConfig()
	}
	markStartup(markCaptureServerReady)
	a.loadRequestAssets()
	markStartup(markRequestAssetsLoaded)
	a.loadCookies()
	markStartup(markCookiesLoaded)
	if a.settingsPath != "" {
		saved := a.saved
		a.appearance = saved.Appearance
		if saved.SidebarWidth > 0 {
			a.sidebarWidth = saved.SidebarWidth
		}
		if saved.TablePercent > 0 {
			a.tablePercent = saved.TablePercent
		}
		a.workbench.layout = inspectorLayout(saved.InspectorLayout)
		a.workbench.sidebarHidden = saved.SidebarHidden
		a.regions, a.shutRegions = nil, slices.Clone(saved.SidebarClosed)
		a.paneHeights, a.savedHeights = nil, maps.Clone(saved.SidebarPaneHeights)
		if saved.SideTablePercent > 0 {
			a.workbench.sidePercent = saved.SideTablePercent
		}
		if saved.DiffSplit > 0 {
			a.diffSplit = saved.DiffSplit
		}
		a.setColumns(saved.Columns.layout())
		a.customColumns, a.hiddenColumns = slices.Clone(saved.CustomColumns), saved.HiddenColumns
		a.shownColumns = slices.Clone(saved.ShownColumns)
	}
	a.placeProjectFiles()
	a.loadSavedAPIs()
	a.recount()
	a.applyFilter()
	if a.dataDir != "" {
		a.views.init(a, filepath.Join(a.dataDir, "views.json"))
		a.protobuf.init(filepath.Join(a.dataDir, "protobuf.pb"))
		a.protoTypesPath = filepath.Join(a.dataDir, "protobuf-types.json")
		a.loadProtoTypeRules()
		a.queriesPath = queryPath(a.dataDir)
		a.loadQueries()
		a.loadExperiments(filepath.Join(a.dataDir, "experiments.json"))
		markStartup(markExperimentsLoaded)
		if err := a.sessions.load(filepath.Join(a.dataDir, "sessions")); err != nil {
			a.sessions.problem = err.Error()
			a.fail(locales.Sessions(), err)
		}
		markStartup(markSessionsLoaded)
	}
	a.loadAndroidProxyRecords()
	// Whether this system trusts the CA decides whether HTTPS reads as
	// traffic or as tunnels: the capture screen's banner says so, so the
	// check starts with the app rather than with the first tunnel clicked.
	a.checkCertificate(false)
	go guarded(func() { cleanupClientDirs() })
	a.lifecycle = &lifecycle.Lifecycle{}
	a.startupLifetime = lifecycle.NewDisposableStore()
	a.initProblems()
	a.startupLifetime.Add(a.problems.subscription)
	a.startupLifetime.Add(lifecycle.ToDisposable(a.markers.Dispose))
	a.sessionStore = newCaptureSessionStore(a)
	markStartup(markAppConstructed)
	return a
}

// A failed read protects that asset until the user repairs or moves the file
// aside and explicitly retries. A missing file is a valid empty asset.
func (a *app) loadRequestAssets() {
	a.loadRequestLibrary()
	a.refreshDocuments()
	a.loadEnvsState()
}

// loadEnvsState reads the environments and variables in use from their files.
func (a *app) loadEnvsState() {
	if f, err := a.loadEnvFile(); err != nil {
		a.envsProblem = locales.VariablesProblem(a.envsPath, fmt.Sprint(err))
	} else {
		a.envs, a.activeEnv, a.globals, a.globalsCurrent, a.folderCurrent, a.folderCredentials, a.envsProblem = f.Envs, f.Active, f.Globals, f.GlobalsCurrent, f.FolderCurrent, f.FolderCredentials, ""
		if a.activeEnv == "" && len(a.envs) > 0 {
			a.activeEnv = a.envs[0].Name
		}
	}
}

// startCapture listens and hands captured flows to the view.
// reloadConfig reads the rules and the script and hands them to the proxy.
// It runs on the main thread, from startup or the Reload button.
func (a *app) reloadConfig() {
	unreadable := func(path string, err error) {
		a.fail(locales.FileNotRead(path), err)
	}
	if a.network.path != "" {
		a.loadNetworkPresets()
	}
	if a.srv != nil {
		a.loadHosts()
	}
	if a.ignoreAppsPath != "" && a.srv != nil {
		apps, err := a.readList(a.ignoreAppsPath)
		if err != nil {
			unreadable(a.ignoreAppsPath, err)
		} else {
			a.srv.SetIgnoreApps(apps)
			a.ignoreAppsText = strings.Join(apps, "\n")
		}
	}
	if a.ignorePath != "" && a.srv != nil {
		patterns, err := a.readList(a.ignorePath)
		if err != nil {
			unreadable(a.ignorePath, err)
		} else {
			a.srv.SetIgnore(patterns)
			a.ignoreText = strings.Join(patterns, "\n")
		}
	}
	if a.srv != nil {
		for _, l := range []struct {
			path string
			text *string
		}{{a.allowPath, &a.allowText}, {a.allowAppsPath, &a.allowAppsText}} {
			if l.path == "" {
				continue
			}
			if lines, err := a.readList(l.path); err != nil {
				unreadable(l.path, err)
			} else {
				*l.text = strings.Join(lines, "\n")
			}
		}
		a.applyAllow()
	}
	if a.sslPath != "" && a.srv != nil {
		cfg, err := a.readSSL()
		if err != nil {
			unreadable(a.sslPath, err)
		} else {
			a.srv.SetSSL(cfg)
			a.sslAll = cfg.All
			a.sslHosts = strings.Join(cfg.Hosts, "\n")
			a.sslExclude = strings.Join(cfg.Exclude, "\n")
			a.sslUntrusted = strings.Join(cfg.AllowUntrusted, "\n")
			a.sslApps = slices.Clone(cfg.Apps)
		}
	}
	if a.rulesPath != "" {
		rules, err := a.readRules()
		if err != nil {
			unreadable(a.rulesPath, err)
		} else {
			a.rules = rules
			if a.srv != nil {
				a.srv.SetRules(rules)
			}
		}
	}
	if a.bpTemplatesPath != "" {
		if list, err := a.readBreakTemplates(); err != nil {
			unreadable(a.bpTemplatesPath, err)
		} else {
			a.bpTemplates = list
		}
	}
	a.loadScripts()
}

// invalidate asks for a frame, so what a goroutine changed shows.
func (a *app) invalidate() {
	if a.changed != nil {
		select {
		case a.changed <- struct{}{}:
		default:
		}
	}
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.inspectorWin != nil {
		a.inspectorWin.Invalidate()
	}
}

// syncNativeAppearance makes the desktop's own chrome follow the app's
// Light, Dark or System choice: native title bars, menus and sheets.
func (a *app) syncNativeAppearance() {
	if a.win == nil {
		return // the native theme waits for the first window
	}
	switch a.appearance {
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// clamp shortens a label where its full text is available separately.
func clamp(msg string, max int) string {
	r := []rune(msg)
	if len(r) <= max {
		return msg
	}
	return string(r[:max-1]) + "…"
}

func (a *app) revealCA() {
	if a.caPath == "" {
		a.warn(locales.CertificateNoPathKnown())
		return
	}
	path := a.caPath
	go guarded(func() {
		if err := system.RevealCA(path); err != nil {
			a.fail(locales.RevealCertificate(), err)
		}
	})
}

// replayFlow sends the flow's request again through the proxy, so the result
// is captured like any other exchange. The scheduler records the inspectable
// result for a one-request, one-repeat send; larger batches keep their own
// summary instead of a toast per send.
func (a *app) replayFlow(ctx context.Context, f flow, record bool) (capture.Flow, error) {
	if a.srv == nil || !a.srv.Running() || f.url == "" {
		return capture.Flow{}, fmt.Errorf("capture proxy is not available")
	}
	reply, err := a.srv.Replay(ctx, capture.Flow{
		ID:              f.id,
		Method:          f.method,
		Scheme:          f.scheme,
		Host:            f.host,
		Path:            f.path,
		URL:             f.url,
		RequestHeader:   f.reqHeader,
		RequestBody:     f.reqBody,
		RequestBodyFile: f.reqFile,
	})
	if err != nil {
		return capture.Flow{}, err
	}
	if record {
		a.mu.Lock()
		a.replayResults = append(a.replayResults, replayResult{source: f, reply: reply})
		a.mu.Unlock()
	}
	a.invalidate()
	return reply, nil
}

// stopCapture closes the listener and releases the system proxy if this
// session set it. Start brings the same server back; the collectors keep
// reading its channels across the switch.
func (a *app) stopCapture() {
	if a.srv == nil || !a.srv.Running() {
		return
	}
	if a.systemProxy.owned() {
		if err := a.systemProxy.change(false, "", a.writeSystemProxy); err != nil {
			a.fail(locales.StopCapture(), err)
			return
		}
	}
	if err := a.srv.Stop(); err != nil {
		a.fail(locales.StopCapture(), err)
		return
	}
	applog.For("capture").Debug("capture stopped")
	a.recordingPaused = false
	a.newRows = 0
	a.invalidate()
}

func (a *app) startCapture() {
	if a.srv == nil {
		return
	}
	if err := a.srv.Start(); err != nil {
		a.mu.Lock()
		a.err = err
		a.mu.Unlock()
		a.invalidate()
		return
	}
	a.captureReadyOnce.Do(func() { markStartup(markCaptureReady) })
	applog.For("capture").Debug("capture started")
	if err := a.srv.SOCKSError(); err != nil {
		a.warn(locales.SocksNotListening(err.Error()))
	}
	for _, st := range a.srv.ReverseProxyStatus() {
		if st.Error != "" {
			a.warn(locales.ReverseProxyError(st.Error))
		}
	}
	a.invalidate()
	if a.collecting {
		// Stop closed the socket; the collectors are still reading the
		// server's channels and will see the restarted listener's flows.
		return
	}
	a.collecting = true
	go guarded(func() { a.collectBreakpoints() })
	go guarded(func() { a.collectFrames() })
	done := make(chan struct{})
	defer close(done)
	rateDone := done
	go guarded(func() { a.tickRate(rateDone) })
	for f := range a.srv.Flows() {
		a.lastFlow.Store(time.Now().Unix())
		a.mu.Lock()
		a.pending = append(a.pending, f)
		a.mu.Unlock()
		a.win.Invalidate()
	}
}

// collectFrames queues the WebSocket messages and SSE events.
func (a *app) collectFrames() {
	for ev := range a.srv.Frames() {
		a.mu.Lock()
		if len(a.pendingFrames) < 1024 {
			a.pendingFrames = append(a.pendingFrames, ev)
		} else {
			a.frameLost.Add(1)
		}
		a.mu.Unlock()
		a.win.Invalidate()
	}
}

// collectBreakpoints queues the exchanges the proxy paused for the UI.
func (a *app) collectBreakpoints() {
	for bp := range a.srv.Breakpoints() {
		a.queueBreakpoint(bp)
	}
}

// queueBreakpoint is the arrival path for a paused exchange, also used by
// tests: queue it for the modal and ask for the user's attention if the
// window is not in front.
func (a *app) queueBreakpoint(bp capture.Breakpoint) {
	a.mu.Lock()
	a.bps = append(a.bps, bp)
	a.mu.Unlock()
	item := breakpointAttention(bp.Method, bp.URL)
	item.id = breakpointAttentionID(bp.ID)
	if a.attention != nil {
		a.attention.play(item)
	}
	a.invalidate()
}

// showBreakpoint puts a paused exchange in the editor. The queue's next
// breakpoint and a notification's exact target both come through here.
func (a *app) showBreakpoint(bp *capture.Breakpoint) {
	a.bp = bp
	a.bpOpen = true
	a.bpMethod, a.bpURL = bp.Method, bp.URL
	a.bpStatus = ""
	if bp.Status > 0 {
		a.bpStatus = strconv.Itoa(bp.Status)
	}
	a.bpBody = bp.Body
	a.bpHeaders = request.HeaderBlock(bp.Header)
	a.bpAbort = "502 Bad Gateway"
	a.bpTemplate, a.bpSaving, a.bpNote = "", false, ""
	a.bpRawMode, a.bpRawNote = false, ""
	a.splitTables()
	a.dialogs.arrive(dialogBreakpoint)
}

// resolveBreak answers the paused exchange and lets the proxy go on. drop
// closes the client's connection without an answer.
func (a *app) resolveBreak(abort, original, drop bool) {
	bp := a.bp
	if bp == nil {
		return
	}
	a.syncRaw()
	if !abort && !original && !drop && a.breakpointError() != "" {
		return
	}
	if a.attention != nil {
		a.attention.clear(breakpointAttentionID(bp.ID))
	}
	if a.srv != nil {
		status := 0
		if bp.Phase == capture.PhaseResponse {
			status, _ = strconv.Atoi(strings.TrimSpace(a.bpStatus))
		}
		abortCode, _, _ := strings.Cut(a.bpAbort, " ")
		abortStatus, _ := strconv.Atoi(abortCode)
		body := a.composedBody()
		resumed := a.srv.Resume(bp.ID, capture.Decision{
			Abort:       abort,
			AbortStatus: abortStatus,
			Original:    original,
			Drop:        drop,
			Method:      a.bpMethod,
			URL:         a.composedURL(),
			Status:      status,
			Body:        body,
			Header:      request.ParseHeaderBlock(a.bpHeaders),
			KeepBody:    bp.BodyReadOnly || body == bp.Body,
		})
		if !resumed {
			a.bp, a.bpOpen = nil, false
			a.inform(locales.BreakpointExpiredTheExchangeIs())
			return
		}
	}
	a.bp = nil
	a.bpOpen = false
	if drop {
		a.inform(locales.BreakpointDropped(bp.Method, bp.URL))
		return
	}
	if abort {
		if bp.Phase == capture.PhaseMessage {
			a.inform(locales.DroppedWebsocketMessageConnectionRemains())
			return
		}
		a.inform(locales.BreakpointAborted(bp.Method, bp.URL))
		return
	}
	if original {
		a.done(locales.BreakpointContinuedOriginal(bp.Method, bp.URL))
		return
	}
	a.done(locales.BreakpointContinued(bp.Method, bp.URL))
}

// Validate before parsing so a malformed line cannot silently delete a header.
func (a *app) breakpointError() string {
	if a.bp == nil {
		return ""
	}
	if a.bpRawMode && a.bpRawNote != "" {
		return a.bpRawNote
	}
	if a.bp.Phase == capture.PhaseMessage {
		if a.bp.BodyReadOnly {
			return ""
		}
		data := []byte(a.bpBody)
		if a.bp.MessageType == "binary" {
			var err error
			data, err = base64.StdEncoding.Strict().DecodeString(a.bpBody)
			if err != nil || base64.StdEncoding.EncodeToString(data) != a.bpBody {
				return locales.EnterCanonicalBase64()
			}
		} else if !utf8.Valid(data) {
			return locales.TextMessageUTF8()
		}
		if len(data) > 1<<20 {
			return locales.MessageEditLimit()
		}
		return ""
	}
	if a.bp.Phase == capture.PhaseResponse {
		n, err := strconv.Atoi(strings.TrimSpace(a.bpStatus))
		if err != nil || n < 200 || n > 599 {
			return locales.EnterFinalStatus()
		}
	} else {
		if !httpguts.ValidHeaderFieldName(a.bpMethod) {
			return locales.EnterValidHTTPMethod()
		}
		u, err := url.Parse(a.bpURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return locales.EnterAbsoluteHTTPURL()
		}
	}
	for i, line := range strings.Split(a.bpHeaders, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !httpguts.ValidHeaderFieldName(strings.TrimSpace(key)) || !httpguts.ValidHeaderFieldValue(value) {
			return locales.HeaderLineInvalid(i + 1)
		}
	}
	return ""
}

// drain moves the flows the capture goroutine collected into the table.
func (a *app) drain() {
	a.drainSessionHistory()
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	results := a.requestResults
	a.requestResults = nil
	err := a.err
	a.mu.Unlock()
	if len(pending) > 0 && a.toolsPage != "hosts" {
		a.invalidateTools() // for the rules' hit counts
	}
	for _, result := range results {
		d := result.document
		d.composeWaiting = false
		d.sendCancel, d.sendCancelling = nil, false
		d.sendOutcome = result.outcome
		d.composeResult = result.summary
		d.composeTests = result.tests
		a.applySendProblems(result)
		d.composeReplies = result.replies
		d.composeExchanges = result.exchanges
		d.console = result.console
		if result.grpc != nil {
			g := d.grpcState()
			g.session, g.unary = result.grpc, false
		}
		if result.outcome == "Cancelled" {
			d.responseTab = resViewBody
		} else if result.response == nil && len(result.console) > 0 {
			d.responseTab = resViewConsole
		}
		d.experimentID = 0
		if result.response != nil {
			first := d.response == nil
			d.response = result.response
			d.responseEditor = nil
			d.comparison.diffPart = "" // even an identical URL is a new send
			// The first response is what the reader came for; a divider they
			// set themselves stays where they put it.
			if first {
				d.responseArrived()
			}
		}
		if result.vars != nil && !maps.Equal(result.varsBefore, result.vars) {
			a.applyVars(result.environment, result.varsBefore, result.vars, result.writes...)
		}
		a.applyWrites(result.writes)
		if result.experiment != nil {
			for _, f := range a.flows {
				if f.live {
					result.experiment.keepWire(f)
				}
			}
			if err := a.experiments.log.retain(*result.experiment, a.experimentLimits()); err != nil {
				a.experiments.problem = locales.ExperimentNotRetained(err.Error())
			} else {
				d.experimentID = a.experiments.log.NextID
				a.experiments.persist()
			}
		}
	}

	if err != nil {
		a.errText = err.Error()
		a.err = nil
	}
	a.mu.Lock()
	pendingFrames := a.pendingFrames
	a.pendingFrames = nil
	a.mu.Unlock()
	for _, ev := range pendingFrames {
		if !a.unrecorded[ev.FlowID] {
			a.addFrame(ev)
		}
	}
	if a.bp == nil {
		a.mu.Lock()
		var next *capture.Breakpoint
		for len(a.bps) > 0 {
			bp := a.bps[0]
			a.bps = a.bps[1:]
			select {
			case <-bp.Done:
				if a.attention != nil {
					a.attention.clear(breakpointAttentionID(bp.ID))
				}
				continue
			default:
			}
			next = &bp
			break
		}
		a.mu.Unlock()
		if next != nil {
			a.showBreakpoint(next)
		}
	}
	if a.recordingPaused {
		a.stopCollectionRecording(locales.CaptureWasPausedStartCollection())
	}
	if s := a.collectionRecording; s != nil && s.Active && a.srv.Dropped() != s.dropped {
		a.stopCollectionRecording(locales.CaptureDroppedTrafficReduceLoad())
	}
	if pending = a.record(pending); len(pending) == 0 {
		return
	}
	// New traffic is selected and scrolled to while Auto-select is on. A
	// draft being edited keeps the reader where they are: the arrivals are
	// counted on the footer instead of replacing the row under the cursor.
	editing := a.requestDocument != nil && a.requestDocument.dirty()
	paused := !a.autoSelect || editing
	if view := a.views.activeView(); view != nil && len(a.visible) > 0 && !view.list.AtEnd() {
		paused = paused || !a.autoSelect
	}
	// Code that replaced a.flows without recounting leaves the tally and the
	// rows stale: rebuild both rather than merge into them.
	stale := a.tallyStale()
	if stale {
		a.recount()
	}
	start := len(a.flows)
	for _, f := range pending {
		kind := f.Kind
		if kind == "http" {
			kind = ""
		}
		a.flows = append(a.flows, flow{
			id:          f.ID,
			debugKey:    uuid.New().String(),
			debugRun:    f.DebugRun,
			live:        true,
			replayOf:    f.ReplayOf,
			actions:     f.Actions,
			method:      f.Method,
			scheme:      f.Scheme,
			host:        f.Host,
			path:        f.Path,
			url:         f.URL,
			status:      f.Status,
			bytes:       f.Bytes,
			millis:      f.Millis,
			reqHeaders:  f.RequestHeaders,
			resHeaders:  f.ResponseHeaders,
			reqBody:     f.RequestBody,
			reqFile:     f.RequestBodyFile,
			reqSize:     f.RequestBodySize,
			reqWire:     f.RequestBodyWireSize,
			reqError:    f.RequestBodyError,
			resBody:     f.ResponseBody,
			resFile:     f.ResponseBodyFile,
			resSize:     f.ResponseBodySize,
			resError:    f.ResponseBodyError,
			reqHeader:   f.RequestHeader,
			respHeader:  f.ResponseHeader,
			reqTrailer:  f.RequestTrailer,
			resTrailer:  f.ResponseTrailer,
			resProto:    f.ResponseProto,
			at:          f.At,
			kind:        kind,
			color:       f.Color,
			strike:      f.Strike,
			process:     f.Process,
			processPath: f.ProcessPath,
			dns:         f.DNSMapped,
			hostProfile: f.HostProfile,
			conn: conn{
				proto:      f.Conn.Proto,
				client:     f.Conn.Client,
				proxy:      f.Conn.Proxy,
				server:     f.Conn.Server,
				remote:     f.Conn.Remote,
				resolved:   f.Conn.Resolved,
				lookupMs:   f.Conn.LookupMs,
				tls:        f.Conn.TLS,
				cipher:     f.Conn.Cipher,
				certCN:     f.Conn.CertCN,
				issuer:     f.Conn.Issuer,
				certUntil:  f.Conn.CertUntil,
				flows:      f.Conn.Flows,
				reused:     f.Conn.Reused,
				dnsFresh:   f.Conn.DNSFresh,
				log:        f.Conn.Log,
				error:      f.Conn.Error,
				errorStage: f.Conn.ErrorStage,
			},
			wait:     f.Wait,
			download: f.Download,
			timing:   f.Timing,
		})
	}
	checkpoint := a.experiments.log
	wireChanged := false
	for _, f := range a.flows[len(a.flows)-len(pending):] {
		a.totalBytes += int64(f.bytes)
		if f.debugRun == "" {
			continue
		}
		for i := range checkpoint.Records {
			record := checkpoint.Records[i]
			if record.keepWire(f) {
				if !wireChanged {
					checkpoint.Records = slices.Clone(checkpoint.Records)
				}
				checkpoint.Records[i] = record
				wireChanged = true
			}
		}
	}
	if wireChanged {
		if _, err := encodeExperiments(checkpoint, a.experimentLimits()); err != nil {
			a.experiments.problem = locales.WireEvidenceNotRetained(err.Error())
		} else {
			a.experiments.log = checkpoint
			a.experiments.persist()
		}
	}
	for i := start; i < len(a.flows); i++ {
		a.count(&a.flows[i], 1)
	}
	a.recordCollection(a.flows[start:])
	end := len(a.flows)
	from, past := a.trimFlows()
	var arrived []int
	for i := max(start, past); i < end; i++ {
		arrived = append(arrived, i-from)
	}
	a.listCounts()
	if stale {
		a.applyFilter()
	} else {
		a.showArrivals(arrived)
	}
	switch {
	case len(pending) > 0 && a.autoSelect && !editing:
		a.selectNewest()
	case paused:
		a.newRows += len(pending)
	default:
		a.newRows = 0
	}
	if a.srv != nil {
		a.dropped = a.srv.Dropped()
		a.scriptErrs = a.srv.ScriptErrors()
	}
}

// applyFilter rebuilds the visible rows from the search text and the sidebar
// choice, and keeps the choice on a row that still shows.
func (a *app) applyFilter() {
	a.columnRead.dirty = true
	if a.tallyStale() {
		a.recount()
	}
	match := a.captureSearch()
	a.visible = a.visible[:0]
	for i := range a.flows {
		f := &a.flows[i]
		if f.debugKey == "" {
			f.debugKey = uuid.New().String()
		}
		if a.shows(f, match) {
			a.visible = append(a.visible, i)
		}
	}
	a.sortVisible()
	a.views.narrow(a)
	a.activityRows = append(a.activityRows[:0], a.visible...)
	a.activityCounts.valid = false
	if a.activity.active() {
		a.visible = slices.DeleteFunc(a.visible, a.outsideActivity)
	}
	a.views.rows = len(a.visible)
	a.restoreSelection()
}

// showArrivals adds the rows among rows, new flows, that pass the filters,
// merged into the table in its order, without walking the rows it has.
func (a *app) showArrivals(rows []int) {
	a.columnRead.dirty = true
	if view := a.views.activeView(); view != nil && strings.TrimSpace(view.body) != "" {
		// A body condition reads spilled bodies off the UI thread: the
		// full pass starts those reads and hides rows until they answer.
		a.applyFilter()
		return
	}
	match := a.captureSearch()
	fresh := rows[:0]
	for _, at := range rows {
		if a.shows(&a.flows[at], match) {
			fresh = append(fresh, at)
		}
	}
	if len(fresh) == 0 {
		return
	}
	slices.SortFunc(fresh, a.rowOrder())
	a.activityRows = a.mergeRows(a.activityRows, fresh)
	a.activityCounts.valid = false
	if a.activity.active() {
		fresh = slices.DeleteFunc(fresh, a.outsideActivity)
	}
	a.visible = a.mergeRows(a.visible, fresh)
	a.views.rows = len(a.visible)
	a.restoreSelection()
}

// mergeRows merges fresh, in table order, into rows, in table order.
func (a *app) mergeRows(rows, fresh []int) []int {
	order := a.rowOrder()
	if len(fresh) == 0 || len(rows) == 0 || order(rows[len(rows)-1], fresh[0]) < 0 {
		return append(rows, fresh...) // new traffic usually sorts last
	}
	merged := make([]int, 0, len(rows)+len(fresh))
	i, j := 0, 0
	for i < len(rows) && j < len(fresh) {
		if order(fresh[j], rows[i]) < 0 {
			merged = append(merged, fresh[j])
			j++
		} else {
			merged = append(merged, rows[i])
			i++
		}
	}
	return append(append(merged, rows[i:]...), fresh[j:]...)
}

// outsideActivity reports whether the flow at a row is outside the time the
// activity strip selects.
func (a *app) outsideActivity(at int) bool {
	from, to := a.activity.bounds()
	t := a.flows[at].at
	return t.Before(from) || !t.Before(to)
}

// shows reports whether a flow passes the sidebar choice, the compiled search
// and the capture conditions, all but a view's body condition.
func (a *app) shows(f *flow, match func(string) bool) bool {
	switch {
	case !a.matchesChip(*f),
		a.filter == "pinned" && !f.pinned,
		a.filter == "errors" && !f.failed(),
		a.filter == "slow" && f.millis < slowMillis:
		return false
	}
	if host, ok := strings.CutPrefix(a.filter, "host:"); ok && f.host != host {
		return false
	}
	if client, ok := strings.CutPrefix(a.filter, "client:"); ok && f.process != client {
		return false
	}
	if !match(f.host + f.path) {
		return false
	}
	matched, active := a.matchesContent(*f)
	// An OR body alternative can match a row the immediate fields don't.
	// narrow keeps only proven matches and schedules unknown full-body reads.
	return matched || a.filterAny && (!active || strings.TrimSpace(a.views.bodyFilter()) != "")
}

// restoreSelection maps the stable choice to the table's current row index.
func (a *app) restoreSelection() {
	// Resolve the identity only after all filters and sorting have finished.
	// If it disappeared, choose the nearest surviving row, or none.
	for row, at := range a.visible {
		if a.flows[at].id == a.pickedID {
			a.picked = row
			return
		}
	}
	a.pickedID = 0
	a.picked = min(max(a.picked, 0), len(a.visible)-1)
	if f, ok := a.current(); ok {
		a.pickedID = f.id
	}
}

// current is the flow the detail panes show, if any.
func (a *app) current() (flow, bool) {
	if len(a.visible) == 0 || a.picked < 0 || a.picked >= len(a.visible) {
		return flow{}, false
	}
	return a.flows[a.visible[a.picked]], true
}

// choosePicked puts the row the inspector shows in the table's choice. MyGo
// paints a table's chosen rows from the selection set it is given, not from
// the row the keys move from, so a picked row outside that set was drawn like
// every other one. The app moves the row itself when the first request
// arrives, an import or a link lands on one, or a filter leaves a neighbour
// in its place; choosing it is what a click on it does, so the row a person
// is reading is the row they see marked. A choice the user widened stays as
// it is: only a row missing from it is added back, replacing the others, as a
// click on that row would.
func (a *app) choosePicked() {
	f, ok := a.current()
	if !ok || a.chosen.Has(f.id) {
		return
	}
	a.chosen.Clear()
	a.chosen.Add(f.id)
}

// previousResponse is the last response for the same method and URL, the
// other side of the diff. An empty response is still a valid baseline.
func (a *app) previousResponse() (string, bool) {
	if len(a.visible) == 0 || a.picked < 0 || a.picked >= len(a.visible) {
		return "", false
	}
	at := a.visible[a.picked]
	f := a.flows[at]
	for i := at - 1; i >= 0; i-- {
		prev := a.flows[i]
		if prev.method == f.method && prev.scheme == f.scheme && prev.host == f.host && prev.path == f.path && prev.status > 0 {
			return prev.body(true), true
		}
	}
	return "", false
}

// syncDetail rebuilds the text and the diff when the choice moves.
func (a *app) syncDetail() {
	f, ok := a.current()
	if !ok {
		a.detailFor = -1
		a.connOpen = false
		a.protobuf.stop()
		a.structured[0].stop()
		a.structured[1].stop()
		return
	}
	if a.detailFor == f.id {
		return
	}
	a.structured[0].stop()
	a.structured[1].stop()
	a.detailFor = f.id
	a.connOpen = false
	if f.kind == "" && a.inspect.pane == inspectorMessages {
		a.inspect.pane = inspectorSplit
	}
	if f.kind == "connect" {
		if f.conn.error != "" {
			a.tab = 1 // open the failure evidence, not tunnel-only setup instructions
		}
	} else if a.tab != 0 {
		a.tab = 0
	}
	if (f.kind == "ws" || f.kind == "sse") && a.inspect.pane == inspectorSplit {
		a.inspect.pane = inspectorMessages
	}
	if f.kind != "ws" && f.kind != "sse" && a.inspect.pane == inspectorMessages {
		a.inspect.pane = inspectorSplit
	}
	a.body, a.bodyNotice[0] = f.bodyPreview(true)
	a.reqBody, a.bodyNotice[1] = f.bodyPreview(false)
	a.rawText[0], a.rawText[1] = rawMessage(f, 0), rawMessage(f, 1)
	a.rawEditor = [2]*editor.State{}
	a.inspect.reveal = [2]bool{}
	a.bodyMode, a.bodyQuery = [2]int{}, [2]string{}
	a.headerScroll, a.bodyText = [2]ui.ScrollState{}, [2]*editor.State{}
	a.jsonScroll, a.hexScroll = [2]ui.ListState{}, [2]ui.ListState{}
	for side := range a.jsonReader {
		if a.jsonReader[side].cancel != nil {
			a.jsonReader[side].cancel()
		}
	}
	a.jsonReader = [2]jsonReader{}
	a.protoFor, a.htmlFor, a.imageFor = [2]string{}, [2]string{}, [2]string{}
	a.jsonTrees, a.protoOK = [2]jsonTreeResult{}, [2]bool{}
	a.protoOwner = -1
	a.htmlDoc, a.imageBmp, a.imageErr = [2]*richtext.Document{}, [2]*ui.Bitmap{}, [2]string{}
	a.protobuf.stop()
	previous, baseline := a.previousResponse()
	a.diffBaseline = baseline
	a.diff = &editor.DiffState{
		Old:          editor.NewBuffer(previous),
		New:          editor.NewBuffer(a.body + a.bodyNotice[0]),
		Layout:       editor.DiffSplit,
		SplitPercent: a.diffSplit,
	}
	a.diff.Old.SetLanguage(editor.LanguageFor("response.json"))
	a.diff.New.SetLanguage(editor.LanguageFor("response.json"))
}

func (a *app) view(c *ui.Context) {
	defer uikit.FrameEnd(c)
	a.firstFrame()
	// A modifier release outside the main column still settles the quick
	// switch; the column's input observer handles it when focus is inside.
	if a.mruPending && c.Modifiers()&ui.Ctrl == 0 {
		a.commitMRUPick()
	}
	// Screen readers hear every signal, whether or not the window was in
	// front. Announcements are drained once so no frame repeats them.
	for _, text := range a.attention.drainAnnouncements() {
		c.Announce(text)
	}
	if len(a.pendingFlowDelete) > 0 {
		pending := a.pendingFlowDelete
		a.pendingFlowDelete = nil
		a.deleteFlows(pending)
	}
	a.builds++
	// The pass starts from MyGo's theme, which follows the system.
	a.dark = a.appearance == "dark" || a.appearance == "" && c.Theme().Dark
	a.windowWidth, _ = c.Size()
	// The desktop's text size reaches the four sizes the screens use, and its
	// contrast the theme; the accent stays ours.
	prefs := c.Preferences()
	uikit.SetTextScale(prefs.TextScale)
	theme := uikit.Theme(a.dark).WithPreferences(prefs)
	theme.Accent, theme.AccentHover, theme.AccentPressed, theme.AccentText = uikit.Theme(a.dark).Accent, uikit.Theme(a.dark).AccentHover, uikit.Theme(a.dark).AccentPressed, uikit.Theme(a.dark).AccentText
	c.SetTheme(theme)
	c.SetMenuPresentation(ui.PopoverMenus)
	a.inspectorEditing = a.workbench.inspector.FocusWithin(c)
	a.applyPendingMove()
	a.drain()
	a.drainSearch()
	a.drainCompare()
	a.syncProblems()
	if !a.problems.fixMenuOpen {
		// The focused field records the caret target as the view builds.
		a.problems.caret, a.problems.caretFixes = nil, nil
		a.problems.variableCaret = nil
	}
	a.applyProblemLocation()
	a.pruneURLPaste()
	// A row the reader picked by hand is theirs: Auto-select steps aside.
	// A missing row normalizes to -1; that is the list being empty, not the
	// reader taking over. Only a move between two real rows is a manual pick.
	if a.picked >= 0 && a.lastPicked >= 0 && a.picked != a.lastPicked && !a.autoSelecting {
		a.autoSelect = false
	}
	a.lastPicked = a.picked
	// The activity strip is the session's timeline, not the current rows':
	// once traffic has been here it keeps its place through Clear.
	a.hadFlows = a.hadFlows || len(a.flows) > 0
	a.drainUpstream()
	a.drainGRPC()
	a.drainGraphQL()
	a.drainSocketIO()
	a.drainMQTT()
	a.drainMCP()
	a.scripts.setEnv(a.globals, a.activeVars(), a.addons.sources())
	// The Settings window shows these checks too: it redraws when one
	// finishes, and shows the messages while it is in front.
	busy := [3]bool{a.certificate.checking, a.systemProxy.checking, a.systemProxy.applying}
	a.drainCertificate()
	a.drainSystemProxy()
	a.drainClientSetup()
	a.drainRunner()
	a.drainLoadRunner()
	a.drainHosts()
	settings := a.settingsWin != nil
	if settings && busy != [3]bool{a.certificate.checking, a.systemProxy.checking, a.systemProxy.applying} {
		a.settingsWin.Invalidate()
	}
	if (!settings || !a.settingsFocused) && (a.toolsWin == nil || !a.toolsFocused) {
		a.showNotices(c)
	}
	a.mu.Lock()
	replays := a.replayResults
	a.replayResults = nil
	a.mu.Unlock()
	for _, result := range replays {
		a.notify(notice{text: locales.ReplayedCapture(result.source.id, result.source.method, result.source.path, result.reply.Status), action: locales.InspectReplay(), run: func() {
			// The response belongs to this exact send, not the next capture of
			// the same URL. Never move the reader until they ask to inspect it.
			if d := a.editCapture(result.source); d != nil {
				d.response = &result.reply
				d.responseArrived()
			}
		}})
	}
	a.actionShortcuts(c)
	if a.sort != a.sortApplied {
		a.sortApplied = a.sort
		a.applyFilter()
	}
	a.syncColumnValues(c)
	a.views.drain(a)
	a.views.sync(a)
	// Bulk actions must never include requests hidden by this view's filters.
	if a.chosen.Len() > 0 {
		visibleIDs := make(map[int]bool, len(a.visible))
		for _, i := range a.visible {
			visibleIDs[a.flows[i].id] = true
		}
		for id := range a.chosen.All() {
			if !visibleIDs[id] {
				a.chosen.Remove(id)
			}
		}
	}
	a.choosePicked()
	// The keys belong to the traffic list when LIVE opens: the window's own
	// column takes the focus first, so the arrows, Home, End and Enter did
	// nothing until the list was clicked. It is asked once per visit, not
	// every frame, so that Tab can leave the list for the sidebar and the
	// title bar.
	if a.page == pageLive {
		if view := a.views.activeView(); view != nil && len(a.visible) > 0 {
			switch {
			case view.list.Handle.Focused(c) || view.list.Handle.FocusWithin(c):
				a.workbench.listFocusAsked = true
			case !a.workbench.listFocusAsked && a.workbench.content.Focused(c):
				view.list.Handle.Focus(c)
			}
		}
	} else {
		a.workbench.listFocusAsked = false
	}
	a.nativeMenuActions(c)
	t := c.Theme()
	a.syncSections()
	a.syncDocument()
	// On macOS the sidebar runs the window's full height, the traffic
	// lights at its top, as a window with a source list does there, and
	// the title bar heads the column beside it. Elsewhere the window
	// controls are on the right: the title bar spans the window, the
	// brand at its start.
	main := func() {
		ui.Column(c).Fill().Children(func() { a.mainColumn(c, t) })
	}
	// The window is one chrome surface: the title bar, the sidebar and the
	// footer are the same color, and the screen floats on top of them as a
	// raised canvas panel.
	// The window is one chrome surface, except on macOS, where the sidebar's
	// material shows the desktop through it: there the root stays clear and
	// each part of the frame paints its own share of chrome.
	// The root paints chrome itself: a clear root let the desktop's wallpaper
	// through the title bar and the gutters between the cards.
	frame := uikit.Colors(t).Window
	ui.Column(c).Bind(&a.workbench.content).Focusable().AutoFocus().Fill().Background(frame).
		ObserveInput(a.observeMRUInput).Children(func() {
		if !a.macChrome {
			a.shell(c, t)
		}
		if a.sidebarShown() {
			ui.Row(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
				ui.Column(c).Width(a.sidebarWidth).Shrink(0).FillHeight().Children(func() { a.sidebar(c) })
				a.sidebarGutter(c)
				ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(main)
				a.sidebarGrip(c, t)
			})
		} else {
			ui.Column(c).Grow(1).FillWidth().MinHeight(0).Children(main)
		}
		a.footer(c.Key("workspace-footer"), t)
	})
	// The target editor exists now: selecting a Search result can reveal it.
	a.revealSearchMatch()
	a.revealVariableMatch(c)
	a.persistSettings()
	a.checkpointExperiments(c)
	a.checkpointSessions()
	// Dialogs keep their input and actions above background notifications.
	uikit.Toasts(c)
	a.commandPalette(c)
	uikit.Modal(c, &a.clearOpen, locales.ClearCapturedRequests(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.ClearCapturedRequests())
			ui.Scroll(c).MinHeight(0).Children(func() {
				uikit.Prose(c, locales.UnpinnedRequestsWillBeRemoved())
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.clearOpen = false
				}
				if uikit.DestructiveButton(c, locales.ClearUnpinnedRequests()).Clicked() {
					a.clearCaptured()
					a.clearOpen = false
				}
			})
		})
	})
	a.settingsModal(c, t)
	a.workspaceTrustModal(c, t)
	a.logLevelModal(c)
	a.sslModal(c, t)
	a.toolsModal(c, t)
	a.configImportDialog(c, t)
	a.progressDialog(c, t)
	a.dnsRuleModal(c, t)
	a.hostListModal(c, t, &a.ignoreAppsOpen, locales.IgnoredApps2(),
		locales.AppsWhoseFlowsStayOut(),
		a.ignoreAppsPath, &a.ignoreAppsText, func(apps []string) {
			if a.srv != nil {
				a.srv.SetIgnoreApps(apps)
			}
		})
	a.hostListModal(c, t, &a.ignoreOpen, locales.IgnoredHosts2(),
		locales.HostsWhoseFlowsStayOut(),
		a.ignorePath, &a.ignoreText, func(patterns []string) {
			if a.srv != nil {
				a.srv.SetIgnore(patterns)
			}
		})
	a.hostListModal(c, t, &a.allowOpen, locales.AllowedHosts(),
		locales.WhileTheAllowListIs2(),
		a.allowPath, &a.allowText, func([]string) { a.applyAllow() })
	a.hostListModal(c, t, &a.allowAppsOpen, locales.AllowedApps(),
		locales.WhileTheAllowListIs(),
		a.allowAppsPath, &a.allowAppsText, func([]string) { a.applyAllow() })
	a.varsModal(c, t)
	a.moveToModal(c)
	a.folderModal(c, t)
	a.renameModal(c, t)
	a.referencesModal(c, t)
	a.searchPreviewModal(c, t)
	a.historyModal(c, t)
	a.docsModal(c, t)
	// A link in an imported description can be clicked while Documentation
	// is open, so its confirmation has to draw above that modal.
	a.linkModal(c)
	a.aboutModal(c, t)
	a.deleteModal(c, t)
	a.codeModal(c, t)
	a.vaultModal(c, t)
	a.markModal(c, t)
	a.views.modals(c, t, a)
	a.columnModal(c, t)
	a.networkModal(c, t)
	a.keyLogModal(c, t)
	a.reverseProxyModal(c, t)
	a.closeRequestModal(c, t)
	a.apiGenerationModal(c, t)
	a.curlImportModal(c, t)
	a.urlImportModal(c, t)
	a.devicesModal(c, t)
	a.clientSetupModal(c, t)
	a.experimentsModal(c, t)
	a.socketModal(c, t)
	a.sseModal(c, t)
	a.requestRenameModal(c, t)
	a.headerWarnModal(c, t)
	a.runnerModal(c, t)
	a.testDiffModal(c, t)
	a.compareModal(c, t)
	a.loadRunnerModal(c, t)
	a.cookiesModal(c, t)
	a.decoderModal(c)
	a.jsonQueryModal(c)
	a.exportModal(c, t)
	a.replayModal(c, t)
	a.collectionSaveModal(c, t)
	a.collectionWizardModal(c, t)
	a.collectionRecordingModal(c, t)
	a.tokenLinksModal(c, t)
	a.protobuf.modal(c, a)
	// Last of the dialogs: an event-driven prompt, especially a breakpoint
	// holding traffic, must never draw beneath a modal the reader opened.
	a.autoDialogs(c)
	a.views.capture(a)
	a.syncTitle()
	// Last, since every SetTheme above paints the root again: the shell paints
	// its own surface, so nothing of the desktop shows through it.
	c.Root().Background(uikit.Colors(c.Theme()).Window)
}

// mainColumn is everything beside the sidebar: on macOS the title bar (which
// carries the tools that are on), then problems, the content and the footer.
func (a *app) mainColumn(c *ui.Context, t *ui.Theme) {
	if a.macChrome {
		a.shell(c, t)
	}
	a.panel(c.Key("workspace-content"), t)
}

// panel is the raised content surface: every screen floats on it as one
// canvas panel, rounded and edged, inset from the window's chrome on the
// right, and on the left too while the sidebar is away. It runs flush under
// the title bar and above the footer, so the shell reads as one frame.
// sidebarGutter is the space between the sidebar card and the content card:
// the shell shows through it, and the pointer drags its divider to resize the
// sidebar.
const (
	sidebarGutter float32 = 2
	sidebarGrip   float32 = 8
)

// sidebarGutter is the space between the sidebar card and the content card:
// the shell shows through it.
func (a *app) sidebarGutter(c *ui.Context) {
	// The gutter is the shell's own surface: on macOS the window's root is
	// clear, so a gap that paints nothing shows the desktop instead.
	ui.Box(c).Width(sidebarGutter).Shrink(0).FillHeight().Background(uikit.Colors(c.Theme()).Window)
}

// sidebarGrip is the handle that resizes the sidebar, and the line that shows
// where the gutter is. The handle spans 8 DIPs over both cards, so the pointer
// finds it without aiming at the 2 DIPs of gutter; the line is invisible at
// rest and the text color at 28% while the pointer is over the handle or drags
// it. It comes after the content in the row so it takes the pointer over it.
func (a *app) sidebarGrip(c *ui.Context, t *ui.Theme) {
	p := uikit.Colors(t)
	handle := ui.Box(c).Absolute().Left(a.sidebarWidth - sidebarGrip/2).Width(sidebarGrip).Top(0).Bottom(0).
		Cursor(ui.CursorResizeEW)
	var line ui.Element
	handle.Children(func() {
		line = ui.Box(c).Absolute().Left((sidebarGrip - sidebarGutter) / 2).Width(sidebarGutter).Top(0).Bottom(0)
	})
	dx, _, held := handle.Dragged()
	switch {
	case held && handle.DoubleClicked():
		a.sidebarWidth = 220
	case held && dx != 0:
		a.sidebarWidth = clampSidebar(a.sidebarWidth + dx)
	}
	// The line lights at once under the pointer or while dragged, and goes
	// out at once. A zero color would leave the previous frame's line in
	// place, so the resting line is the text color at alpha 0.
	if held || handle.Hovered() {
		line.Background(p.Foreground.Alpha(0.12))
	} else {
		line.Background(p.Foreground.Alpha(0))
	}
}

// clampSidebar keeps the sidebar between a size it can be read at and one that
// leaves the content most of the window.
func clampSidebar(w float32) float32 {
	return min(max(w, 180), 520)
}

func (a *app) panel(c *ui.Context, t *ui.Theme) {
	p := uikit.Colors(t)
	left := float32(0)
	if !a.sidebarShown() {
		left = t.Space(uikit.SpaceM)
	}
	// A row of three: the gutter, the panel, and the gutter at the window's
	// right edge. Padding on the panel itself would join its own size and
	// push it past the edge.
	// The content is a card on the shell: no edge and no shadow, told apart
	// by its surface alone, as the sidebar beside it is.
	ui.Row(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		if left > 0 {
			ui.Box(c).Width(left).Shrink(0).FillHeight()
		}
		// The content card meets the window's right edge, so only the corners
		// that face the shell are round: both of them once the sidebar is
		// gone and the shell runs down its left side too.
		radii := [4]float32{uikit.RadiusPanel, 0, 0, uikit.RadiusPanel}
		if !a.sidebarShown() {
			radii = [4]float32{uikit.RadiusPanel, uikit.RadiusPanel, uikit.RadiusPanel, uikit.RadiusPanel}
		}
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(p.Background).
			Radius(radii[0], radii[1], radii[2], radii[3]).Clip().Children(func() {
			if a.srv == nil && a.errText != "" {
				uikit.Callout(c, uikit.CalloutDanger, locales.ProxyUnavailable(a.errText), nil).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
			}
			if a.requestsProblem != "" || a.envsProblem != "" {
				ui.Column(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					uikit.Callout(c, uikit.CalloutDanger, locales.LocalAssetsProtectedSavingDisabled(), func() {
						if uikit.SecondaryButton(c, locales.ReloadLocalAssets()).Clicked() {
							a.loadRequestAssets()
						}
					})
					for _, problem := range []string{a.requestsProblem, a.envsProblem} {
						if problem != "" {
							uikit.ErrorText(c, problem)
						}
					}
					uikit.Meta(c, locales.OriginalFilesLeftUntouchedRepair())
				})
			}
			a.desktopContent(c.Key("workspace-content"))
		})
	})
}

// breakpointModal edits the request the proxy paused, and lets it continue.
func (a *app) breakpointModal(c *ui.Context, t *ui.Theme) {
	if a.bp == nil {
		return
	}
	select {
	case <-a.bp.Done:
		if a.attention != nil {
			a.attention.clear(breakpointAttentionID(a.bp.ID))
		}
		a.bp, a.bpOpen = nil, false
		return
	default:
	}
	message := a.bp.Phase == capture.PhaseMessage
	a.syncRaw()
	if a.bpWait == "" {
		a.bpWait = breakWaitLabel(a.waitSeconds())
	}
	// Feedback from a previous decision must not cover this pause's footer,
	// especially when queued breakpoints open in a small window.
	c.CloseToast("")
	_, height := c.Size()
	title := locales.BreakpointRequestPaused()
	if a.bp.Phase == capture.PhaseResponse {
		title = locales.BreakpointResponsePaused()
	}
	if message {
		title = locales.BreakpointWebSocketPaused()
	}
	if uikit.Modal(c, &a.bpOpen, title, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(640, height-80)).Children(func() {
			uikit.DialogTitle(c, title)
			a.mu.Lock()
			waiting := len(a.bps) + 1
			a.mu.Unlock()
			validation := a.breakpointError()
			ui.Row(c).FillWidth().Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if message {
					uikit.Meta(c, locales.PausedMessages(waiting))
				} else {
					uikit.Meta(c, locales.PausedExchanges(waiting)).Grow(1).MinWidth(0)
					a.breakTemplates(c, t, validation == "")
				}
			})
			if !message {
				ui.Row(c).FillWidth().Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					view := locales.Raw()
					if a.bpRawMode {
						view = locales.FieldsView()
					}
					if uikit.SecondaryButton(c, view).Clicked() {
						a.toggleRaw()
					}
					ui.Row(c).Grow(1)
					uikit.Meta(c, locales.BreakWaitLabel())
					if uikit.Select(c, &a.bpWait, breakWaitLabels()).Width(190).Label(locales.BreakWaitLabel()).Changed() {
						a.setBreakWait()
					}
				})
			}
			if a.bpNote != "" {
				uikit.Hint(c, a.bpNote)
			}
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(uikit.SpaceM)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				if message {
					direction := locales.ServerToClient()
					if a.bp.Direction == "out" {
						direction = locales.ClientToServer()
					}
					uikit.Body(c, direction+" · "+a.bp.MessageType)
					uikit.Tip(c, uikit.Hint(c, a.bp.URL).SingleLine(), a.bp.URL)
					if a.bp.BodyReadOnly {
						uikit.Hint(c, locales.MessageExceedsTheEditorLimit())
					} else {
						uikit.Meta(c, locales.Utf8TextOrCanonical())
						uikit.TextArea(c, &a.bpBody).Height(240).Label(locales.MessagePayload()).Font(uikit.FontMono).Radius(uikit.RadiusControl)
					}
					return
				}
				if a.bpRawMode {
					uikit.TextArea(c, &a.bpRaw).Height(340).Label(locales.RawMessage()).Font(uikit.FontMono).Radius(uikit.RadiusControl)
					if a.bp.BodyReadOnly {
						uikit.Hint(c, locales.BinaryUndecodableOrLargeBody())
					}
					return
				}
				if a.bp.Phase == capture.PhaseResponse {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
						uikit.Input(c, &a.bpStatus).Width(90).Label(locales.Status())
						uikit.Hint(c, a.bp.URL).SingleLine().Grow(1).MinWidth(0)
					})
				} else {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
						uikit.Input(c, &a.bpMethod).Width(90).Label(locales.Method())
						uikit.Input(c, &a.bpURL).Grow(1).MinWidth(0).Label(locales.Url())
					})
				}
				if a.bp.Phase == capture.PhaseRequest {
					uikit.Meta(c, locales.QueryParameters())
					a.requestFieldsEditor(c, &a.bpQuery, fieldsOptions{}, locales.Parameter())
					uikit.Meta(c, locales.UncheckedRowsStayHereAnd())
				}
				uikit.Meta(c, locales.HeadersOneNameValuePer())
				uikit.TextArea(c, &a.bpHeaders).Height(130).Label(locales.BreakpointHeaders()).Radius(uikit.RadiusControl)
				if a.bp.BodyReadOnly {
					uikit.Hint(c, locales.BinaryUndecodableOrLargeBody())
				} else if a.bpFormRows {
					uikit.Meta(c, locales.FormFields())
					a.requestFieldsEditor(c, &a.bpForm, fieldsOptions{}, locales.Parameter())
					uikit.Meta(c, locales.UncheckedRowsStayHereAnd())
				} else {
					uikit.TextArea(c, &a.bpBody).Height(160).Label(locales.Body()).Radius(uikit.RadiusControl)
					uikit.Meta(c, locales.BodyEditsUseDecodedBytes())
				}
			})
			if validation != "" {
				uikit.ErrorText(c, validation)
			}
			uikit.DialogFooter(c).Children(func() {
				abortLabel := locales.DropMessage()
				if !message {
					uikit.Select(c, &a.bpAbort, []string{"502 Bad Gateway", "429 Too Many Requests", "503 Service Unavailable"}).Width(uikit.WidthFieldWide).Label(locales.AbortStatus())
					abortLabel = locales.AbortBreakpoint()
				}
				if uikit.SecondaryButton(c, abortLabel).Clicked() {
					a.resolveBreak(true, false, false)
				}
				if !message && uikit.SecondaryButton(c, locales.DropConnection()).Clicked() {
					a.resolveBreak(false, false, true)
				}
				if uikit.SecondaryButton(c, locales.ContinueOriginal()).Clicked() {
					a.resolveBreak(false, true, false)
				}
				if uikit.PrimaryButton(c, locales.ApplyContinue()).Disabled(validation != "").Clicked() {
					a.resolveBreak(false, false, false)
				}
			})
		})
	}).Dismissed() {
		// Escape or an accidental outside click must not abort a live request.
		a.bpOpen = true
	}
}

// clearCaptured runs only after confirmation. Pinned exchanges, including
// their streaming messages, remain available for inspection.
func (a *app) clearCaptured() {
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
	// DeleteFunc compacts the backing array, so collect the removed IDs before
	// it runs; iterating the slice afterwards would visit the survivors.
	removed := make([]int, 0, len(a.flows))
	for _, f := range a.flows {
		if !f.pinned {
			removed = append(removed, f.id)
		}
	}
	a.flows = slices.DeleteFunc(a.flows, func(f flow) bool { return !f.pinned })
	a.frames = pruneMessages(a.frames, a.flows)
	a.chosen.Clear()
	for _, id := range removed {
		a.clearCompareSelection(compareTarget{kind: "flow", id: id})
	}
	a.totalBytes = 0
	for _, f := range a.flows {
		a.totalBytes += int64(f.bytes)
	}
	a.recount()
	a.applyFilter()
	a.done(locales.UnpinnedRequestsCleared())
}

// goTo shows what a sidebar choice names: an investigation, an API
// contract, or a LIVE scope of the traffic. Saved requests are tree items
// that open themselves.
func (a *app) goTo(choice string) {
	if id, ok := strings.CutPrefix(choice, "session:"); ok {
		a.selectSession(id)
		return
	}
	if id, ok := strings.CutPrefix(choice, "contract:"); ok {
		a.page, a.apiChoice = pageAPI, id
		return
	}
	if choice == "diagnostics" {
		a.openDiagnostics(nil, a.diagnostics.client)
		return
	}
	if choice == "overview" {
		a.openOverview()
		return
	}
	if choice == "search" {
		a.page, a.sidebarChoice = pageSearch, "search"
		return
	}
	a.page = pageLive
	if host, ok := strings.CutPrefix(choice, "host:"); ok {
		a.filterTriage.Host = host
	} else if client, ok := strings.CutPrefix(choice, "client:"); ok {
		a.filterTriage.Client = textToken(client)
	} else {
		a.filter = cmp.Or(choice, "all")
	}
	a.applyFilter()
}

// sidebarRegions are the sidebar's foldable regions, in the order they appear.
var sidebarRegions = []string{"traffic", "sources", "sessions", "collections"}

// regionOpen is the state one region of the sidebar folds by, created on first
// use from what settings remembered: every region starts open.
func (a *app) regionOpen(id string) *bool {
	if a.regions == nil {
		a.regions = make(map[string]*bool, len(sidebarRegions))
		for _, shut := range a.shutRegions {
			open := false
			a.regions[shut] = &open
		}
	}
	if open, ok := a.regions[id]; ok {
		return open
	}
	open := new(bool)
	*open = true
	a.regions[id] = open
	return open
}

// closedRegions lists the regions folded away, in the order they appear, for
// settings to remember.
func (a *app) closedRegions() []string {
	var closed []string
	for _, id := range sidebarRegions {
		if open, ok := a.regions[id]; ok && !*open {
			closed = append(closed, id)
		}
	}
	return closed
}

// paneHeight is one middle region's body height, created on first use from
// what settings kept, or the room its content asks for.
func (a *app) paneHeight(id string, def float32) *float32 {
	if a.paneHeights == nil {
		a.paneHeights = make(map[string]*float32, 3)
	}
	if h, ok := a.paneHeights[id]; ok {
		return h
	}
	if saved, ok := a.savedHeights[id]; ok && saved > 0 {
		def = saved
	}
	a.paneHeights[id] = &def
	return &def
}

// paneHeightValues are the middle's heights as settings writes them.
func (a *app) paneHeightValues() map[string]float32 {
	values := make(map[string]float32, len(a.paneHeights))
	for id, h := range a.paneHeights {
		values[id] = *h
	}
	return values
}

// sourcesBodyHeight is the room the Sources list asks for: its rows, up to
// the five it shows before the rest, and the row that opens them. An empty
// list asks for its one hint line. A short window asks for three rows at
// most, so the collections below keep their room.
func (a *app) sourcesBodyHeight(windowHeight float32) float32 {
	rows := len(a.domains)
	if a.sourcesApps {
		rows = len(a.clients)
	}
	if rows == 0 {
		return uikit.HeightRow * 2
	}
	n := min(rows, sourcesShown)
	if rows > sourcesShown {
		n++
	}
	if windowHeight > 0 && windowHeight < 700 {
		n = min(n, 3)
	}
	return float32(n) * uikit.HeightRow
}

// sessionsBodyHeight is the room the sessions ask for: up to six rows and
// the row that opens a session file, or two rows in a short window.
func (a *app) sessionsBodyHeight(windowHeight float32) float32 {
	n := min(len(a.sessions.items), 6) + 1
	if windowHeight > 0 && windowHeight < 700 {
		n = min(n, 2)
	}
	return float32(n) * uikit.HeightRow
}

// sourcesShown is how many hosts or apps the sidebar's Sources lists before
// its last row offers the rest.
const sourcesShown = 5

// The sidebar's Sources switch, in the order it reads: the apps, then the
// hosts. A fresh app lists the hosts, as the design does.
const (
	sourcesApp = iota
	sourcesHost
)

// sidebar is the window's one source list: TRAFFIC fixed at the top, the
// foldable Sources, Sessions and Collections in the middle, and the tools at
// the bottom. Its one choice is what the content area shows; syncSections
// keeps it in step. The middle takes the room the fixed parts leave, so a
// long list of hosts never pushes the collections or the footer off the
// window.
func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	// The sidebar is part of the shell, not a card on it: it paints chrome, the
	// same surface as the title bar above it and the footer below, so the
	// brand's row and the sidebar meet without a seam.
	panel := uikit.Colors(t).Window
	// MyGo draws a source list on its Surface, and the window has one
	// Surface for the panel too: here the sidebar's own surface is chrome,
	// so the list does not cut a canvas hole in the frame.
	local := *t
	local.Surface = panel
	c.SetTheme(&local)
	ui.Column(c).Fill().Background(panel).Children(func() {
		if a.macChrome {
			a.brandBar(c, t)
		}
		a.trafficRegion(c, t)
		ui.Divider(c).Shrink(0)
		a.sidebarPanes(c, t)
		// No rule above the sidebar's footer: the shell is one surface from
		// the title bar to the window's bottom edge.
		a.sidebarFooter(c, t)
	})
	c.SetTheme(t)
}

// trafficRegion is the fixed top of the sidebar: the scopes the traffic list
// narrows to and the views saved over them. It does not scroll with the
// regions below, and its heading folds it to one row.
func (a *app) trafficRegion(c *ui.Context, t *ui.Theme) {
	traffic := a.regionOpen("traffic")
	// Traffic is fixed chrome: every scope and view stays in place, and a
	// short window takes its room from the middle rather than cutting the
	// scopes or scrolling them away.
	uikit.SidebarList(c, "traffic-region", &a.sidebarChoice, func(id string) { a.goTo(id) }, func() {
		uikit.SidebarHeading(c, locales.Traffic(), traffic, func() {
			// Search sits in the heading, like an activity-bar entry: it must
			// not take a row from the scopes and views below it.
			if uikit.RowIconButton(c, locales.SearchEverywhere(), locales.SearchEverywhere(), searchIcon).Clicked() {
				a.goTo("search")
			}
			if uikit.RowIconButton(c, locales.NewView(), locales.ANewViewWithIts(), plusIcon).Clicked() {
				a.views.capture(a)
				a.views.newView(a)
			}
		})

		if *traffic {
			ui.Column(c.Key("traffic-region")).Gap(0).Shrink(0).Children(func() {
				a.trafficRows(c)
				a.views.sidebar(c, t, a)
			})
		}
	}).Padding(t.Space(uikit.SpaceXS), 0)
}

// sidebarCollectionsBody is the room the collections ask for when they are
// not the growing region: enough for the search and two rows before it
// scrolls.
const sidebarCollectionsBody = 144

// sidebarPanes are the middle's foldable regions, in the order they read:
// where the capture came from, the sessions kept from it, and the saved
// requests. Each keeps the height its content asks for; the last open one
// takes the room the others leave, so folding the two above Collections
// gives the tree the whole middle.
func (a *app) sidebarPanes(c *ui.Context, t *ui.Theme) {
	_, windowHeight := c.Size()
	sources, sessions := a.sourcesBodyHeight(windowHeight), a.sessionsBodyHeight(windowHeight)
	stack := uikit.SidebarPanes(c, "sidebar-panes", a.paneViewport, []uikit.SidebarPane{
		{
			ID: "sources", Title: locales.Sources(), Open: a.regionOpen("sources"),
			Height: a.paneHeight("sources", sources), Default: sources, Min: uikit.HeightRow,
			Trailing: func() { a.sourcesSwitch(c, t) },
			Body:     func() { a.sourcesPane(c, t) },
		},
		{
			ID: "sessions", Title: locales.Sessions(), Open: a.regionOpen("sessions"),
			Height: a.paneHeight("sessions", sessions), Default: sessions, Min: uikit.HeightRow,
			Body: func() { a.sessionsPane(c, t) },
		},
		{
			ID: "collections", Title: locales.Collections(), Open: a.regionOpen("collections"),
			Height: a.paneHeight("collections", sidebarCollectionsBody), Default: sidebarCollectionsBody, Min: uikit.HeightRow * 5,
			Trailing: func() { a.collectionsHeadingActions(c) },
			Body:     func() { a.collectionsPane(c, t) },
		},
	})
	// The scroll is the middle's viewport: its height is what the fixed
	// traffic and footer leave. Read it once the frame has laid it out, and
	// build the next frame with it, so the last open region fills exactly
	// the room there is.
	if h := stack.Bounds().H; h > 0 {
		if h-a.paneViewport > 0.5 || a.paneViewport-h > 0.5 {
			a.paneViewport = h
			c.Invalidate()
		}
	} else if a.paneViewport == 0 && !a.paneViewportAsked {
		a.paneViewportAsked = true
		c.Invalidate()
	}
}

// sourcesPane is the Sources list inside its region. The list scrolls within
// the region, and the region grows with the room the other regions leave.
func (a *app) sourcesPane(c *ui.Context, t *ui.Theme) {
	uikit.SidebarList(c, "sources-region", &a.sidebarChoice, func(id string) { a.goTo(id) }, func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() { a.sourcesRows(c, t) })
	}).Grow(1).MinHeight(0)
}

// sessionsPane is the sessions kept beyond live traffic, inside its region.
func (a *app) sessionsPane(c *ui.Context, t *ui.Theme) {
	uikit.SidebarList(c, "sessions-region", &a.sidebarChoice, func(id string) { a.goTo(id) }, func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() { a.sessionsRows(c, t) })
	}).Grow(1).MinHeight(0)
}

// collectionsHeadingActions are what the Collections heading offers: its menu
// and, once something is saved, a new request.
func (a *app) collectionsHeadingActions(c *ui.Context) {
	uikit.MoreMenu(c, locales.CollectionOptions(), a.collectionsMenu)
	// With nothing saved yet the tree's own row is the way to a request:
	// one entry rather than two.
	if len(a.requests) > 0 || len(a.folders) > 0 {
		if uikit.RowIconButton(c, locales.NewRequest(), locales.NewRequest(), plusIcon).Clicked() {
			a.openCompose()
		}
	}
}

// collectionsPane is the saved requests and the API contracts inside their
// region: the search stays above the tree, and the tree scrolls in the room
// the region has, which is all of the middle once the two above it fold.
func (a *app) collectionsPane(c *ui.Context, t *ui.Theme) {
	uikit.SidebarList(c, "collections-region", &a.sidebarChoice, func(id string) { a.goTo(id) }, func() {
		// The search stays above the tree: what is typed is not scrolled
		// away with the rows it narrows.
		uikit.SearchField(c, &a.requestQuery, locales.SearchCollections(), locales.Search(), true).
			Margin(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL), t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			a.collectionTree(c)
			query := strings.TrimSpace(a.requestQuery)
			hits := a.fuzzyList(query, "collections-contracts", strconv.Itoa(len(a.apis))+":"+strconv.Itoa(a.requestGen), len(a.apis), func(i int) fuzzyListItem {
				return fuzzyListItem{label: a.apis[i].title()}
			}, false)
			for _, hit := range hits {
				d := a.apis[hit.index]
				uikit.SidebarItem(c, "contract:"+d.spec.ID, apiIcon, d.title(), a.sidebarChoice == "contract:"+d.spec.ID, !a.windowBehind).Label(locales.ApiTitle(d.title())).Children(func() {
					if d.dirty() {
						ui.Box(c).Size(5, 5).Radius(uikit.RadiusControl).Background(t.Accent).Label(locales.UnsavedApi())
					}
				})
			}
		})
	}).Grow(1).MinHeight(0)
}

// sessionsRows are the sessions kept beyond live traffic, each opening the
// requests it holds, and the way to open a session file. They are rows of the
// traffic region's list, so the keys move through them with the rest.
func (a *app) sessionsRows(c *ui.Context, t *ui.Theme) {
	for _, s := range a.sessions.items {
		id := "session:" + s.key
		uikit.SidebarItem(c, id, clockIcon, s.data.Title, a.sidebarChoice == id, !a.windowBehind).
			PopoverMenu(func(m *ui.Menu) {
				actionMenu(m, []action{do("session.delete", locales.DeleteSession()+"…", func() { a.confirmDeleteSession(s) }).in("danger")})
			}).Children(func() {
			uikit.Meta(c, fmt.Sprint(s.count)).FontFeatures("tnum")
		})
	}
	if row := uikit.SidebarButton(c, locales.OpenSession(), locales.OpenHistoricalSessionFilesWithout(), clockIcon, false).TextColor(t.TextMuted); row.Clicked() {
		a.openSession()
	}
}

// trafficRows are the scopes the traffic list narrows to, each with what it
// counts.
func (a *app) trafficRows(c *ui.Context) {
	rows := []struct {
		id    string
		icon  *ui.SVG
		label string
		count int
	}{
		{"all", trafficIcon, locales.AllRequests(), len(a.flows)},
		{"errors", errorIcon, locales.Errors(), a.tally.errors},
		{"slow", captureIcon, locales.Slow(), a.tally.slow},
		{"pinned", pinIcon, locales.Pinned(), a.tally.pinned},
		{"overview", overviewIcon, locales.Overview(), 0},
	}
	for _, row := range rows {
		uikit.SidebarItem(c, row.id, row.icon, row.label, a.sidebarChoice == row.id, !a.windowBehind).Children(func() {
			// A count of nothing says nothing: the row is its name.
			if row.count > 0 {
				uikit.Meta(c, fmt.Sprint(row.count)).FontFeatures("tnum")
			}
		})
	}
}

// sourcesSwitch picks which of the capture's two lists Sources shows.
func (a *app) sourcesSwitch(c *ui.Context, t *ui.Theme) {
	mode := sourcesApp
	if !a.sourcesApps {
		mode = sourcesHost
	}
	uikit.Segmented(c, &mode, locales.Apps(), locales.Domains()).Height(t.Space(uikit.SpaceXXL))
	a.sourcesApps = mode == sourcesApp
}

// sources lists the apps or the hosts of the capture, the busiest first, with
// one row to show the rest: the switch in the heading chooses which list.
func (a *app) sourcesRows(c *ui.Context, t *ui.Theme) {
	apps := a.sourcesApps
	rows := a.domains
	if apps {
		rows = a.clients
	}
	if len(rows) == 0 {
		uikit.Hint(c, locales.AppsAndDomainsShowHere()).LineHeight(uikit.LineMeta).
			Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL))
		return
	}
	shown := rows
	if !a.sourcesAll {
		shown = shown[:min(len(shown), sourcesShown)]
	}
	for _, d := range shown {
		if apps {
			a.clientRow(c, d)
		} else {
			a.hostRow(c, d)
		}
	}
	if len(rows) > sourcesShown {
		a.sourcesAllRow(c, t, apps)
	}
}

// hostRow is one host of the capture: a click narrows the traffic to it, and
// its menu makes a collection, a recording or a DNS mapping from it.
func (a *app) hostRow(c *ui.Context, d domain) {
	uikit.SidebarItemLetter(c, "host:"+d.name, d.name, a.sidebarChoice == "host:"+d.name, !a.windowBehind).PopoverMenu(func(m *ui.Menu) {
		actionMenu(m, []action{
			do("collection.from-host", locales.CreateCollection(), func() { a.openCollectionWizard(a.collectionScope(d.name, "")) }).in("edit"),
			do("collection.record-host", locales.RecordIntoCollection(), func() { a.openCollectionRecording("", d.name, "") }).in("edit"),
			do("dns.map-host", locales.MapThisHost(), func() { a.openDNSRule(hostOnly(d.name), "") }).in("edit"),
		})
	}).Children(func() {
		if a.filterTriage.Host == textToken(d.name) {
			uikit.Tip(c, uikit.Caption(c, "✓").TextColor(c.Theme().Accent), locales.HostFilterIsActiveRemove())
		}
		uikit.Meta(c, fmt.Sprint(d.n)).FontFeatures("tnum")
	})
}

// firstAppPath is the executable the icon comes from: the first app the
// capture named that still has a path, so a helper with no path of its own
// does not hide the host app behind it.
func firstAppPath(apps []capture.SSLApp) string {
	for _, app := range apps {
		if app.Path != "" {
			return app.Path
		}
	}
	return ""
}

// clientRow is one app of the capture, with the marks that say whether the
// traffic is narrowed to it and whether its executables are inspected.
func (a *app) clientRow(c *ui.Context, d domain) {
	t := c.Theme()
	apps := a.capturedApps(d.name)
	id, active := "client:"+d.name, a.sidebarChoice == "client:"+d.name
	// An app has its own icon: the row shows it where a host has its dot.
	row := ui.Element{}
	if len(apps) > 0 {
		if src, ok := a.clientIcon(d.name, firstAppPath(apps)); ok {
			row = uikit.SidebarItemImage(c, id, src, d.name, active, !a.windowBehind)
		}
	}
	if !row.Valid() {
		row = uikit.SidebarItem(c, id, nil, d.name, active, !a.windowBehind)
	}
	row.PopoverMenu(func(m *ui.Menu) { actionMenu(m, a.clientActions(d.name, apps)) }).
		Children(func() {
			if a.filterTriage.Client == textToken(d.name) {
				uikit.Tip(c, uikit.Caption(c, "✓").TextColor(t.Accent), locales.AppFilterIsActiveRemove())
			}
			if slices.ContainsFunc(apps, func(app capture.SSLApp) bool { return a.appInspectionEnabled(app.Path) }) {
				uikit.Tip(c, ui.Icon(c, lockIcon).Size(uikit.IconInline, uikit.IconInline), locales.AnExactExecutableRuleIs())
			}
			uikit.Meta(c, fmt.Sprint(d.n)).FontFeatures("tnum")
		})
}

// sourcesAllRow opens the rest of the Sources list, or puts it away again.
func (a *app) sourcesAllRow(c *ui.Context, t *ui.Theme, apps bool) {
	label, icon := locales.ShowAllDomains(), statusMoreIcon
	if apps {
		label = locales.ShowAllApps()
	}
	if a.sourcesAll {
		label, icon = locales.ShowFewer(), chevronIcon
	}
	row := uikit.SidebarButton(c, label, label, icon, false).TextColor(t.TextMuted)
	if row.Clicked() {
		a.sourcesAll = !a.sourcesAll
	}
}

// sidebarFooter is fixed under the middle: the setup of a client, the HTTPS rules
// and the connection's diagnostics. They open a window or a page rather than
// choose a place in the list above, so they carry no selection.
func (a *app) sidebarFooter(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Shrink(0).Padding(t.Space(uikit.SpaceXS), 0).Gap(0).Children(func() {
		if uikit.SidebarButton(c, locales.ClientSetup(), locales.CopyAProcessScopedProxy(), monitorIcon, false).Clicked() {
			a.openClientSetup()
		}
		if uikit.SidebarButton(c, locales.HttpsDecryption(), locales.HttpsInspectionRules(), lockIcon, false).
			Label(locales.ManageHttpsRules()).Clicked() {
			a.sslOpen = true
		}
		if uikit.SidebarButton(c, locales.Diagnostics(), locales.ConnectionDiagnostics(), diagnosticsIcon, a.page == pageDiagnostics).Clicked() {
			a.goTo("diagnostics")
		}
	})
}

// clientActions are a sidebar app's: HTTPS inspection of its executables
// and mapping hosts for it.
func (a *app) clientActions(name string, apps []capture.SSLApp) []action {
	var actions []action
	if len(apps) == 1 {
		actions = a.appInspectionActions(apps[0])
	} else {
		var executables []action
		if len(apps) == 0 {
			executables = append(executables, note(locales.LocalExecutableIdentityUnavailable()))
		}
		for _, app := range apps {
			executables = append(executables, submenu(app.Path, a.appInspectionActions(app)...))
		}
		actions = append(actions, submenu(locales.HttpsInspectionByExecutable(), executables...))
	}
	return append(append([]action{
		do("https.rules", locales.ViewHttpsRules(), func() { a.sslOpen = true }).in("open"),
	}, actions...),
		do("collection.from-app", locales.CreateCollection(), func() { a.openCollectionWizard(a.collectionScope("", name)) }).in("edit"),
		do("collection.record-app", locales.RecordIntoCollection(), func() { a.openCollectionRecording("", "", name) }).in("edit"),
		do("dns.map-client", locales.MapHostFor(name), func() { a.openDNSRule("", name) }).in("edit"),
	)
}

func (a *app) collectionsMenu(m *ui.Menu) { actionMenu(m, a.collectionsActions()) }

// collectionsActions are the Collections header's: importing, the tools that
// run requests, API contracts, and the stores requests draw on.
func (a *app) collectionsActions() []action {
	return []action{
		do("collection.import", locales.ImportText(), a.importRequests),
		do("collection.import-url", locales.ImportFromUrl(), a.openImportURL),
		do("collection.import-folder", locales.ImportCollectionFolder(), a.importCollectionFolder),
		do("request.import-curl", locales.ImportCurl(), a.openCurlImport),
		do("collection.record", locales.RecordIntoCollection(), func() { a.openCollectionRecording("", "", "") }).in("run"),
		do("environment.manage", locales.Environments(), a.openVars).in("run"),
		do("history.show", locales.RunsPage(), a.showRunRecords).in("run"),
		do("runner.open", locales.Runner(), a.openRunner).in("run"),
		do("api.new", locales.NewApiContract(), func() {
			a.newAPI()
			a.page = pageAPI
		}).in("api"),
		do("api.import-openapi", locales.ImportOpenapi(), a.importOpenAPI).in("api"),
		do("api.generate", locales.GenerateApiFromCollection(), a.openAPIGeneration).
			when(len(a.requests) > 0, locales.SaveARequestToA()).in("api"),
		do("vault.open", locales.Vault(), a.openVault).in("stores"),
		do("cookies.open", locales.Cookies(), a.openCookies).in("stores"),
	}
}

// showRunRecords shows every run record in the content area.
func (a *app) showRunRecords() {
	a.experiments.scope, a.experiments.draftKey = nil, ""
	a.experiments.assetID, a.experiments.status, a.experiments.sourceFilter = "", "", ""
	a.page = pageRuns
}

// captureCard is the rich tip of a captured row: the request line as its
// title, what came back as one line of meta, and the full address and the
// client below it. The table's columns cut a long URL; the card does not.
func captureCard(f flow) uikit.HoverCard {
	title := f.method + " " + cmp.Or(f.path, "/")
	if f.kind == "connect" {
		title = "CONNECT " + f.host
	}
	var meta []string
	switch {
	case f.conn.error != "":
		meta = append(meta, locales.Failed())
	case f.kind == "connect" && f.status > 0:
		meta = append(meta, locales.Tunnel())
	case f.status > 0:
		meta = append(meta, fmt.Sprint(f.status))
	}
	if f.millis > 0 {
		meta = append(meta, fmt.Sprintf("%d ms", f.millis))
	}
	if f.bytes > 0 {
		meta = append(meta, humanBytes(f.bytes))
	}
	rows := []uikit.HoverCardRow{{Key: "URL", Value: f.url}}
	if f.host != "" {
		rows = append(rows, uikit.HoverCardRow{Key: "Host", Value: f.host})
	}
	if f.process != "" {
		rows = append(rows, uikit.HoverCardRow{Key: locales.Client(), Value: f.process})
	}
	if f.conn.error != "" {
		rows = append(rows, uikit.HoverCardRow{Key: "Error", Value: f.conn.errorStage + ": " + f.conn.error})
	}
	return uikit.HoverCard{Title: title, Meta: strings.Join(meta, " · "), Rows: rows}
}

func (a *app) table(c *ui.Context) {
	t := c.Theme()
	if len(a.visible) == 0 {
		if len(a.flows) > 0 || a.searchProblem != "" {
			note, failure := a.views.status()
			title := locales.NoMatchingRequests()
			hint := locales.HiddenByFilters(len(a.flows))
			if note != "" {
				title, hint = locales.SearchingBodiesTitle(), locales.ResultsNotFinal()
				if failure {
					title, hint = locales.NoMatchesSearchable(), locales.SearchSomeBodiesIncomplete()
				}
			}
			if a.searchProblem != "" {
				title, hint = locales.InvalidSearchPattern(), a.searchProblem
			}
			// The conditions are chips in the field above; the empty state
			// says how many rows they hide and offers the two ways out, and
			// nothing about how conditions combine.
			uikit.EmptyState(c, title, hint, func() {
				if note != "" {
					color := t.TextMuted
					if failure {
						color = t.Warning
					}
					uikit.Tip(c, uikit.Body(c, note).TextColor(color), a.views.statusDetail())
				}
				if uikit.SecondaryButton(c, locales.EditFilters()).Clicked() {
					a.openFilter()
				}
				if uikit.SecondaryButton(c, locales.ShowAllRequests()).Clicked() {
					a.views.resetFilters(a)
				}
			})
			return
		}
		running := a.srv != nil && a.srv.Running()
		title, hint := locales.CaptureIsStopped(), ""
		if running {
			title, hint = locales.ConnectClientToCapture(), locales.ListeningNoRequests(a.srv.Addr())
		}
		// One sentence and at most two buttons. Setting up a client is the
		// first thing a capture needs, so it is here as well as in the
		// sidebar's footer; a stopped listener is the other way forward.
		uikit.EmptyState(c, title, hint, func() {
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().Justify(ui.Center).Children(func() {
				if !running && uikit.PrimaryButton(c, locales.StartCapture()).Disabled(a.srv == nil).Clicked() {
					// The listener runs until it is closed, as it does from
					// the launch: never on the thread that draws.
					go guarded(func() { a.startCapture() })
				}
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.SetUpAClient()), locales.CopyAProcessScopedProxy()).Clicked() {
					a.openClientSetup()
				}
				if running && uikit.SecondaryButton(c, locales.DiagnoseCapture()).Clicked() {
					a.openDiagnostics(nil, diagnoseMac)
				}
			})
		})
		return
	}
	cols := []ui.TableColumn{
		// The number names the row, so it keeps the left edge: the marker
		// for a chosen row sits on it and no column can take its place.
		{ID: "id", Title: "#", Width: 48, Fixed: true, Sortable: true},
		{ID: "ssl", Title: locales.Ssl(), Width: 40},
		{ID: "status", Title: locales.Status(), Width: 78, Sortable: true},
		{ID: "method", Title: locales.Method(), Width: 76, Sortable: true},
		{ID: "host", Title: locales.Host(), Width: 160, Sortable: true},
		{ID: "remote", Title: locales.RemoteIp(), Width: 124, Sortable: true},
		// Keep the URL identity and sorting so saved column layouts survive.
		{ID: "url", Title: locales.Path(), MinWidth: 160, Sortable: true},
		{ID: "client", Title: locales.Client(), Width: 132, Sortable: true},
		{ID: "duration", Title: locales.Duration(), Width: 112, Align: ui.End, Sortable: true},
		{ID: "latency", Title: locales.Latency(), Width: 84, Align: ui.End, Sortable: true},
		{ID: "reqtime", Title: locales.RequestTime(), Width: 100, Align: ui.End, Sortable: true},
		{ID: "restime", Title: locales.ResponseTime(), Width: 112, Align: ui.End, Sortable: true},
		{ID: "size", Title: locales.Size(), Width: 78, Align: ui.End, Sortable: true},
		{ID: "ressize", Title: locales.SizeDecoded(), Width: 130, Align: ui.End, Sortable: true},
		{ID: "reqwire", Title: locales.RequestSize(), Width: 110, Align: ui.End, Sortable: true},
		{ID: "reqsize", Title: locales.RequestSizeDecoded(), Width: 170, Align: ui.End, Sortable: true},
		{ID: "comment", Title: locales.Comment(), Width: 160, Sortable: true},
		{ID: "tools", Title: "", Width: 30},
		{ID: "captured", Title: locales.Time(), Width: 84, Sortable: true},
	}
	// A wider text needs wider columns: the widths above are the ones the
	// usual size needs, and everything that fits or does not fit is measured in
	// the same units as the text.
	for i := range cols {
		if cols[i].Width > 0 {
			cols[i].Width = uikit.Scaled(cols[i].Width)
		}
		if cols[i].MinWidth > 0 {
			cols[i].MinWidth = uikit.Scaled(cols[i].MinWidth)
		}
	}
	width, _ := c.Size()
	available := width
	fitAt := func(dips float32) bool { return available >= uikit.Scaled(dips) }
	if !a.workbench.sidebarHidden {
		available -= a.sidebarWidth + 6
	}
	if a.workbench.sideBySide {
		available = available * a.workbench.sidePercent / 100
	}
	// A narrow list keeps what identifies a request and what it returned;
	// the columns that only repeat the inspector's numbers give way.
	if !fitAt(900) {
		for i := range cols {
			switch cols[i].ID {
			case "host":
				cols[i].Width = uikit.Scaled(132)
			case "client":
				cols[i].Width = uikit.Scaled(100)
			}
		}
		// A column the user put in stays, whatever the width.
		cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool {
			return slices.Contains(narrowColumns, col.ID) && !slices.Contains(a.shownColumns, col.ID)
		})
	}
	// The address a request reached is the inspector's to report as well, so
	// it gives way before the duration is cut off at the list's edge; the
	// client follows it when even that does not fit.
	if !fitAt(860) {
		cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool { return col.ID == "remote" })
	}
	if !fitAt(740) {
		cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool { return col.ID == "client" })
	}
	for _, cc := range a.customColumns {
		width := float32(150)
		if cc.derived() {
			width = 240
		}
		cols = append(cols, ui.TableColumn{ID: cc.id(), Title: cc.title(), Width: width, Sortable: true})
	}
	cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool { return !a.columnShown(col.ID) })
	// One column takes the room the others leave, so the rows end at the
	// list's edge whatever is shown: the address does, and when it is hidden
	// the last column that can give does.
	if !slices.ContainsFunc(cols, func(col ui.TableColumn) bool { return col.Width == 0 && !col.Fixed }) {
		for i := len(cols) - 1; i >= 0; i-- {
			if !cols[i].Fixed {
				cols[i].Width = 0
				break
			}
		}
	}
	remoteShown := slices.ContainsFunc(cols, func(col ui.TableColumn) bool { return col.ID == "remote" })
	cols = append(cols, ui.TableColumn{Title: "", ID: "filler", Width: 14, Fixed: true})
	state := &a.views.activeView().list
	state.FollowEnd = true
	state.Selected = &a.picked
	state.Selection = &a.chosen
	state.Key = func(row int) any { return a.flows[a.visible[row]].id }
	state.Sort = &a.sort
	// The table owns the live layout while it is on screen: the app's copy is
	// only what it was given, or what it changed from its own menus.
	if a.columnsDirty || len(state.Columns.Order) == 0 && len(state.Columns.Widths) == 0 {
		state.Columns = a.columns
		a.columnsDirty = false
	}
	state.HeaderMenu = func(m *ui.Menu, col int) {
		id := ""
		if col >= 0 {
			id = cols[col].ID
		}
		a.columnsMenu(m, id)
	}
	state.HeaderCell = func(cell ui.Element, col int) {
		cell.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, t.Border, 0)
		})
	}
	focused := state.Handle.Focused(c)
	selectionTheme := uikit.ListTheme(t, focused)
	// Table's built-in row minimum is eight spacing units and its cells pad
	// 1.5 units around 15px lines. Scope the 3px unit here only: 24px rows
	// without shrinking the rest of the application. Rows have no hover, as
	// in a macOS table. The headers take the data's 12px, so their
	// semibold labels stay quieter than the rows they name.
	selectionTheme.Spacing = 3
	selectionTheme.SurfaceHover = ui.Color{}
	// No rules inside the list: the header's columns are told apart by the
	// grip under the pointer, not by a line at rest. The header keeps its
	// own edge below it, drawn per cell.
	selectionTheme.Border = ui.Color{}
	c.SetTheme(selectionTheme)
	// Bare: the kit has no table; this is MyGo's table of flows, with its own cell edges.
	table := ui.Table(c, state, cols, len(a.visible), func(row, col int) {
		f := a.flows[a.visible[row]]
		if col == 0 && a.chosen.Has(f.id) {
			// The marker sits on the row's first visible cell, whichever
			// column the user moved there.
			uikit.RowMarker(c, focused)
		}
		text := func(s string) ui.Element {
			return uikit.Mono(c, s).Height(15).FixedLineHeight(15).FontFeatures("tnum").SingleLine()
		}

		// Facts beside the request: its number, host, client, timing and size.
		quiet := func(s string) ui.Element {
			return uikit.Mono(c, s).TextColor(c.Theme().TextMuted).Height(15).FixedLineHeight(15).FontFeatures("tnum").SingleLine()
		}
		// What a person reads as a name rather than as wire data: the host,
		// the client, the path and a note keep the interface font, as the
		// sidebar does. Mono is for the numbers a reader compares down a
		// column, and for code.
		plain := func(s string) ui.Element {
			return uikit.Hint(c, s).Height(15).FixedLineHeight(15).SingleLine()
		}
		named := func(s string) ui.Element {
			return uikit.Small(c, s).Height(15).FixedLineHeight(15).SingleLine()
		}
		switch cols[col].ID {
		case "id":
			quiet(fmt.Sprint(f.id))
		case "ssl":
			if !f.secure() {
				quiet("—")
				return
			}
			uikit.Tip(c, ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center), securityNote(f)).Children(func() {
				ui.Icon(c, lockIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Text)
			})
		case "method":
			// The verb is a name here, not a value: the interface font, in
			// the method's own ink. CONNECT opens a tunnel rather than
			// asking for something, so it stays quiet.
			ink := uikit.MethodColor(f.method, t)
			if f.method == "CONNECT" {
				ink = t.TextMuted
			}
			uikit.CardTip(c, uikit.Small(c, f.method).FontWeight(500).Height(15).FixedLineHeight(15).
				TextColor(ink).SingleLine(), captureCard(f)).PopoverMenu(a.rowMenu(c, f))
		case "host":
			uikit.CardTip(c, plain(f.host), captureCard(f)).PopoverMenu(a.rowMenu(c, f))
		case "remote":
			a.remoteCell(c, t, f, quiet)
		case "url":
			if a.edited(f.id) {
				uikit.Tip(c, ui.Box(c).Size(6, 6).Radius(3).Background(uikit.Colors(t).Primary).MarginEnd(t.Space(uikit.SpaceS)).
					Label(locales.Edited()), locales.EditedInTheInspectorThe())
			}
			if f.pinned {
				uikit.Glyph(c, pinIcon).MarginEnd(t.Space(uikit.SpaceXS))
			}
			if f.color != "" {
				if dot, ok := highlightColor(f.color); ok {
					ui.Box(c).Size(8, 8).Radius(uikit.RadiusControl).Background(dot).MarginEnd(t.Space(uikit.SpaceXS))
				}
			}
			rest := t.Text
			ui.Row(c).Gap(t.Space(uikit.SpaceHair)).Children(func() {
				if f.kind == "connect" {
					// Nothing to read: the request line of a tunnel is its
					// host, and the Status column says it is a tunnel.
					return
				}
				if f.note != "" {
					uikit.Tip(c, uikit.Glyph(c, noteIcon).MarginEnd(t.Space(uikit.SpaceXS)), f.note)
				}
				if f.dns != "" && !remoteShown {
					// Without the Remote IP column, the path says a Host
					// profile sent the request elsewhere.
					accent := uikit.Colors(t).Primary
					uikit.Tip(c, ui.Box(c).Height(15).Padding(0, t.Space(uikit.SpaceXS)).Radius(uikit.RadiusControl).Background(accent.Alpha(0.12)).Center().MarginEnd(t.Space(uikit.SpaceXS)).
						Label(locales.MappedBy(hostProfileLabel(f.hostProfile))), mappedBy(f)).Children(func() {
						uikit.Caption(c, locales.Hosts()).FontWeight(500).TextColor(accent)
					})
				}
				label := named(cmp.Or(f.path, "/")).Label(f.host + f.path).TextColor(rest)
				if f.strike {
					label = label.Strikethrough()
				}
				uikit.CardTip(c, label, captureCard(f)).PopoverMenu(a.rowMenu(c, f))
			})
		case "status":
			if f.conn.error != "" {
				uikit.CardTip(c, text(locales.Failed()).TextColor(t.Danger), captureCard(f)).PopoverMenu(a.rowMenu(c, f))
			} else if f.kind == "connect" && f.status < 400 {
				// A tunnel that opened has no status of its own: the lock
				// says what the row is, and the card says how the setup
				// went. One that never opened keeps its number, so a refused
				// tunnel does not read as a working one.
				uikit.CardTip(c, ui.Row(c).Grow(1).AlignItems(ui.Center).Children(func() {
					uikit.Glyph(c, lockIcon)
				}), captureCard(f)).Label(locales.TunnelTooltip(f.status)).PopoverMenu(a.rowMenu(c, f))
			} else {
				// Only a client or server error is colored: a normal answer is
				// its number in the secondary ink, with nothing beside it.
				ink := t.TextMuted
				if f.status >= 400 {
					ink = uikit.StatusColor(f.status, t)
				}
				uikit.CardTip(c, text(fmt.Sprint(f.status)).TextColor(ink), captureCard(f)).PopoverMenu(a.rowMenu(c, f))
			}
		case "client":
			if src, ok := a.clientIcon(f.process, f.processPath); ok {
				ui.Image(c, src).Size(uikit.IconInline, uikit.IconInline)
			} else {
				clientBadge(c, f.process)
			}
			plain(f.process).Padding(0, 0, 0, t.Space(uikit.SpaceM)).PopoverMenu(a.rowMenu(c, f))
		case "duration":
			durationCell(c, t, f.millis, quiet)
		case "latency":
			timeCell(quiet, f.latencyMillis)
		case "reqtime":
			timeCell(quiet, f.requestMillis)
		case "restime":
			timeCell(quiet, f.responseMillis)
		case "size":
			quiet(humanBytes(f.bytes))
		case "ressize":
			quiet(humanBytes(f.resSize))
		case "reqwire":
			quiet(humanBytes(f.wireSize()))
		case "reqsize":
			quiet(humanBytes(f.reqSize))
		case "comment":
			if f.note == "" {
				quiet("—")
				return
			}
			uikit.Tip(c, plain(f.note).TextColor(t.Text), f.note)
		case "tools":
			if len(f.actions) == 0 {
				quiet("—")
				return
			}
			uikit.Tip(c, ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center), strings.Join(f.actions, "\n")).Children(func() {
				uikit.Glyph(c, toolsIcon)
			})
		case "captured":
			at := "—"
			if !f.at.IsZero() {
				at = f.at.Local().Format("15:04:05")
			}
			quiet(at)
		default:
			cc, ok := a.customColumnAt(cols[col].ID)
			if !ok {
				return
			}
			if cc.derived() {
				if cell := f.columnData[cc.id()]; cell != nil {
					if cell.pending {
						uikit.Tip(c, quiet("…"), locales.ReadingRetainedBody())
						return
					}
					if cell.problem != "" {
						uikit.Tip(c, quiet(locales.Unavailable()), cell.problem)
						return
					}
				}
			}
			value := a.columnValue(cc, f)
			if value == "" {
				quiet("—")
				return
			}
			cell := uikit.Tip(c, text(value).TextColor(t.Text), value)
			if cc.header() {
				cell.PopoverMenu(a.headerFilterMenu(c, cc.Name, []string{value}))
			}
		}
	}).Label(locales.CapturedRequests()).Grow(1)
	c.SetTheme(t)
	if table.Submitted() {
		a.workbench.inspector.Focus(c)
	}
	// Dragging a column, reordering one or sorting the table is the table's
	// change, not the app's: it is copied back then, not every frame.
	if table.Changed() {
		a.columns = state.Columns
	}
}

var noteIcon = lucide.MessageSquare()

// timeCell shows a measured phase, or a dash when the capture has none.
func timeCell(number func(string) ui.Element, phase func() (int, bool)) {
	millis, ok := phase()
	if !ok {
		number("—")
		return
	}
	number(fmt.Sprintf("%d ms", millis))
}

// millisOrZero is a phase's duration for sorting, zero when it has none.
func millisOrZero(phase func() (int, bool)) int {
	millis, _ := phase()
	return millis
}

// statusNote is what a row's status says: its code, or why it has none.
func statusNote(f flow) string {
	switch {
	case f.conn.error != "":
		return f.conn.errorStage + ": " + f.conn.error
	case f.kind == "connect":
		return locales.TunnelTooltip(f.status)
	}
	return fmt.Sprint(f.status)
}

// secureRank sorts the SSL column: plaintext first, then TLS.
func secureRank(f flow) int {
	if f.secure() {
		return 1
	}
	return 0
}

// securityNote says how a row was encrypted, for the SSL column's tooltip.
func securityNote(f flow) string {
	if !f.secure() {
		return locales.NotEncrypted()
	}
	parts := []string{locales.Encrypted()}
	for _, detail := range []string{f.conn.tls, f.conn.cipher, f.conn.certCN} {
		if detail != "" {
			parts = append(parts, detail)
		}
	}
	return strings.Join(parts, " · ")
}

// remoteCell is the address a request reached; one a Host profile mapped
// is in the accent color, and says which profile and line did it.
func (a *app) remoteCell(c *ui.Context, t *ui.Theme, f flow, quiet func(string) ui.Element) {
	if f.dns == "" {
		quiet(cmp.Or(f.conn.remote, "—")).PopoverMenu(a.rowMenu(c, f))
		return
	}
	accent := uikit.Colors(t).Primary
	ip := f.conn.remote
	if _, mapped, ok := strings.Cut(f.dns, " → "); ok && ip == "" {
		ip = mapped
	}
	uikit.Tip(c, ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center), mappedBy(f)).Label(locales.MappedBy(hostProfileLabel(f.hostProfile))).Children(func() {
		quiet(ip).TextColor(accent).PopoverMenu(a.rowMenu(c, f))
		ui.Box(c).Size(5, 5).Radius(uikit.RadiusControl).Background(accent)
	})
}

// mappedBy says which Host profile sent a request elsewhere.
func mappedBy(f flow) string {
	if f.hostProfile == "" {
		return f.dns
	}
	return f.dns + " · " + hostProfileLabel(f.hostProfile)
}

// slowMillis is where a request's duration turns amber in the table.
const slowMillis = 1000

// durationCell lays a bar behind the number, a row's height less a
// little, that always covers the number and reaches further left by the
// logarithm of the time up to a minute: a column of numbers becomes a
// column of lengths without a column of its own, and no edge cuts through
// a digit. A slow request fills in amber: slow is not broken, so it keeps
// out of the color a failure uses.
func durationCell(c *ui.Context, t *ui.Theme, millis int, number func(string) ui.Element) {
	// A normal row is its numbers and nothing else; only a slow exchange
	// carries the signal color.
	cell := number(fmt.Sprintf("%d ms", millis))
	if millis >= slowMillis {
		cell.TextColor(t.Warning)
	}
}

// highlightColor is the color a flow is marked with.
func highlightColor(name string) (ui.Color, bool) {
	for _, c := range uikit.MarkColors {
		if c.Name == name {
			return c.Color, true
		}
	}
	return ui.Color{}, false
}

// openMark fills the mark dialog from a flow.
func (a *app) openMark(f flow) {
	a.markID = f.id
	a.markColor = f.color
	a.markNote = f.note
	a.markStrike = f.strike
	a.markOpen = true
}

// markModal marks a flow with a color and a note, the two things a person adds
// to traffic while looking for the request that misbehaves.
func (a *app) markModal(c *ui.Context, t *ui.Theme) {
	if !a.markOpen {
		return
	}
	uikit.Modal(c, &a.markOpen, locales.MarkTheRequest(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.MarkTheRequest())
			ui.Scroll(c).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Meta(c, locales.AColorShowsInThe())
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
					for _, color := range uikit.MarkColors {
						selected := a.markColor == color.Name
						uikit.Tip(c, uikit.SwatchButton(c, locales.MarkSwatch(color.Name), color.Color, selected), color.Name).
							OnClick(func() { a.markColor = color.Name })
					}
					if uikit.SecondaryButton(c, locales.None()).Clicked() {
						a.markColor = ""
					}
				})
				uikit.Checkbox(c, &a.markStrike, locales.Strikethrough())
				uikit.TextArea(c, &a.markNote).Height(90).Label(locales.Note()).Radius(uikit.RadiusControl)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.markOpen = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Clicked() {
					a.saveMark()
				}
			})
		})
	})
}

// saveMark writes the dialog's color and note onto the flow.
func (a *app) saveMark() {
	for i := range a.flows {
		if a.flows[i].id == a.markID {
			a.count(&a.flows[i], -1) // the note counts toward memory
			a.flows[i].color = a.markColor
			a.flows[i].note = strings.TrimSpace(a.markNote)
			a.flows[i].strike = a.markStrike
			a.count(&a.flows[i], 1)
			break
		}
	}
	a.markOpen = false
	a.applyFilter()
	a.invalidate()
	a.done(locales.Marked(

	// infoIcon marks a header the Headers tab can explain, or a connection detail
	// worth opening.
	))
}

var infoIcon = lucide.Info()

// connectionView makes transport failures inspectable and copyable, including
// failures after a successful CONNECT but before any HTTP request was received.
func (a *app) connectionView(c *ui.Context, f flow) {
	t := c.Theme()
	ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
		if f.conn.error != "" {
			uikit.Body(c, locales.StageFailed(cmp.Or(f.conn.errorStage, locales.Connection()))).TextColor(t.Danger).Grow(1)
		} else {
			uikit.Hint(c, locales.ConnectionLogSnapshotAtCapture()).Grow(1)
		}
		if uikit.SecondaryButton(c, locales.CopyConnectionLog()).Disabled(f.conn.log == "").Clicked() {
			c.Services().WriteClipboard(f.conn.log)
			a.done(locales.CopiedConnectionLog())
		}
	})
	if f.conn.error != "" {
		uikit.Tip(c, uikit.ErrorText(c, f.conn.error).Selectable().MaxLines(3), f.conn.error).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM))
	}
	if f.kind == "connect" {
		// What decides whether the next connection is decrypted: the tunnel's
		// own tab says what this one was, not what today's rules would do.
		trust, trustColor := a.certificateStatus(t)
		domain := locales.OffFor(hostOnly(f.host))
		if a.inspectionConfig().Decrypts(f.host) {
			domain = locales.OnFor(hostOnly(f.host))
		}
		app := locales.NoLocalExecutableWasRecorded()
		if filepath.IsAbs(f.processPath) {
			app = locales.OffFor(cmp.Or(f.process, filepath.Base(f.processPath)))
			if a.appInspectionEnabled(f.processPath) {
				app = locales.OnFor(cmp.Or(f.process, filepath.Base(f.processPath)))
			}
		}
		ui.Column(c).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM)).Children(func() {
			field(c, t, locales.Decryption(), domain)
			field(c, t, locales.AppRule(), app)
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).Padding(t.Space(uikit.SpaceHair), 0).Children(func() {
				uikit.Meta(c, locales.Trust()).Width(96)
				uikit.DataBody(c, trust).TextColor(trustColor).Grow(1).
					Tooltip(locales.SystemTrustIsNotProof())
			})
		})
	}
	text := cmp.Or(f.conn.log, "No connection history was recorded for this exchange (for example, an older or imported session).")
	uikit.TextArea(c, &text).ReadOnly(true).Label(locales.ConnectionLog()).Font(uikit.FontMono).Grow(1).FillWidth()
}

// connPanel is the connection behind a flow: who talked to the proxy, where it
// dialled, the addresses the name resolved to, and what TLS agreed on.
func (a *app) connPanel(c *ui.Context, t *ui.Theme, f flow) {
	cn := f.conn
	ui.Column(c).Width(440).Gap(t.Space(uikit.SpaceHair)).Padding(t.Space(uikit.SpaceXS)).Children(func() {
		uikit.BodyStrong(c, locales.Connection()).TextColor(t.Text).Padding(0, 0, t.Space(uikit.SpaceM), 0)
		field(c, t, locales.HttpVersion(), cn.proto)
		field(c, t, locales.Client(), cn.client)
		field(c, t, "Proxy", cn.proxy)
		field(c, t, "Upstream", cn.server)
		field(c, t, locales.RemoteIp(), cn.remote)
		if f.dns != "" {
			field(c, t, locales.HostProfile(), mappedBy(f))
		}
		if cn.error != "" {
			field(c, t, locales.FailedAt(), cn.errorStage+": "+cn.error)
		}
		if cn.flows > 0 {
			field(c, t, locales.ConnectionUse(), locales.RequestReused(cn.flows, fmt.Sprint(cn.reused)))
		}
		resolved := cn.resolved
		if resolved != "" {
			resolved = locales.ResolvedInMs(resolved, cn.lookupMs)
		}
		field(c, t, "Resolved", resolved)
		tls := cn.tls
		if cn.cipher != "" {
			if tls != "" {
				tls += " · "
			}
			tls += cn.cipher
		}
		field(c, t, "TLS", tls)
		cert := cn.certCN
		if cn.issuer != "" {
			cert = locales.IssuedBy(cert, cn.issuer)
		}
		if cn.certUntil != "" {
			cert += locales.UntilTime(cn.certUntil)
		}
		field(c, t, locales.DiagCertificate(), strings.TrimPrefix(cert, ", "))
	})
}

// field is one label and value of the connection panel, left out when the flow
// has nothing to say about it.
func field(c *ui.Context, t *ui.Theme, label, value string) {
	if value == "" {
		return
	}
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).Padding(t.Space(uikit.SpaceHair), 0).Children(func() {
		uikit.Meta(c, label).Width(96)
		uikit.DataBody(c, value).TextColor(t.Text).Grow(1)
	})
}

// headersView shows the request and the response headers row by row, each
// standard one with a note on what it is for, and the status line with what
// the code means.
// headerBlock is one side of the Headers tab: the line that starts the message
// and then its headers, sorted by name.
func (a *app) headerBlock(c *ui.Context, t *ui.Theme, title, line, note string, headers http.Header, raw string, trailers http.Header) {
	ui.Column(c).FillWidth().Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Background(uikit.Colors(t).Window).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			uikit.Body(c, title).FontWeight(500)
			ui.Spacer(c)
			if len(headers) > 0 {
				count := locales.RequestHeadersCount(len(headers))
				if title == "Response" {
					count = locales.ResponseHeadersCount(len(headers))
				}
				uikit.Meta(c, count)
			}
		})
		uikit.Tip(c, uikit.Meta(c, line).Font(uikit.FontMono).SingleLine().Selectable(), strings.TrimSpace(line+"\n"+note))
	})
	ui.Divider(c)
	side := 0
	if title == "Response" {
		side = 1
	}
	ui.Scroll(c).TrackScroll(&a.headerScroll[side]).Grow(1).FillWidth().Children(func() {
		for i, block := range []http.Header{headers, trailers} {
			if i == 1 && len(block) > 0 {
				uikit.Label(c, locales.Trailers()).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL), t.Space(uikit.SpaceM)).FillWidth().Background(uikit.Colors(t).Window)
			}
			if i == 0 && len(block) == 0 {
				if raw == "" {
					uikit.Hint(c, locales.NoHeadersRecorded()).Padding(t.Space(uikit.SpaceL))
				} else {
					uikit.DataBody(c, raw).TextColor(t.Text).Selectable().Padding(t.Space(uikit.SpaceL))
				}
			}
			for _, key := range slices.Sorted(maps.Keys(block)) {
				value := strings.Join(block[key], "\n")
				ui.Row(c).Gap(t.Space(uikit.SpaceL)).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).AlignItems(ui.Start).FillWidth().Children(func() {
					uikit.Tip(c, uikit.Meta(c, key).Font(uikit.FontMono).Width(124).SingleLine(), strings.TrimSpace(key+"\n"+headerDoc(key)))
					uikit.DataBody(c, value).TextColor(t.Text).Grow(1).Selectable()
				})
				ui.Divider(c).Background(t.Border.Alpha(0.45))
			}
		}
	})
}

// messages lists the WebSocket messages or SSE events of a flow.
func (a *app) messages(c *ui.Context, f flow) {
	a.messageUI.view(c, a, f, a.frames[f.id])
}

// firstLaunch reports whether this is the first start: no settings file has
// been written yet, though the log directory above it may exist already.
func firstLaunch(settingsPath string) bool {
	_, err := os.Stat(settingsPath)
	return errors.Is(err, fs.ErrNotExist)
}

func shortenHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(path, home); ok {
			return "~" + rest
		}
	}
	return path
}

func (f flow) statusColor(t *ui.Theme) ui.Color {
	if f.status < 400 && f.failed() {
		return t.Danger
	}
	return uikit.StatusColor(f.status, t)
}

// Run starts the native desktop app and releases its resources on exit.
func Run() error {
	markStartup(markProcessStart)
	// MyGo delivers second launches on the main thread. Register before
	// taking the lock: another launch during startup must still focus this
	// instance once its window exists, rather than being lost.
	var active *app
	var focusOnReady bool
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		applog.For("app").Info("second instance handed off", "args", len(args))
		if active != nil && active.win != nil {
			active.win.Restore()
			active.win.Focus()
			return
		}
		focusOnReady = true
	})
	root, err := datadir.Root()
	if err != nil {
		return err
	}
	markStartup(markDataRootReady)
	// MyGo keys its single-instance socket by the app name. Key it by the
	// data root too, so two roots forward launches to their own window,
	// and a second launch on this root hands off before any file lock.
	// Restore the name before anything else looks at it.
	name := mygo.App.Name()
	mygo.App.SetName(name + "-" + datadir.InstanceKey(root))
	single := mygo.App.RequestSingleInstanceLock()
	mygo.App.SetName(name)
	if !single {
		return nil
	}
	// The file lock is only a guard for a crashed socket or a headless
	// runner; it must never turn a normal second launch into an error.
	lock, err := datadir.LockRoot(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	mygo.App.OnQuit(func() { _ = lock.Close() })
	if logs, err := applog.Open(filepath.Join(root, "logs")); err != nil {
		applog.For("app").Warn("log file unavailable; logging to stderr only", "err", err)
	} else {
		defer logs.Close()
	}
	markStartup(markLogsOpened)
	if saved, err := loadSettings(filepath.Join(root, "settings.json")); err == nil {
		if err := applog.SetLevel(saved.LogLevel); err != nil {
			applog.For("app").Warn("saved log level is unknown; using Info", "saved", saved.LogLevel)
		}
	}
	applog.For("app").Info("vscroxy starting", "dataDir", root, "version", version.String(), "commit", version.CommitString(), "os", runtime.GOOS, "arch", runtime.GOARCH)
	a := newApp()
	active = a
	setUnexpectedErrorHandler(a.fail)
	// Resolve the login shell environment once in the background: spawned
	// tools get it from ShellEnv, while startup never waits for a login
	// script that sources a slow profile.
	go guarded(func() { system.ShellEnv(context.Background()) })
	if a.updateCheck {
		go guarded(func() { a.checkForUpdate(false) })
	}
	// The release page link needs no lookup: compare the last run's version
	// with this one and post the notice before recording the new number.
	a.offerWhatsNew()
	markStartup(markUpdateStarted)
	if a.srv != nil {
		defer a.srv.Close()
	}
	cleanup := a.installShutdown()
	a.sleep.hold = mygoKeepAwake
	// MyGo reports resume on every desktop platform. Recheck the OS through
	// the same path the Settings panel uses, including its interrupted-session
	// recovery, rather than writing a second proxy checker.
	a.resumeWatch.start(func() {
		if a.quitting() {
			return
		}
		a.checkSystemProxy(true)
	})
	stopWatchdog := func() { a.startupLifetime.Dispose() }
	defer stopWatchdog()
	mygo.App.OnQuit(stopWatchdog) // native termination may not return from Run
	defer a.closeControl()
	mygo.App.OnQuit(a.closeControl)
	mygo.App.OnQuit(a.closeLoadRunner)
	mygo.App.OnQuit(a.stopCollectionMocks)
	defer a.closeLoadRunner()
	mygo.App.OnQuit(cleanup.flushCookies)
	defer cleanup.flushCookies()
	cleanupProxy := func() {
		if err := a.systemProxy.close(); err != nil {
			applog.For("system-proxy").Error(locales.ReleaseSystemProxy(), "err", err)
		}
	}
	defer cleanupProxy()          // before the listener closes, even if Run returns an error
	mygo.App.OnQuit(cleanupProxy) // native termination may not return from Run
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if e.SessionEnding {
			// The process may end before a goroutine reports back.
			cleanup.flushSettings()
			return
		}
		a.requestQuit(e)
	})
	mygo.App.OnQuit(cleanup.flushSettings)
	defer cleanup.flushSettings()
	mygo.App.OnQuit(cleanup.closeExperiments)
	defer cleanup.closeExperiments()
	mygo.App.OnQuit(cleanup.closeSessions)
	defer cleanup.closeSessions()
	mygo.App.OnQuit(cleanup.stopRecording)
	defer cleanup.stopRecording()
	mygo.App.OnQuit(cleanup.cancelRequests)
	defer cleanup.cancelRequests()
	mygo.App.OnQuit(func() { a.socket.session.disconnect() })
	defer func() { a.socket.session.disconnect() }()
	mygo.App.OnQuit(func() { a.sse.session.stop() })
	defer func() { a.sse.session.stop() }()
	defer a.discardRuleDraft()
	defer a.disableDevices()
	defer a.views.stopSearch()
	defer a.messageUI.stopSearch()
	defer a.protobuf.stop()
	defer a.socket.protobuf.stop()
	mygo.App.OnQuit(a.jsonQuery.job.stop)
	defer a.jsonQuery.job.stop()
	defer func() {
		if a.runner.cancel != nil {
			a.runner.cancel()
		}
	}()
	mygo.App.SetLocale(a.language)
	// A notification can be clicked after a restart, so its ID is enough to
	// find the item again. Other apps' and older notifications are ignored.
	mygo.App.OnNotificationClick(func(id string) {
		if !strings.HasPrefix(id, attentionNotificationPrefix) {
			return
		}
		target := strings.TrimPrefix(id, attentionNotificationPrefix)
		a.onMain(func() { a.openAttentionItem(target) })
	})
	a.watchLocale()
	// A run that is not a bundle has no icon of its own: the Dock (and the
	// taskbar) read it from the binary instead. Setting it costs a native
	// round trip and the first frame does not need it, so it belongs to the
	// phase that runs after the window has drawn.
	a.startup.add(startupAfterFirstFrame, markDockIconDone, func() {
		if err := mygo.App.Dock.SetIcon(iconPNG); err != nil && runtime.GOOS == "darwin" {
			applog.For("app").Warn("dock icon", "err", err)
		}
	})
	markStartup(markRunSetupDone)
	mygo.App.WhenReady(func() {
		markStartup(markAppReady)
		// The first frame arms the watchdog only in a real window; Tester
		// views in tests have no app window to watch.
		a.watchdogArmed = true
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "vscroxy",
			Width:           1280,
			Height:          800,
			MinWidth:        960,
			MinHeight:       600,
			AutoHideMenuBar: true,
			// The view draws the title bar under the window controls, so the
			// toolbar and the traffic share one continuous surface.
			TitleBarStyle: mygo.TitleBarHidden,
			// The lights (14 tall) sit on the 44px title bar's middle line.
			TrafficLightPosition: &mygo.Point{X: 16, Y: 13},
			// The sidebar shows the window's material on macOS only; the
			// view keeps it mostly opaque and draws it flat elsewhere.
			Vibrancy: sidebarVibrancy(),
			// MyGo keeps the window's bounds under this key.
			StateKey: "vscroxy",
			Content:  ui.View(a.view),
		})
		markStartup(markWindowCreated)
		a.titleWindow = a.win
		a.syncTitle()
		// A window behind others shows its selections gray.
		a.win.OnFocus(func() {
			a.win.Update(func() {
				a.windowBehind = false
				if a.attention != nil {
					a.attention.focusGained()
				}
			})
		})
		a.win.OnBlur(func() { a.win.Update(func() { a.windowBehind = true }) })
		a.win.OnFileDrop(func(e *mygo.FileDropEvent) { a.importDropped(e.Paths) })
		if focusOnReady {
			focusOnReady = false
			a.win.Restore()
			a.win.Focus()
		}
		a.startProjectWatch()
		markStartup(markProjectWatch)
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.lifecycle == nil || !a.lifecycle.ShutdownSucceeded() {
				e.PreventDefault()
				mygo.App.Quit()
			}
		})
		mygo.App.SetMenu(a.menu())
		markStartup(markMenuReady)
		a.syncNativeAppearance()
		markStartup(markAppearanceSynced)
		a.checkSystemProxy(true) // retry only a recorded, interrupted proxy session
		markStartup(markSystemProxyChecked)
		// An explicit VSCROXY_CONTROL_FILE wins over the saved Settings choice.
		path := os.Getenv("VSCROXY_CONTROL_FILE")
		if path == "" && a.localControl {
			path = a.controlPath
		}
		if path != "" {
			if err := a.startControl(path); err != nil {
				a.fail(locales.EnableLocalAutomation(), err)
			}
		}
		markStartup(markControlStarted)
		go guarded(func() { a.startCapture() })
	})
	return mygo.App.Run()
}

// clientIcon returns the app's icon, loading it the first time it is asked
// for. A process with no icon gets none, and the table shows a badge.
func (a *app) clientIcon(process, path string) (ui.ImageSource, bool) {
	if process == "" && path == "" {
		return nil, false
	}
	key := cmp.Or(path, process)
	if src, ok := a.icons[key]; ok {
		return src, src != nil
	}
	lookup := a.iconLookup
	if lookup == nil {
		lookup = systemIcon
	}
	src := loadIconWith(lookup, process, path)
	a.icons[key] = src
	return src, src != nil
}

// loadIcon decodes an app's icon: an SVG as an SVG, anything else as a
// bitmap.
func loadIcon(process, path string) ui.ImageSource {
	return loadIconWith(systemIcon, process, path)
}

// loadIconWith is loadIcon with where the desktop's icon comes from, which a
// test stands in for.
func loadIconWith(lookup func(string) ([]byte, bool), process, path string) ui.ImageSource {
	// The desktop's own icon first: the app a process belongs to, or the
	// executable itself. It is read once per path and kept, never per frame.
	if data, ok := lookup(path); ok {
		if bmp, err := ui.DecodeBitmap(data); err == nil {
			return bmp
		}
	}
	icon, ok := appicon.Lookup(process, path)
	if !ok {
		return nil
	}
	if icon.SVG {
		svg, err := ui.ParseSVG(icon.Data)
		if err != nil {
			return nil
		}
		return svg
	}
	bmp, err := ui.DecodeBitmap(icon.Data)
	if err != nil {
		return nil
	}
	return bmp
}

// systemIcon asks the desktop for the icon of the app an executable belongs to:
// its .app bundle on macOS, the executable itself elsewhere.
func systemIcon(path string) ([]byte, bool) {
	target, ok := iconTarget(path)
	if !ok {
		return nil, false
	}
	data, err := mygo.Shell.FileIcon(target, 32)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// iconTarget is what the desktop is asked to draw: the app a process belongs
// to on macOS, the executable itself elsewhere. A daemon outside every bundle
// has no icon of its own — the desktop answers with the generic black "exec"
// tile, which reads as a black square in the table — so it is asked for
// nothing and the row falls back to the app's initial.
func iconTarget(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if runtime.GOOS != "darwin" {
		return path, true
	}
	return appBundle(path)
}

// appBundle is the outermost .app folder a path sits in, if any: a helper
// inside an app shows the app's own icon, not a generic helper tile.
func appBundle(path string) (string, bool) {
	var found string
	for p := filepath.Clean(path); len(p) > 1; p = filepath.Dir(p) {
		if strings.HasSuffix(p, ".app") {
			found = p
		}
	}
	return found, found != ""
}

// clientBadge draws the app's initial for an app whose icon the system does
// not have: the same neutral block a host row uses, never a color of its own.
func clientBadge(c *ui.Context, name string) {
	label := name
	if label == "" {
		label = locales.UnknownClient()
	}
	uikit.LetterSquare(c, name, uikit.IconInline, uikit.Colors(c.Theme()).MutedForeground).Label(label)
}

func (a *app) rowMenu(c *ui.Context, f flow) func(*ui.Menu) {
	return func(m *ui.Menu) { actionMenu(m, a.flowActions(c, f)) }
}

// flowActions are what a captured request is worth once it leaves the table.
// The request's own next steps come first, then copying and finding related
// traffic, then how this row is kept, then the tools that change future
// traffic like it, as Proxyman's Tools submenu does.
func (a *app) flowActions(c *ui.Context, f flow) []action {
	services := c.Services()
	requests := a.requestActions(c, f, true)
	target := flowTarget(f)
	held := a.compare.selected
	actions := []action{
		pick(requests, "flow.replay").as(locales.Replay()).in("open"),
		pick(requests, "flow.edit").in("open"),
		do("compare.select", locales.SelectForCompare(), func() { a.selectForCompare(target) }).
			when(target.valid(), locales.SelectCapturedRequestsFirst()).in("open"),
		do("compare.with-selected", locales.CompareWithSelected(), func() { a.compareWithSelected(target) }).
			when(target.valid() && held.valid() && !held.same(target), locales.SelectFirstExchangeForCompare()).in("open"),
	}
	if held.valid() {
		actions = append(actions, do("compare.clear-selection", locales.ClearCompareSelection(), func() {
			a.compare.selected = compareTarget{}
		}).in("open"))
	}
	if f.kind == "" && f.url != "" {
		actions = append(actions, do("flow.open-browser", locales.OpenInBrowser(), func() { services.OpenURL(f.url) }).in("open"))
	}
	var formats []action
	for _, format := range requestCopyFormats {
		formats = append(formats, pick(requests, format.id))
	}
	formats = append(formats, pick(requests, "flow.copy-markdown"), pick(requests, "flow.copy-csv"),
		pick(requests, "flow.copy-raw"), pick(requests, "flow.copy-har"), pick(requests, "flow.copy-har-raw"))
	copyItems := []action{do("flow.copy-url", locales.Url(), func() {
		services.WriteClipboard(f.url)
		a.done(locales.CopiedUrl())
	})}
	copyItems = append(copyItems, copyAsMenu(formats).items...)
	copyItems = append(copyItems, do("flow.copy-response", locales.ResponseBody(), func() {
		services.WriteClipboard(f.body(true))
		a.done(locales.CopiedResponseBody())
	}).when(f.resBody != "", locales.ResponseHasNoBody()))
	// Sync the conditions on the next frame; changing visible rows while the
	// table is building would invalidate its row indices.
	filters := []action{
		do("flow.filter-host", locales.ChipHost(f.host), func() { a.filterTriage.Host = textToken(f.host); c.AnimationFrame() }).
			when(f.host != "", "").in("edit"),
		do("flow.filter-app", locales.ChipApp(cmp.Or(f.process, locales.Unidentified())), func() { a.filterTriage.Client = textToken(f.process); c.AnimationFrame() }).
			when(f.process != "", "").in("edit"),
		do("flow.filter-method", locales.MethodNamed(f.method), func() { a.filterTriage.Method = f.method; c.AnimationFrame() }).
			when(f.method != "", "").in("edit"),
	}
	// A CONNECT status acknowledges a tunnel; it is not the status of an
	// HTTP response from the destination.
	if f.kind != "connect" && f.conn.error == "" && f.status >= 100 && f.status <= 599 {
		filters = append(filters, do("flow.filter-status", locales.StatusCode(f.status), func() {
			a.filterStatus = strconv.Itoa(f.status)
			c.AnimationFrame()
		}))
	}
	tools := []action{}
	for _, kind := range ruleTypes {
		label := ruleLabel(kind) + "…"
		if kind == capture.RuleMapLocal {
			label = locales.MockThisResponse()
		}
		tools = append(tools, do("flow.rule", label, func() { a.ruleFromFlow(f, kind) }).
			when(f.url != "" && f.method != "CONNECT", locales.TunnelsHaveNoRequest()))
	}
	tools = append(tools,
		do("flow.map-host", locales.MapThisHost(), func() { a.openDNSRule(hostOnly(f.host), f.process) }).in("host"),
		submenu(locales.HttpsDecryption(), append(a.appInspectionActions(capture.SSLApp{Name: f.process, Path: f.processPath}),
			do("https.rules", locales.ViewHttpsRules(), func() { a.sslOpen = true }))...).in("host"),
	)
	var saveChosen, compareChosen action
	if chosen := a.selectedFlows(); len(chosen) > 1 {
		saveChosen = do("flow.save-chosen", locales.SaveRequestsEllipsis(len(chosen)), func() { a.openCollectionSave(chosen) }).in("save")
	}
	if chosen := a.selectedFlows(); len(chosen) == 2 {
		compareChosen = do("compare.two", locales.Compare(), func() {
			a.compareTwo(flowTarget(chosen[0]), flowTarget(chosen[1]))
		}).in("open")
	}
	return append(actions,
		compareChosen,
		do("flow.pin", cmp.Or(pinLabel(f), locales.Pin()), func() { a.togglePin(f) }).in("keep"),
		do("flow.mark", locales.Mark(), func() { a.openMark(f) }).in("keep"),
		submenu(locales.FilterCaptures(), filters...).in("keep"),
		do("flow.save-request", locales.SaveAsRequest(), func() { a.saveCapture(f) }).
			when(f.kind != "connect" && f.method != "CONNECT", locales.TunnelsCannotBeSaved()).in("save"),
		saveChosen,
		do("flow.regression", locales.SaveAsRegression(), func() { a.regressionFrom(f) }).
			when(f.status != 0 && f.method != "CONNECT" && f.kind != "ws", locales.NeedsFinishedExchange()).in("save"),
		do("flow.save-evidence", locales.SaveEvidenceToSessions(), func() { a.saveEvidence(f) }).in("save"),
		pick(requests, "collection.from-capture").in("save"),
		submenu(locales.Copy(), copyItems...).in("copy"),
		submenu(locales.Tools(), tools...).in("copy"),
		do("flow.delete", locales.Delete(), func() { a.queueDeleteFlows(a.deleteTargets(f)) }).
			in("danger").keys(ui.Cmd, flowDeleteKey()),
	)
}

// openCompose shows the request editor with an empty request.
func (a *app) openCompose() {
	a.addDocument(savedRequest{Method: "GET", URL: "https://", Auth: request.Auth{Type: request.AuthInherit}}, "")
}

// envNames are the environments the editor offers, sorted.
func (a *app) envNames() []string {
	names := make([]string, 0, len(a.envs))
	for _, e := range a.envs {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	return names
}

func (a *app) activeEnvironment() environment {
	for _, e := range a.envs {
		if e.Name == a.activeEnv {
			return e
		}
	}
	return environment{}
}

// activeVars are the variables of the environment in use, which a request
// fills its {{name}} from.
func (a *app) activeVars() map[string]string {
	return a.activeEnvironment().values()
}

// applyVars merges only the variables changed by this run. Another tab may
// have completed or the user may have edited the environment in the meantime.
// Script writes are Current values: the environment's Initial values, which
// exports carry, stay as they were.
func (a *app) applyVars(name string, before, vars map[string]string, writes ...request.Write) {
	if a.envsProblem != "" {
		a.warn(locales.VariablesNotSaved(a.envsProblem))
		return
	}
	name = cmp.Or(name, "default")
	at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == name })
	if at < 0 {
		a.envs = append(a.envs, environment{Name: name})
		at = len(a.envs) - 1
	}
	if a.envs[at].Current == nil {
		a.envs[at].Current = map[string]string{}
	}
	unset := map[string]bool{}
	for _, w := range writes {
		if w.Scope == "environment" && w.Unset {
			unset[w.Name] = true
		}
	}
	for key, value := range vars {
		if unset[key] {
			continue
		}
		if old, ok := before[key]; !ok || value != old {
			a.envs[at].Current[key] = value
		}
	}
	for key := range before {
		if _, ok := vars[key]; !ok {
			delete(a.envs[at].Current, key) // pm.environment.unset
			delete(a.envs[at].Credentials, key)
		}
	}
	for key := range unset {
		delete(a.envs[at].Current, key)
		delete(a.envs[at].Credentials, key)
	}
	if a.activeEnv == "" {
		a.activeEnv = name
	}
	if err := a.saveLocalEnvs(); err != nil {
		a.fail(locales.Variables(), err)
	}
}

// openVars fills the variables editor with the environment in use.
func (a *app) openVars() {
	a.editEnvironment(cmp.Or(a.activeEnv, "default"))
	a.varsOpen = true
}

func (a *app) editEnvironment(name string) {
	if s := a.collectionRecording; s != nil && s.Active && s.Environment == name {
		a.stopCollectionRecording(locales.RecordingStoppedBeforeOpeningIts())
	}
	a.varsGlobals = false
	a.varsName, a.varsChoice, a.varsProblem = name, name, ""
	a.varsRows = nil
	for _, env := range a.envs {
		if env.Name == name {
			a.varsRows = variableRowsOf(env.Vars, env.Current, env.Credentials)
		}
	}
}

// editGlobals puts the global variables, beneath every environment, in the
// Variables dialog.
func (a *app) editGlobals() {
	a.varsGlobals, a.varsProblem, a.varsRows = true, "", variableRowsOf(a.globals, a.globalsCurrent, nil)
}

// varsModal edits the variables of one environment, or the globals: each row
// has a shared Initial value and a local Current value.
func (a *app) varsModal(c *ui.Context, t *ui.Theme) {
	if !a.varsOpen {
		return
	}
	uikit.Modal(c, &a.varsOpen, locales.Variables(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Children(func() {
			uikit.DialogTitle(c, locales.Variables())
			if a.envsProblem != "" {
				uikit.ErrorText(c, locales.VariablesAreReadOnlyClose())
			}
			scope := 0
			if a.varsGlobals {
				scope = 1
			}
			if uikit.Segmented(c, &scope, locales.Environments2(), locales.Globals()).Changed() {
				if scope == 1 {
					a.editGlobals()
				} else {
					a.editEnvironment(cmp.Or(a.activeEnv, "default"))
				}
			}
			if a.varsGlobals {
				uikit.Meta(c, locales.EveryRequestReadsTheseWhatever())
			} else {
				uikit.Meta(c, locales.UseNameInRequestsEnvironment())
				ui.Form(c, func() {
					uikit.FormRow(c, locales.Environment(), "", "", func() {
						ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
							if names := a.envNames(); len(names) > 0 {
								if uikit.Select(c, &a.varsChoice, names).Grow(1).MinWidth(0).Changed() {
									a.editEnvironment(a.varsChoice)
								}
							}
							if uikit.SecondaryButton(c, locales.NewEnvironment()).Clicked() {
								a.editEnvironment("")
							}
						})
					})
					uikit.FormRow(c, locales.EnvironmentName(), "", "", func() {
						uikit.Input(c, &a.varsName).FillWidth()
					})
				}).Gap(t.Space(uikit.SpaceM))
			}
			uikit.Hint(c, locales.InitialValuesAreSharedAnd())
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if anyCurrent(a.varsRows) {
					if uikit.Tip(c, uikit.SecondaryButton(c, locales.PersistAllCurrent()), locales.WriteNonSensitiveCurrentValues()).Clicked() {
						persisted, skipped := persistAllCurrent(a.varsRows)
						if skipped > 0 {
							a.inform(locales.PersistedValues(persisted, skipped))
						}
					}
					if uikit.Tip(c, uikit.SecondaryButton(c, locales.ResetAllCurrent()), locales.DropEveryLocalOverrideAnd()).Clicked() {
						resetAllCurrent(a.varsRows)
					}
				}
			})
			tableHeight := min(float32(320), uikit.HeightRow*float32(len(a.varsRows)+2)+2)
			ui.Column(c).Height(tableHeight).Disabled(a.envsProblem != "").Children(func() {
				a.variableTable(c, &a.varsRows, variableTableOptions{
					scope: varsEditorScope(a), reveal: a.variableRefs.reveal.name,
				})
			})
			if a.varsProblem != "" {
				uikit.ErrorText(c, a.varsProblem)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.ExportAsPostman()), locales.WritesTheInitialValuesShown()).Clicked() {
					a.exportPostmanVariables()
				}
				ui.Spacer(c)
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.varsOpen = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Disabled(a.envsProblem != "").Clicked() {
					a.saveVars()
				}
			})
		})
	})
}

// saveVars keeps the edited environment or globals and puts an environment in
// use. Only Initial values reach the shared environment format; Current values
// stay in the local file.
func (a *app) saveVars() {
	if a.envsProblem != "" {
		a.warn(locales.VariablesNotSaved(a.envsProblem))
		return
	}
	name := strings.TrimSpace(a.varsName)
	if name == "" && !a.varsGlobals {
		a.varsProblem = locales.EnvironmentNeedsName()
		return
	}
	if s := a.collectionRecording; s != nil && !a.varsGlobals && name == s.Environment && a.varsChoice != name {
		a.varsProblem = locales.SelectRecordingEnvironment()
		return
	}
	initial, current, credentials, err := variablesOf(a.varsRows)
	if err != nil {
		a.varsProblem = locales.VariableNamesRules()
		return
	}
	if a.varsGlobals {
		f := a.localEnvs()
		f.Globals, f.GlobalsCurrent = initial, current
		if err := a.saveEnvFile(f); err != nil {
			a.varsProblem = locales.SaveFailedWith(err.Error())
			return
		}
		a.globals, a.globalsCurrent, a.varsOpen = initial, current, false
		a.inform(locales.GlobalsCount(len(initial)))
		return
	}
	envs := slices.Clone(a.envs)
	found := false
	for i, e := range envs {
		if e.Name == name {
			envs[i].Vars, envs[i].Current, envs[i].Credentials = initial, current, credentials
			found = true
			break
		}
	}
	if !found {
		envs = append(envs, environment{Name: name, Vars: initial, Current: current, Credentials: credentials})
	}
	f := a.localEnvs()
	f.Active, f.Envs = name, envs
	if err := a.saveEnvFile(f); err != nil {
		a.varsProblem = locales.SaveFailedWith(err.Error())
		return
	}
	a.envs, a.activeEnv = envs, name
	a.varsOpen = false
	a.inform(locales.VariablesCount(name, len(initial)))
}

// sendBudget is how long a send may take before the editor cancels it: the
// usual 30 seconds, or the request's own timeout plus a moment.
func (a *app) sendBudget(r savedRequest) time.Duration {
	in := a.inheritance(r.Collection)
	if r.TimeoutUnlimited(in) {
		return 0 // explicit 0 removes the editor's budget; Cancel still stops it
	}
	budget := 30 * time.Second
	if limit := r.Timeout(in); limit > 0 && limit+2*time.Second > budget {
		budget = limit + 2*time.Second
	}
	return budget
}

// sendComposed sends the editor's request through the proxy, so the result
// lands in the table with the rest of the traffic. Variables fill it in, the
// pre-request script may change it, and the post-response script sees what came
// back and asserts on it.
func (a *app) sendComposed() {
	d := a.requestDocument
	if d == nil || d.composeWaiting {
		return
	}
	// Send is the URL field's apply point. Keep what the user authored,
	// before variables or a script can change the URL on the wire.
	a.rememberURLHistory(d.composeURL)
	if d.protocol == "Socket.IO" {
		a.connectSocketIO(d)
		return
	}
	if d.protocol == "MQTT" {
		a.connectMQTT(d)
		return
	}
	if d.protocol == "gRPC" {
		a.sendGRPC(d)
		return
	}
	if d.protocol == "MCP" {
		a.connectMCP(d)
		return
	}
	if a.srv == nil || !a.srv.Running() {
		a.warn(locales.NoProxyRunningStartCapture())
		return
	}
	d.composeTests = nil
	a.clearSendProblemMarkers(d)
	d.composeReplies = nil
	d.composeExchanges = nil
	d.console = nil
	d.historyPick = 0 // The new response replaces a retained run on screen.
	d.response = nil
	d.responseEditor = nil
	// A send replaces the response, so the comparison of the previous one
	// has nothing left to stand on.
	d.comparing = false
	d.sendOutcome, d.sendCancelling, d.sendDetails = "", false, false
	d.composeWaiting, d.composeResult = true, locales.Sending()
	r, env, vars, srv, jar := d.draft(), a.activeEnv, a.activeVars(), a.srv, a.requestCookies()
	// Characters that survive expansion get one non-blocking warning; the
	// send still goes out unchanged, so nothing about the wire is altered.
	a.warnUnicodeBeforeSend(r, d, vars)
	in := a.sendContext(r.Request)
	d.sent, d.sentAt, d.sentSet = r, time.Now(), true
	runKey, draftKey, origin := uuid.New().String(), d.debugKey, d.origin
	d.sentRun = runKey
	// The request's own timeout may exceed the editor's usual budget; leave
	// it a moment to report its deadline before the overall cancel.
	var ctx context.Context
	var cancel context.CancelFunc
	if budget := a.sendBudget(r); budget > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), budget)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	d.sendCancel = cancel
	ctx = request.WithRunKey(ctx, runKey)
	if r.Checks != (request.Checks{}) {
		d.responseTab = resViewTests
	}
	go guarded(func() {
		defer cancel()
		got := r.Execute(ctx, srv, in, vars, jar)
		result := requestResult{document: d, environment: env, summary: runSummary(got), outcome: executionOutcome(got), err: got.Err, response: got.Response, tests: got.Tests, replies: got.Replies, exchanges: got.WebSocket, console: got.Console, varsBefore: vars, vars: got.Vars, writes: got.Writes}
		record := snapshotExperiment(0, r, env, got)
		record.RunKey, record.DraftKey, record.Source = runKey, draftKey, origin
		result.experiment = &record
		a.mu.Lock()
		a.requestResults = append(a.requestResults, result)
		a.mu.Unlock()
		a.invalidate()
	})
}

// plural picks the word for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// openKeyLog fills the key log dialog with what is set now.
func (a *app) openKeyLog() {
	a.keyLogInput = a.keyLogPath
	a.keyLogOpen = true
}

// keyLogModal turns the TLS key log on or off: the secrets go to a file
// Wireshark reads, which is how it decrypts what this app captured.
func (a *app) keyLogModal(c *ui.Context, t *ui.Theme) {
	if !a.keyLogOpen {
		return
	}
	uikit.Modal(c, &a.keyLogOpen, locales.TlsKeyLog(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.TlsKeyLog())
			ui.Scroll(c).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Meta(c, locales.TheSecretsGoToA())
				placeholder := "~/.vscroxy/keys.log"
				if a.dataDir != "" {
					placeholder = shortenHome(filepath.Join(a.dataDir, "keys.log"))
				}
				uikit.Input(c, &a.keyLogInput).Placeholder(placeholder).Label(locales.File())
			})
			uikit.DialogFooter(c).Children(func() {
				if a.keyLogPath != "" && uikit.SecondaryButton(c, locales.TurnOff()).Clicked() {
					a.setKeyLog("")
					a.keyLogOpen = false
				}
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.keyLogOpen = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Clicked() {
					a.setKeyLog(strings.TrimSpace(a.keyLogInput))
					a.keyLogOpen = false
				}
			})
		})
	})
}

// setKeyLog keeps the file the secrets go to and detaches the engine's writer
// from the previous path, so new handshakes use the new file this run.
func (a *app) setKeyLog(path string) {
	if path != "" {
		if home, err := os.UserHomeDir(); err == nil {
			if rest, ok := strings.CutPrefix(path, "~/"); ok {
				path = filepath.Join(home, rest)
			}
		}
	}
	a.keyLogPath = path
	capture.SetTLSKeyLogPath(path)
	if path == "" {
		a.inform(locales.TlsKeyLogOff())
	} else {
		a.inform(locales.TlsKeysGoTo(path))
	}
	a.persistSettings()
	a.invalidate()
}

// filtersActive includes every condition that can hide captured requests.
func (a *app) filtersActive() bool {
	return a.query != "" || a.conditionsActive()
}

// conditionsActive is filtersActive without the search field's own text:
// the conditions the field shows as chips.
func (a *app) conditionsActive() bool {
	return a.filter != "" && a.filter != "all" || a.chip != 0 ||
		a.filterStatus != "" || a.filterHeader != "" || a.views.bodyFilter() != "" || a.filterTriage != (captureTriage{}) || a.filterAny || a.activity.active()
}

// openFilter shows the content filter with what it already holds.
func (a *app) openFilter() {
	a.views.openFilter(a)
}

// clearFilters drops the content filter.
func (a *app) clearFilters() {
	a.views.clearFilters(a)
}

// statusMatches accepts comma-separated alternatives; invalid input never
// silently turns into a partial filter.
func statusMatches(expr string, status int) bool {
	if strings.TrimSpace(expr) == "" {
		return true
	}
	matched := false
	for part := range strings.SplitSeq(expr, ",") {
		lo, hi, ok := statusBounds(part)
		if !ok {
			return false
		}
		matched = matched || status >= lo && status <= hi
	}
	return matched
}

// statusBounds is shared by the filter validator and matcher.
func statusBounds(expr string) (lo, hi int, valid bool) {
	expr = strings.ToLower(strings.TrimSpace(expr))
	code := func(s string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		return n, err == nil && n >= 100 && n <= 599
	}
	switch {
	case strings.HasPrefix(expr, ">="):
		n, ok := code(expr[2:])
		return n, 599, ok
	case strings.HasPrefix(expr, "<="):
		n, ok := code(expr[2:])
		return 100, n, ok
	case strings.HasPrefix(expr, ">"):
		n, ok := code(expr[1:])
		return n + 1, 599, ok
	case strings.HasPrefix(expr, "<"):
		n, ok := code(expr[1:])
		return 100, n - 1, ok
	case strings.HasSuffix(expr, "xx"):
		n, err := strconv.Atoi(strings.TrimSuffix(expr, "xx"))
		return n * 100, n*100 + 99, err == nil && n >= 1 && n <= 5
	case strings.Contains(expr, "-"):
		low, high, _ := strings.Cut(expr, "-")
		lo, a := code(low)
		hi, b := code(high)
		return lo, hi, a && b && lo <= hi
	default:
		n, ok := code(expr)
		return n, n, ok
	}
}

// headerMatches reports whether a flow carries the header a filter names, as
// "name" alone or "name: value" to match the value too.
func headerMatches(f flow, expr string) bool {
	name, value, hasValue := strings.Cut(expr, ":")
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	values := append(f.reqHeader.Values(name), f.respHeader.Values(name)...)
	if len(values) == 0 {
		return false
	}
	if !hasValue {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, have := range values {
		if strings.Contains(strings.ToLower(have), value) {
			return true
		}
	}
	return false
}

// sortVisible orders the shown rows; applyFilter restores selection by ID.
func (a *app) sortVisible() {
	if len(a.visible) < 2 {
		return
	}
	slices.SortFunc(a.visible, a.rowOrder())
}

// rowOrder is the table's order: pinned flows first, whatever the column
// says, then the column, then arrival.
func (a *app) rowOrder() func(x, y int) int {
	less := a.sortOrder()
	return func(x, y int) int {
		px, py := a.flows[x].pinned, a.flows[y].pinned
		if px != py {
			if px {
				return -1
			}
			return 1
		}
		return cmp.Or(less(x, y), cmp.Compare(x, y))
	}
}

// sortOrder compares two flows by the chosen column.
func (a *app) sortOrder() func(x, y int) int {
	desc := a.sort.Descending
	return func(x, y int) int {
		fx, fy := a.flows[x], a.flows[y]
		var c int
		switch a.sort.Column {
		case "id":
			c = cmp.Compare(fx.id, fy.id)
		case "captured":
			c = fx.at.Compare(fy.at)
		case "method":
			c = cmp.Compare(fx.method, fy.method)
		case "proto":
			c = cmp.Compare(fx.scheme, fy.scheme)
		case "status":
			c = cmp.Compare(fx.status, fy.status)
		case "host":
			c = cmp.Compare(fx.host, fy.host)
		case "remote":
			c = cmp.Compare(fx.conn.remote, fy.conn.remote)
		case "url":
			c = cmp.Compare(fx.url, fy.url)
		case "client":
			c = cmp.Compare(fx.process, fy.process)
		case "path":
			c = cmp.Compare(fx.path, fy.path)
		case "size":
			c = cmp.Compare(fx.bytes, fy.bytes)
		case "ressize":
			c = cmp.Compare(fx.resSize, fy.resSize)
		case "reqwire":
			c = cmp.Compare(fx.wireSize(), fy.wireSize())
		case "reqsize":
			c = cmp.Compare(fx.reqSize, fy.reqSize)
		case "latency":
			c = cmp.Compare(millisOrZero(fx.latencyMillis), millisOrZero(fy.latencyMillis))
		case "reqtime":
			c = cmp.Compare(millisOrZero(fx.requestMillis), millisOrZero(fy.requestMillis))
		case "restime":
			c = cmp.Compare(millisOrZero(fx.responseMillis), millisOrZero(fy.responseMillis))
		case "ssl":
			c = cmp.Compare(secureRank(fx), secureRank(fy))
		case "comment":
			c = cmp.Compare(fx.note, fy.note)
		case "tools":
			c = cmp.Compare(len(fx.actions), len(fy.actions))
		case "time", "duration":
			c = cmp.Compare(fx.millis, fy.millis)
		default:
			if cc, ok := a.customColumnAt(a.sort.Column); ok {
				c = cmp.Compare(a.columnValue(cc, fx), a.columnValue(cc, fy))
			}
		}
		if desc {
			return -c
		}
		return c
	}
}

// hostListModal edits a list of hosts, one per line, and keeps it in a file
// the next start reads.
func (a *app) hostListModal(c *ui.Context, t *ui.Theme, open *bool, title, hint, path string, text *string, apply func([]string)) {
	if !*open {
		return
	}
	uikit.Modal(c, open, title, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, title)
			ui.Scroll(c).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Meta(c, hint)
				uikit.TextArea(c, text).Height(140).Label(title).Radius(uikit.RadiusControl)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					*open = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Clicked() {
					patterns := splitHosts(*text)
					if path != "" {
						if err := a.writeList(path, patterns); err != nil {
							a.fail(title, err)
							return
						}
					}
					apply(patterns)
					*open = false
					a.inform(locales.PatternsCount(title, len(patterns)))
				}
			})
		})
	})
}

// splitHosts reads a list of hosts, one per line, where a line starting with #
// is a comment.
func splitHosts(text string) []string {
	var patterns []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// saveComposed keeps the editor's request under its name, replacing one of
// the same name.
func (a *app) saveComposed() bool {
	name := strings.TrimSpace(a.composeName)
	if name == "" {
		name = strings.TrimSpace(a.composeMethod + " " + a.composeURL)
	}
	if name == "" {
		a.warn(locales.SaveTheRequestHasNo())
		return false
	}
	r := a.requestDocument.value()
	if _, _, _, err := r.Checks.Parse(); err != nil {
		a.fail(locales.Save(), err)
		return false
	}
	r.Name, r.Collection = name, cleanPath(r.Collection)
	r.Method, r.URL = strings.ToUpper(strings.TrimSpace(cmp.Or(r.Method, "GET"))), strings.TrimSpace(r.URL)
	for _, existing := range a.requests {
		if existing.Name == r.Name && existing.Collection == r.Collection && !(r.ID != "" && existing.ID == r.ID) && existing.key() != a.savedKey {
			a.warn(locales.SaveAnotherRequestHasThis())
			return false
		}
	}
	beforeUndo, err := a.snapshotUndoLibrary()
	if err != nil {
		a.fail(locales.Save(), err)
		return false
	}
	list := upsertRequest(slices.Clone(a.requests), r)
	for _, saved := range list {
		if saved.Name == r.Name && saved.Collection == r.Collection {
			r.ID = saved.ID
			break
		}
	}
	if a.settingsPath != "" {
		saved, err := loadSettings(a.settingsPath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.saved = saved
			a.logLevel = saved.LogLevel
			a.appearance = saved.Appearance
			if saved.SidebarWidth > 0 {
				a.sidebarWidth = saved.SidebarWidth
			}
			if saved.TablePercent > 0 {
				a.tablePercent = saved.TablePercent
			}
			a.setColumns(saved.Columns.layout())
			a.customColumns, a.hiddenColumns = slices.Clone(saved.CustomColumns), saved.HiddenColumns
			a.shownColumns = slices.Clone(saved.ShownColumns)
		}
	}
	if err := a.persistRequests(list); err != nil {
		a.fail(locales.Save(), err)
		return false
	}
	a.requests = list
	a.composeName = name
	a.composeCollection, a.composeMethod, a.composeURL = r.Collection, r.Method, r.URL
	a.assetID = r.ID
	a.baseline = a.requestDocument.value()
	a.savedKey = r.key()
	a.requestChoice = a.savedKey
	*a.collectionShown(r.Collection) = true
	afterUndo, err := a.snapshotUndoLibrary()
	if err == nil {
		code := "library.save"
		for _, old := range beforeUndo.data.requests {
			if old.key() == r.key() && (old.Name != r.Name || old.Collection != r.Collection) {
				code = "library.rename"
				break
			}
		}
		label := locales.SavedName(name)
		handle := a.recordUndo(undoRecord{code: code, label: label, before: beforeUndo, after: afterUndo})
		a.undoNotice(label, handle)
	} else {
		a.done(locales.SavedName(name))
	}
	return true
}

// openSaved fills the request editor from a saved request.
func (a *app) openSaved(key string) {
	for _, d := range a.documents {
		if d.savedKey == key {
			a.activateDocument(d)
			return
		}
	}
	for i, r := range a.requests {
		if r.key() != key {
			continue
		}
		if r.ID == "" {
			r.ID = uuid.New().String()
			a.requests[i] = r
			a.requestGen++
		}
		a.addDocument(r, r.key())
		return
	}
}

// matchesChip reports whether the flow belongs to the chip chosen above the
// table: a protocol, or a kind of body.
func (a *app) matchesChip(f flow) bool {
	json := strings.Contains(f.contentType(true), "json")
	version := cmp.Or(f.conn.proto, f.resProto)
	switch a.chip {
	case 1: // HTTP
		return f.kind == "" && f.scheme == "http"
	case 2: // HTTPS
		return f.kind == "connect" || (f.kind == "" && f.scheme == "https")
	case 3: // WebSocket
		return f.kind == "ws"
	case 4: // SSE
		return f.kind == "sse"
	case 5: // JSON
		return json
	case 6: // Images
		return strings.HasPrefix(f.contentType(true), "image/")
	case 7: // HTML
		return strings.Contains(f.contentType(true), "html")
	case 8: // Form: the request body is form-encoded or multipart
		ct := f.contentType(false)
		return ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data"
	case 9: // XML
		return strings.Contains(f.contentType(true), "xml")
	case 10: // JS
		ct := f.contentType(true)
		return strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript")
	case 11: // CSS
		return f.contentType(true) == "text/css"
	case 12: // Media
		ct := f.contentType(true)
		return strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/")
	case 13: // h1: the client spoke HTTP/1.x
		return strings.HasPrefix(version, "HTTP/1")
	case 14: // h2
		return strings.HasPrefix(version, "HTTP/2")
	}
	return true
}

// sslModal edits what the proxy decrypts.
func (a *app) sslModal(c *ui.Context, t *ui.Theme) {
	if !a.sslOpen {
		a.sslDraft = nil
		return
	}
	if a.sslDraft == nil {
		// The enable-rule toast must not cover this editor's Save/Cancel
		// buttons in a small window. Later save errors still remain visible.
		c.CloseToast("")
		cfg := a.inspectionConfig()
		a.sslDraft, a.sslDraftHosts, a.sslDraftExclude, a.sslDraftUntrusted = &cfg, a.sslHosts, a.sslExclude, a.sslUntrusted
	}
	draft := a.sslDraft
	_, height := c.Size()
	uikit.Modal(c, &a.sslOpen, locales.HttpsDecryption(), func() {
		uikit.DialogColumn(c, uikit.SettingsWidth).Height(min(720, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.HttpsDecryption())
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(uikit.SpaceL)).Children(func() {
				uikit.SettingsCard(c, "", func() {
					uikit.SettingsCardRow(c, locales.DecryptEveryDomain(), locales.DecryptEveryNewHttpsConnection(), func() {
						uikit.Switch(c, &draft.All)
					})
				})
				uikit.FormRowTip(c, locales.DecryptionDomains(), locales.DomainsAndSubdomains(), func() ui.Element {
					return uikit.TextArea(c, &a.sslDraftHosts).Height(80).Radius(uikit.RadiusControl).FillWidth()
				})
				uikit.FormRow(c, locales.NeverDecrypt(), locales.NeverDecryptHint(), "", func() {
					uikit.TextArea(c, &a.sslDraftExclude).Height(64).Radius(uikit.RadiusControl).FillWidth()
				})
				uikit.FormRow(c, locales.AllowUntrustedUpstream(), locales.AllowUntrustedUpstreamHint(), "", func() {
					uikit.TextArea(c, &a.sslDraftUntrusted).Height(64).Radius(uikit.RadiusControl).FillWidth()
				})
				uikit.SettingsCard(c, locales.LocalApps(), func() {
					if len(draft.Apps) == 0 {
						uikit.Hint(c, locales.NoAppRulesEnabled())
						return
					}
					remove := -1
					listHeight := min(float32(180), float32(len(draft.Apps))*uikit.SettingsRowHeight+2)
					ui.Column(c).Height(listHeight).Children(func() {
						uikit.DataListIndexed(c, &a.sslList, len(draft.Apps), func(i int) {
							app := draft.Apps[i]
							// Rows divide from each other; the last one has no rule of
							// its own, or it doubles the card's bottom edge.
							rule := float32(1)
							if i == len(draft.Apps)-1 {
								rule = 0
							}
							ui.Row(c).Height(uikit.SettingsRowHeight).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).
								BorderWidth(0, 0, rule, 0).BorderColor(t.Border).Children(func() {
								ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
									uikit.Body(c, app.Name).SingleLine()
									uikit.Tip(c, uikit.Meta(c, app.Path).SingleLine(), app.Path)
								})
								if uikit.Tip(c, uikit.RowIconButton(c, locales.RemoveHttpsApp(app.Path), locales.RemoveHttpsApp(app.Path), closeIcon), app.Path).Clicked() {
									remove = i
								}
							})
						})
					})
					if remove >= 0 {
						draft.Apps = slices.Delete(draft.Apps, remove, remove+1)
					}
				})
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.sslOpen = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Clicked() {
					draft.Hosts = splitHosts(a.sslDraftHosts)
					draft.Exclude = splitHosts(a.sslDraftExclude)
					draft.AllowUntrusted = splitHosts(a.sslDraftUntrusted)
					if a.saveSSL(*draft) {
						a.sslOpen = false
					}
				}
			})
		})
	})
}

// saveSSL keeps the SSL proxying settings and hands them to the proxy. A
// connection already open keeps the choice it was made with.
func (a *app) saveSSL(cfg capture.SSLConfig) bool {
	if a.sslPath != "" {
		data, err := json.Marshal(cfg)
		if err != nil {
			a.fail(locales.HttpsDecryption(), err)
			return false
		}
		if err := a.file(a.sslPath).Write(append(data, '\n')); err != nil {
			a.fail(locales.HttpsDecryption(), err)
			return false
		}
	}
	if a.srv != nil {
		a.srv.SetSSL(cfg)
	}
	a.sslAll, a.sslHosts = cfg.All, strings.Join(cfg.Hosts, "\n")
	a.sslExclude = strings.Join(cfg.Exclude, "\n")
	a.sslUntrusted = strings.Join(cfg.AllowUntrusted, "\n")
	a.sslApps = slices.Clone(cfg.Apps)
	if cfg.All {
		a.inform(locales.SslProxyingEveryDomain())
	} else {
		a.inform(locales.SslSummary(len(cfg.Hosts), len(cfg.Apps)))
	}
	return true
}

// decryptHost adds a domain to what the proxy decrypts, the button under a
// tunnel.
func (a *app) decryptHost(host string) {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(hostOnly(host), "[]"), "."))
	cfg := a.inspectionConfig()
	if host != "" && !slices.Contains(cfg.Hosts, host) {
		cfg.Hosts = append(cfg.Hosts, host)
	}
	if a.saveSSL(cfg) {
		a.done(locales.InspectionEnabledFor(host))
	}
}

// persistSettings writes what changed since the last write, at most every
// 400ms, so dragging a divider does not write a file a frame.
func (a *app) persistSettings() error {
	now := a.currentSettings()
	if sameSettings(now, a.saved) {
		if a.settingsSaver != nil {
			a.settingsSaver.Cancel()
		}
		return nil
	}
	a.settingsGen++ // the modified flags depend on what this write saves
	if elapsed := time.Since(a.lastSave); elapsed < settingsWriteGap {
		// A write just went out. Schedule the remainder of the gap once, so
		// the change is written even if nothing else moves the window.
		saver := a.settingsSaveScheduler()
		if !saver.IsScheduled() {
			saver.Schedule(settingsWriteGap - elapsed)
		}
		return nil
	}
	if a.settingsSaver != nil {
		a.settingsSaver.Cancel()
	}
	a.lastSave = time.Now()
	if a.settingsPath == "" {
		a.saved = now
		return nil
	}
	// Serialize these small, debounced writes on the UI thread so an older
	// layout snapshot cannot overwrite a port explicitly saved after it.
	err := os.MkdirAll(filepath.Dir(a.settingsPath), 0o755)
	if err == nil {
		err = writeSettings(a.settingsPath, now, a.backupDir)
	}
	if err != nil {
		a.fail(locales.Settings(), err)
		return err
	}
	applog.For("store").Debug("settings saved", "path", a.settingsPath)
	a.saved = now
	return nil
}

// settingsWriteGap keeps a drag of a split from writing settings on every
// frame; the change it holds back is written once the gap passes.
const settingsWriteGap = 400 * time.Millisecond

func (a *app) settingsSaveScheduler() *async.RunOnceScheduler {
	a.settingsOnce.Do(func() {
		runner := func() error {
			// persistSettings reads app state and can show feedback; the
			// scheduler calls this from a timer goroutine.
			a.onMain(func() { _ = a.persistSettings() })
			return nil
		}
		if a.settingsClock != nil {
			a.settingsSaver = async.NewRunOnceScheduler(runner, settingsWriteGap, async.WithClock(a.settingsClock))
		} else {
			a.settingsSaver = async.NewRunOnceScheduler(runner, settingsWriteGap)
		}
	})
	return a.settingsSaver
}

// bypassSettingsSave lets an explicit save write at once, without a pending
// layout write overtaking it later.
func (a *app) bypassSettingsSave() {
	a.lastSave = time.Time{}
	if a.settingsSaver != nil {
		a.settingsSaver.Cancel()
	}
}

// flushSettings writes the settings now, whatever the gap: the app does it as
// it quits, so a change made in the last moment is not lost. Cancel the
// trailing schedule first; the direct write below is the whole flush.
func (a *app) flushSettings() {
	if a.settingsSaver != nil {
		a.settingsSaver.Cancel()
	}
	a.lastSave = time.Time{}
	_ = a.persistSettings()
}

// pinLabel is what the row menu says about pinning a flow.
func pinLabel(f flow) string {
	if f.pinned {
		return locales.Unpin()
	}
	return locales.PinToTheTop()
}

// togglePin pins a flow to the top of the table, or unpins it. A pinned flow
// stays when the table is cleared.
func (a *app) togglePin(f flow) {
	for i := range a.flows {
		if a.flows[i].id != f.id {
			continue
		}
		a.count(&a.flows[i], -1)
		a.flows[i].pinned = !a.flows[i].pinned
		a.count(&a.flows[i], 1)
		if a.flows[i].pinned {
			a.done(locales.PinnedFlow(f.method, f.path))
		} else {
			a.done(locales.UnpinnedFlow(f.method, f.path))
		}
		a.applyFilter()
		return
	}
}

// collectionShown returns the open state of a collection's section, making it
// open the first time it is drawn.
func (a *app) collectionShown(name string) *bool {
	if open, ok := a.collectionOpen[name]; ok {
		return open
	}
	open := true
	a.collectionOpen[name] = &open
	return &open
}

// selectedFlows are the flows chosen in the table, in the order captured.
func (a *app) selectedFlows() []flow {
	if a.chosen.Len() == 0 {
		return nil
	}
	var out []flow
	for _, f := range a.flows {
		if a.chosen.Has(f.id) {
			out = append(out, f)
		}
	}
	return out
}

// exportChosen writes the chosen flows to a HAR file the user picks.
func (a *app) exportChosen() {
	flows := a.selectedFlows()
	if len(flows) == 0 {
		a.warn(locales.ExportChosenNoFlowChosen())
		return
	}
	a.reviewHAR(flows, "vscroxy-chosen.har")
}

// dnsRuleModal maps one host, opened from a flow: the host and the app that
// sent it come from the row, so the scope is a picker over the apps seen
// rather than a name to spell.
func (a *app) dnsRuleModal(c *ui.Context, t *ui.Theme) {
	if !a.dnsRuleOpen {
		return
	}
	uikit.Modal(c, &a.dnsRuleOpen, locales.MapAHost(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.MapAHost())
			ui.Scroll(c).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Meta(c, locales.TheRequestKeepsItsName())
				uikit.Input(c, &a.dnsRuleHost).Label(locales.Host())
				uikit.Input(c, &a.dnsRuleAddr).Label(locales.Address())
				uikit.Meta(c, locales.AppliesTo())
				uikit.Select(c, &a.dnsRuleScope, a.dnsRuleApps)
				uikit.Meta(c, locales.HostProfile())
				uikit.Select(c, &a.dnsRuleProfile, a.mappableProfiles())
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.dnsRuleOpen = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Clicked() {
					a.saveDNSRule()
				}
			})
		})
	})
}

// openDNSRule fills the dialog from a flow's host and app, or from an app in
// the sidebar that has no host yet, so the app is picked rather than spelled.
func (a *app) openDNSRule(host, app string) {
	a.dnsRuleHost = host
	a.dnsRuleAddr = ""
	a.dnsRuleScope = "Any app"
	a.dnsRuleApps = []string{"Any app"}
	for _, d := range a.clients {
		if d.name != "" && !slices.Contains(a.dnsRuleApps, d.name) {
			a.dnsRuleApps = append(a.dnsRuleApps, d.name)
		}
	}
	if app != "" {
		if !slices.Contains(a.dnsRuleApps, app) {
			a.dnsRuleApps = append(a.dnsRuleApps, app)
		}
		a.dnsRuleScope = app
	}
	a.dnsRuleProfile = "Default"
	if on := a.profiles.set.On(); len(on) > 0 && slices.Contains(a.mappableProfiles(), on[0]) {
		a.dnsRuleProfile = on[0]
	} else if names := a.mappableProfiles(); len(names) > 0 {
		a.dnsRuleProfile = names[0]
	}
	a.dnsRuleOpen = true
}

// mappableProfiles are the profiles a mapping can be added to: the local
// ones, or Default, which adding creates.
func (a *app) mappableProfiles() []string {
	var names []string
	for _, p := range a.profiles.set.Profiles {
		if p.URL == "" {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		names = []string{"Default"}
	}
	return names
}

// saveDNSRule adds the dialog's mapping to the end of the chosen profile
// and turns it on.
func (a *app) saveDNSRule() {
	host := strings.TrimSpace(a.dnsRuleHost)
	addr := strings.TrimSpace(a.dnsRuleAddr)
	if host == "" || net.ParseIP(addr) == nil {
		a.warn(locales.MapAHostEnterA())
		return
	}
	line := addr + " " + host
	if a.dnsRuleScope != "" && a.dnsRuleScope != "Any app" {
		line += " @ " + a.dnsRuleScope
	}
	if !a.mapHostLine(a.dnsRuleProfile, line) {
		return
	}
	a.dnsRuleOpen = false
	a.inform(locales.MappedHost(host, addr, a.dnsRuleProfile))
}

// hostOnly drops the port from a host the table shows, for a rule's name.
func hostOnly(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return host
}

// sidebarVibrancy is the sidebar's material: macOS's, and none elsewhere,
// where Windows' Mica would cost the window its menu bar.
func sidebarVibrancy() mygo.Vibrancy {
	if runtime.GOOS == "darwin" {
		return mygo.VibrancySidebar
	}
	return mygo.VibrancyNone
}
