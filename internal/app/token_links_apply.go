package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
)

type tokenLinkConfig struct {
	Collection  string `json:"collection"`
	Environment string `json:"environment"`
	Variable    string `json:"variable"`
}

// Only the proposed script fragments and stable request IDs cross the control
// boundary. Captured credentials and complete authored documents never do.
type tokenLinkPlan struct {
	ID               string              `json:"id"`
	Source           string              `json:"source"`
	Consumers        []string            `json:"consumers"`
	Environment      string              `json:"environment"`
	Variable         string              `json:"variable"`
	SourcePre        string              `json:"sourcePre"`
	SourcePost       string              `json:"sourcePost"`
	ConsumerPre      string              `json:"consumerPre"`
	ResetBeforeSetup request.TokenSource `json:"resetBeforeSetup"`
	before           map[string][]byte
	after            map[string]savedRequest
	folders          []byte
	globals          []byte
	environment      []byte
	reserved         bool
}

func (a *app) tokenVariableAvailable(name string) bool {
	if !templateSegment.MatchString(name) || len(name) > 64 {
		return false
	}
	if _, ok := a.globals[name]; ok {
		return false
	}
	if _, ok := a.globalsCurrent[name]; ok {
		return false
	}
	for _, e := range a.envs {
		if _, ok := e.Vars[name]; ok {
			return false
		}
		if _, ok := e.Current[name]; ok {
			return false
		}
	}
	for _, f := range a.folders {
		if slices.ContainsFunc(f.Variables, func(v request.Variable) bool { return v.Name == name }) {
			return false
		}
	}
	for _, values := range a.folderCurrent {
		if _, ok := values[name]; ok {
			return false
		}
	}
	for _, r := range a.requests {
		if slices.Contains(r.Variables(), name) {
			return false
		}
	}
	return true
}

func (a *app) planTokenLink(w *tokenLinkPreview, index int, config tokenLinkConfig) (*tokenLinkPlan, error) {
	if w == nil || w.busy || w.problem != "" || index < 0 || index >= len(w.analysis.Links) {
		return nil, errors.New("choose a completed token-link analysis")
	}
	if !a.tokenVariableAvailable(config.Variable) {
		return nil, errors.New("choose a new variable name (letters, digits and underscores; at most 64 characters)")
	}
	env := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == config.Environment })
	if env < 0 || config.Environment == "" || a.envsPath == "" || a.library.root == "" || a.envsProblem != "" || a.requestsProblem != "" {
		return nil, errors.New("select an existing environment and repair collection/environment storage first")
	}
	if s := a.collectionRecording; s != nil && (s.Active || s.Busy) {
		return nil, errors.New("stop collection recording and wait for pending work before preparing token links")
	}
	link := w.analysis.Links[index]
	p := &tokenLinkPlan{ID: uuid.New().String(), Environment: config.Environment, Variable: config.Variable, before: map[string][]byte{}, after: map[string]savedRequest{}}
	p.ResetBeforeSetup = request.TokenSource{Environment: config.Environment, Variable: config.Variable}
	p.folders, _ = json.Marshal(a.folders, json.Deterministic(true))
	p.globals, _ = json.Marshal(a.globals, json.Deterministic(true))
	p.environment, _ = json.Marshal(a.envs[env], json.Deterministic(true))
	p.SourcePre, p.SourcePost, p.ConsumerPre = tokenLinkScripts(link.Path, config.Environment, config.Variable)
	for _, id := range append([]int{link.Source}, link.Consumers...) {
		at := slices.IndexFunc(w.flows, func(f flow) bool { return f.id == id })
		if at < 0 {
			return nil, errors.New("capture evidence is unavailable; analyze again")
		}
		matches := a.matchingRequestsWithVars(w.flows[at], a.envs[env].values())
		matches = slices.DeleteFunc(matches, func(r savedRequest) bool {
			return r.Collection != config.Collection && (config.Collection == "" || !strings.HasPrefix(r.Collection, config.Collection+"/"))
		})
		if len(matches) != 1 || matches[0].ID == "" {
			return nil, errors.New("each capture must match exactly one saved request in the selected collection; save or disambiguate requests first")
		}
		r := matches[0]
		if id != link.Source && r.ID == p.Source {
			return nil, errors.New("source and consumer must be different saved requests")
		}
		if _, ok := p.after[r.ID]; ok {
			continue
		}
		p.before[r.ID], _ = json.Marshal(r, json.Deterministic(true))
		if id == link.Source {
			if slices.ContainsFunc(r.TokenSources, func(s request.TokenSource) bool { return s.Environment != config.Environment }) {
				return nil, errors.New("source is already linked to another environment")
			}
			p.Source = r.ID
			r.TokenSources = append(slices.Clone(r.TokenSources), p.ResetBeforeSetup)
			r.Pre = p.SourcePre + "\n;\n" + r.Pre
			r.Post += "\n;\n" + p.SourcePost
		} else {
			vars := a.inheritance(r.Collection).Values(a.envs[env].values())
			var err error
			r, err = linkBearerRequest(r, vars, link.token, config.Variable)
			if err != nil {
				return nil, err
			}
			r.Pre += "\n;\n" + p.ConsumerPre
			p.Consumers = append(p.Consumers, r.ID)
		}
		p.after[r.ID] = r
	}
	if err := a.checkTokenLinkPlan(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (a *app) checkTokenLinkPlan(p *tokenLinkPlan) error {
	if a.envsProblem != "" || a.requestsProblem != "" || a.systemProxy.quitting || a.systemProxy.quitReady {
		return errors.New("storage or application is unavailable")
	}
	if s := a.collectionRecording; s != nil && (s.Active || s.Busy) {
		return errors.New("stop collection recording and wait for pending work first")
	}
	if a.varsOpen && !a.varsGlobals && (a.varsChoice == p.Environment || a.varsName == p.Environment) {
		return errors.New("close the target environment's variable editor first")
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
		now, _ := json.Marshal(a.requests[at], json.Deterministic(true))
		if !bytes.Equal(now, before) {
			return errors.New("a target request changed; prepare the proposal again")
		}
	}
	folders, _ := json.Marshal(a.folders, json.Deterministic(true))
	if !bytes.Equal(folders, p.folders) {
		return errors.New("collection settings changed; prepare the proposal again")
	}
	globals, _ := json.Marshal(a.globals, json.Deterministic(true))
	if !bytes.Equal(globals, p.globals) {
		return errors.New("global variables changed; prepare the proposal again")
	}
	at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == p.Environment })
	if at < 0 {
		return errors.New("environment was removed; prepare the proposal again")
	}
	env, _ := json.Marshal(a.envs[at], json.Deterministic(true))
	if !bytes.Equal(env, p.environment) {
		return errors.New("environment changed; prepare the proposal again")
	}
	return nil
}

func (a *app) applyTokenLink(p *tokenLinkPlan) error {
	if err := a.checkTokenLinkPlan(p); err != nil {
		return err
	}
	if !p.reserved {
		if !a.tokenVariableAvailable(p.Variable) {
			return errors.New("variable name is now in use; prepare the proposal again")
		}
		// Dependencies first: an interrupted multi-file library save must not
		// leave executable references without their private Current slot.
		envs := slices.Clone(a.envs)
		at := slices.IndexFunc(envs, func(e environment) bool { return e.Name == p.Environment })
		envs[at].Current = maps.Clone(envs[at].Current)
		envs[at].Credentials = maps.Clone(envs[at].Credentials)
		if envs[at].Current == nil {
			envs[at].Current = map[string]string{}
		}
		if envs[at].Credentials == nil {
			envs[at].Credentials = map[string]bool{}
		}
		envs[at].Current[p.Variable] = "" // Never seed captured, potentially expired credentials.
		envs[at].Credentials[p.Variable] = true
		if err := func() error {
			f := a.localEnvs()
			f.Envs = envs
			return a.saveEnvFile(f)
		}(); err != nil {
			return errors.New("could not reserve private Current value; reload environments before retrying")
		}
		a.envs = envs
		p.reserved = true
		p.environment, _ = json.Marshal(envs[at], json.Deterministic(true))
	}
	list := slices.Clone(a.requests)
	for i, r := range list {
		if next, ok := p.after[r.ID]; ok {
			list[i] = next
		}
	}
	if err := a.persistRequests(list); err != nil {
		return errors.New("collection save failed; private empty Current value was kept. Some files may be saved; reload collections before preparing again")
	}
	a.refreshDocuments()
	return nil
}

func linkBearerRequest(r savedRequest, vars map[string]string, captured, name string) (savedRequest, error) {
	ref := "{{" + name + "}}"
	switch r.Auth.Type {
	case "Bearer":
		if request.ExpandVars(r.Auth.Token, vars) != captured {
			return r, errors.New("saved Bearer value no longer matches the captured evidence")
		}
		r.Auth.Token = ref
	case "", "None":
		h := request.ParseHeaderBlock(r.Headers)
		values := h.Values("Authorization")
		if len(values) != 1 {
			return r, errors.New("consumer must have one explicit Authorization header")
		}
		scheme, value, _ := strings.Cut(request.ExpandVars(values[0], vars), " ")
		if !strings.EqualFold(scheme, "Bearer") || strings.TrimLeft(value, " ") != captured {
			return r, errors.New("consumer must have one matching explicit Bearer header or Bearer Auth; inherited/other Auth is not overwritten")
		}
		lines := strings.Split(r.Headers, "\n")
		for i, line := range lines {
			key, _, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(strings.TrimSpace(key), "Authorization") {
				lines[i] = key + ": Bearer " + ref
			}
		}
		r.Headers = strings.Join(lines, "\n")
		r.HeaderList = slices.Clone(r.HeaderList)
		for i := range r.HeaderList {
			if !r.HeaderList[i].Disabled && strings.EqualFold(strings.TrimSpace(r.HeaderList[i].Name), "Authorization") {
				r.HeaderList[i].Value = "Bearer " + ref
			}
		}
	default:
		return r, errors.New("consumer uses inherited or non-Bearer Auth; make its Bearer Auth explicit before applying")
	}
	return r, nil
}

func tokenLinkScripts(path []string, environment, variable string) (pre, post, consumer string) {
	key, _ := json.Marshal(variable)
	env, _ := json.Marshal(environment)
	segments, _ := json.Marshal(path)
	guard := fmt.Sprintf(`if (pm.environment.name !== %s || !pm.environment.has(%s)) {
  throw new Error("Select the token link environment with its local Current slot");
}`, env, key)
	pre = fmt.Sprintf("// vscroxy token link: clear before login, including network/script failures.\n(function () {\n%s\npm.environment.set(%s, \"\");\n})();", guard, key)
	post = fmt.Sprintf(`// vscroxy token link: guarded extraction; no captured credential is embedded.
(function () {
%s
pm.environment.set(%s, "");
if (pm.response.code < 200 || pm.response.code >= 300) throw new Error("Token source did not succeed");
var value;
try { value = pm.response.json(); } catch (_) { throw new Error("Token source response is not valid JSON"); }
%s.forEach(function (part) {
  if (value === null || typeof value !== "object" || !Object.prototype.hasOwnProperty.call(value, part)) {
    throw new Error("Token response field is missing");
  }
  value = value[part];
});
if (typeof value !== "string" || value.length < 16 || value.length > 8192 || !/^[A-Za-z0-9._~+\/-]+=*$/.test(value)) {
  throw new Error("Token response field is not a valid Bearer token");
}
pm.environment.set(%s, value);
})();`, guard, key, segments, key)
	consumer = fmt.Sprintf(`// vscroxy token link: require login first; reject shadowed or empty tokens.
(function () {
%s
var value = pm.environment.get(%s);
if (typeof value !== "string" || value.length < 16 || value.length > 8192 || !/^[A-Za-z0-9._~+\/-]+=*$/.test(value) || pm.variables.get(%s) !== value) {
  throw new Error("Run the token source successfully before this request");
}
})();`, guard, key, key)
	return
}
