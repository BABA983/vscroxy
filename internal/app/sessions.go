package app

import (
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/egoist/mygo/ui/editor"
)

// Sessions keep immutable exchanges in ordinary session files. Only annotations
// are editable here; opening a file never replaces or joins the live capture.
type savedSession struct {
	key, path string
	data      sessionFile
	open      bool
	picked    int
	query     string
	side, tab int
	reveal    bool
	reader    *editor.State
	jobs      chan sessionFile
	results   chan error
	writer    sync.WaitGroup
	closed    bool
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
	problem := migrateInvestigations(filepath.Join(filepath.Dir(dir), "investigations.json"), dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
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
		var file sessionFile
		if err := json.Unmarshal(data, &file); err != nil {
			problem = errors.Join(problem, fmt.Errorf("%s: invalid session JSON", entry.Name()))
			continue
		}
		if file.Version != 1 {
			problem = errors.Join(problem, fmt.Errorf("%s: unsupported session version", entry.Name()))
			continue
		}
		s.items = append(s.items, &savedSession{key: entry.Name(), path: path, data: file})
	}
	slices.SortStableFunc(s.items, func(a, b *savedSession) int { return b.data.Created.Compare(a.data.Created) })
	return problem
}

func (s *sessionWorkspace) keep(file sessionFile) *savedSession {
	key := uuid.New().String() + ".json"
	item := &savedSession{key: key, data: file, open: true}
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
	if item.path == "" || item.closed {
		return
	}
	if item.jobs == nil {
		jobs, results, path := make(chan sessionFile, 1), make(chan error, 1), item.path
		item.jobs, item.results = jobs, results
		item.writer.Go(func() {
			for state := range jobs {
				data, err := json.Marshal(state)
				if err == nil {
					err = writePrivateFile(path, data)
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

func (s *sessionWorkspace) close() {
	for _, item := range s.items {
		if !item.closed && item.jobs != nil {
			close(item.jobs)
			item.writer.Wait()
		}
		item.closed = true
	}
}

func (a *app) selectSession(key string) {
	s := &a.sessions
	s.active, s.comparing = key, false
	if item := s.current(); item != nil {
		item.open = true
		a.page, a.revealGroup = pageSession, "sessions"
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

// Migration is restartable and never rewrites the legacy file. Each complete
// session is published before the marker; a failed write leaves the old board
// available for retry. Deterministic names avoid duplicate migrations.
func migrateInvestigations(path, dir string) error {
	marker := filepath.Join(dir, ".investigations-migrated")
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy investigationFile
	if json.Unmarshal(data, &legacy) != nil || legacy.Version != 1 {
		return errors.New("cannot migrate investigations: invalid format or version; original file left untouched")
	}
	for i, inv := range legacy.Investigations {
		if inv == nil {
			continue
		}
		file := sessionFile{Version: 1, Title: inv.Title, Notes: inv.Notes, Created: inv.Created}
		ids := map[int]bool{}
		for j, item := range inv.Items {
			f := item.Source
			// Separate captures commonly reuse numeric IDs. Preserve provenance
			// explicitly while giving the combined session unique row IDs.
			f.OriginalID = f.ID
			if ids[f.ID] {
				f.ID = j + 1
				for ids[f.ID] {
					f.ID++
				}
			}
			ids[f.ID] = true
			f.Note = strings.TrimSpace(f.Note + "\n" + item.Note)
			file.Flows = append(file.Flows, f)
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", i, inv.ID)))
		target := filepath.Join(dir, fmt.Sprintf("migrated-%x.json", digest[:12]))
		if prior, err := os.ReadFile(target); err == nil {
			// A previous partial migration may have already published this file.
			var existing sessionFile
			if json.Unmarshal(prior, &existing) != nil || existing.Version != 1 || len(existing.Flows) != len(file.Flows) {
				return fmt.Errorf("cannot finish migration: %s is incomplete; original investigation left untouched", filepath.Base(target))
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		encoded, err := json.Marshal(file)
		if err != nil {
			return err
		}
		if err := writePrivateFile(target, encoded); err != nil {
			return err
		}
	}
	return writePrivateFile(marker, []byte("1\n"))
}
