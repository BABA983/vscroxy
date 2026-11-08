package app

import (
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

const collectionRecordingQueue = 32

type collectionRecordingConfig struct {
	Collection  string   `json:"collection"`
	Environment string   `json:"environment"`
	Hosts       []string `json:"hosts,omitempty"`
	Apps        []string `json:"apps,omitempty"`
}

// UI-owned session. Workers only convert one immutable flow at a time; guarded
// writes and editor merges stay with the library owner on the UI thread.
type collectionRecording struct {
	collectionRecordingConfig
	Active        bool      `json:"active"`
	Busy          bool      `json:"busy"`
	StartedAt     time.Time `json:"startedAt,omitzero"`
	Added         int       `json:"added"`
	Updated       int       `json:"updated"`
	Skipped       int       `json:"skipped"`
	Discarded     int       `json:"discarded"`
	Problem       string    `json:"problem"`
	EvidenceIDs   []int     `json:"evidenceIds"`
	evidence      []flow    // bounded raw inference evidence, never exported or persisted
	evidenceBytes int
	queue         []flow
	endpoints     map[string]string // origin + method + inferred path → saved ID
	latest        map[string]time.Time
	dropped       int64
	spills        map[string]recordingSpill
}

type recordingSpill struct {
	refs    int
	discard bool
}

type recordedCapture struct {
	request  savedRequest
	example  request.Example
	shared   map[string]string
	current  map[string]string
	before   environment
	evidence flow // worker-owned complete response, not the sanitized Example
}

func (a *app) controlCollectionRecording(command control.Command) (any, error) {
	switch command.Action {
	case "collection-recording.get":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
	case "collection-recording.start":
		p, err := controlParams[struct {
			collectionRecordingConfig
			Confirm bool `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("recording configuration and confirm:true are required")
		}
		if err := a.startCollectionRecording(p.collectionRecordingConfig); err != nil {
			return nil, err
		}
	case "collection-recording.stop":
		p, err := controlParams[struct {
			Confirm bool `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("confirm:true is required")
		}
		a.stopCollectionRecording("")
	}
	if a.collectionRecording == nil {
		return collectionRecording{}, nil
	}
	return a.collectionRecording, nil
}

func (a *app) startCollectionRecording(config collectionRecordingConfig) error {
	if s := a.collectionRecording; s != nil && (s.Active || s.Busy) {
		return errors.New("collection recording is already active or stopping")
	}
	if a.systemProxy.quitting || a.systemProxy.quitReady || a.srv == nil || !a.srv.Running() || a.recordingPaused {
		return errors.New("resume live capture before recording a collection")
	}
	if config.Collection == "" || config.Collection != cleanPath(config.Collection) || len(config.Collection) > 256 {
		return errors.New("a nonempty normalized collection name is required")
	}
	if a.library.root == "" || a.envsPath == "" || a.requestsProblem != "" || a.envsProblem != "" {
		return errors.New("repair collection and environment storage before recording")
	}
	if config.Environment == "" || !slices.ContainsFunc(a.envs, func(e environment) bool { return e.Name == config.Environment }) {
		return errors.New("select an existing environment for recording variables")
	}
	if a.varsOpen && !a.varsGlobals && (a.varsChoice == config.Environment || a.varsName == config.Environment) {
		return errors.New("close the selected environment's variable editor before recording")
	}
	if len(config.Hosts) > 32 || len(config.Apps) > 32 {
		return errors.New("at most 32 host and App filters are supported")
	}
	for _, host := range config.Hosts {
		u, err := url.Parse("http://" + host)
		if err != nil || len(host) > 256 || u.Hostname() == "" || u.Host != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(host, "*{} \t\r\n") {
			return errors.New("host filters must be exact hostnames or host:port values, without schemes or wildcards")
		}
	}
	for _, app := range config.Apps {
		if strings.TrimSpace(app) != app || app == "" || len(app) > 256 || strings.ContainsAny(app, "\r\n") {
			return errors.New("App filters must be nonempty process names")
		}
	}
	config.Hosts, config.Apps = slices.Clone(config.Hosts), slices.Clone(config.Apps)
	a.collectionRecording = &collectionRecording{collectionRecordingConfig: config, Active: true, StartedAt: time.Now(), endpoints: map[string]string{}, latest: map[string]time.Time{}, dropped: a.srv.Dropped(), spills: map[string]recordingSpill{}}
	return nil
}

// Stop is a write barrier, not a promise to flush. An in-flight conversion can
// finish reading, but cannot publish after Stop; no worker waits for the UI.
func (a *app) stopCollectionRecording(problem string) {
	s := a.collectionRecording
	if s == nil || !s.Active {
		return
	}
	s.Active, s.Problem = false, problem
	s.Discarded += len(s.queue)
	for _, f := range s.queue {
		a.releaseRecordingSpills(s, f)
	}
	s.queue = nil
	if s.Busy {
		s.Discarded++
	}
	if problem != "" {
		a.fail(locales.CollectionRecordingStopped(), errors.New(problem))
	}
}

// Called only for new arrivals, before capture-table eviction. Session imports,
// historical traffic and our own Send/Runner/replays never enter this pipeline.
func (a *app) recordCollection(flows []flow) {
	s := a.collectionRecording
	if s == nil || !s.Active {
		return
	}
	if a.srv.Dropped() != s.dropped {
		a.stopCollectionRecording("Capture dropped traffic; reduce load and start recording again")
		return
	}
	if a.recordingPaused {
		a.stopCollectionRecording("Capture was paused; start collection recording again after resuming")
		return
	}
	for _, f := range flows {
		if !f.live || f.at.Before(s.StartedAt) || f.debugRun != "" || f.replayOf != 0 {
			continue
		}
		u, err := url.Parse(f.url)
		if err != nil || u.User != nil || len(s.Hosts) > 0 && !slices.ContainsFunc(s.Hosts, func(h string) bool {
			return strings.EqualFold(h, u.Host) || strings.EqualFold(h, u.Hostname())
		}) || len(s.Apps) > 0 && !slices.Contains(s.Apps, f.process) {
			s.Skipped++
			continue
		}
		w := collectionWizard{flows: []flow{f}, static: true, tunnels: true, failed: true}
		if len(w.scope()) == 0 {
			s.Skipped++
			continue
		}
		if len(s.queue) >= collectionRecordingQueue {
			a.stopCollectionRecording("Recording queue is full; reduce the host/App scope and start again")
			return
		}
		// Bound retained queue memory as well as later spill reads. Captures
		// beyond the text-request/example contract remain in LIVE, not a preview
		// silently masquerading as a complete request in the collection.
		size := len(f.reqBody) + len(f.resBody) + len(f.url) + len(f.reqHeaders) + len(f.resHeaders)
		for _, h := range []map[string][]string{f.reqHeader, f.respHeader, f.reqTrailer} {
			for name, values := range h {
				size += len(name)
				for _, value := range values {
					size += len(value)
				}
			}
		}
		if f.reqSize > request.MaxExampleBody || f.resSize > request.MaxExampleBody || size > 1<<20 {
			s.Skipped++
			continue
		}
		f.reqHeader, f.respHeader, f.reqTrailer = f.reqHeader.Clone(), f.respHeader.Clone(), f.reqTrailer.Clone()
		for _, file := range []string{f.reqFile, f.resFile} {
			if file != "" {
				lease := s.spills[file]
				lease.refs++
				s.spills[file] = lease
			}
		}
		s.queue = append(s.queue, f)
	}
	a.nextCollectionRecording()
}

func (a *app) nextCollectionRecording() {
	s := a.collectionRecording
	if s == nil || !s.Active || s.Busy || len(s.queue) == 0 {
		return
	}
	if a.systemProxy.quitting || a.systemProxy.quitReady || a.recordingPaused {
		a.stopCollectionRecording("Capture was paused or the application is quitting")
		return
	}
	env := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == s.Environment })
	if env < 0 || a.envsProblem != "" || a.requestsProblem != "" {
		a.stopCollectionRecording("Collection or environment is unavailable; repair storage before restarting")
		return
	}
	f := s.queue[0]
	s.queue[0] = flow{}
	s.queue = s.queue[1:]
	s.Busy = true
	// Reserve all effective scope names; never hide a private credential under
	// a folder/global override or replace a reader's existing variable value.
	vars := a.globalValues()
	if vars == nil {
		vars = map[string]string{}
	}
	maps.Copy(vars, a.envs[env].values())
	for _, folder := range a.folders {
		for _, v := range folder.Variables {
			vars[v.Name] = v.Value()
		}
	}
	config := s.collectionRecordingConfig
	before := a.envs[env]
	before.Vars, before.Current = maps.Clone(before.Vars), maps.Clone(before.Current)
	win := a.win
	convert := func() {
		result, err := prepareRecordedCapture(f, config, before, vars)
		finish := func() {
			a.finishRecordedCapture(s, f, result, err)
		}
		if win == nil {
			finish()
		} else {
			win.Update(finish)
		}
	}
	if win == nil {
		convert()
	} else {
		go convert()
	}
}

func (a *app) finishRecordedCapture(s *collectionRecording, f flow, result recordedCapture, err error) {
	defer a.releaseRecordingSpills(s, f)
	s.Busy = false
	if a.collectionRecording != s || !s.Active {
		return
	}
	if a.srv.Dropped() != s.dropped {
		s.Discarded++
		a.stopCollectionRecording("Capture dropped traffic; reduce load and start recording again")
		return
	}
	if a.systemProxy.quitting || a.systemProxy.quitReady || a.recordingPaused {
		s.Discarded++
		a.stopCollectionRecording("Capture was paused or the application is quitting")
		return
	}
	if err != nil {
		s.Skipped++ // no raw body/file/error text in diagnostics
	} else if err := a.keepRecordedCapture(f, result); err != nil {
		s.Discarded++
		a.stopCollectionRecording(err.Error())
		return
	}
	a.nextCollectionRecording()
}

// Retention may evict the row, but queued/in-flight conversions still own its
// engine files. Deferred deletion runs as soon as the last lease is released.
func (a *app) discardCaptureFiles(files []string) {
	var discard []string
	for _, file := range files {
		s := a.collectionRecording
		if s != nil && s.spills[file].refs > 0 {
			lease := s.spills[file]
			lease.discard = true
			s.spills[file] = lease
		} else {
			discard = append(discard, file)
		}
	}
	if a.srv != nil {
		a.srv.Discard(discard)
	}
}

func (a *app) releaseRecordingSpills(s *collectionRecording, f flow) {
	for _, file := range []string{f.reqFile, f.resFile} {
		if file == "" {
			continue
		}
		lease := s.spills[file]
		lease.refs--
		if lease.refs > 0 {
			s.spills[file] = lease
		} else {
			delete(s.spills, file)
			if lease.discard && a.srv != nil {
				a.srv.Discard([]string{file})
			}
		}
	}
}

func prepareRecordedCapture(f flow, config collectionRecordingConfig, before environment, reserved map[string]string) (recordedCapture, error) {
	var err error
	// Use the same bounded, complete spill-file contract as raw HAR export.
	f.reqBody, err = controlHARBody(f.reqBody, f.reqFile, f.reqSize, f.reqError, request.MaxExampleBody)
	if err != nil {
		return recordedCapture{}, err
	}
	f.resBody, err = controlHARBody(f.resBody, f.resFile, f.resSize, f.resError, request.MaxExampleBody)
	if err != nil {
		return recordedCapture{}, err
	}
	f.reqFile, f.resFile = "", ""
	if f.respHeader == nil {
		f.respHeader = request.ParseHeaderBlock(f.resHeaders)
	}
	w := collectionWizard{flows: []flow{f}, destination: config.Collection, grouping: 1}
	w.regroup()
	if len(w.groups) != 1 {
		return recordedCapture{}, errors.New("not a finite HTTP exchange")
	}
	g := w.groups[0]
	shared, current := map[string]string{}, map[string]string{}
	allocate := func(base, value string, target, original map[string]string) string {
		name := base
		for n := 2; ; n++ {
			old, exists := reserved[name]
			// Equal effective values are not enough: a Current override must
			// never authorize rewriting a different underlying shared value.
			if !exists || old == value && original[name] == value {
				break
			}
			name = fmt.Sprintf("%s_%d", base, n)
		}
		reserved[name], target[name] = value, value
		return name
	}
	hostKey := fmt.Sprintf("record_%x", sha256.Sum256([]byte(g.origin)))[:19]
	endpointKey := fmt.Sprintf("record_%x", sha256.Sum256([]byte(g.origin+"\x00"+g.method+"\x00"+g.template)))[:19]
	w.hosts[0].variable = allocate(hostKey+"_base_url", g.origin, shared, before.Vars)
	for i := range w.credentials {
		c := &w.credentials[i]
		c.variable = allocate(hostKey+"_"+c.variable, c.value, current, before.Current)
	}
	u, _ := url.Parse(f.url)
	actual, template := strings.Split(cmp.Or(u.EscapedPath(), "/"), "/"), strings.Split(g.template, "/")
	for i, part := range template {
		if name, ok := strings.CutPrefix(part, ":"); ok {
			template[i] = ":" + allocate(endpointKey+"_"+name, actual[i], shared, before.Vars)
		}
	}
	g.template = strings.Join(template, "/")
	r, err := w.requestFor(g)
	if err != nil {
		return recordedCapture{}, err
	}
	snapshot, err := w.requestSnapshot(f)
	if err != nil {
		return recordedCapture{}, err
	}
	ex, err := newExample(snapshot, &capture.Flow{Status: f.status, ResponseHeader: f.respHeader, ResponseBody: f.resBody, ResponseBodySize: len(f.resBody)}, nil)
	if err != nil {
		return recordedCapture{}, err
	}
	// Continuous recording replaces only its own example for this response
	// category. Manually saved examples retain their IDs, names and contents.
	category := f.status
	if category >= 200 && category < 300 {
		category = 200
	}
	ex.ID = fmt.Sprintf("recorded-%x-%d", sha256.Sum256([]byte(recordedEndpointKey(f))), category)
	h := request.ParseHeaderBlock(ex.Headers)
	h.Del("Set-Cookie")
	ex.Headers = request.HeaderBlock(h)
	// Keep only what inference and request matching need. This deliberately
	// comes from the complete raw exchange, never the substituted snapshot.
	evidence := flow{id: f.id, live: f.live, at: f.at, millis: f.millis,
		method: f.method, scheme: f.scheme, host: f.host, path: f.path, url: f.url,
		status: f.status, reqHeaders: f.reqHeaders, resHeaders: f.resHeaders,
		reqHeader: f.reqHeader, respHeader: f.respHeader, resBody: f.resBody, resSize: len(f.resBody)}
	return recordedCapture{request: r, example: ex, shared: shared, current: current, before: before, evidence: evidence}, nil
}

// Payload bytes include metadata and header framing, not just response bodies.
// The retained flows contain no other variable-sized fields.
func recordedEvidenceBytes(f flow) int {
	size := len(f.resBody)
	for _, value := range []string{f.url, f.scheme, f.host, f.path, f.method, f.reqHeaders, f.resHeaders} {
		size += len(value)
	}
	for _, h := range []map[string][]string{f.reqHeader, f.respHeader} {
		for name, values := range h {
			size += len(name) + 2
			for _, value := range values {
				size += len(value) + 2
			}
		}
	}
	return size
}

func (s *collectionRecording) retainEvidence(f flow) {
	if f.id <= 0 || len(f.resBody) > tokenResponseLimit || !tokenMetadataOK(f) {
		return
	}
	size := recordedEvidenceBytes(f)
	for len(s.evidence) > 0 && (len(s.evidence) >= tokenSelectionLimit || s.evidenceBytes+size > tokenReadLimit) {
		s.evidenceBytes -= recordedEvidenceBytes(s.evidence[0])
		s.evidence[0] = flow{}
		s.evidence = s.evidence[1:]
	}
	s.evidence = append(s.evidence, f)
	s.evidenceBytes += size
	s.EvidenceIDs = s.EvidenceIDs[:0]
	for _, f := range s.evidence {
		s.EvidenceIDs = append(s.EvidenceIDs, f.id)
	}
	slices.Sort(s.EvidenceIDs) // completion order need not be capture-ID order
}

func recordedEndpointKey(f flow) string {
	u, _ := url.Parse(f.url)
	return strings.ToLower(u.Scheme+"://"+u.Host) + "\x00" + f.method + "\x00" + inferredCollectionPath(cmp.Or(u.EscapedPath(), "/"))
}

func (a *app) keepRecordedCapture(f flow, result recordedCapture) error {
	s := a.collectionRecording
	env := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == s.Environment })
	if env < 0 || a.envsProblem != "" || a.requestsProblem != "" {
		return errors.New("Collection or environment is unavailable; repair storage before restarting")
	}
	if last := s.latest[result.example.ID]; f.at.Before(last) {
		s.Skipped++
		return nil // delivery order is not capture-time order
	}
	key := recordedEndpointKey(f)
	index := -1
	for i, r := range a.requests {
		if r.Collection != s.Collection && !strings.HasPrefix(r.Collection, s.Collection+"/") {
			continue
		}
		match := r.ID == s.endpoints[key]
		if s.endpoints[key] == "" {
			vars := a.inheritance(r.Collection).Values(a.envs[env].values())
			u, p := matchTemplateURL(r.URL, vars)
			target, _ := url.Parse(f.url)
			match = u != nil && strings.EqualFold(cmp.Or(r.Method, "GET"), f.method) && strings.EqualFold(u.Scheme, target.Scheme) && strings.EqualFold(u.Hostname(), target.Hostname()) && matchPort(u) == matchPort(target) && matchPath(p, target.EscapedPath())
		}
		if match {
			if index >= 0 {
				return errors.New("Multiple saved requests match an endpoint; narrow the destination collection")
			}
			index = i
		}
	}
	if s.endpoints[key] != "" && index < 0 {
		return errors.New("A recorded request was moved or deleted; choose a destination and start again")
	}
	r := result.request
	if index >= 0 {
		r = a.requests[index] // never rewrite authored URL, body, auth, scripts, etc.
		result.shared = nil   // Examples have literal paths; keep the initial URL variables.
	} else {
		r.ID = uuid.New().String()
		r.Name = uniqueRequestName(a.requests, r.Name)
	}
	r.Examples = slices.Clone(r.Examples)
	if i := slices.IndexFunc(r.Examples, func(e request.Example) bool { return e.ID == result.example.ID }); i >= 0 {
		r.Examples[i] = result.example
	} else {
		r.Examples = append(r.Examples, result.example)
	}
	envs := slices.Clone(a.envs)
	e := &envs[env]
	e.Vars, e.Current, e.Credentials = maps.Clone(e.Vars), maps.Clone(e.Current), maps.Clone(e.Credentials)
	if e.Vars == nil {
		e.Vars = map[string]string{}
	}
	if e.Current == nil {
		e.Current = map[string]string{}
	}
	if e.Credentials == nil {
		e.Credentials = map[string]bool{}
	}
	for _, set := range []struct{ values, target map[string]string }{{result.shared, e.Vars}, {result.current, e.Current}} {
		for name, value := range set.values {
			for _, layer := range []struct{ before, now map[string]string }{{result.before.Vars, e.Vars}, {result.before.Current, e.Current}} {
				old, had := layer.before[name]
				now, has := layer.now[name]
				if had != has || old != now {
					return errors.New("Recording variables changed while preparing a capture; restart to preserve your edits")
				}
			}
			// A new folder/global override introduced while converting must not
			// redirect a recorded URL or expose a different credential.
			if old, ok := a.globals[name]; ok && old != value {
				return errors.New("Recording variable conflicts with a global value; restart recording")
			}
			for _, folder := range a.folders {
				for _, v := range folder.Variables {
					if v.Name == name && v.Value() != value {
						return errors.New("Recording variable conflicts with a folder value; restart recording")
					}
				}
			}
			set.target[name] = value
			if _, recorded := result.current[name]; recorded {
				e.Credentials[name] = true
			}
		}
	}
	// Dependencies first. A later library conflict may leave unused variables,
	// never a request referencing a value that wasn't saved. No raw error text
	// (which may quote a capture) enters notifications or control responses.
	if err := func() error {
		f := a.localEnvs()
		f.Envs = envs
		return saveEnvs(a.file(a.envsPath), f)
	}(); err != nil {
		return errors.New("Could not save recording variables; resolve the environment file conflict and restart")
	}
	a.envs = envs
	list := upsertRequest(slices.Clone(a.requests), r)
	if err := a.persistRequests(list); err != nil {
		return errors.New("Could not save recorded request; resolve the collection file conflict and restart")
	}
	s.retainEvidence(result.evidence)
	s.endpoints[key] = r.ID
	s.latest[result.example.ID] = f.at
	if index < 0 {
		s.Added++
	} else {
		s.Updated++
	}
	// Merge only the recorder's example into open drafts, including dirty ones.
	// Baselines advance too, so a later Save cannot restore the stale example.
	for _, d := range a.documents {
		if d.savedKey == r.key() {
			d.examples = slices.Clone(d.examples)
			if i := slices.IndexFunc(d.examples, func(e request.Example) bool { return e.ID == result.example.ID }); i >= 0 {
				d.examples[i] = result.example
			} else {
				d.examples = append(d.examples, result.example)
			}
			d.baseline.Examples, d.exampleEditor = slices.Clone(r.Examples), nil
		}
	}
	return nil
}
