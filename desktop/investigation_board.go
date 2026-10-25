package main

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// The board keeps investigations on disk: named collections of immutable
// capture snapshots, each with a note. An investigation is the unit a person
// hands to someone else, so its evidence must not change when live traffic
// or the capture list does.
const (
	boardFileLimit      = 32 << 20
	boardInvestigations = 20
	boardItems          = 64
	boardPreviewLimit   = 512 << 10
)

type investigation struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Notes   string              `json:"notes,omitempty"`
	Created time.Time           `json:"created"`
	Items   []investigationItem `json:"items,omitempty"`
}

// investigationItem is one exchange as it was when it was added: the same
// lossless wire fields a saved session keeps, never a pointer to live traffic.
type investigationItem struct {
	ID     string      `json:"id"`
	Note   string      `json:"note,omitempty"`
	Source sessionFlow `json:"source"`
}

type investigationFile struct {
	Version        int              `json:"version"`
	Investigations []*investigation `json:"investigations"`
}

type investigationBoard struct {
	path        string
	items       []*investigation
	active      string
	selected    string
	picked      []string
	query       string
	percent     float32
	row         int
	list        ui.ListState
	deleting    bool
	reveal      bool
	dirty       bool
	lastSave    time.Time
	notesOpen   bool
	previewSide int    // 0 response, 1 request
	previewTab  [2]int // headers, body

	comparing  bool
	comparison captureAnalysis
	problem    string
	note       string
}

func (b *investigationBoard) current() *investigation {
	for _, inv := range b.items {
		if inv.ID == b.active {
			return inv
		}
	}
	if len(b.items) > 0 {
		b.active = b.items[0].ID
		return b.items[0]
	}
	b.active = ""
	return nil
}

// create starts an investigation and makes it the one on screen.
func (b *investigationBoard) create(title string) *investigation {
	inv := &investigation{
		ID:      uuid.NewString(),
		Title:   cmp.Or(strings.TrimSpace(title), "Untitled investigation"),
		Created: time.Now(),
	}
	b.items = append([]*investigation{inv}, b.items...)
	b.active, b.selected = inv.ID, ""
	b.picked, b.comparing = nil, false
	b.problem = ""
	if err := b.save(); err != nil {
		b.problem = err.Error()
	}
	return inv
}

// deleteActive forgets the investigation on screen. Its evidence is not
// recoverable afterwards, so the view asks first.
func (b *investigationBoard) deleteActive() {
	inv := b.current()
	if inv == nil {
		return
	}
	b.items = slices.DeleteFunc(b.items, func(other *investigation) bool { return other.ID == inv.ID })
	b.active, b.selected = "", ""
	b.picked, b.comparing = nil, false
	b.comparison = captureAnalysis{}
	if len(b.items) > 0 {
		b.active = b.items[0].ID
	}
	if err := b.save(); err != nil {
		b.problem = err.Error()
	}
}

// add snapshots f into the investigation on screen, starting one when the
// board is empty. The snapshot keeps the bodies, headers and connection
// evidence as they are now; later live traffic never changes it.
func (b *investigationBoard) add(f flow) (*investigation, error) {
	inv := b.current()
	if inv == nil {
		if len(b.items) >= boardInvestigations {
			return nil, fmt.Errorf("keep at most %d investigations", boardInvestigations)
		}
		inv = b.create("Untitled investigation")
	}
	if len(inv.Items) >= boardItems {
		return nil, fmt.Errorf("an investigation holds at most %d exchanges", boardItems)
	}
	item := investigationItem{ID: uuid.NewString(), Source: snapshotDebugFlow(f)}
	data, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if b.retained()+len(data) > boardFileLimit {
		return nil, errors.New("the investigation board would exceed 32 MiB; remove exchanges first")
	}
	inv.Items = append(inv.Items, item)
	b.selected, b.comparing = item.ID, false
	if err := b.save(); err != nil {
		inv.Items = inv.Items[:len(inv.Items)-1]
		return nil, err
	}
	return inv, nil
}

func (b *investigationBoard) find(id string) (*investigation, *investigationItem) {
	for _, inv := range b.items {
		for i := range inv.Items {
			if inv.Items[i].ID == id {
				return inv, &inv.Items[i]
			}
		}
	}
	return nil, nil
}

func (b *investigationBoard) removeItem(id string) {
	inv, _ := b.find(id)
	if inv == nil {
		return
	}
	inv.Items = slices.DeleteFunc(inv.Items, func(item investigationItem) bool { return item.ID == id })
	b.picked = slices.DeleteFunc(b.picked, func(other string) bool { return other == id })
	if b.selected == id {
		b.selected = ""
	}
	b.comparing = false
	b.comparison = captureAnalysis{}
	if err := b.save(); err != nil {
		b.problem = err.Error()
	}
}

func (b *investigationBoard) chosen(id string) bool { return slices.Contains(b.picked, id) }

func (b *investigationBoard) choose(id string, on bool) {
	if on {
		if !slices.Contains(b.picked, id) {
			b.picked = append(b.picked, id)
		}
	} else {
		b.picked = slices.DeleteFunc(b.picked, func(other string) bool { return other == id })
	}
	b.comparing = false
	b.comparison = captureAnalysis{}
}

// visible lists the investigation's exchanges matching the board's search,
// in the order they were added.
func (b *investigationBoard) visible(inv *investigation) []investigationItem {
	query := strings.ToLower(strings.TrimSpace(b.query))
	if query == "" {
		return inv.Items
	}
	return slices.DeleteFunc(slices.Clone(inv.Items), func(item investigationItem) bool {
		f := item.Source
		for _, value := range []string{f.Method, f.URL, fmt.Sprint(f.Status), f.Note, item.Note, f.ConnectionError, f.ConnectionStage} {
			if strings.Contains(strings.ToLower(value), query) {
				return false
			}
		}
		return true
	})
}

// pickedItems resolves the chosen IDs in the order they were chosen, so a
// comparison keeps its left and right sides.
func (b *investigationBoard) pickedItems() []investigationItem {
	out := make([]investigationItem, 0, len(b.picked))
	for _, id := range b.picked {
		if _, item := b.find(id); item != nil {
			out = append(out, *item)
		}
	}
	return out
}

func (b *investigationBoard) retained() int {
	data, err := json.Marshal(investigationFile{Version: 1, Investigations: b.items})
	if err != nil {
		return boardFileLimit + 1
	}
	return len(data)
}

// mark records a text edit that the frame loop writes shortly after, so
// typing a title does not rewrite the whole board on every keystroke.
func (b *investigationBoard) mark() { b.dirty = true }

// persist writes a marked edit at most twice a second.
func (b *investigationBoard) persist() {
	if !b.dirty || time.Since(b.lastSave) < 500*time.Millisecond {
		return
	}
	b.flush()
}

// flush writes a marked edit now, as leaving the app does.
func (b *investigationBoard) flush() {
	if !b.dirty {
		return
	}
	b.dirty = false
	if err := b.save(); err != nil {
		b.problem = err.Error()
	}
}

func (b *investigationBoard) save() error {
	b.dirty, b.lastSave = false, time.Now()
	if b.path == "" {
		return nil
	}
	data, err := json.Marshal(investigationFile{Version: 1, Investigations: b.items})
	if err != nil {
		return err
	}
	if len(data) > boardFileLimit {
		return errors.New("the investigation board exceeds 32 MiB; remove exchanges first")
	}
	return writePrivateFile(b.path, data)
}

// load reads the board. A missing file is an empty board; a damaged one is
// reported and left alone, never overwritten with what could be read.
func (b *investigationBoard) load(path string) error {
	b.path = path
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, boardFileLimit+1))
	if err != nil {
		return err
	}
	if len(data) > boardFileLimit {
		return errors.New("the investigation board exceeds 32 MiB")
	}
	var stored investigationFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return errors.New("invalid investigation board JSON")
	}
	if stored.Version != 1 {
		return fmt.Errorf("unsupported investigation board version %d", stored.Version)
	}
	b.items = nil
	for i, inv := range stored.Investigations {
		if i >= boardInvestigations {
			break
		}
		if inv == nil {
			continue
		}
		if inv.ID == "" {
			inv.ID = uuid.NewString()
		}
		if inv.Title == "" {
			inv.Title = "Untitled investigation"
		}
		if len(inv.Items) > boardItems {
			inv.Items = inv.Items[:boardItems]
		}
		for j := range inv.Items {
			if inv.Items[j].ID == "" {
				inv.Items[j].ID = uuid.NewString()
			}
		}
		b.items = append(b.items, inv)
	}
	if len(b.items) > 0 {
		b.active = b.items[0].ID
	}
	return nil
}

// boardItemLabel names one exchange in evidence and comparisons.
func boardItemLabel(item investigationItem) string {
	f := item.Source
	return fmt.Sprintf("%s %s", cmp.Or(f.Method, "REQUEST"), cmp.Or(f.URL, f.Path))
}

// boardBody is a bounded, readable preview of one side of an exchange.
func boardBody(body string) string {
	if len(body) > boardPreviewLimit {
		body = body[:boardPreviewLimit]
	}
	limited := len(body) >= boardPreviewLimit
	value := jsontext.Value(body)
	if value.Indent(jsontext.WithIndent("  ")) == nil {
		body = string(value)
	}
	if limited {
		body += "\n\n… preview limited to 512 KiB"
	}
	return body
}

// boardEvidence turns the chosen exchanges into the bundle the review flow
// exports: one source per exchange, so labels stay attached to their data.
func boardEvidence(inv *investigation, items []investigationItem) ([]byte, error) {
	sources := make([]EvidenceSource, 0, len(items))
	picks := make([]investigationPick, 0, len(items))
	for i, item := range items {
		sources = append(sources, EvidenceSource{
			Label:   boardItemLabel(item),
			Notes:   item.Note,
			Session: sessionFile{Version: 1, Flows: []sessionFlow{item.Source}},
		})
		picks = append(picks, investigationPick{source: i, row: 0})
	}
	title := cmp.Or(strings.TrimSpace(inv.Title), "Untitled investigation")
	return buildEvidence(sources, picks, title, inv.Notes)
}
