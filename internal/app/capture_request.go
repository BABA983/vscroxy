package app

import (
	"slices"
	"strings"
	"uuid"
)

// saveCapture keeps a captured request in the library under a name of its
// own, without sending it. The conversion is the same complete, decoded
// payload Edit & replay opens; a second save makes a second asset.
func (a *app) saveCapture(f flow) {
	r, err := f.requestDraft()
	if err != nil {
		a.setMessage("Save as request: " + err.Error())
		return
	}
	r.ID = uuid.New().String()
	r.Source = newDebugSource(f)
	r.Name = uniqueRequestName(a.requests, strings.TrimSpace(f.method+" "+f.host+f.path))
	list := upsertRequest(slices.Clone(a.requests), r)
	if err := a.persistRequests(list); err != nil {
		a.setMessage("Save as request: " + err.Error())
		return
	}
	a.requests = list
	a.setMessage("Saved " + r.Name + " to Requests; not sent")
}
