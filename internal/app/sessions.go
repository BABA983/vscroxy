package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo/ui/editor"
)

// Sessions keep immutable exchanges in ordinary session files. Only annotations
// are editable here; opening a file never replaces or joins the live capture.
// Startup reads only each file's title and size; payloads load when a session
// opens and are dropped again when its tab closes.
type savedSession struct {
	key, path       string
	data            sessionFile
	count           int
	loaded, loading bool
	loadDone        chan sessionLoad
	saveErr         string
	open            bool
	picked          int
	query           string
	side, tab       int
	reveal          bool
	reader          *editor.State
	jobs            chan sessionFile
	results         chan error
	writer          sync.WaitGroup
	closed          bool
}

type sessionLoad struct {
	file sessionFile
	err  error
}

type sessionWorkspace struct {
	dir, active, problem string
	items                []*savedSession
	left                 *flow
	leftLabel            string
	comparing            bool
	comparison           captureAnalysis
}

func (s *sessionWorkspace) current() *savedSession {
	for _, item := range s.items {
		if item.key == s.active {
			return item
		}
	}
	return nil
}

func (s *sessionWorkspace) load(dir string) error {
	s.dir = dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var problem error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			problem = errors.Join(problem, err)
			continue
		}
		// Flows decode as empty structs: their contents are skipped.
		var header struct {
			Version int        `json:"version"`
			Title   string     `json:"title"`
			Created time.Time  `json:"created"`
			Flows   []struct{} `json:"flows"`
		}
		if err := json.Unmarshal(data, &header); err != nil || header.Version != 1 {
			problem = errors.Join(problem, fmt.Errorf("%s: not a version 1 session", entry.Name()))
			continue
		}
		s.items = append(s.items, &savedSession{key: entry.Name(), path: path, count: len(header.Flows),
			data: sessionFile{Version: 1, Title: header.Title, Created: header.Created}})
	}
	slices.SortStableFunc(s.items, func(a, b *savedSession) int { return b.data.Created.Compare(a.data.Created) })
	return problem
}

func (s *sessionWorkspace) keep(file sessionFile) *savedSession {
	key := uuid.New().String() + ".json"
	item := &savedSession{key: key, data: file, count: len(file.Flows), loaded: true, open: true}
	if s.dir != "" {
		item.path = filepath.Join(s.dir, key)
	}
	s.items = append([]*savedSession{item}, s.items...)
	s.active, s.comparing = key, false
	return item
}

// One ordered writer per file coalesces annotation saves without letting an
// older snapshot overwrite a newer one. Payload maps and bytes are immutable;
// only the flow slice containing annotations needs its own copy.
func (item *savedSession) persist(notify func()) {
	if item.path == "" || item.closed || !item.loaded {
		return
	}
	if item.jobs == nil {
		jobs, results, path := make(chan sessionFile, 1), make(chan error, 1), item.path
		item.jobs, item.results = jobs, results
		item.writer.Go(func() {
			for state := range jobs {
				data, err := json.Marshal(state)
				if err == nil {
					err = store.WriteFile(path, data)
				}
				select {
				case <-results:
				default:
				}
				results <- err
				if notify != nil {
					notify()
				}
			}
		})
	}
	state := item.data
	state.Flows = slices.Clone(state.Flows)
	select {
	case <-item.jobs:
	default:
	}
	item.jobs <- state
}

// stop lets the writer finish what it was given; nothing is written after.
func (item *savedSession) stop() {
	if !item.closed && item.jobs != nil {
		close(item.jobs)
		item.writer.Wait()
	}
	item.closed = true
}

func (s *sessionWorkspace) close() {
	for _, item := range s.items {
		item.stop()
	}
}

// remove deletes the session's file after any pending save has landed, so a
// late write cannot recreate it.
func (s *sessionWorkspace) remove(key string) error {
	i := slices.IndexFunc(s.items, func(item *savedSession) bool { return item.key == key })
	if i < 0 {
		return nil
	}
	item := s.items[i]
	item.stop()
	if item.path != "" {
		if err := os.Remove(item.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	s.items = slices.Delete(s.items, i, i+1)
	if s.active == key {
		s.active = ""
		for _, next := range s.items {
			if next.open {
				s.active = next.key
				break
			}
		}
	}
	return nil
}

// closeTab keeps the saved session but releases its payloads.
func (item *savedSession) closeTab() {
	item.open, item.reader, item.picked = false, nil, 0
	if item.path != "" && !item.loading {
		item.data.Flows, item.data.Frames, item.loaded = nil, nil, false
	}
}

func (a *app) selectSession(key string) {
	s := &a.sessions
	s.active, s.comparing = key, false
	item := s.current()
	if item == nil {
		return
	}
	item.open = true
	a.page, a.revealGroup = pageSession, "sessions"
	if item.loaded || item.loading {
		return
	}
	item.loading, item.loadDone = true, make(chan sessionLoad, 1)
	go func(path string, done chan sessionLoad) {
		file, err := readSessionFile(path)
		done <- sessionLoad{file, err}
		a.invalidate()
	}(item.path, item.loadDone)
}

// checkpointSessions takes finished loads and saves on the UI thread.
func (a *app) checkpointSessions() {
	for _, item := range a.sessions.items {
		select {
		case done := <-item.loadDone:
			item.loading, item.loadDone = false, nil
			if done.err != nil {
				item.saveErr = "Open session: " + done.err.Error()
				break
			}
			item.data, item.loaded, item.count = done.file, true, len(done.file.Flows)
			if !item.open {
				item.closeTab()
			}
		default:
		}
		select {
		case err := <-item.results:
			if err != nil {
				item.saveErr = "Save session: " + err.Error()
				a.fail("Save session", err)
			} else {
				item.saveErr = ""
			}
		default:
		}
	}
}

func (a *app) keepSession(file sessionFile) {
	item := a.sessions.keep(file)
	item.persist(a.invalidate)
	a.selectSession(item.key)
}

func (a *app) saveEvidence(f flow) {
	// Resolve spill payloads now, before live retention can remove them.
	file := sessionFrom([]flow{f}, map[int][]frame{f.id: slices.Clone(a.frames[f.id])})
	file.Title = f.method + " " + f.host + f.path
	file.Created = time.Now()
	a.keepSession(file)
}

func (a *app) confirmDeleteSession(item *savedSession) {
	a.deleting = deletion{open: true, session: item.key, name: "session “" + item.data.Title + "”",
		detail: fmt.Sprintf("Its %d %s and annotations are removed.", item.count, plural(item.count, "exchange", "exchanges"))}
}

func (f sessionFlow) flow() flow {
	req, res := f.ReqBody, f.ResBody
	if len(f.ReqBytes) > 0 {
		req = string(f.ReqBytes)
	}
	if len(f.ResBytes) > 0 {
		res = string(f.ResBytes)
	}
	return flow{
		id: f.ID, replayOf: f.ReplayOf, actions: f.Actions, pinned: f.Pinned,
		debugKey: f.DebugKey, debugRun: f.DebugRun,
		method: f.Method, scheme: f.Scheme, host: f.Host, path: f.Path, url: f.URL,
		status: f.Status, bytes: f.Bytes, millis: f.Millis,
		timing: f.Timing, wait: f.Wait, download: f.Download,
		reqHeaders: f.ReqHeaders, resHeaders: f.ResHeaders, reqBody: req, resBody: res,
		reqSize: max(f.ReqSize, len(req)), resSize: max(f.ResSize, len(res)),
		reqError: f.ReqError, resError: f.ResError, reqHeader: f.ReqHeader, respHeader: f.RespHeader,
		reqTrailer: f.ReqTrailer, resTrailer: f.ResTrailer, resProto: f.ResProto,
		conn: conn{proto: f.ReqProto, log: f.ConnectionLog, error: f.ConnectionError, errorStage: f.ConnectionStage, remote: f.Remote},
		dns:  f.HostsMapped, hostProfile: f.HostProfile,
		at: f.At, kind: f.Kind, color: f.Color, note: f.Note, streamEnd: f.StreamEnd,
	}
}

func (f sessionFlow) matches(notes, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, value := range []string{f.Method, f.URL, fmt.Sprint(f.Status), f.Note, notes, f.ReqError, f.ResError, f.ConnectionError, f.ConnectionStage} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
