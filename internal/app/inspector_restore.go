package app

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo"
)

// Detached inspector windows are session state, not captured traffic: the
// file holds only a target identity and a window frame. A flow is named by its
// debugKey, which survives row rebuilds and is independent of the capture IDs
// that restart at 1. Bodies, headers, URLs, notes and library secrets never
// take part in this format.
const (
	inspectorTargetFlow = "flow"

	inspectorWindowWidth     = 900
	inspectorWindowHeight    = 700
	inspectorWindowMinWidth  = 600
	inspectorWindowMinHeight = 420
)

type inspectorTarget struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type savedWindowBounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type savedInspectorWindow struct {
	Target inspectorTarget   `json:"target"`
	Bounds savedWindowBounds `json:"bounds"`
}

type savedInspectorWindows struct {
	Windows []savedInspectorWindow `json:"windows"`
}

func inspectorWindowsPath(root string) string {
	return filepath.Join(root, "inspector-windows.json")
}

func loadInspectorWindows(path string) (savedInspectorWindows, error) {
	var saved savedInspectorWindows
	data, err := store.ReadFile(path, store.InspectorWindows)
	if err != nil || data == nil {
		return saved, err
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return savedInspectorWindows{}, err
	}
	return saved, nil
}

// snapshotInspectorWindows builds the state the next launch reads. bounds is
// passed in so this step, and its content-free test, needs no native window.
func (a *app) snapshotInspectorWindows(bounds mygo.Rectangle) savedInspectorWindows {
	saved := savedInspectorWindows{Windows: []savedInspectorWindow{}}
	if !a.inspectorDetached {
		return saved
	}
	f, ok := a.current()
	if !ok || f.debugKey == "" {
		return saved
	}
	saved.Windows = append(saved.Windows, savedInspectorWindow{
		Target: inspectorTarget{Kind: inspectorTargetFlow, Key: f.debugKey},
		Bounds: savedWindowBounds{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height},
	})
	return saved
}

func (a *app) flushInspectorWindows() {
	if a.inspectorWindowsPath == "" {
		return
	}
	bounds := mygo.Rectangle{Width: inspectorWindowWidth, Height: inspectorWindowHeight}
	if a.inspectorWin != nil {
		bounds = a.inspectorWin.Bounds()
	}
	if err := saveInspectorWindows(a.inspectorWindowsPath, a.snapshotInspectorWindows(bounds)); err != nil {
		applog.For("app").Debug("detached inspector windows not saved", "err", err)
	}
}

// restoreInspectorWindows runs after the main window's first frame. Only a
// target that is still in the capture table reopens a window; a flow that did
// not survive the restart is dropped without a notice.
func (a *app) restoreInspectorWindows() {
	if a.inspectorWin != nil || a.inspectorWindowsPath == "" {
		return
	}
	saved, err := loadInspectorWindows(a.inspectorWindowsPath)
	if err != nil {
		applog.For("app").Debug("detached inspector windows not restored", "reason", "unreadable")
		return
	}
	opened := false
	for _, w := range saved.Windows {
		if w.Target.Kind != inspectorTargetFlow || w.Target.Key == "" {
			applog.For("app").Debug("skipping detached inspector", "reason", "unknown target")
			continue
		}
		i := slices.IndexFunc(a.flows, func(f flow) bool { return f.debugKey == w.Target.Key })
		if i < 0 {
			applog.For("app").Debug("skipping detached inspector", "target", w.Target.Key)
			continue
		}
		// The current app has one inspector window. Ignore further entries
		// rather than opening a second one over the first.
		if opened {
			return
		}
		bounds := mygo.Rectangle{
			X: w.Bounds.X, Y: w.Bounds.Y, Width: w.Bounds.Width, Height: w.Bounds.Height,
		}
		a.inspectCapture(a.flows[i].id)
		if a.win != nil {
			bounds = clampWindowBounds(bounds, mygo.Screen.Displays())
		}
		a.openInspectorWindowAt(bounds)
		opened = true
	}
}

// clampWindowBounds brings saved frame back onto a connected display, keeping
// the window's size when it fits. A rectangle on an unplugged monitor is
// placed on the display it overlaps most, or the nearest one when it overlaps
// none at all.
func clampWindowBounds(saved mygo.Rectangle, displays []mygo.Display) mygo.Rectangle {
	if len(displays) == 0 {
		return saved
	}
	d := displayForBounds(saved, displays)
	area := d.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		area = d.Bounds
	}
	if area.Width <= 0 || area.Height <= 0 {
		return saved
	}
	width := min(saved.Width, area.Width)
	height := min(saved.Height, area.Height)
	if width <= 0 {
		width = min(inspectorWindowWidth, area.Width)
	}
	if height <= 0 {
		height = min(inspectorWindowHeight, area.Height)
	}
	return mygo.Rectangle{
		X:      clampInt(saved.X, area.X, area.X+area.Width-width),
		Y:      clampInt(saved.Y, area.Y, area.Y+area.Height-height),
		Width:  width,
		Height: height,
	}
}

func displayForBounds(saved mygo.Rectangle, displays []mygo.Display) mygo.Display {
	best, bestOverlap, bestDistance := displays[0], 0, maxInt
	center := mygo.Point{X: saved.X + saved.Width/2, Y: saved.Y + saved.Height/2}
	for _, d := range displays {
		area := d.WorkArea
		if area.Width <= 0 || area.Height <= 0 {
			area = d.Bounds
		}
		if area.Width <= 0 || area.Height <= 0 {
			continue
		}
		right, bottom := saved.X+saved.Width, saved.Y+saved.Height
		overlap := max(0, min(right, area.X+area.Width)-max(saved.X, area.X)) *
			max(0, min(bottom, area.Y+area.Height)-max(saved.Y, area.Y))
		dx := max(area.X-center.X, 0, center.X-(area.X+area.Width))
		dy := max(area.Y-center.Y, 0, center.Y-(area.Y+area.Height))
		distance := dx*dx + dy*dy
		if overlap > 0 && (bestOverlap == 0 || overlap > bestOverlap) ||
			overlap == 0 && bestOverlap == 0 && distance < bestDistance {
			best, bestOverlap, bestDistance = d, overlap, distance
		}
	}
	return best
}

const maxInt = int(^uint(0) >> 1)

func clampInt(n, lo, hi int) int {
	return max(lo, min(n, hi))
}

// saveInspectorWindows is the test and quit path through the versioned file.
func saveInspectorWindows(path string, saved savedInspectorWindows) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return store.WriteFileVersioned(path, store.InspectorWindows, data, "")
}
