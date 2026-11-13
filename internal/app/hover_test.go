package app

import (
	"image/color"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// actionInk reports whether the control labeled label paints anything over its
// own background: a hidden action is still mounted and keeps its room, so the
// only way to tell it apart is what reaches the frame.
func actionInk(t *testing.T, tt *ui.Tester, label string) bool {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q is not mounted: %q", label, tt.Texts())
	}
	im := tt.Image()
	bg := im.RGBAAt(max(0, int(r.X)-6), int(r.Y+r.H/2))
	t.Logf("rect=%v bg=%v", r, bg)
	bounds := im.Bounds()
	for y := max(bounds.Min.Y, int(r.Y)); y < min(bounds.Max.Y, int(r.Y+r.H)); y++ {
		for x := max(bounds.Min.X, int(r.X)); x < min(bounds.Max.X, int(r.X+r.W)); x++ {
			c := im.RGBAAt(x, y)
			if abs(int(c.R)-int(bg.R))+abs(int(c.G)-int(bg.G))+abs(int(c.B)-int(bg.B)) > 40 {
				return true
			}
		}
	}
	return false
}

// actionFace is the colour of a row action's own face, clear of its icon: a
// hidden action paints none, so the row's fill shows through.
func actionFace(t *testing.T, tt *ui.Tester, label string) (color.RGBA, color.RGBA) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q is not mounted: %q", label, tt.Texts())
	}
	im := tt.Image()
	return im.RGBAAt(int(r.X)+2, int(r.Y)+2), im.RGBAAt(max(0, int(r.X)-6), int(r.Y+r.H/2))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// The chosen capture view keeps its close action visible without the pointer:
// a row being worked in is not a row to hunt through with the mouse.
func TestChosenViewKeepsItsCloseAction(t *testing.T) {
	a := workspaceApp()
	a.views.views = []*captureView{{id: 1}, {id: 2, name: "Second"}}
	a.views.active = 0
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if !actionInk(t, tt, locales.CloseViewNamed("View 1")) {
		t.Fatal("the chosen view hides its close action")
	}
	if actionInk(t, tt, locales.CloseViewNamed("Second")) {
		t.Fatal("an unchosen view shows its close action without hover or focus")
	}
}

// The request open in the editor keeps its actions visible in the collection
// tree, so the row that says what is being edited also offers what can be done
// with it.
func TestOpenRequestKeepsItsActions(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	a.requestChoice = a.requests[0].key()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	// Put the keyboard focus in the search field: the chosen row is not the
	// focused row, and its actions must still be there.
	if err := tt.Click("Search collections"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	face, bg := actionFace(t, tt, locales.RequestActionsPrefix("List projects"))
	if face == bg {
		t.Fatalf("the open request hides its actions: face %v is the row's %v", face, bg)
	}
}

// Hovering a collection request row reveals its actions, and leaving the row
// puts them away again; the control keeps its room either way.
func TestRequestActionsFollowThePointer(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	row, ok := tt.Find("List projects")
	if !ok {
		t.Fatalf("the request row is missing: %q", tt.Texts())
	}
	label := locales.RequestActionsPrefix("List projects")
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	face, bg := actionFace(t, tt, label)
	if face == bg {
		t.Fatal("hovering the request row did not reveal its actions")
	}
	tt.Move(900, 700)
	tt.Frame()
	face, bg = actionFace(t, tt, label)
	if face != bg {
		t.Fatal("leaving the request row left its actions in sight")
	}
}
