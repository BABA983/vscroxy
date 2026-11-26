package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/locales"
	"github.com/google/uuid"
)

// saveCapture keeps a captured request in the library under a name of its
// own, without sending it. The conversion is the same complete, decoded
// payload Edit & replay opens; a second save makes a second asset.
func (a *app) saveCapture(f flow) {
	r, err := a.saveCapturedRequest(f, "", "")
	if err != nil {
		a.fail(locales.SaveAsRequest(), err)
		return
	}
	a.done(locales.SavedToRequests(r.Name))
}

// saveCapturedRequest is the shared conversion behind the desktop's Save as
// request command and the requests.save tool. An empty name uses the capture
// label made unique; an explicit name is kept. The caller validates collection
// paths and whether the flow is representable.
func (a *app) saveCapturedRequest(f flow, name, collection string) (savedRequest, error) {
	r, err := f.requestDraft()
	if err != nil {
		return savedRequest{}, err
	}
	r.ID = uuid.New().String()
	r.Source = newDebugSource(f)
	r.Collection = collection
	if name = strings.TrimSpace(name); name == "" {
		name = uniqueRequestName(a.requests, strings.TrimSpace(f.method+" "+f.host+f.path))
	}
	r.Name = name
	list := upsertRequest(slices.Clone(a.requests), r)
	if err := a.persistRequests(list); err != nil {
		return savedRequest{}, err
	}
	a.requests = list
	return r, nil
}

// controlSaveRequest is the requests.save action. It returns only the new
// asset's identity; saved contents, which may include captured credentials,
// remain behind requests.get's disclosure gate.
func (a *app) controlSaveRequest(command control.Command) (any, error) {
	p, err := controlParams[struct {
		FlowID     int    `json:"flowId"`
		Name       string `json:"name"`
		Collection string `json:"collection"`
		Confirm    bool   `json:"confirm"`
	}](command)
	if err != nil || !p.Confirm || p.FlowID <= 0 {
		return nil, errors.New("flowId and confirm:true are required")
	}
	i := slices.IndexFunc(a.flows, func(f flow) bool { return f.live && f.id == p.FlowID })
	if i < 0 {
		return nil, errors.New("live flow not found")
	}
	f := a.flows[i]
	if f.kind != "" || f.url == "" || f.method == "CONNECT" {
		return nil, errors.New("only HTTP(S) captures with a URL can be saved as requests")
	}
	// collections.list derives collection paths from saved requests. Naming an
	// unknown path would create it, so refuse instead.
	if p.Collection != "" && !slices.Contains(runnableCollections(a.requests), p.Collection) {
		return nil, errors.New("collection not found; list collections again")
	}
	saved, err := a.saveCapturedRequest(f, p.Name, p.Collection)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": saved.ID, "name": saved.Name, "collection": saved.Collection}, nil
}
