package app

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/datadir"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// relaunch starts a fresh app over the same private data root, as a new
// process launch would: datadir caches the root for the process.
func relaunch(t *testing.T, root string) *app {
	t.Helper()
	useTempHome(t, t.TempDir())
	t.Setenv(datadir.Env, root)
	datadir.Reset()
	a := newApp()
	t.Cleanup(func() { _ = a.srv.Close() })
	a.quitOnMain = func(fn func()) { fn() }
	return a
}

// quitForSession uses the same quit path as the native app: lifecycle
// cleanup flushes the last session and settings, then the app would exit.
func quitForSession(t *testing.T, a *app) {
	t.Helper()
	a.installShutdown()
	a.quitOnMain = func(fn func()) { fn() }
	a.systemProxy.clear = func(string) error { return nil }
	quits := make(chan struct{}, 1)
	a.quitApp = func() { quits <- struct{}{} }
	a.requestQuit(&mygo.QuitEvent{})
	select {
	case <-quits:
	case <-time.After(5 * time.Second):
		t.Fatal("quit did not finish")
	}
}

// waitLastSessionFinish waits on the workers' signals, never on the clock.
// The settle step is the frame work that moves a worker's answer into state,
// so the wait does not steal a session's load result from checkpointSessions.
func waitLastSessionFinish(t *testing.T, a *app) {
	t.Helper()
	watchApp(a)
	if r := a.lastSessionRestore; r != nil {
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Fatal("the last session reader did not finish")
		}
		a.checkpointLastSession()
	}
	loading := func() bool {
		for _, item := range a.sessions.items {
			if item.loading || item.loadDone != nil && !item.loaded {
				return true
			}
		}
		return false
	}
	waitAppSignal(t, a, func() bool { return !loading() }, func() {
		a.checkpointSessions()
		a.checkpointInspectorRestore()
	}, func() string { return "a saved session did not finish loading" })
	a.checkpointSessions()
	a.checkpointInspectorRestore()
}

func lastSessionFlows() []flow {
	return []flow{
		{id: 11, method: "GET", scheme: "https", host: "api.test", path: "/ok", url: "https://api.test/ok", status: 200},
		{id: 12, method: "GET", scheme: "https", host: "other.test", path: "/health", url: "https://other.test/health", status: 200},
		{id: 13, method: "POST", scheme: "https", host: "api.test", path: "/login", url: "https://api.test/login", status: 500},
	}
}

func TestLastSessionRestoresTableFiltersSelectionAndGroups(t *testing.T) {
	root := t.TempDir()
	a := relaunch(t, root)
	a.flows = lastSessionFlows()
	a.recount()
	a.applyFilter()
	a.views.splitRight(a)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "")
	a.views.focusCaptureGroup(a, 0)
	a.pickedID, a.picked = 13, 1
	a.views.capture(a)
	a.views.focusCaptureGroup(a, 1)
	a.pickedID, a.picked = 12, 0
	a.views.capture(a)
	quitForSession(t, a)

	snapshot := filepath.Join(root, "sessions", lastSessionFile)
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("quit wrote no last session: %v", err)
	}

	b := relaunch(t, root)
	waitLastSessionFinish(t, b)
	var ids []int
	for _, f := range b.flows {
		ids = append(ids, f.id)
	}
	if !slices.Equal(ids, []int{11, 12, 13}) {
		t.Fatalf("restored flows = %v, want 11, 12, 13", ids)
	}
	if !b.views.split {
		t.Fatal("the split capture groups were not restored")
	}
	if got := groupIDs(b, 0); !slices.Equal(got, []int{13}) {
		t.Fatalf("group 1 rows = %v, want the failing row", got)
	}
	if got := groupIDs(b, 1); !slices.Equal(got, []int{12}) {
		t.Fatalf("group 2 rows = %v, want the other host", got)
	}
	if got := b.views.viewByID(b.views.groups[0]).pickedID; got != 13 {
		t.Fatalf("group 1 picked = %d, want 13", got)
	}
	if got := b.views.viewByID(b.views.groups[1]).pickedID; got != 12 {
		t.Fatalf("group 2 picked = %d, want 12", got)
	}
	if b.workbench.captureSplit != true || b.workbench.captureFocus != b.views.focus {
		t.Fatalf("saved workbench = split %v focus %d, live focus %d", b.workbench.captureSplit, b.workbench.captureFocus, b.views.focus)
	}
}

func TestRestoreLastSessionCanBeTurnedOff(t *testing.T) {
	root := t.TempDir()
	a := relaunch(t, root)
	a.flows = lastSessionFlows()
	a.recount()
	a.applyFilter()
	quitForSession(t, a)
	if _, err := os.Stat(filepath.Join(root, "sessions", lastSessionFile)); err != nil {
		t.Fatalf("quit wrote no last session: %v", err)
	}

	off := relaunch(t, root)
	off.restoreLastSession = false
	off.bypassSettingsSave()
	if err := off.persistSettings(); err != nil {
		t.Fatal(err)
	}
	quitForSession(t, off)
	snapshot := filepath.Join(root, "sessions", lastSessionFile)
	if _, err := os.Stat(snapshot); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("turning the setting off left the capture on disk: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(settings, []byte(lastSessionFile)) {
		t.Fatalf("turning the setting off kept the saved pointer: %s", settings)
	}

	b := relaunch(t, root)
	waitLastSessionFinish(t, b)
	if b.restoreLastSession {
		t.Fatal("the saved off setting was not read back")
	}
	if b.lastSessionRestore != nil {
		t.Fatal("a disabled restore still started a session read")
	}
	if len(b.flows) != 0 || len(b.visible) != 0 {
		t.Fatalf("restore off started with %d flows and %d rows", len(b.flows), len(b.visible))
	}
}

func TestRestoreSettingSwitchDeletesTheSnapshot(t *testing.T) {
	root := t.TempDir()
	a := relaunch(t, root)
	a.flows = lastSessionFlows()
	a.recount()
	a.applyFilter()
	quitForSession(t, a)
	path := filepath.Join(root, "sessions", lastSessionFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("quit wrote no last session: %v", err)
	}

	b := relaunch(t, root)
	b.revealSetting("capture.restoreLastSession")
	tt := ui.NewTester(b.settingsView, 1000, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	var found bool
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleSwitch && info.Name == locales.RestoreLastSession() {
			tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the restore switch is not accessible: %v", tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if b.restoreLastSession {
		t.Fatal("clicking the switch did not turn the setting off")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the switch left the snapshot on disk: %v", err)
	}
}

func TestLastSessionKeepsNoBackupCopiesOfCapturedTraffic(t *testing.T) {
	root := t.TempDir()
	for range 2 {
		a := relaunch(t, root)
		a.flows = lastSessionFlows()
		a.recount()
		a.applyFilter()
		quitForSession(t, a)
	}
	path := filepath.Join(root, "sessions", lastSessionFile)
	backups, err := store.ListBackups(filepath.Join(root, "backups"), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("two saves left %d backups of the captured traffic: %v", len(backups), backups)
	}
}

func TestLastSessionOpensTheSavedCaptureTabs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const key = "saved-capture.json"
	if err := writeSession(filepath.Join(dir, key), []flow{{id: 31, method: "GET", host: "saved.test", path: "/", status: 200}}, nil); err != nil {
		t.Fatal(err)
	}
	a := relaunch(t, root)
	a.selectSession(key)
	waitLastSessionFinish(t, a)
	quitForSession(t, a)

	b := relaunch(t, root)
	waitLastSessionFinish(t, b)
	if b.sessions.active != key {
		t.Fatalf("active session = %q, want %q", b.sessions.active, key)
	}
	item := b.sessions.current()
	if item == nil || !item.open {
		t.Fatal("the saved capture tab was not reopened")
	}
	if len(item.data.Flows) != 1 || item.data.Flows[0].ID != 31 {
		t.Fatalf("the reopened tab holds %+v", item.data.Flows)
	}
}

func TestLastSessionTableOpensBeforeTheBodiesAreDecoded(t *testing.T) {
	root := t.TempDir()
	a := relaunch(t, root)
	a.flows = lastSessionFlows()
	a.recount()
	a.applyFilter()
	quitForSession(t, a)

	started, release := make(chan struct{}), make(chan struct{})
	previous := lastSessionReader
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	t.Cleanup(func() { lastSessionReader = previous })
	lastSessionReader = func(path string) (sessionFile, error) {
		close(started)
		<-release
		return previous(path)
	}
	t.Cleanup(func() { lastSessionReader = previous })

	b := relaunch(t, root)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the last session reader never started")
	}
	if len(b.flows) != 0 || len(b.visible) != 0 {
		t.Fatalf("the table waited for the bodies: %d flows, %d rows", len(b.flows), len(b.visible))
	}
	select {
	case <-b.lastSessionRestore.done:
		t.Fatal("the reader finished before it was released")
	default:
	}
	unblock()
	waitLastSessionFinish(t, b)
	if len(b.flows) != 3 {
		t.Fatalf("after the read, flows = %d, want 3", len(b.flows))
	}
}

func TestLastSessionRefusesCorruptAndNewerFiles(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
	}{
		{"corrupt", "{not a session"},
		{"newer", `{"version":2,"flows":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			a := relaunch(t, root)
			a.flows = lastSessionFlows()
			a.recount()
			a.applyFilter()
			quitForSession(t, a)
			path := filepath.Join(root, "sessions", lastSessionFile)
			broken := []byte(tc.contents)
			if err := os.WriteFile(path, broken, 0o600); err != nil {
				t.Fatal(err)
			}

			b := relaunch(t, root)
			waitLastSessionFinish(t, b)
			if len(b.flows) != 0 {
				t.Fatalf("a refused file restored %d flows", len(b.flows))
			}
			if !strings.Contains(b.message, locales.LastSessionNotRestored()) {
				t.Fatalf("notice = %q, want the restore failure", b.message)
			}
			// The same refusal must hold at the next quit: a broken file is
			// left for the person who owns it, never replaced by a rewrite.
			quitForSession(t, b)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, broken) {
				t.Fatalf("refused file changed:\nbefore %q\nafter  %q", broken, after)
			}
			// Opting out does not make a refused file disposable either.
			c := relaunch(t, root)
			c.restoreLastSession = false
			c.bypassSettingsSave()
			if err := c.persistSettings(); err != nil {
				t.Fatal(err)
			}
			quitForSession(t, c)
			afterOptOut, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(afterOptOut, broken) {
				t.Fatalf("opting out changed a refused file:\nbefore %q\nafter  %q", broken, afterOptOut)
			}
		})
	}
}

func TestDetachedInspectorReopensOnlyForAnExistingExchange(t *testing.T) {
	root := t.TempDir()
	a := relaunch(t, root)
	a.flows = lastSessionFlows()
	a.recount()
	a.applyFilter()
	a.picked, a.pickedID = 2, 13
	a.applyFilter()
	a.openInspectorWindow()
	if !a.inspectorDetached {
		t.Fatal("the inspector did not detach in the first session")
	}
	quitForSession(t, a)

	t.Run("exchange-still-exists", func(t *testing.T) {
		b := relaunch(t, root)
		waitLastSessionFinish(t, b)
		if !b.inspectorDetached {
			t.Fatal("the detached inspector did not reopen for its exchange")
		}
	})

	t.Run("exchange-gone", func(t *testing.T) {
		path := filepath.Join(root, "sessions", lastSessionFile)
		if err := writeSession(path, []flow{{id: 99, method: "GET", path: "/gone"}}, nil); err != nil {
			t.Fatal(err)
		}
		b := relaunch(t, root)
		waitLastSessionFinish(t, b)
		if b.inspectorDetached {
			t.Fatal("the inspector reopened for an exchange that no longer exists")
		}
		if !strings.Contains(b.message, locales.InspectorNotRestored()) {
			t.Fatalf("notice = %q, want the inspector notice", b.message)
		}
	})
}

func TestLastSessionStateKeepsPayloadsOutOfSettings(t *testing.T) {
	root := t.TempDir()
	a := relaunch(t, root)
	secret := "Bearer not-for-settings"
	a.flows = []flow{{id: 21, method: "GET", host: "api.test", url: "https://api.test/", reqBody: secret, resBody: "response-body"}}
	a.recount()
	a.applyFilter()
	quitForSession(t, a)

	data, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{secret, "response-body", "reqBody", "resBody"} {
		if bytes.Contains(data, []byte(word)) {
			t.Fatalf("settings.json contains %q: %s", word, data)
		}
	}

	b := relaunch(t, root)
	waitLastSessionFinish(t, b)
	if len(b.flows) != 1 || b.flows[0].reqBody != secret {
		t.Fatalf("the capture body was not restored: %+v", b.flows)
	}
	if b.searchAll.input != "" {
		t.Fatal("restart restored a body preview, search or hover cache")
	}
}

func TestRelaxedRestoredFieldsMatchSavedSettings(t *testing.T) {
	state := lastSessionState{Capture: "x", Captures: []string{"a"}, Views: []lastSessionView{{ID: 2, Chosen: []int{3}}}}
	copyState := lastSessionState{Capture: "x", Captures: []string{"a"}, Views: []lastSessionView{{ID: 2, Chosen: []int{3}}}}
	if !sameLastSession(state, copyState) || !reflect.DeepEqual(state, copyState) {
		t.Fatal("equal last-session states differ")
	}
	copyState.Views[0].Chosen = []int{4}
	if sameLastSession(state, copyState) {
		t.Fatal("a changed selection counted as the same state")
	}
	if sameRestoreChoice(nil, new(true)) {
		t.Fatal("an omitted restore setting should not equal an explicit one")
	}
}
