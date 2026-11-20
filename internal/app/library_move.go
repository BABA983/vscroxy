package app

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/natural"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

type libraryKind uint8

const (
	requestItem libraryKind = iota
	folderItem
)

// libraryDrag is one thing the tree can drag: a request keyed by its stable
// identity, or a folder keyed by its path. A single type lets one drop target
// accept rows of both kinds and lets the app reject the moves MyGo cannot
// know are impossible.
type libraryDrag struct {
	kind libraryKind
	id   string
}

func (d libraryDrag) valid() bool { return d.id != "" }

func (d libraryDrag) key() string {
	if d.kind == folderItem {
		return "folder:" + d.id
	}
	return "request:" + d.id
}

// libraryDrop is inside parent, before the sibling keyed by before; an empty
// before appends at the end of that parent's children. A folder row uses its
// middle as "inside", its top and bottom as "before this" and "after this".
type libraryDrop struct {
	parent string
	before libraryDrag
}

type libraryMove struct {
	item libraryDrag
	to   libraryDrop
}

func parentPath(p string) string {
	parent, _, found := strings.CutLast(p, "/")
	if !found {
		return ""
	}
	return parent
}

func nameOfPath(p string) string {
	_, name, found := strings.CutLast(p, "/")
	if found {
		return name
	}
	return p
}

// libraryPathsOf walks the collections the requests and folder settings
// mention, including the ancestors between them. A folder with no settings
// still exists here, because it is a row the tree can drag.
func libraryPathsOf(requests []savedRequest, folders []request.Folder) map[string]bool {
	paths := map[string]bool{}
	add := func(p string) {
		for p != "" {
			paths[p] = true
			p = parentPath(p)
		}
	}
	for _, r := range requests {
		add(r.Collection)
	}
	for _, f := range folders {
		add(f.Path)
	}
	return paths
}

// folderOrder returns a parent's child folders in their saved order, with
// folders new to it placed by natural name after those that already have a
// place. The tree and the drop targets use the same order.
func folderOrder(parent string, paths map[string]bool, manual map[string][]string) []string {
	rank := make(map[string]int)
	if names, ok := manual[parent]; ok {
		for i, name := range names {
			rank[parentPathOfName(parent, name)] = i
		}
	}
	var children []string
	for p := range paths {
		if parentPath(p) == parent {
			children = append(children, p)
		}
	}
	slices.SortStableFunc(children, func(x, y string) int {
		xRank, xKnown := rank[x]
		yRank, yKnown := rank[y]
		switch {
		case xKnown && yKnown && xRank != yRank:
			return xRank - yRank
		case xKnown != yKnown:
			if xKnown {
				return -1
			}
			return 1
		}
		return natural.Compare(nameOfPath(x), nameOfPath(y))
	})
	return children
}

func parentPathOfName(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// librarySiblings returns the rows under parent in tree order: folders first,
// then the requests in their saved order. A drop before one of these means
// "put it where this row is".
func (a *app) librarySiblings(parent string, paths map[string]bool) []libraryDrag {
	var out []libraryDrag
	for _, p := range folderOrder(parent, paths, a.library.folderOrder) {
		out = append(out, libraryDrag{kind: folderItem, id: p})
	}
	for _, r := range a.requests {
		if r.Collection == parent {
			out = append(out, libraryDrag{kind: requestItem, id: r.key()})
		}
	}
	return out
}

func (a *app) libraryItemName(d libraryDrag) string {
	if d.kind == folderItem {
		return d.id
	}
	for _, r := range a.requests {
		if r.key() == d.id {
			return r.Name
		}
	}
	return d.id
}

func (a *app) savedRequest(key string) (savedRequest, bool) {
	for _, r := range a.requests {
		if r.key() == key {
			return r, true
		}
	}
	return savedRequest{}, false
}

// canMoveFolderInto refuses a folder's own subtree: moving it there would
// leave no path back to where it was.
func canMoveFolderInto(from, parent string) bool {
	_, base, found := strings.CutLast(from, "/")
	if !found {
		base = from
	}
	to := base
	if parent != "" {
		to = parent + "/" + base
	}
	return to != from && !within(to, from)
}

// libraryMoveBack is the inverse position of a move already made. Undo uses
// it only to tell whether a notice still points at the move's current result.
type libraryMoveBack struct {
	item        libraryDrag
	to          libraryDrop
	afterParent string
}

func (a *app) moveBackFor(m libraryMove) libraryMoveBack {
	origin := m.to.parent
	back := libraryMoveBack{item: m.item, afterParent: m.to.parent}
	switch m.item.kind {
	case folderItem:
		origin = parentPath(m.item.id)
		back.item.id = parentPathOfName(m.to.parent, nameOfPath(m.item.id))
	case requestItem:
		if r, ok := a.savedRequest(m.item.id); ok {
			origin = r.Collection
		}
	}
	back.to.parent = origin
	paths := libraryPathsOf(a.requests, a.folders)
	siblings := a.librarySiblings(origin, paths)
	if at := slices.IndexFunc(siblings, func(x libraryDrag) bool { return x.key() == m.item.key() }); at >= 0 && at+1 < len(siblings) {
		back.to.before = siblings[at+1]
	}
	return back
}

// moveBackValid only lets an Undo act on the item where this move left it.
// A later move or delete of that item makes the old notice stale, while an
// edit to another request leaves the inverse move available.
func (a *app) moveBackValid(back libraryMoveBack) bool {
	if !back.item.valid() {
		return false
	}
	if back.item.kind == folderItem {
		if !canMoveFolderInto(back.item.id, back.to.parent) || parentPath(back.item.id) != back.afterParent {
			return false
		}
		return libraryPathsOf(a.requests, a.folders)[back.item.id]
	}
	r, ok := a.savedRequest(back.item.id)
	return ok && r.Collection == back.afterParent
}

func (a *app) moveLibrary(m libraryMove) bool {
	if !m.item.valid() || m.parentInsideItself() {
		a.warn(locales.MoveIntoItself())
		return false
	}
	back := a.moveBackFor(m)
	before, err := a.snapshotUndoLibrary()
	if err != nil {
		a.fail(locales.MoveToEllipsis(), err)
		return false
	}
	if !a.performMove(m) {
		return false
	}
	after, err := a.snapshotUndoLibrary()
	if err != nil {
		a.fail(locales.MoveToEllipsis(), err)
		return true
	}
	name := a.libraryItemName(m.item)
	if m.item.kind == folderItem {
		name = parentPathOfName(m.to.parent, nameOfPath(m.item.id))
	}
	parent := m.to.parent
	label := locales.MovedTo(name, cmp.Or(parent, locales.NoCollection()))
	handle := a.recordUndo(undoRecord{
		code: "library.move", label: label, before: before, after: after,
		notice: func(bool) { a.done(locales.MoveUndone()) },
	})
	a.undoNotice(label, handle, func() bool { return a.moveBackValid(back) })
	return true
}

// performMove makes the library change without adding a notice, for both a
// drop and the Undo that moves the same item back.
func (a *app) performMove(m libraryMove) bool {
	if !m.item.valid() || m.parentInsideItself() {
		return false
	}
	var err error
	if m.item.kind == folderItem {
		_, base, found := strings.CutLast(m.item.id, "/")
		if !found {
			base = m.item.id
		}
		to := base
		if m.to.parent != "" {
			to = m.to.parent + "/" + base
		}
		err = a.moveFolderTo(m.item.id, to, m.to.before, true)
	} else {
		err = a.moveSavedRequest(m.item.id, m.to.parent, m.to.before)
	}
	if err != nil {
		a.fail(locales.MoveToEllipsis(), err)
		return false
	}
	return true
}

func (m libraryMove) parentInsideItself() bool {
	return m.item.kind == folderItem && !canMoveFolderInto(m.item.id, m.to.parent)
}

// applyPendingMove runs after the frame that collected the drop, never
// during a build: writing the library removes and re-adds tree rows.
func (a *app) applyPendingMove() {
	if a.pendingMove == nil {
		return
	}
	m := *a.pendingMove
	a.pendingMove = nil
	if a.moveLibrary(m) {
		*a.collectionShown(m.to.parent) = true
	}
}

// moveSavedRequest files key under parent at the drop's place. Every tab
// holding it, and every closed tab that can still be reopened, follows.
func (a *app) moveSavedRequest(key, parent string, before libraryDrag) error {
	at := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.key() == key })
	if at < 0 {
		return errors.New("the request is no longer in the library")
	}
	r := a.requests[at]
	from := r.Collection
	parent = cleanPath(parent)
	if slices.ContainsFunc(a.requests, func(other savedRequest) bool {
		return other.key() != key && other.Collection == parent && other.Name == r.Name
	}) {
		return fmt.Errorf("“%s” already exists in %q", r.Name, parent)
	}
	requests := slices.Delete(slices.Clone(a.requests), at, at+1)
	r.Collection = parent
	to := len(requests)
	switch {
	case before.kind == requestItem && before.id != "":
		if at := slices.IndexFunc(requests, func(x savedRequest) bool { return x.key() == before.id }); at >= 0 {
			to = at
		}
	case before.kind == folderItem && before.id != "":
		// Folders are drawn above a parent's requests, so a folder as the
		// boundary means before its first request.
		for i, x := range requests {
			if x.Collection == parent {
				to = i
				break
			}
		}
	default:
		for i, x := range requests {
			if x.Collection == parent {
				to = i + 1
			}
		}
	}
	requests = slices.Insert(requests, to, r)
	if err := a.writeLibrary(requests, a.folders); err != nil {
		return err
	}
	for _, d := range a.documents {
		if d.savedKey != key {
			continue
		}
		if d.baseline.Collection == from {
			d.baseline.Collection = parent
		}
		if d.composeCollection == from {
			d.composeCollection = parent
		}
	}
	for _, id := range slices.Collect(a.closedDocuments().Keys()) {
		entry, _ := a.closedDocuments().Get(id)
		if entry.savedKey != key || entry.baseline.Collection != from {
			continue
		}
		if entry.draft.Collection == from {
			entry.draft.Collection = parent
		}
		entry.baseline.Collection = parent
		if entry.doc.composeCollection == from {
			entry.doc.composeCollection = parent
		}
		if entry.doc.baseline.Collection == from {
			entry.doc.baseline.Collection = parent
		}
		a.closedDocuments().Set(id, entry)
	}
	return nil
}

// moveFolderTo moves a collection or folder to to, rewriting its subtree's
// requests, settings, open tabs and closed-tab snapshots the way a folder
// rename does. before places it among the destination's folders.
func (a *app) moveFolderTo(from, to string, before libraryDrag, keepOrder bool) error {
	from, to = cleanPath(from), cleanPath(to)
	if from == "" || to == "" || from == to {
		return nil
	}
	if parentPath(from) == parentPath(to) && nameOfPath(from) == nameOfPath(to) {
		return nil
	}
	if within(to, from) {
		return errors.New(locales.MoveIntoItself())
	}
	under := func(p, root string) bool { return within(p, root) }
	moved := func(p string) string { return to + strings.TrimPrefix(p, from) }
	for _, r := range a.requests {
		if under(r.Collection, to) {
			return fmt.Errorf("“%s” already exists", to)
		}
	}
	for _, f := range a.folders {
		if under(f.Path, to) {
			return fmt.Errorf("“%s” already exists", to)
		}
	}
	requests, folders := slices.Clone(a.requests), slices.Clone(a.folders)
	for i := range requests {
		if under(requests[i].Collection, from) {
			requests[i].Collection = moved(requests[i].Collection)
		}
	}
	for i := range folders {
		if under(folders[i].Path, from) {
			folders[i].Path = moved(folders[i].Path)
		}
	}
	folders = reorderMovedFolders(folders, to, before)
	if keepOrder {
		a.setFolderOrder(requests, folders, parentPath(to), to, before)
	}
	if err := a.writeLibrary(requests, folders); err != nil {
		return err
	}
	a.moveFolderCurrent(from, to)
	a.followFolderPath(from, to)
	return nil
}

// setFolderOrder records a drag's choice as the parent's manual order. Other
// direct children keep their current order, with newly added ones sorted by
// name; the next save writes the list into the parent's folder.json.
func (a *app) setFolderOrder(requests []savedRequest, folders []request.Folder, parent, to string, before libraryDrag) {
	paths := libraryPathsOf(requests, folders)
	names := make([]string, 0, len(paths))
	for _, p := range folderOrder(parent, paths, a.library.folderOrder) {
		names = append(names, nameOfPath(p))
	}
	moved := nameOfPath(to)
	if at := slices.Index(names, moved); at >= 0 {
		names = slices.Delete(names, at, at+1)
	}
	into := len(names)
	if before.kind == folderItem && before.id != "" {
		if at := slices.Index(names, nameOfPath(before.id)); at >= 0 {
			into = at
		}
	}
	names = slices.Insert(names, into, moved)
	if a.library.folderOrder == nil {
		a.library.folderOrder = map[string][]string{}
	}
	a.library.folderOrder[parent] = names
}

// reorderMovedFolders keeps the moved subtree next to its new path in the
// folders slice, so settings saves do not scatter its entries again.
func reorderMovedFolders(folders []request.Folder, to string, before libraryDrag) []request.Folder {
	destination := parentPath(to)
	// A folder with no settings still needs a folder.json at its new path
	// after a move, or nothing on disk would stand for it.
	if !slices.ContainsFunc(folders, func(f request.Folder) bool { return f.Path == to }) {
		folders = append(folders, request.Folder{Path: to})
	}
	var moving, kept []request.Folder
	for _, f := range folders {
		if within(f.Path, to) {
			moving = append(moving, f)
		} else {
			kept = append(kept, f)
		}
	}
	if len(moving) == 0 {
		moving = append(moving, request.Folder{Path: to})
	}
	children := map[string]bool{}
	for _, f := range kept {
		if parentPath(f.Path) == destination {
			children[f.Path] = true
		}
	}
	toIndex := len(kept)
	if before.kind == folderItem && before.id != "" {
		if at := slices.IndexFunc(kept, func(f request.Folder) bool { return f.Path == before.id }); at >= 0 {
			toIndex = at
		}
	} else {
		last := -1
		for i, f := range kept {
			for child := range children {
				if within(f.Path, child) {
					last = max(last, i)
				}
			}
		}
		if last >= 0 {
			toIndex = last + 1
		}
	}
	out := make([]request.Folder, 0, len(folders))
	out = append(out, kept[:toIndex]...)
	out = append(out, moving...)
	out = append(out, kept[toIndex:]...)
	return out
}

// followFolderPath rewrites every in-memory reference to a folder's old
// path, including the closed tabs that reopen against the saved request.
func (a *app) followFolderPath(from, to string) {
	under := func(p string) bool { return within(p, from) }
	moved := func(p string) string { return to + strings.TrimPrefix(p, from) }
	for _, d := range a.documents {
		if under(d.composeCollection) {
			d.composeCollection = moved(d.composeCollection)
		}
		if d.savedKey != "" && under(d.baseline.Collection) {
			d.baseline.Collection = moved(d.baseline.Collection)
		}
	}
	for _, id := range slices.Collect(a.closedDocuments().Keys()) {
		entry, _ := a.closedDocuments().Get(id)
		changed := false
		if under(entry.draft.Collection) {
			entry.draft.Collection = moved(entry.draft.Collection)
			changed = true
		}
		if under(entry.baseline.Collection) {
			entry.baseline.Collection = moved(entry.baseline.Collection)
			changed = true
		}
		if under(entry.doc.composeCollection) {
			entry.doc.composeCollection = moved(entry.doc.composeCollection)
			changed = true
		}
		if under(entry.doc.baseline.Collection) {
			entry.doc.baseline.Collection = moved(entry.doc.baseline.Collection)
			changed = true
		}
		if !changed {
			continue
		}
		entry.doc = entry.doc.detachedCopy()
		a.closedDocuments().Set(id, entry)
	}
	for _, p := range slices.Collect(maps.Keys(a.collectionOpen)) {
		if under(p) {
			a.collectionOpen[moved(p)] = a.collectionOpen[p]
			delete(a.collectionOpen, p)
		}
	}
	if under(a.runner.collection) {
		a.runner.collection = moved(a.runner.collection)
		a.runner.load(a.requests)
	}
}

// moveToState is the keyboard and menu route to the same move as a drag.
type moveToState struct {
	open        bool
	item        libraryDrag
	destination string
}

func (a *app) moveToChoices() []string {
	paths := libraryPathsOf(a.requests, a.folders)
	var out []string
	for p := range paths {
		if a.moveToAllowed(p) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, natural.Compare)
	return out
}

func (a *app) moveToAllowed(parent string) bool {
	return a.moveTo.item.kind != folderItem || canMoveFolderInto(a.moveTo.item.id, parent)
}

func (a *app) libraryMoveTarget() libraryDrag {
	if a.requestChoice != "" && strings.HasPrefix(a.requestChoice, "saved:") {
		if _, ok := a.savedRequest(a.requestChoice); ok {
			return libraryDrag{kind: requestItem, id: a.requestChoice}
		}
	}
	if d := a.requestDocument; d != nil && d.savedKey != "" {
		if _, ok := a.savedRequest(d.savedKey); ok {
			return libraryDrag{kind: requestItem, id: d.savedKey}
		}
	}
	return libraryDrag{}
}

func (a *app) openMoveTo(item libraryDrag) {
	if !item.valid() {
		return
	}
	a.moveTo = moveToState{open: true, item: item}
	if item.kind == requestItem {
		if r, ok := a.savedRequest(item.id); ok {
			a.moveTo.destination = r.Collection
		}
	} else {
		a.moveTo.destination = parentPath(item.id)
	}
	if !a.moveToAllowed(a.moveTo.destination) {
		a.moveTo.destination = ""
	}
}

func (a *app) commitMoveTo() bool {
	item := a.moveTo.item
	if !item.valid() || !a.moveToAllowed(a.moveTo.destination) {
		return false
	}
	m := libraryMove{item: item, to: libraryDrop{parent: a.moveTo.destination}}
	if !a.moveLibrary(m) {
		return false
	}
	a.moveTo.open = false
	*a.collectionShown(a.moveTo.destination) = true
	return true
}

// moveToModal is the pointer-free path to the same library writes a drop
// makes. A Select keeps the destination in the keyboard flow.
func (a *app) moveToModal(c *ui.Context) {
	s := &a.moveTo
	if !s.open || !s.item.valid() {
		return
	}
	uikit.Modal(c, &s.open, locales.MoveToEllipsis(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.MoveToEllipsis())
			uikit.Meta(c, locales.MoveItemTo(a.libraryItemName(s.item)))
			uikit.Meta(c, locales.MoveHereHint())
			choices := append([]string{locales.NoCollection()}, a.moveToChoices()...)
			selected := s.destination
			if selected == "" {
				selected = choices[0]
			}
			selectDestination := uikit.Select(c, &selected, choices).Label(locales.MoveDestination()).FillWidth()
			if selectDestination.Changed() {
				if selected == choices[0] {
					s.destination = ""
				} else {
					s.destination = selected
				}
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					s.open = false
				}
				if uikit.PrimaryButton(c, locales.Move()).Clicked() {
					a.commitMoveTo()
				}
			})
		})
	})
}
