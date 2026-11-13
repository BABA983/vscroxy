package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/locales"
	"github.com/google/uuid"
)

// saveCapture keeps a captured request in the library under a name of its
// own, without sending it. The conversion is the same complete, decoded
// payload Edit & replay opens; a second save makes a second asset.
func (a *app) saveCapture(f flow) {
	r, err := f.requestDraft()
	if err != nil {
		a.fail(locales.SaveAsRequest(), err)
		return
	}
	r.ID = uuid.New().String()
	r.Source = newDebugSource(f)
	r.Name = uniqueRequestName(a.requests, strings.TrimSpace(f.method+" "+f.host+f.path))
	list := upsertRequest(slices.Clone(a.requests), r)
	if err := a.persistRequests(list); err != nil {
		a.fail(locales.SaveAsRequest(), err)
		return
	}
	a.requests = list
	a.done(locales.SavedToRequests(r.Name))
}

// saveFlowsToFolder files the exchanges into the collection or folder at
// path, without sending them. It is the drop side of dragging a table row
// onto the collection tree.
func (a *app) saveFlowsToFolder(flows []flow, path string) {
	var skips importSkips
	var incoming []savedRequest
	all := slices.Clone(a.requests)
	for _, f := range flows {
		r, skipped := collectionRequest(f)
		if skipped != "" {
			skips.add(skipped)
			continue
		}
		r.Source = newDebugSource(f)
		r.Collection = path
		all = fileRequest(all, r)
		incoming = append(incoming, all[len(all)-1])
	}
	warnings := skips.lines("row", "rows")
	if len(incoming) == 0 {
		a.warn(locales.SaveToCollectionNothing(strings.Join(warnings, "; ")))
		return
	}
	if err := a.addToLibrary(incoming, nil); err != nil {
		a.fail(locales.SaveToCollection(), err)
		return
	}
	a.reportImport(fmt.Sprintf("Saved %d %s to “%s”; not sent", len(incoming), plural(len(incoming), "request", "requests"), path), warnings)
}
