package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/uikit"
)

// controlCapture runs on the UI thread. It covers the live table (clear, mark),
// paused exchanges, the SSL proxying domains and saved sessions. Only the
// outcomes are returned; captured bodies and headers stay in the desktop.
func (a *app) controlCapture(command control.Command) (any, error) {
	switch command.Action {
	case "flows.clear":
		p, err := controlParams[struct {
			Confirm bool `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("confirm:true is required")
		}
		// Like the Clear command, pinned flows stay.
		a.clearCaptured()
		return map[string]int{"flows": len(a.flows)}, nil
	case "flows.annotate":
		p, err := controlParams[struct {
			ID      int     `json:"id"`
			Pinned  *bool   `json:"pinned"`
			Color   *string `json:"color"`
			Note    *string `json:"note"`
			Confirm bool    `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm || p.ID <= 0 || p.Pinned == nil && p.Color == nil && p.Note == nil {
			return nil, errors.New("id, confirm:true and at least one of pinned, color or note are required")
		}
		if p.Color != nil && *p.Color != "" && !slices.ContainsFunc(uikit.MarkColors, func(s uikit.Swatch) bool { return s.Name == *p.Color }) {
			return nil, errors.New("color must be empty or one of Red, Orange, Yellow, Green, Blue, Purple")
		}
		if p.Note != nil && len(*p.Note) > 4096 {
			return nil, errors.New("note is limited to 4096 bytes")
		}
		i := slices.IndexFunc(a.flows, func(f flow) bool { return f.id == p.ID })
		if i < 0 {
			return nil, errors.New("flow not found")
		}
		// The note counts toward memory, so adjust the tally around the change.
		a.count(&a.flows[i], -1)
		if p.Pinned != nil {
			a.flows[i].pinned = *p.Pinned
		}
		if p.Color != nil {
			a.flows[i].color = *p.Color
		}
		if p.Note != nil {
			a.flows[i].note = strings.TrimSpace(*p.Note)
		}
		a.count(&a.flows[i], 1)
		a.applyFilter()
		a.invalidate()
		return map[string]int{"id": p.ID}, nil
	case "breakpoints.list":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
		var rows []map[string]any
		for _, bp := range append([]*capture.Breakpoint{a.bp}, a.pausedQueue()...) {
			if bp == nil {
				continue
			}
			rows = append(rows, map[string]any{"id": bp.ID, "phase": breakpointPhase(bp.Phase), "method": bp.Method, "url": redactExportURL(bp.URL, safeExportOptions()), "status": bp.Status, "current": bp == a.bp})
		}
		return rows, nil
	case "breakpoints.resolve":
		p, err := controlParams[struct {
			ID      int    `json:"id"`
			Outcome string `json:"outcome"`
			Confirm bool   `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("id, outcome and confirm:true are required")
		}
		if a.bp == nil || a.bp.ID != p.ID {
			return nil, errors.New("breakpoint not found; list breakpoints again")
		}
		switch p.Outcome {
		case "continue": // unchanged, as Continue original
			a.resolveBreak(false, true, false)
		case "abort":
			a.resolveBreak(true, false, false)
		default:
			return nil, errors.New("outcome must be continue or abort")
		}
		return map[string]bool{"accepted": true}, nil
	case "ssl.get":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
		cfg := a.inspectionConfig()
		return map[string]any{"all": cfg.All, "hosts": cfg.Hosts, "apps": len(cfg.Apps)}, nil
	case "ssl.update":
		p, err := controlParams[struct {
			All     *bool     `json:"all"`
			Domains *[]string `json:"domains"`
			Confirm bool      `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm || p.All == nil && p.Domains == nil {
			return nil, errors.New("all or domains, and confirm:true, are required")
		}
		cfg := a.inspectionConfig()
		if p.All != nil {
			cfg.All = *p.All
		}
		if p.Domains != nil {
			if len(*p.Domains) > 1000 {
				return nil, errors.New("domains is limited to 1000 entries")
			}
			hosts := []string{}
			for _, host := range *p.Domains {
				host = strings.ToLower(strings.TrimSpace(host))
				if host == "" || strings.ContainsAny(host, " \t/?#@") {
					return nil, errors.New("domains must be host names without schemes, paths or spaces")
				}
				hosts = append(hosts, host)
			}
			cfg.Hosts = hosts
		}
		// Application entries are kept; only domains and the all-traffic switch change.
		if !a.saveSSL(cfg) {
			return nil, errors.New("could not save SSL proxying; nothing changed")
		}
		return map[string]any{"all": cfg.All, "hosts": len(cfg.Hosts)}, nil
	case "sessions.list":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
		var rows []map[string]any
		for _, item := range a.sessions.items {
			rows = append(rows, map[string]any{"key": item.key, "title": item.data.Title, "count": item.count, "created": item.data.Created})
		}
		return rows, nil
	case "sessions.save":
		p, err := controlParams[struct {
			Confirm bool `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("confirm:true is required")
		}
		a.saveSession()
		return map[string]string{"key": a.sessions.active}, nil
	case "sessions.open":
		p, err := controlParams[struct {
			Key     string `json:"key"`
			Confirm bool   `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm || p.Key == "" {
			return nil, errors.New("key and confirm:true are required")
		}
		if !slices.ContainsFunc(a.sessions.items, func(item *savedSession) bool { return item.key == p.Key }) {
			return nil, errors.New("session not found; list sessions again")
		}
		a.selectSession(p.Key)
		return map[string]bool{"opened": true}, nil
	}
	return nil, errors.New("unknown action")
}

// pausedQueue is the breakpoints waiting behind the one on screen.
func (a *app) pausedQueue() []*capture.Breakpoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	var queue []*capture.Breakpoint
	for i := range a.bps {
		queue = append(queue, &a.bps[i])
	}
	return queue
}

func breakpointPhase(phase capture.Phase) string {
	switch phase {
	case capture.PhaseResponse:
		return "response"
	case capture.PhaseMessage:
		return "message"
	}
	return "request"
}
