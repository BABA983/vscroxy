package app

import (
	"errors"
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/control"
)

// Analysis and proposals are UI-owned, bounded staging, never credentials or
// a durable job queue. All clients share the same latest analysis and proposal.
func (a *app) controlTokenLinks(command control.Command) (any, error) {
	if a.systemProxy.quitting || a.systemProxy.quitReady {
		return nil, errors.New("application is quitting")
	}
	if command.Action == "token-links.analyze" {
		p, err := controlParams[struct {
			IDs []int `json:"ids"`
		}](command)
		if err != nil || len(p.IDs) == 0 || len(p.IDs) > tokenSelectionLimit {
			return nil, errors.New("select 1–200 unique positive HTTP flow IDs")
		}
		var selected []flow
		for _, id := range p.IDs {
			if id <= 0 || slices.ContainsFunc(selected, func(f flow) bool { return f.id == id }) {
				return nil, errors.New("select 1–200 unique positive HTTP flow IDs")
			}
			var f flow
			// Saved recorder evidence owns complete response bytes, unlike a
			// table row whose engine spill may already have been evicted.
			if s := a.collectionRecording; s != nil {
				if i := slices.IndexFunc(s.evidence, func(f flow) bool { return f.id == id }); i >= 0 {
					f = s.evidence[i]
				}
			}
			if f.id == 0 {
				if i := slices.IndexFunc(a.flows, func(f flow) bool { return f.id == id }); i >= 0 {
					f = a.flows[i]
				}
			}
			if f.id == 0 || f.kind != "" || f.method == "CONNECT" || !tokenMetadataOK(f) {
				return nil, errors.New("selected HTTP evidence is unavailable or exceeds metadata limits")
			}
			selected = append(selected, f)
		}
		w := a.startTokenLinks(selected, false)
		return map[string]any{"id": w.id, "done": !w.busy}, nil
	}
	if command.Action == "token-links.apply" {
		p, err := controlParams[struct {
			Key     string `json:"key"`
			Confirm bool   `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm || p.Key == "" {
			return nil, errors.New("proposal key and confirm:true are required")
		}
		w, err := a.currentTokenLinks("")
		if err != nil || w.plan == nil || w.plan.ID != p.Key || w.applied {
			return nil, errors.New("proposal is unavailable; analyze and prepare again")
		}
		if err := a.applyTokenLink(w.plan); err != nil {
			return nil, err
		}
		w.applied, w.plan = true, nil // never apply a successful proposal twice
		return map[string]bool{"applied": true}, nil
	}
	if command.Action == "token-links.get" {
		p, err := controlParams[struct {
			ID string `json:"id"`
		}](command)
		if err != nil || p.ID == "" {
			return nil, errors.New("analysis id is required")
		}
		w, err := a.currentTokenLinks(p.ID)
		if err != nil {
			return nil, err
		}
		return struct {
			ID   string `json:"id"`
			Done bool   `json:"done"`
			tokenAnalysis
			Problem string `json:"problem"`
		}{w.id, !w.busy, w.analysis, w.problem}, nil
	}
	p, err := controlParams[struct {
		ID          string  `json:"id"`
		Index       *int    `json:"index"`
		Collection  *string `json:"collection"`
		Environment string  `json:"environment"`
		Variable    string  `json:"variable"`
	}](command)
	if err != nil || p.ID == "" || p.Index == nil || *p.Index < 0 || *p.Index >= tokenSelectionLimit || p.Collection == nil || p.Environment == "" {
		return nil, errors.New("analysis id, index, collection, environment and variable are required")
	}
	w, err := a.currentTokenLinks(p.ID)
	if err != nil {
		return nil, err
	}
	plan, err := a.planTokenLink(w, *p.Index, tokenLinkConfig{Collection: *p.Collection, Environment: p.Environment, Variable: p.Variable})
	if err != nil {
		return nil, err
	}
	w.plan, w.reviewed, w.applied = plan, false, false
	return plan, nil
}

func (a *app) currentTokenLinks(id string) (*tokenLinkPreview, error) {
	w := a.tokenLinks
	if w == nil || !w.valid || w.visible && !w.open || !time.Now().Before(w.expires) {
		if w != nil {
			w.close()
		}
		return nil, errors.New("analysis is unavailable; analyze again")
	}
	if id != "" && w.id != id {
		return nil, errors.New("analysis was replaced; analyze again")
	}
	return w, nil
}
