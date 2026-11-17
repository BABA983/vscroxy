package app

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/openapi"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/google/uuid"
)

// openapiSync is a reviewed change set between a spec and its collection.
// Nothing is written until Apply.
type openapiSync struct {
	Direction string // "to-collection" or "to-spec"
	Diff      string
	Requests  []savedRequest
	Source    string
	Added     int
	Updated   int
	Unchanged int
}

// planOpenAPISync builds the change set for one direction. to-collection
// updates matching requests (method and normalized path) and appends missing
// operations; requests the spec does not describe stay untouched. to-spec
// regenerates the draft from the collection.
func (a *app) planOpenAPISync(d *apiDocument, direction string) (openapiSync, error) {
	plan := openapiSync{Direction: direction}
	if d.spec.Collection == "" {
		return plan, errors.New("this spec has no source collection")
	}
	switch direction {
	case "to-collection":
		if d.parsed == nil || d.checked != d.code.Buffer.String() {
			return plan, errors.New("validate the current source first")
		}
		current := slices.Clone(a.requests)
		next := slices.Clone(current)
		for _, endpoint := range apiOperations(d.parsed) {
			incoming := requestFromOperation(d.parsed, endpoint)
			incoming.Collection = d.spec.Collection
			at := slices.IndexFunc(next, func(r savedRequest) bool {
				return r.Collection == d.spec.Collection && strings.EqualFold(r.Method, incoming.Method) &&
					openapi.NormalizePath(r.URL) == openapi.NormalizePath(incoming.URL)
			})
			if at < 0 {
				incoming.ID = uuid.New().String()
				peers := slices.DeleteFunc(slices.Clone(next), func(r savedRequest) bool { return r.Collection != incoming.Collection })
				incoming.Name = uniqueRequestName(peers, incoming.Name)
				next = append(next, incoming)
				plan.Added++
				continue
			}
			old := next[at]
			updatedURL := incoming.URL
			// A generated {{base_url}} means the spec declares no server;
			// keep the request's own host or base variable and take only the
			// path from the spec.
			if strings.HasPrefix(incoming.URL, "{{base_url}}") {
				if origin := urlOrigin(old.URL); origin != "" {
					updatedURL = origin + strings.TrimPrefix(incoming.URL, "{{base_url}}")
				}
			}
			if old.URL == updatedURL && old.Description == incoming.Description {
				plan.Unchanged++
				continue
			}
			old.URL, old.Description = updatedURL, incoming.Description
			if strings.TrimSpace(old.Name) == "" {
				old.Name = incoming.Name
			}
			next[at] = old
			plan.Updated++
		}
		plan.Requests = next
		plan.Diff = requestSummaryDiff(current, next, d.spec.Collection)
	case "to-spec":
		generated, err := generateOpenAPI(a.requests, d.spec.Collection)
		if err != nil {
			return plan, err
		}
		plan.Source = generated.Source
		plan.Diff = unifiedDiff("saved spec", "generated from collection", d.spec.Source, generated.Source)
	default:
		return plan, errors.New("direction must be to-collection or to-spec")
	}
	return plan, nil
}

// applyOpenAPISync writes a reviewed plan. to-collection replaces the request
// library and saves the spec's new fingerprint; to-spec replaces the draft
// source and validates it, leaving Save to the user.
func (a *app) applyOpenAPISync(d *apiDocument, plan openapiSync) error {
	switch plan.Direction {
	case "to-collection":
		if err := a.persistRequests(plan.Requests); err != nil {
			return err
		}
		a.requests = plan.Requests
		d.spec.Fingerprint = collectionFingerprint(collectionRequests(a.requests, d.spec.Collection))
		a.saveAPI(d)
	case "to-spec":
		d.code.Buffer.Set(plan.Source)
		d.validate()
	}
	return nil
}

// urlOrigin is a URL's scheme and host, or its leading {{base}} variable.
func urlOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{{") {
		if end := strings.Index(raw, "}}"); end >= 0 {
			return raw[:end+2]
		}
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return raw[:i+3+j]
		}
		return raw
	}
	return ""
}

// requestSummary is the reviewable part of a collection's requests, one JSON
// object per line, sorted by method and path.
func requestSummary(requests []savedRequest, collection string) string {
	list := collectionRequests(requests, collection)
	var lines []string
	for _, r := range list {
		data, err := json.Marshal(struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Method      string `json:"method"`
			URL         string `json:"url"`
			Description string `json:"description,omitempty"`
		}{r.ID, r.Name, strings.ToUpper(r.Method), r.URL, r.Description}, json.Deterministic(true))
		if err != nil {
			continue
		}
		lines = append(lines, string(data))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func requestSummaryDiff(before, after []savedRequest, collection string) string {
	return unifiedDiff("collection", "collection after sync", requestSummary(before, collection), requestSummary(after, collection))
}

// unifiedDiff is a small line diff for review. Very large inputs are compared
// by line count instead, so a huge spec cannot stall the UI thread.
func unifiedDiff(oldName, newName, oldText, newText string) string {
	var oldLines, newLines []string
	if oldText != "" {
		oldLines = strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	}
	if newText != "" {
		newLines = strings.Split(strings.TrimSuffix(newText, "\n"), "\n")
	}
	if len(oldLines)+len(newLines) > 4000 {
		return fmt.Sprintf("diff omitted: %d old and %d new lines; use a file diff\n", len(oldLines), len(newLines))
	}
	// Longest common subsequence table, then walk it to emit unified hunks.
	n, m := len(oldLines), len(newLines)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && oldLines[i] == newLines[j]:
			out.WriteString(" " + oldLines[i] + "\n")
			i, j = i+1, j+1
		case j < m && (i == n || table[i][j+1] >= table[i+1][j]):
			out.WriteString("+" + newLines[j] + "\n")
			j++
		default:
			out.WriteString("-" + oldLines[i] + "\n")
			i++
		}
	}
	return out.String()
}

// apiSyncState is the Sync dialog on one API document.
type apiSyncState struct {
	open      bool
	direction int // 0: spec to collection, 1: collection to spec
	plan      openapiSync
	problem   string
	reader    *editor.State
}

func (a *app) openAPISync(d *apiDocument) {
	s := &d.sync
	if s.reader == nil {
		s.reader = &editor.State{Buffer: editor.NewBuffer(""), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.SyncDiff()}
	}
	s.open, s.direction, s.problem = true, 0, ""
	a.replanAPISync(d)
}

func (a *app) replanAPISync(d *apiDocument) {
	s := &d.sync
	direction := "to-collection"
	if s.direction == 1 {
		direction = "to-spec"
	}
	plan, err := a.planOpenAPISync(d, direction)
	if err != nil {
		s.plan, s.problem = openapiSync{}, err.Error()
		s.reader.Buffer.Set("")
		return
	}
	s.plan, s.problem = plan, ""
	s.reader.Buffer.Set(plan.Diff)
}

// apiSyncModal shows the reviewed diff and applies it only on Apply.
func (a *app) apiSyncModal(c *ui.Context, t *ui.Theme, d *apiDocument) {
	s := &d.sync
	direction := map[int]string{0: "to-collection", 1: "to-spec"}[s.direction]
	if s.open && s.plan.Direction != direction {
		a.replanAPISync(d)
	}
	uikit.Modal(c, &s.open, locales.SyncWithCollection(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.SyncWithCollection())
			uikit.Segmented(c, &s.direction, locales.SpecToCollection(), locales.CollectionToSpec())
			if s.problem != "" {
				uikit.ErrorText(c, s.problem)
			} else {
				uikit.Meta(c, fmt.Sprintf("%d added · %d updated · %d unchanged", s.plan.Added, s.plan.Updated, s.plan.Unchanged))
			}
			ui.Box(c).Height(360).Children(func() {
				requestCodeEditor(c, s.reader)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					s.open = false
				}
				noChange := s.plan.Direction == "to-collection" && s.plan.Added+s.plan.Updated == 0 ||
					s.plan.Direction == "to-spec" && s.plan.Source == d.spec.Source
				if uikit.PrimaryButton(c, locales.Apply()).Disabled(s.problem != "" || noChange).Clicked() {
					if err := a.applyOpenAPISync(d, s.plan); err != nil {
						s.problem = err.Error()
						return
					}
					s.open = false
					a.done(locales.SyncApplied())
				}
			})
		})
	})
}

// controlOpenAPI serves the desktop control's OpenAPI actions. Sync is a dry
// run unless write and confirm are both true.
func (a *app) controlOpenAPI(command control.Command) (any, error) {
	p, err := controlParams[struct {
		ID         string `json:"id"`
		Collection string `json:"collection"`
		Direction  string `json:"direction"`
		Write      bool   `json:"write"`
		Confirm    bool   `json:"confirm"`
	}](command)
	if err != nil {
		return nil, err
	}
	var d *apiDocument
	for _, candidate := range a.apis {
		if p.ID != "" && candidate.spec.ID == p.ID || p.ID == "" && p.Collection != "" && candidate.spec.Collection == p.Collection {
			d = candidate
			break
		}
	}
	if d == nil {
		return nil, errors.New("OpenAPI document not found")
	}
	if command.Action == "openapi.lint" {
		if d.parsed == nil {
			return nil, errors.New("validate the document first")
		}
		return map[string]any{"issues": d.lint, "endpoints": len(apiOperations(d.parsed))}, nil
	}
	direction := cmp.Or(p.Direction, "to-collection")
	plan, err := a.planOpenAPISync(d, direction)
	if err != nil {
		return nil, err
	}
	if p.Write {
		if !p.Confirm {
			return nil, errors.New("confirm:true is required to write a sync")
		}
		if err := a.applyOpenAPISync(d, plan); err != nil {
			return nil, err
		}
	}
	return map[string]any{"direction": plan.Direction, "diff": plan.Diff, "added": plan.Added, "updated": plan.Updated, "unchanged": plan.Unchanged, "written": p.Write}, nil
}
