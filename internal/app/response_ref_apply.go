package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
)

type responseRefPlan struct {
	ID              string
	Source          string
	Consumers       []string
	Expression      string
	before          map[string][]byte
	after           map[string]savedRequest
	folders         []byte
	globals         []byte
	environment     []byte
	environmentName string
}

// responseRefPointer renders a document path as RFC 6901 JSON pointer text.
func responseRefPointer(path []string) string {
	var b strings.Builder
	for _, segment := range path {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

func (a *app) planResponseRefs(w *tokenLinkPreview, index int) (*responseRefPlan, error) {
	if w == nil || w.busy || len(w.refAnalysis.Suggestions) == 0 ||
		index < 0 || index >= len(w.refAnalysis.Suggestions) || len(w.flows) == 0 {
		return nil, errors.New("choose a completed response reference analysis")
	}
	if a.requestsProblem != "" || a.envsProblem != "" || a.library.root == "" || a.envsPath == "" || a.activeEnv == "" {
		return nil, errors.New("repair collection and environment storage first")
	}
	if s := a.collectionRecording; s != nil && (s.Active || s.Busy) {
		return nil, errors.New("stop collection recording before applying response references")
	}
	env := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == a.activeEnv })
	if env < 0 {
		return nil, errors.New("select an existing environment")
	}
	suggestion := w.refAnalysis.Suggestions[index]
	source := slices.IndexFunc(w.flows, func(f flow) bool { return f.id == suggestion.Source })
	if source < 0 {
		return nil, errors.New("capture evidence is unavailable; analyze again")
	}
	sourceMatches := a.matchingRequestsWithVars(w.flows[source], a.envs[env].values())
	if len(sourceMatches) != 1 || sourceMatches[0].ID == "" {
		return nil, errors.New("the response source must match exactly one saved request")
	}
	src := sourceMatches[0]
	expression := responseRefExpression(src.Name, suggestion.Path)
	p := &responseRefPlan{ID: uuid.New().String(), Source: src.ID, Expression: expression,
		before: map[string][]byte{}, after: map[string]savedRequest{}, environmentName: a.activeEnv}
	p.folders, _ = json.Marshal(a.folders, json.Deterministic(true))
	p.globals, _ = json.Marshal(a.globals, json.Deterministic(true))
	p.environment, _ = json.Marshal(a.envs[env], json.Deterministic(true))
	p.after[src.ID] = src
	p.before[src.ID], _ = json.Marshal(src, json.Deterministic(true))
	for _, consumer := range suggestion.Consumers {
		at := slices.IndexFunc(w.flows, func(f flow) bool { return f.id == consumer.Flow })
		if at < 0 {
			return nil, errors.New("capture evidence is unavailable; analyze again")
		}
		matches := a.matchingRequestsWithVars(w.flows[at], a.envs[env].values())
		if len(matches) != 1 || matches[0].ID == "" {
			return nil, errors.New("each reused value must match exactly one saved request")
		}
		r := matches[0]
		if r.ID == src.ID {
			return nil, errors.New("the source and consumer must be different requests")
		}
		if responseRefCollectionRoot(r.Collection) != responseRefCollectionRoot(src.Collection) {
			return nil, errors.New("a response reference only reaches requests in the same collection")
		}
		current, ok := p.after[r.ID]
		if !ok {
			current, p.after[r.ID], p.before[r.ID] = r, r, mustJSON(r)
		}
		next, err := replaceResponseRefInRequest(current, consumer, suggestion.token, expression,
			a.inheritance(r.Collection).Values(a.envs[env].values()))
		if err != nil {
			return nil, err
		}
		p.after[r.ID] = next
		if !slices.Contains(p.Consumers, r.ID) {
			p.Consumers = append(p.Consumers, r.ID)
		}
	}
	if len(p.Consumers) == 0 {
		return nil, errors.New("no saved consumer to update")
	}
	if err := a.checkResponseRefPlan(p); err != nil {
		return nil, err
	}
	return p, nil
}

func mustJSON(r savedRequest) []byte {
	data, _ := json.Marshal(r, json.Deterministic(true))
	return data
}

func responseRefCollectionRoot(collection string) string {
	root, _, _ := strings.Cut(strings.Trim(collection, "/"), "/")
	return root
}

func replaceResponseRefInRequest(r savedRequest, at responseRefConsumer, token, expression string, vars map[string]string) (savedRequest, error) {
	switch at.Field {
	case "Bearer":
		return linkBearerRequest(r, vars, token, expression)
	case "Header":
		if r.Auth.Type != "" && r.Auth.Type != "None" {
			return r, errors.New("a response reference cannot overwrite inherited or generated auth")
		}
		var changed bool
		lines := strings.Split(r.Headers, "\n")
		for i, line := range lines {
			key, value, ok := strings.Cut(line, ":")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), at.Name) {
				continue
			}
			if strings.Count(request.ExpandVars(value, vars), token) != 1 {
				return r, errors.New("the saved header no longer matches the captured evidence")
			}
			lines[i] = key + ":" + strings.Replace(value, token, expression, 1)
			changed = true
			break
		}
		r.HeaderList = slices.Clone(r.HeaderList)
		for i := range r.HeaderList {
			if r.HeaderList[i].Disabled || !strings.EqualFold(strings.TrimSpace(r.HeaderList[i].Name), at.Name) {
				continue
			}
			if strings.Count(request.ExpandVars(r.HeaderList[i].Value, vars), token) != 1 {
				return r, errors.New("the saved header no longer matches the captured evidence")
			}
			r.HeaderList[i].Value = strings.Replace(r.HeaderList[i].Value, token, expression, 1)
			changed = true
			break
		}
		if !changed {
			return r, errors.New("the saved header no longer matches the captured evidence")
		}
		r.Headers = strings.Join(lines, "\n")
		return r, nil
	case "Query":
		u, err := url.Parse(request.ExpandVars(r.URL, vars))
		if err != nil || !slices.Contains(u.Query()[at.Name], token) {
			return r, errors.New("the saved query parameter no longer matches the captured evidence")
		}
		if strings.Count(r.URL, token) != 1 {
			return r, errors.New("the query value comes from a variable; make it explicit before applying")
		}
		r.URL = strings.Replace(r.URL, token, expression, 1)
		return r, nil
	case "Body":
		if strings.Count(request.ExpandVars(r.Body, vars), token) != 1 || strings.Count(r.Body, token) != 1 {
			return r, errors.New("the saved body no longer matches the captured evidence; make the value explicit first")
		}
		r.Body = strings.Replace(r.Body, token, expression, 1)
		return r, nil
	}
	return r, fmt.Errorf("unsupported response reference location %q", at.Field)
}

func (a *app) checkResponseRefPlan(p *responseRefPlan) error {
	if a.envsProblem != "" || a.requestsProblem != "" || a.quitting() {
		return errors.New("storage or application is unavailable")
	}
	if s := a.collectionRecording; s != nil && (s.Active || s.Busy) {
		return errors.New("stop collection recording before applying response references")
	}
	for _, d := range a.documents {
		if _, affected := p.before[d.assetID]; affected && (d.dirty() || d.composeWaiting) {
			return errors.New("save or close affected request drafts and wait for sends to finish")
		}
	}
	for id, before := range p.before {
		at := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.ID == id })
		if at < 0 {
			return errors.New("a target request was removed; prepare the proposal again")
		}
		if !bytes.Equal(mustJSON(a.requests[at]), before) {
			return errors.New("a target request changed; prepare the proposal again")
		}
	}
	folders, _ := json.Marshal(a.folders, json.Deterministic(true))
	globals, _ := json.Marshal(a.globals, json.Deterministic(true))
	if !bytes.Equal(folders, p.folders) || !bytes.Equal(globals, p.globals) {
		return errors.New("collection settings changed; prepare the proposal again")
	}
	at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == p.environmentName })
	if at < 0 {
		return errors.New("the environment was removed; prepare the proposal again")
	}
	environment, _ := json.Marshal(a.envs[at], json.Deterministic(true))
	if !bytes.Equal(environment, p.environment) {
		return errors.New("the environment changed; prepare the proposal again")
	}
	return nil
}

func (a *app) applyResponseRefs(p *responseRefPlan) error {
	if err := a.checkResponseRefPlan(p); err != nil {
		return err
	}
	list := slices.Clone(a.requests)
	for i, r := range list {
		if next, ok := p.after[r.ID]; ok {
			list[i] = next
		}
	}
	if err := a.persistRequests(list); err != nil {
		return errors.New("collection save failed; some files may be saved, so reload collections before trying again")
	}
	a.refreshDocuments()
	return nil
}
