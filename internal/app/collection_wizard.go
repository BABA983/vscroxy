package app

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/baba983/vscroxy/locales"
)

// collectionWizard owns a draft, never live library assets. Back and Cancel
// cannot change requests, environments or the vault.
type collectionWizard struct {
	open                           bool
	step, active, grouping         int
	flows                          []flow
	static, tunnels, failed        bool
	name, destination, environment string
	environmentName, problem       string
	groups                         []*collectionEndpoint
	hosts                          []collectionHost
	credentials                    []collectionCredential
	examples                       map[int]bool // index in flows, not capture ID
	generateAPI                    bool
	building                       *collectionWizard
}

type collectionEndpoint struct {
	id, origin, method, template string
	name, folder                 string
	include                      bool
	flows                        []int
}

type collectionHost struct{ origin, variable string }

type collectionCredential struct {
	header, value, variable, storage string
	count                            int
}

const (
	credentialCurrent = "Environment current value"
	credentialVault   = "Vault"
	credentialKeep    = "Leave in requests"
)

var numericSegment = regexp.MustCompile(`^[0-9]+$`)
var uuidSegment = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var templateSegment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var versionSegment = regexp.MustCompile(`^v[0-9]+$`)

// Only whole numeric/UUID segments are inferred. Slugs, versions and arbitrary
// strings remain literal; the endpoint editor is the authority for those.
func inferredCollectionPath(p string) string {
	parts := strings.Split(p, "/")
	n := 0
	for i, part := range parts {
		if numericSegment.MatchString(part) || uuidSegment.MatchString(part) {
			n++
			parts[i] = ":id"
			if n > 1 {
				parts[i] += strconv.Itoa(n)
			}
		}
	}
	return strings.Join(parts, "/")
}

func (w *collectionWizard) scope() []int {
	var out []int
	for i, f := range w.flows {
		u, err := url.Parse(f.url)
		if w.tunnels && (f.method == "CONNECT" || f.kind == "connect") ||
			w.failed && (f.conn.error != "" || f.status == 0 || f.reqError != "") {
			continue
		}
		// Streams/tunnels have no finite HTTP request/response to turn into
		// an example. They remain visible in Scope's omitted count.
		if f.kind != "" || err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		if w.static {
			ct := f.contentType(true)
			if strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "font/") || slices.Contains([]string{"text/css", "text/javascript", "application/javascript"}, ct) ||
				slices.Contains([]string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".js", ".css", ".woff", ".woff2", ".ttf", ".otf"}, strings.ToLower(path.Ext(u.Path))) {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

func (w *collectionWizard) regroup() {
	w.groups, w.hosts, w.credentials = nil, nil, nil
	w.active = 0
	for _, i := range w.scope() {
		f := w.flows[i]
		u, _ := url.Parse(f.url)
		origin := u.Scheme + "://" + u.Host
		template := inferredCollectionPath(cmp.Or(u.EscapedPath(), "/"))
		at := slices.IndexFunc(w.groups, func(g *collectionEndpoint) bool {
			return g.origin == origin && g.method == f.method && g.template == template
		})
		if at < 0 {
			w.groups = append(w.groups, &collectionEndpoint{id: uuid.New().String(), origin: origin, method: f.method, template: template, include: true})
			at = len(w.groups) - 1
		}
		w.groups[at].flows = append(w.groups[at].flows, i)
		if !slices.ContainsFunc(w.hosts, func(h collectionHost) bool { return h.origin == origin }) {
			w.hosts = append(w.hosts, collectionHost{origin: origin})
		}
	}
	for i := range w.hosts {
		name := "base_url"
		if len(w.hosts) > 1 {
			u, _ := url.Parse(w.hosts[i].origin)
			prefix, _, _ := strings.Cut(u.Hostname(), ".")
			prefix = strings.ReplaceAll(prefix, "-", "_")
			if !templateSegment.MatchString(prefix) {
				prefix = "base"
			}
			name = prefix + "_url"
			for n, base := 2, name; slices.ContainsFunc(w.hosts[:i], func(h collectionHost) bool { return h.variable == name }); n++ {
				name = fmt.Sprintf("%s_%d", base, n)
			}
		}
		w.hosts[i].variable = name
	}
	w.examples = map[int]bool{}
	for _, g := range w.groups {
		w.defaultExamples(g)
	}
	w.organize()
	w.findCredentials()
}

func (w *collectionWizard) defaultExamples(g *collectionEndpoint) {
	latest := map[int]int{}
	for _, i := range g.flows {
		f := w.flows[i]
		if f.status < 200 || f.status > 599 || f.resError != "" || f.conn.error != "" {
			continue
		}
		key := f.status
		if f.status < 400 {
			if f.status >= 300 { // Redirects are selectable, not default successes.
				continue
			}
			key = 200
		}
		if old, ok := latest[key]; !ok || !f.at.Before(w.flows[old].at) {
			latest[key] = i
		}
	}
	for _, i := range latest {
		w.examples[i] = true
	}
}

func (w *collectionWizard) organize() {
	for _, g := range w.groups {
		g.folder = ""
		switch w.grouping {
		case 0:
			u, _ := url.Parse(g.origin)
			g.folder = collectionSegment(u.Host)
		case 1:
			for _, segment := range strings.Split(g.template, "/") {
				if segment != "" && !strings.HasPrefix(segment, ":") && !versionSegment.MatchString(segment) {
					g.folder = collectionSegment(segment)
					break
				}
			}
		}
		if g.name == "" {
			g.name = collectionRequestName(g.method, g.template)
		}
	}
}

func collectionRequestName(method, template string) string {
	parts := strings.Split(strings.Trim(template, "/"), "/")
	resource := "request"
	parameter := false
	for _, part := range parts {
		if strings.HasPrefix(part, ":") {
			parameter = true
		} else if part != "" {
			resource, parameter = part, false
		}
	}
	verb := map[string]string{"GET": "Get", "POST": "Create", "PUT": "Update", "PATCH": "Update", "DELETE": "Delete"}[method]
	if verb == "" {
		verb = method
	}
	if method == "GET" && !parameter {
		verb = "List"
	} else {
		resource = strings.TrimSuffix(resource, "s")
	}
	return verb + " " + resource
}

func (w *collectionWizard) split(at int) {
	g := w.groups[at]
	var split []*collectionEndpoint
	for _, i := range g.flows {
		u, _ := url.Parse(w.flows[i].url)
		p := cmp.Or(u.EscapedPath(), "/")
		n := slices.IndexFunc(split, func(other *collectionEndpoint) bool { return other.template == p })
		if n < 0 {
			split = append(split, &collectionEndpoint{id: uuid.New().String(), origin: g.origin, method: g.method, template: p, include: g.include, folder: g.folder, name: g.method + " " + p})
			n = len(split) - 1
		}
		split[n].flows = append(split[n].flows, i)
		delete(w.examples, i)
	}
	for _, endpoint := range split {
		w.defaultExamples(endpoint)
	}
	w.groups = slices.Replace(w.groups, at, at+1, split...)
}

// A manual merge infers only differing whole segments, never across methods
// or origins. Different segment counts must first be edited into one template.
func (w *collectionWizard) merge(from, to int) error {
	a, b := w.groups[from], w.groups[to]
	x, y := strings.Split(a.template, "/"), strings.Split(b.template, "/")
	if from == to || a.origin != b.origin || a.method != b.method || len(x) != len(y) {
		return errors.New("merge endpoints with the same host, method and path depth")
	}
	reserved := map[string]bool{}
	for _, parts := range [][]string{x, y} {
		for _, part := range parts {
			if strings.HasPrefix(part, ":") {
				reserved[part] = true
			}
		}
	}
	n := 0
	for i := range x {
		if x[i] != y[i] {
			for {
				n++
				if !reserved[fmt.Sprintf(":param%d", n)] {
					break
				}
			}
			x[i] = fmt.Sprintf(":param%d", n)
		}
	}
	a.template, a.flows = strings.Join(x, "/"), append(a.flows, b.flows...)
	slices.Sort(a.flows)
	for _, i := range a.flows {
		delete(w.examples, i)
	}
	w.defaultExamples(a)
	w.groups = slices.Delete(w.groups, to, to+1)
	w.active = slices.Index(w.groups, a)
	return nil
}

func (w *collectionWizard) findCredentials() {
	for _, i := range w.scope() {
		f := w.flows[i]
		for _, pair := range headerPairs(f.reqHeaders, f.reqHeader) {
			lower := strings.ToLower(pair.Name)
			if pair.Value == "" || lower != "authorization" && lower != "cookie" && lower != "x-api-key" && lower != "api-key" {
				continue
			}
			at := slices.IndexFunc(w.credentials, func(c collectionCredential) bool {
				return strings.EqualFold(c.header, pair.Name) && c.value == pair.Value
			})
			if at >= 0 {
				w.credentials[at].count++
				continue
			}
			name := strings.ReplaceAll(lower, "-", "_")
			if slices.ContainsFunc(w.credentials, func(c collectionCredential) bool { return c.variable == name }) {
				name += "_" + strconv.Itoa(len(w.credentials)+1)
			}
			w.credentials = append(w.credentials, collectionCredential{header: pair.Name, value: pair.Value, variable: name, count: 1, storage: credentialCurrent})
		}
	}
}

func (w *collectionWizard) reference(c collectionCredential) string {
	if c.storage == credentialKeep {
		return c.value
	}
	if c.storage == credentialVault {
		return "{{vault:" + c.variable + "}}"
	}
	return "{{" + c.variable + "}}"
}

func (w *collectionWizard) latest(g *collectionEndpoint) flow {
	i := g.flows[0]
	for _, other := range g.flows[1:] {
		if !w.flows[other].at.Before(w.flows[i].at) {
			i = other
		}
	}
	return w.flows[i]
}

func (w *collectionWizard) requestFor(g *collectionEndpoint) (savedRequest, error) {
	// Prefer the newest capture, including its query and payload.
	f := w.latest(g)
	r, err := w.requestSnapshot(f)
	if err != nil {
		return savedRequest{}, err
	}
	u, _ := url.Parse(f.url)
	base := g.origin
	for _, h := range w.hosts {
		if h.origin == g.origin {
			base = "{{" + h.variable + "}}"
		}
	}
	parts := strings.Split(g.template, "/")
	for i, part := range parts {
		if name, ok := strings.CutPrefix(part, ":"); ok {
			if !templateSegment.MatchString(name) {
				return savedRequest{}, errors.New("path parameter names must be identifiers")
			}
			parts[i] = "{{" + name + "}}"
		}
	}
	r.ID, r.Name = g.id, strings.TrimSpace(g.name)
	r.Collection = cmp.Or(w.destination, collectionSegment(w.name))
	if g.folder != "" {
		r.Collection += "/" + cleanPath(g.folder)
	}
	r.URL = base + strings.Join(parts, "/")
	if u.RawQuery != "" {
		r.URL += "?" + u.RawQuery
	}
	return r, nil
}

// requestSnapshot preserves the producing capture, except for credential
// values explicitly governed by the wizard's storage choices. Examples keep
// literal paths and queries: shared path variables describe only the newest
// representative request, not every historical capture.
func (w *collectionWizard) requestSnapshot(f flow) (savedRequest, error) {
	r, err := f.requestDraft()
	if err != nil {
		return savedRequest{}, err
	}
	// Work on raw auth headers so Basic and arbitrary auth schemes are also
	// stored as a single private variable, never split into public fields.
	h := request.ParseHeaderBlock(r.Headers)
	replaced := map[string]bool{}
	for _, pair := range headerPairs(f.reqHeaders, f.reqHeader) {
		for _, c := range w.credentials {
			if strings.EqualFold(pair.Name, c.header) && pair.Value == c.value {
				key := strings.ToLower(pair.Name)
				if !replaced[key] {
					h.Del(pair.Name)
					replaced[key] = true
				}
				h.Add(pair.Name, w.reference(c))
				if strings.EqualFold(pair.Name, "Authorization") {
					r.Auth = request.Auth{Type: "None"}
				}
			}
		}
	}
	r.Headers = request.HeaderBlock(h)
	return r, nil
}

// build performs all body reads and validation before any store is touched.
func (w *collectionWizard) build() ([]savedRequest, []request.Folder, map[string]string, vault.Secrets, error) {
	root := cmp.Or(w.destination, collectionSegment(w.name))
	if root == "" || strings.TrimSpace(w.environmentName) == "" && w.environment == "New environment" {
		return nil, nil, nil, nil, errors.New("name the collection and environment")
	}
	vars := map[string]string{}
	seen := map[string]bool{}
	for _, h := range w.hosts {
		if !slices.ContainsFunc(w.groups, func(g *collectionEndpoint) bool { return g.include && g.origin == h.origin }) {
			continue
		}
		if !templateSegment.MatchString(h.variable) || seen[h.variable] {
			return nil, nil, nil, nil, errors.New("host variable names must be unique identifiers")
		}
		vars[h.variable], seen[h.variable] = h.origin, true
	}
	var secrets vault.Secrets
	for _, c := range w.credentials {
		if c.storage == credentialKeep {
			continue
		}
		var domains []string
		referenced := false
		matches := func(f flow) bool {
			return slices.ContainsFunc(headerPairs(f.reqHeaders, f.reqHeader), func(pair harNameValue) bool {
				return strings.EqualFold(pair.Name, c.header) && pair.Value == c.value
			})
		}
		for _, g := range w.groups {
			if !g.include {
				continue
			}
			if matches(w.latest(g)) {
				u, _ := url.Parse(g.origin)
				if !slices.Contains(domains, u.Hostname()) {
					domains = append(domains, u.Hostname())
				}
			}
			for _, i := range g.flows {
				if w.examples[i] && matches(w.flows[i]) {
					referenced = true
				}
			}
		}
		if len(domains) == 0 && !referenced {
			continue
		}
		if !templateSegment.MatchString(c.variable) || seen[c.variable] {
			return nil, nil, nil, nil, errors.New("credential variable names must be unique identifiers")
		}
		seen[c.variable] = true
		// Examples may keep private references to older credentials, but must
		// not newly persist their values. Reserve their names so those references
		// cannot accidentally resolve to a host, path or another credential.
		if len(domains) == 0 {
			continue
		}
		if c.storage == credentialVault {
			secrets = append(secrets, vault.Secret{Name: c.variable, Value: c.value, Domains: domains})
		} else {
			vars[c.variable] = c.value
		}
	}
	var list []savedRequest
	folders := []request.Folder{{Path: root}}
	for _, g := range w.groups {
		if !g.include {
			continue
		}
		if !strings.HasPrefix(g.template, "/") || strings.ContainsAny(g.template, "?#{}") {
			return nil, nil, nil, nil, errors.New("templates must start with / and use :name for whole path parameters")
		}
		r, err := w.requestFor(g)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("%s: %w", g.method+" "+g.template, err)
		}
		if r.Name == "" {
			return nil, nil, nil, nil, errors.New("every request needs a name")
		}
		// A variable has one initial value in the selected environment. The
		// Variables page lists it; users can rename parameters per endpoint.
		u, _ := url.Parse(w.latest(g).url)
		actual, template := strings.Split(u.EscapedPath(), "/"), strings.Split(g.template, "/")
		for i, part := range template {
			if name, ok := strings.CutPrefix(part, ":"); ok && i < len(actual) {
				if seen[name] {
					return nil, nil, nil, nil, errors.New("path parameters must not reuse host or credential variable names")
				}
				if _, exists := vars[name]; !exists {
					vars[name] = actual[i] // Keep encoded slashes inside the segment.
				}
			}
		}
		for _, i := range g.flows {
			if !w.examples[i] {
				continue
			}
			f := w.flows[i]
			snapshot, err := w.requestSnapshot(f)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%s example %d: %w; deselect this response to continue", r.Name, f.status, err)
			}
			h := f.respHeader
			if h == nil {
				h = request.ParseHeaderBlock(f.resHeaders)
			}
			res := &capture.Flow{Status: f.status, ResponseBody: f.resBody, ResponseBodyFile: f.resFile, ResponseBodySize: f.resSize, ResponseBodyError: f.resError, ResponseHeader: h}
			ex, err := newExample(snapshot, res, r.Examples)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%s example %d: %w; deselect this response to continue", r.Name, f.status, err)
			}
			// Session cookies are not portable example response headers.
			eh := request.ParseHeaderBlock(ex.Headers)
			eh.Del("Set-Cookie")
			ex.Headers = request.HeaderBlock(eh)
			r.Examples = append(r.Examples, ex)
		}
		list = fileRequest(list, r)
	}
	if len(list) == 0 {
		return nil, nil, nil, nil, errors.New("select at least one endpoint")
	}
	// Share exactly identical Authorization across all saved requests. Other
	// credentials remain variable references in headers; Auth is a single
	// scheme, not a container for unrelated Cookie/API-key headers.
	auth := request.ParseHeaderBlock(list[0].Headers).Get("Authorization")
	if auth != "" && slices.ContainsFunc(w.credentials, func(c collectionCredential) bool {
		return strings.EqualFold(c.header, "Authorization") && c.storage != credentialKeep && w.reference(c) == auth
	}) &&
		!slices.ContainsFunc(list, func(r savedRequest) bool { return request.ParseHeaderBlock(r.Headers).Get("Authorization") != auth }) {
		folders[0].Auth = request.Auth{Type: "API key", Key: "Authorization", Value: auth, Location: "Header"}
		for i := range list {
			h := request.ParseHeaderBlock(list[i].Headers)
			h.Del("Authorization")
			list[i].Headers, list[i].Auth = request.HeaderBlock(h), request.Auth{Type: request.AuthInherit}
			// ExampleRequest has no inheritable Auth. Keep its own sanitized
			// headers, including the producing capture's authorization reference.
		}
	}
	return list, folders, vars, secrets, nil
}

func (a *app) openCollectionWizard(flows []flow) {
	w := &a.collectionUI
	*w = collectionWizard{open: true, flows: slices.Clone(flows), static: true, tunnels: true, failed: true, grouping: 1, environment: "New environment", environmentName: "Captured", name: a.newCollectionName("Captured API")}
	w.regroup()
}

func (a *app) createCollection() {
	w := &a.collectionUI
	if w.building != nil {
		return
	}
	w.problem = ""
	// Draft controls are disabled during the build. Cancelling/reopening
	// replaces the draft and rejects the obsolete completion below.
	draft := new(*w)
	w.building = draft
	build := func() {
		list, folders, vars, secrets, err := draft.build()
		finish := func() {
			if w.building != draft || !w.open {
				return
			}
			w.building = nil
			a.saveCollection(list, folders, vars, secrets, err)
		}
		if a.win == nil {
			finish() // Headless callers have no window event loop.
		} else {
			a.win.Update(finish)
		}
	}
	if a.win == nil {
		build()
	} else {
		go build()
	}
}

func (a *app) saveCollection(list []savedRequest, folders []request.Folder, vars map[string]string, secrets vault.Secrets, err error) {
	w := &a.collectionUI
	if err != nil {
		w.problem = err.Error()
		return
	}
	root := folders[0].Path
	if w.destination == "" && slices.Contains(a.collectionNames(), root) {
		w.problem = "This collection exists; choose it in Save to or use another name."
		return
	}
	if a.envsProblem != "" {
		w.problem = a.envsProblem
		return
	}
	envName := w.environment
	if envName == "New environment" {
		envName = strings.TrimSpace(w.environmentName)
		if slices.Contains(a.envNames(), envName) {
			w.problem = "This environment exists; select it instead of New environment."
			return
		}
	}
	envs := slices.Clone(a.envs)
	at := slices.IndexFunc(envs, func(e environment) bool { return e.Name == envName })
	if at < 0 {
		envs = append(envs, environment{Name: envName})
		at = len(envs) - 1
	}
	envs[at].Vars, envs[at].Current, envs[at].Credentials = maps.Clone(envs[at].Vars), maps.Clone(envs[at].Current), maps.Clone(envs[at].Credentials)
	if envs[at].Vars == nil {
		envs[at].Vars = map[string]string{}
	}
	if envs[at].Current == nil {
		envs[at].Current = map[string]string{}
	}
	effective := envs[at].values()
	for name, value := range vars {
		private := slices.ContainsFunc(w.credentials, func(c collectionCredential) bool { return c.variable == name && c.storage == credentialCurrent })
		target := envs[at].Vars
		if private {
			target = envs[at].Current
			if envs[at].Credentials == nil {
				envs[at].Credentials = map[string]bool{}
			}
			envs[at].Credentials[name] = true
		}
		if old, exists := effective[name]; exists && old != value {
			w.problem = "Environment variable “" + name + "” already has a different value; rename the variable or choose a new environment."
			return
		}
		target[name] = value
	}
	// Never replace an existing collection's auth; appended requests keep
	// equivalent per-request auth instead.
	if w.destination != "" {
		for i := range list {
			if list[i].Auth.Type == request.AuthInherit {
				list[i].Auth = folders[0].Auth
			}
		}
		folders = nil
	}
	var spec apiSpec
	if w.generateAPI {
		// Generate from this draft only, including its subfolders.
		apiList := slices.Clone(list)
		for i := range apiList {
			apiList[i].Collection = root
		}
		spec, err = generateOpenAPI(apiList, root)
		if err != nil {
			w.problem = err.Error()
			return
		}
	}
	// Save dependencies before the collection that references them. A library
	// conflict leaves the wizard open; an explicit retry is idempotent.
	if len(secrets) > 0 {
		if err := a.ensureVault(); err != nil {
			w.problem = err.Error()
			return
		}
		all := slices.Clone(a.vault.secrets)
		for _, secret := range secrets {
			if old := slices.IndexFunc(all, func(s vault.Secret) bool { return s.Name == secret.Name }); old >= 0 {
				if all[old].Value != secret.Value || !slices.Equal(all[old].Domains, secret.Domains) {
					w.problem = "Vault name “" + secret.Name + "” already exists; rename the variable."
					return
				}
			} else {
				all = append(all, secret)
			}
		}
		if err := a.writeVault(all); err != nil {
			w.problem = err.Error()
			return
		}
	}
	if err := func() error {
		f := a.localEnvs()
		f.Active, f.Envs = envName, envs
		return a.saveEnvFile(f)
	}(); err != nil {
		w.problem = err.Error()
		return
	}
	a.envs, a.activeEnv = envs, envName
	w.environment = envName // A retry after a library conflict reuses it.
	if err := a.addToLibrary(list, folders); err != nil {
		w.problem = "Collection not saved: " + err.Error() + ". Environment/credential values were kept; retry after resolving the error."
		return
	}
	if w.generateAPI {
		a.addAPI(spec, false)
	}
	w.open = false
	a.done(locales.CollectionCreated(root, len(list)))
}

// collectionScope selects by the sidebar object's actual identity, not by
// the currently active table filters.
func (a *app) collectionScope(host, process string) []flow {
	return slices.DeleteFunc(slices.Clone(a.flows), func(f flow) bool { return host != "" && f.host != host || process != "" && f.process != process })
}
