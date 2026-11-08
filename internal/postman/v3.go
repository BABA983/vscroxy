package postman

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/yamlguard"
)

// Postman Collection v3 is a directory of YAML files rather than one JSON
// document. The collection and every folder may have a
// .resources/definition.yaml, each request is a *.request.yaml file, and
// examples and stream messages live under .resources/<request>.resources/.
// The reader turns that tree back into the v2.1 shapes this package already
// converts, so auth, bodies, scripts, variables and examples have one
// implementation.
//
// A directory is untrusted input: the reader never follows a symbolic link,
// never reads outside the collection root, and stops at fixed file-count and
// file-size limits.

const (
	// v3MaxFile bounds one example, message or request YAML file. Postman
	// examples are capped at 5 MiB; a request body can be larger than its
	// definition.
	v3MaxFile = 8 << 20
	// v3MaxDocument bounds one collection or folder definition.
	v3MaxDocument = 1 << 20
	// v3MaxSchema bounds an imported GraphQL SDL file.
	v3MaxSchema = 1 << 20
	// v3MaxDepth bounds folder nesting.
	v3MaxDepth = 32
)

var (
	// v3MaxFiles bounds how many files and directory entries one import reads.
	v3MaxFiles = 10_000
	// v3MaxBytes bounds the total YAML read by one import.
	v3MaxBytes = 64 << 20

	errV3NotRegular = errors.New("not a regular file")
	errV3Symlink    = errors.New("symbolic link")
	errV3TooMany    = errors.New("too many files")
)

// v3LimitError names the limit a file or the import hit.
type v3LimitError struct{ detail string }

func (e v3LimitError) Error() string { return "collection import limit: " + e.detail }

// v3IDOwner is the request that currently owns an explicit v3 id. Requests are
// visited in display order, so a later request with a smaller path may take
// the id over; the previous owner then gets a path-derived id.
type v3IDOwner struct {
	index int
	file  string
}

// LooksLikeCollectionDir reports whether dir is the root of a Postman
// Collection v3 directory. It checks the directory, its immediate children and
// a few more levels, because a definition is optional and requests may live
// only in nested folders. Detection has its own small budget.
func LooksLikeCollectionDir(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	budget := 2000
	return looksLikeCollectionDir(dir, 0, &budget)
}

func looksLikeCollectionDir(dir string, depth int, budget *int) bool {
	if *budget <= 0 {
		return false
	}
	*budget--
	if hasV3Definition(dir) || hasV3RequestFiles(dir) {
		return true
	}
	if depth >= 3 {
		return false
	}
	entries := readDirBounded(dir, 10_000)
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "node_modules" {
			continue
		}
		if strings.HasPrefix(name, ".") && name != ".resources" {
			continue
		}
		if looksLikeCollectionDir(filepath.Join(dir, name), depth+1, budget) {
			return true
		}
	}
	return false
}

func hasV3Definition(dir string) bool {
	resources := filepath.Join(dir, ".resources")
	info, err := os.Lstat(resources)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	for _, name := range []string{"definition.yaml", "definition.yml"} {
		info, err := os.Lstat(filepath.Join(resources, name))
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return true
		}
	}
	return false
}

func hasV3RequestFiles(dir string) bool {
	for _, entry := range readDirBounded(dir, 10_000) {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if isV3RequestFile(entry.Name()) {
			return true
		}
	}
	return false
}

// readDirBounded lists at most limit entries, for cheap detection on a path
// that may not be a collection at all.
func readDirBounded(dir string, limit int) []os.DirEntry {
	file, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer file.Close()
	entries, err := file.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	return entries
}

func isV3RequestFile(name string) bool {
	return strings.HasSuffix(name, ".request.yaml") || strings.HasSuffix(name, ".request.yml")
}

func requestStem(name string) string {
	name = strings.TrimSuffix(name, ".request.yaml")
	return strings.TrimSuffix(name, ".request.yml")
}

// ReadCollectionDir reads a Postman Collection v3 directory. The collection's
// name defaults to the directory name; folders become paths under it. What the
// app cannot hold is dropped and counted in Warnings, never silently.
func ReadCollectionDir(dir string) (Collection, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return Collection{}, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return Collection{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Collection{}, errors.New("choose a real folder, not a symbolic link")
	}
	if !info.IsDir() {
		return Collection{}, fmt.Errorf("%s is not a folder", filepath.Base(root))
	}
	r := &v3reader{root: root, paths: map[string]bool{}, idOwner: map[string]v3IDOwner{}, reader: reader{warn: tally{}}}
	def, err := r.definition(root, true)
	if err != nil {
		return Collection{}, err
	}
	name := segment(cmp.Or(strings.TrimSpace(def.Name), filepath.Base(root)))
	if name == "" {
		name = "Postman collection"
	}
	name = r.uniquePath(name)
	r.base = cmp.Or(def.ID, name)
	r.out.Name = name
	r.addFolder(name, def, "collection auth")
	if err := r.walk(root, name, 0); err != nil {
		return Collection{}, err
	}
	if r.firstLimit != "" {
		r.warn.add("v3 limit first", r.firstLimit, 1)
	}
	r.out.Warnings = r.warn.list()
	return r.out, nil
}

// v3reader carries one directory import's results and counts. The embedded
// reader is the v2.1 conversion every request and folder goes through.
type v3reader struct {
	reader
	root                string
	files               int
	bytes               int
	paths               map[string]bool
	idOwner             map[string]v3IDOwner
	firstLimit          string
	firstUnreadable     string
	firstDefinitionKind string
}

// uniquePath keeps two folders with the same display name from becoming one
// path. The first keeps its name; later ones get a counted suffix.
func (r *v3reader) uniquePath(path string) string {
	if !r.paths[path] {
		r.paths[path] = true
		return path
	}
	r.warn.add("v3 folder name", "", 1)
	for n := 2; ; n++ {
		candidate := path + " (" + strconv.Itoa(n) + ")"
		if !r.paths[candidate] {
			r.paths[candidate] = true
			return candidate
		}
	}
}

// read reads one regular file with the example/message limit.
func (r *v3reader) read(path string) ([]byte, error) {
	return r.readLimit(path, v3MaxFile, "file size")
}

// readLimit reads one regular file, bounded by its own limit and by the
// import's total byte budget. Missing files are reported as os.ErrNotExist so
// a caller can treat a definition as optional. detail names the limit in a
// warning.
func (r *v3reader) readLimit(path string, limit int, detail string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errV3Symlink
	}
	if !info.Mode().IsRegular() {
		return nil, errV3NotRegular
	}
	if r.files >= v3MaxFiles {
		return nil, errV3TooMany
	}
	if info.Size() > int64(limit) {
		r.files++
		return nil, v3LimitError{detail}
	}
	remaining := v3MaxBytes - r.bytes
	if remaining <= 0 {
		r.files++
		return nil, v3LimitError{"total size"}
	}
	maxRead := min(limit, remaining)
	if info.Size() > int64(maxRead) {
		r.files++
		return nil, v3LimitError{"total size"}
	}
	r.files++
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(maxRead)+1))
	r.bytes += len(data)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRead {
		if remaining <= limit {
			return nil, v3LimitError{"total size"}
		}
		return nil, v3LimitError{detail}
	}
	return data, nil
}

// readDir lists a directory in name order. Directories count toward the file
// budget, and a directory with more entries than the budget is truncated and
// reported, so listing it cannot allocate without bound.
func (r *v3reader) readDir(dir string) ([]os.DirEntry, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errV3Symlink
	}
	if !info.IsDir() {
		return nil, errV3NotRegular
	}
	if r.files >= v3MaxFiles {
		return nil, errV3TooMany
	}
	r.files++
	file, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(v3MaxFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > v3MaxFiles {
		r.limitProblem(dir, "files")
		entries = entries[:v3MaxFiles]
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// limitProblem counts a limit and remembers the first path that hit one, so
// the report can name it.
func (r *v3reader) limitProblem(path, detail string) {
	if r.firstLimit == "" {
		r.firstLimit = r.relative(path)
	}
	r.warn.add("v3 limit", detail, 1)
}

// fileProblem counts why one file or directory could not be read.
func (r *v3reader) fileProblem(err error, path string) {
	var limit v3LimitError
	switch {
	case errors.As(err, &limit):
		r.limitProblem(path, limit.detail)
	case errors.Is(err, errV3TooMany):
		r.limitProblem(path, "files")
	default:
		r.warn.add("v3 path", "", 1)
	}
}

// relative names a file from the collection root for a warning.
func (r *v3reader) relative(path string) string {
	rel, err := filepath.Rel(r.root, path)
	if err != nil {
		return filepath.ToSlash(filepath.Base(path))
	}
	return filepath.ToSlash(rel)
}

// yamlLimit reports whether err is a YAML alias or expansion limit. Those are
// input limits, not malformed files, and are counted as v3 limits.
func (r *v3reader) yamlLimit(path string, err error) bool {
	switch {
	case errors.Is(err, yamlguard.ErrAlias):
		r.limitProblem(path, "YAML aliases")
		return true
	case errors.Is(err, yamlguard.ErrExpansion):
		r.limitProblem(path, "YAML expansion")
		return true
	}
	return false
}

// unreadable counts a file that could not be decoded and remembers the first
// one, so the report can name it. YAML limit errors are counted separately.
func (r *v3reader) unreadable(path string, err error) {
	if r.yamlLimit(path, err) {
		return
	}
	if r.firstUnreadable == "" {
		r.firstUnreadable = r.relative(path)
	}
	r.warn.add("v3 unreadable", r.firstUnreadable, 1)
}

// definitionKind counts a folder definition whose $kind is not collection.
func (r *v3reader) definitionKind(path string) {
	if r.firstDefinitionKind == "" {
		r.firstDefinitionKind = r.relative(path)
	}
	r.warn.add("v3 definition kind", r.firstDefinitionKind, 1)
}

// definition reads a collection or folder definition. A malformed definition
// or a folder-level $kind other than collection is reported and skipped, so
// one bad metadata file does not lose requests. A root definition that names
// another kind is still an error: the chosen folder is not a collection.
func (r *v3reader) definition(dir string, root bool) (v3Definition, error) {
	resources := filepath.Join(dir, ".resources")
	info, err := os.Lstat(resources)
	if errors.Is(err, os.ErrNotExist) {
		return v3Definition{}, nil
	}
	if err != nil {
		r.warn.add("v3 path", "", 1)
		return v3Definition{}, nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		r.warn.add("v3 path", "", 1)
		return v3Definition{}, nil
	}
	for _, name := range []string{"definition.yaml", "definition.yml"} {
		path := filepath.Join(resources, name)
		data, err := r.readLimit(path, v3MaxDocument, "document size")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			r.fileProblem(err, path)
			return v3Definition{}, nil
		}
		var def v3Definition
		if err := unmarshalV3(data, &def); err != nil {
			r.unreadable(path, err)
			return v3Definition{}, nil
		}
		if def.Kind != "" && def.Kind != "collection" {
			if root {
				return v3Definition{}, fmt.Errorf("%s: $kind is %q, want collection", path, def.Kind)
			}
			r.definitionKind(path)
			return v3Definition{}, nil
		}
		return def, nil
	}
	return v3Definition{}, nil
}

// addFolder records a level's settings when it has any, through the v2.1
// folder conversion.
func (r *v3reader) addFolder(path string, def v3Definition, authWarning string) {
	if def.Uses.Kind() != 0 && def.Uses.Kind() != 'n' {
		r.warn.add("collection uses", "", 1)
	}
	r.folder(path, def.Description, r.authValue(def.Auth, strings.TrimSuffix(authWarning, " auth")),
		r.events(def.Scripts, "collection"), r.variables(def.Variables), request.Settings{}, authWarning)
}

// walk reads one directory's folders and request files in Postman's order.
func (r *v3reader) walk(dir, path string, depth int) error {
	if depth > v3MaxDepth {
		r.limitProblem(dir, "depth")
		return nil
	}
	entries, err := r.readDir(dir)
	if err != nil {
		if errors.Is(err, errV3TooMany) {
			r.fileProblem(err, dir)
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}
	type child struct {
		order    float64
		hasOrder bool
		name     string
		dir      string
		path     string
		display  string
		def      v3Definition
		req      *v3Request
		file     string
	}
	var children []child
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := os.Lstat(full)
		if err != nil {
			r.warn.add("v3 path", "", 1)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			r.warn.add("v3 path", "", 1)
			continue
		}
		switch {
		case info.IsDir():
			def, err := r.definition(full, false)
			if err != nil {
				return err
			}
			display := cmp.Or(strings.TrimSpace(def.Name), name)
			children = append(children, child{
				order: orderOf(def.Order), hasOrder: def.Order != nil,
				name: name, dir: full, display: display, def: def,
			})
		case info.Mode().IsRegular() && isV3RequestFile(name):
			data, err := r.readLimit(full, v3MaxFile, "file size")
			if err != nil {
				r.fileProblem(err, full)
				continue
			}
			var req v3Request
			if err := unmarshalV3(data, &req); err != nil {
				r.unreadable(full, err)
				continue
			}
			children = append(children, child{
				order: orderOf(req.Order), hasOrder: req.Order != nil,
				name: name, req: &req, file: full,
			})
		case isV3RequestFile(name):
			// A FIFO or device named like a request must not be opened.
			r.warn.add("v3 path", "", 1)
		}
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].hasOrder != children[j].hasOrder {
			return children[i].hasOrder
		}
		if children[i].hasOrder && children[i].order != children[j].order {
			return children[i].order < children[j].order
		}
		return children[i].name < children[j].name
	})
	// Reserve colliding display names in the sorted order, not in the
	// filesystem's directory order.
	for i := range children {
		if children[i].req == nil {
			children[i].path = r.uniquePath(path + "/" + segment(children[i].display))
		}
	}
	seen := map[string]int{}
	for _, c := range children {
		if c.req != nil {
			name := cmp.Or(strings.TrimSpace(c.req.Name), requestStem(c.name))
			ordinal := seen[name]
			seen[name]++
			if err := r.requestFile(c.file, path, *c.req, name, ordinal); err != nil {
				return err
			}
			continue
		}
		r.addFolder(c.path, c.def, "folder auth")
		if err := r.walk(c.dir, c.path, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func orderOf(order *float64) float64 {
	if order == nil {
		return 0
	}
	return *order
}

// requestFile turns one v3 request into the v2.1 request shape, then hands it
// to the shared reader. Kinds the app has no document for are counted.
func (r *v3reader) requestFile(file, parent string, req v3Request, name string, ordinal int) error {
	it := pmItem{ID: req.ID, Name: name, Description: req.Description}
	switch req.Kind {
	case "http-request":
		it.Request = r.httpRequest(req)
		it.Event = r.events(req.Scripts, "request")
		it.Response = r.examples(req, file, requestStem(filepath.Base(file)))
		it.ProtocolProfileBehavior = req.Settings
	case "graphql-request":
		it.Request = r.graphqlRequest(req)
		it.Event = r.events(req.Scripts, "graphql-request")
		it.Response = r.examples(req, file, requestStem(filepath.Base(file)))
		it.Transport = &request.Settings{}
		r.warnSettings(req.Settings, "graphql")
		it.GraphQLSchema, it.GraphQLSchemaSource = r.graphqlSchema(req.Schema, file)
	case "grpc-request":
		it.Protocol = "gRPC"
		it.GRPC = &pmGRPC{Method: req.MethodPath, Body: text(req.Message.Content)}
		it.Request = r.protocolRequest(req, "POST", grpcHeaders(req), nil)
		it.Transport = r.protocolSettings(req.Settings, "grpc")
		if len(req.Scripts) > 0 {
			r.warn.add("protocol script", "gRPC", len(req.Scripts))
		}
		if hasExamples(req.Examples) {
			r.warn.add("stream example", "gRPC", 1)
		}
		if req.MethodDescriptor != "" {
			r.warn.add("grpc schema", "", 1)
		}
		if source := schemaSource(req.Schema); source != "" && source != "none" && source != "auto" {
			r.warn.add("grpc schema", "", 1)
		}
	case "websocket-request":
		it.Request = r.websocketRequest(req, file)
		it.Transport = r.protocolSettings(req.Settings, "websocket")
		if hasExamples(req.Examples) {
			r.warn.add("stream example", "WebSocket", 1)
		}
	case "socket.io-request":
		it.Protocol = "Socket.IO"
		it.Request = r.protocolRequest(req, "GET", req.Headers, nil)
		it.SocketIO = r.socketIOConfig(req, file)
		it.Transport = r.protocolSettings(req.Settings, "socketio")
		if hasExamples(req.Examples) {
			r.warn.add("stream example", "Socket.IO", 1)
		}
		if len(req.Events) > 0 {
			r.warn.add("socketio events", "", len(req.Events))
		}
	case "mqtt-request":
		it.Protocol = "MQTT"
		it.Request = r.protocolRequest(req, "GET", nil, nil)
		it.MQTT = r.mqttConfig(req)
		it.Transport = r.protocolSettings(req.Settings, "mqtt")
		if len(req.Scripts) > 0 {
			r.warn.add("protocol script", "MQTT", len(req.Scripts))
		}
		if hasExamples(req.Examples) {
			r.warn.add("stream example", "MQTT", 1)
		}
	case "kafka-request", "mcp-request", "llm-request", "data-request":
		r.warn.add("v3 protocol", protocolName(req.Kind), 1)
		return nil
	default:
		r.warn.add("v3 request kind", cmp.Or(req.Kind, "no $kind"), 1)
		return nil
	}
	// The path-first request keeps an explicit id. Requests are visited in
	// display order, so a later file with a smaller path can take the id over;
	// the previous owner gets a path-derived id instead. Deriving from the path
	// keeps the result stable when order changes.
	rel := r.relative(file)
	originalID := it.ID
	if originalID != "" {
		if owner, ok := r.idOwner[originalID]; ok {
			r.warn.add("v3 duplicate id", originalID, 1)
			if strings.Compare(rel, owner.file) < 0 {
				r.out.Requests[owner.index].ID = r.derivedID(owner.file)
			} else {
				it.ID = r.derivedID(rel)
			}
		}
	}
	if err := r.request(it, parent, ordinal); err != nil {
		return err
	}
	if originalID != "" && it.ID == originalID {
		r.idOwner[originalID] = v3IDOwner{index: len(r.out.Requests) - 1, file: rel}
	}
	return nil
}

// derivedID is stable for one request file, so re-importing a tree does not
// leave a request behind when display order changes.
func (r *v3reader) derivedID(rel string) string {
	sum := sha256.Sum256([]byte(r.base + "\x00" + rel))
	return "postman-" + hex.EncodeToString(sum[:])
}

func grpcHeaders(req v3Request) []v3KV {
	if len(req.Metadata) > 0 {
		return req.Metadata
	}
	return req.Headers
}

func hasExamples(v jsontext.Value) bool {
	return v.Kind() != 0 && v.Kind() != 'n'
}

func protocolName(kind string) string {
	switch kind {
	case "kafka-request":
		return "Kafka"
	case "mcp-request":
		return "MCP"
	case "llm-request":
		return "LLM"
	case "data-request":
		return "data"
	}
	return kind
}

// httpRequest builds the v2.1 request shape for an HTTP request.
func (r *v3reader) httpRequest(req v3Request) jsontext.Value {
	body, unsupported := v3BodyToPM(req.Body)
	if unsupported != "" {
		r.warn.add("body", unsupported, 1)
	}
	if req.Body != nil {
		r.paramMetadataBody(req.Body)
	}
	return marshalPM(pmRequest{
		Method: req.Method, Header: r.headers(req.Headers), Body: body,
		URL: r.urlValue(req, req.URL), Auth: r.authValue(req.Auth, "request"),
		Description: req.Description,
	})
}

// graphqlRequest is a native v3 GraphQL request. The app sends GraphQL as an
// HTTP POST body, so it uses the same body conversion as v2.1.
func (r *v3reader) graphqlRequest(req v3Request) jsontext.Value {
	body := &pmBody{Mode: "graphql"}
	body.GraphQL.Query = req.Query
	body.GraphQL.Variables = req.Variables
	body.GraphQL.OperationName = req.OperationName
	return marshalPM(pmRequest{
		Method: "POST", Header: r.headers(req.Headers), Body: body,
		URL: r.urlValue(req, req.URL), Auth: r.authValue(req.Auth, "request"),
		Description: req.Description,
	})
}

// protocolRequest builds the common part of a gRPC, WebSocket or Socket.IO
// document: the app stores them as requests with a protocol name.
func (r *v3reader) protocolRequest(req v3Request, method string, headers []v3KV, body *pmBody) jsontext.Value {
	return marshalPM(pmRequest{
		Method: method, Header: r.headers(headers), Body: body,
		URL: r.urlValue(req, req.URL), Auth: r.authValue(req.Auth, "request"),
		Description: req.Description,
	})
}

func (r *v3reader) websocketRequest(req v3Request, file string) jsontext.Value {
	var body *pmBody
	if content, ok := r.streamBody(req, file); ok && content != "" {
		body = &pmBody{Mode: "raw", Raw: content}
	}
	return r.protocolRequest(req, "GET", req.Headers, body)
}

// mqttConfig maps the v3 MQTT request to the app's document. Fields the
// schema has but the app does not send (a received-message subscription
// identifier) are counted rather than invented.
func (r *v3reader) mqttConfig(req v3Request) *request.MQTTConfig {
	cfg := &request.MQTTConfig{Version: request.MQTT5, ClientID: req.ClientID}
	if req.Version == request.MQTT311 || req.Version == request.MQTT5 {
		cfg.Version = int(req.Version)
	} else if req.Version != 0 {
		r.warn.add("mqtt version", "", 1)
	}
	if req.Settings.Kind() == '{' {
		var settings v3MQTTSettings
		if json.Unmarshal(req.Settings, &settings) == nil {
			cfg.CleanStart, cfg.KeepAlive = settings.CleanSession, settings.KeepAlive
			cfg.AutoReconnect, cfg.ConnectTimeoutMs = settings.AutoReconnect, settings.ConnectionTimeout
		}
	}
	for _, t := range req.Topics {
		topic := request.MQTTTopic{Name: t.Name, QoS: r.mqttQoS(t.QoS), Description: t.Description, Subscribe: t.Subscribe,
			UserProperties: mqttProperties(t.UserProperties)}
		if t.Settings.Kind() == '{' {
			var settings v3MQTTSubscriptionSettings
			if json.Unmarshal(t.Settings, &settings) == nil {
				topic.NoLocal, topic.RetainAsPublished = settings.NoLocal, settings.RetainAsPublished
				topic.RetainHandling = byte(settings.RetainHandling)
				topic.SubscriptionIdentifier = settings.SubscriptionIdentifier
			}
		} else if t.Settings.Kind() != 0 && t.Settings.Kind() != 'n' && text(t.Settings) != "0" {
			r.warn.add("mqtt topic settings", "", 1)
		}
		cfg.Topics = append(cfg.Topics, topic)
	}
	if will := req.LastWill; will != nil {
		cfg.Will = request.MQTTWill{Enabled: will.Topic != "" || will.Payload != "" || will.QoS != 0 || will.Retain || will.Properties != nil,
			Topic: will.Topic, Payload: will.Payload, Type: cmp.Or(will.Type, "text"), QoS: r.mqttQoS(will.QoS), Retain: will.Retain}
		if p := will.Properties; p != nil {
			cfg.Will.Properties = request.MQTTWillProperties{PayloadFormat: p.PayloadFormatIndicator,
				MessageExpiry: p.MessageExpiryInterval, WillDelay: p.WillDelayInterval, ResponseTopic: p.ResponseTopic,
				CorrelationData: p.CorrelationData, ContentType: p.ContentType, UserProperties: mqttProperties(p.UserProperties)}
		}
	}
	if p := req.Properties; p != nil {
		cfg.Properties = request.MQTTConnectProperties{SessionExpiry: p.SessionExpiryInterval, ReceiveMaximum: p.ReceiveMaximum,
			MaximumPacketSize: p.MaximumPacketSize, RequestResponseInformation: p.RequestResponseInformation,
			UserProperties: mqttProperties(p.UserProperties)}
	}
	if req.Messages.Kind() == '"' {
		r.warn.add("mqtt messages", "path", 1)
	}
	if req.Messages.Kind() == '[' {
		var messages []v3MQTTMessage
		if json.Unmarshal(req.Messages, &messages) == nil {
			for _, m := range messages {
				message := request.MQTTMessage{ID: m.ID, Name: m.Name, Description: m.Description, Topic: m.Topic,
					Payload: m.Content, Type: cmp.Or(m.ContentType, "text"), QoS: r.mqttQoS(m.QoS), Retain: m.Retain}
				if p := m.Properties; p != nil {
					message.Properties = request.MQTTMessageProperties{PayloadFormat: p.PayloadFormatIndicator,
						MessageExpiry: p.MessageExpiryInterval, TopicAlias: p.TopicAlias, ResponseTopic: p.ResponseTopic,
						CorrelationData: p.CorrelationData, ContentType: p.ContentType, UserProperties: mqttProperties(p.UserProperties)}
					if p.SubscriptionIdentifier != 0 {
						r.warn.add("mqtt message property", "subscriptionIdentifier", 1)
					}
				}
				cfg.Messages = append(cfg.Messages, message)
			}
		} else {
			r.warn.add("mqtt messages", "", 1)
		}
	}
	if cfg.Version == request.MQTT311 && cfg.HasV5Properties() {
		r.warn.add("mqtt 5 properties", "3.1.1", 1)
	}
	// Basic auth is the only MQTT credential shape the app has; other
	// credential keys have no destination.
	var auth v3Auth
	if req.Auth.Kind() == '{' && json.Unmarshal(req.Auth, &auth) == nil && auth.Type == "basic" {
		var credentials []v3KV
		if json.Unmarshal(auth.Credentials, &credentials) == nil {
			for _, credential := range credentials {
				if credential.Key != "username" && credential.Key != "password" {
					r.warn.add("mqtt credential", credential.Key, 1)
				}
			}
		}
	}
	return cfg
}

func (r *v3reader) mqttQoS(value float64) byte {
	if value != math.Trunc(value) || value < 0 || value > 2 {
		r.warn.add("mqtt qos", strconv.FormatFloat(value, 'g', -1, 64), 1)
		return 0
	}
	return byte(value)
}

func mqttProperties(list v3KVs) []request.MQTTProperty {
	var out []request.MQTTProperty
	for _, kv := range list {
		if kv.Disabled {
			continue
		}
		out = append(out, request.MQTTProperty{Key: kv.Key, Value: text(kv.Value)})
	}
	return out
}

func (r *v3reader) socketIOConfig(req v3Request, file string) *pmSocketIO {
	cfg := &pmSocketIO{}
	if msg, ok := r.firstMessage(req, file); ok {
		cfg.Event = msg.EventName
		cfg.Body = socketIOArgs(msg.Args)
		cfg.WantACK = msg.Acknowledgement
		for _, arg := range msg.Args {
			if arg.ContentType == "hex" || arg.ContentType == "base64" {
				r.warn.add("socketio args", arg.ContentType, 1)
			}
		}
	}
	return cfg
}

func marshalPM(req pmRequest) jsontext.Value {
	data, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	return jsontext.Value(data)
}

// urlValue carries the v3 URL string plus its structured query and path
// variables, so the v2.1 URL conversion fills path variables and counts
// disabled query parameters.
func (r *v3reader) urlValue(req v3Request, raw string) jsontext.Value {
	r.paramMetadata(req.QueryParams)
	r.paramMetadata(req.PathVariables)
	return marshalURL(raw, req.QueryParams, req.PathVariables)
}

// marshalURL carries a URL string plus its structured query and path
// variables, so the v2.1 URL conversion fills path variables and counts
// disabled query parameters.
func marshalURL(raw string, query, path []v3KV) jsontext.Value {
	u := pmURL{Raw: raw}
	for _, q := range query {
		u.Query = append(u.Query, pmKeyValue{Key: q.Key, Value: q.Value, Disabled: q.Disabled})
	}
	for _, p := range path {
		u.Variable = append(u.Variable, pmKeyValue{Key: p.Key, Value: p.Value})
	}
	if u.Raw == "" && len(u.Query) == 0 && len(u.Variable) == 0 {
		return nil
	}
	data, err := json.Marshal(u)
	if err != nil {
		return nil
	}
	return jsontext.Value(data)
}

func (r *v3reader) headers(rows []v3KV) jsontext.Value {
	r.paramMetadata(rows)
	if len(rows) == 0 {
		return nil
	}
	type row struct {
		Key      string         `json:"key"`
		Value    jsontext.Value `json:"value"`
		Disabled bool           `json:"disabled,omitempty"`
	}
	out := make([]row, 0, len(rows))
	for _, h := range rows {
		out = append(out, row{Key: h.Key, Value: h.Value, Disabled: h.Disabled})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return jsontext.Value(data)
}

// paramMetadata counts descriptions and schema types the app's field model
// has nowhere to keep.
func (r *v3reader) paramMetadata(rows []v3KV) {
	n := 0
	for _, row := range rows {
		if strings.TrimSpace(text(row.Description)) != "" || row.SchemaType != "" || strings.TrimSpace(row.ContentType) != "" {
			n++
		}
	}
	r.warn.add("parameter metadata", "", n)
}

func (r *v3reader) paramMetadataBody(body *v3Body) {
	if body == nil {
		return
	}
	if strings.TrimSpace(body.SchemaType) != "" {
		r.warn.add("body metadata", "", 1)
	}
	if body.Type != "formdata" && body.Type != "urlencoded" {
		return
	}
	var rows []v3KV
	if json.Unmarshal(body.Content, &rows) == nil {
		r.paramMetadata(rows)
	}
}

// v3BodyToPM maps one v3 body branch to the v2.1 body shape. The second
// result names an unsupported branch.
func v3BodyToPM(body *v3Body) (*pmBody, string) {
	if body == nil {
		return nil, ""
	}
	switch body.Type {
	case "", "none":
		return nil, ""
	case "json", "text", "xml", "html", "javascript":
		out := &pmBody{Mode: "raw", Raw: text(body.Content)}
		out.Options.Raw.Language = body.Type
		return out, ""
	case "urlencoded":
		var rows []v3KV
		if json.Unmarshal(body.Content, &rows) != nil {
			return nil, body.Type
		}
		out := &pmBody{Mode: "urlencoded"}
		for _, row := range rows {
			out.URLEncoded = append(out.URLEncoded, pmFormField{Key: row.Key, Value: row.Value, Disabled: row.Disabled})
		}
		return out, ""
	case "formdata":
		var rows []v3KV
		if json.Unmarshal(body.Content, &rows) != nil {
			return nil, body.Type
		}
		out := &pmBody{Mode: "formdata"}
		for _, row := range rows {
			field := pmFormField{Key: row.Key, Value: row.Value, Type: row.Type, Src: row.Src, Disabled: row.Disabled}
			if field.Type == "" {
				field.Type = "text"
			}
			out.FormData = append(out.FormData, field)
		}
		return out, ""
	case "file":
		var content struct {
			Src jsontext.Value `json:"src"`
		}
		if json.Unmarshal(body.Content, &content) != nil {
			return nil, body.Type
		}
		out := &pmBody{Mode: "file"}
		out.File.Src = content.Src
		return out, ""
	case "graphql":
		var content struct {
			Query         string         `json:"query"`
			Variables     jsontext.Value `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if json.Unmarshal(body.Content, &content) != nil {
			return nil, body.Type
		}
		out := &pmBody{Mode: "graphql"}
		out.GraphQL.Query = content.Query
		out.GraphQL.Variables = content.Variables
		out.GraphQL.OperationName = content.OperationName
		return out, ""
	default:
		return nil, body.Type
	}
}

// authValue maps v3 auth to the v2.1 auth object. An array is Postman's
// multi-auth; the app has one auth per item, so the first is imported and the
// rest are counted.
func (r *v3reader) authValue(v jsontext.Value, scope string) jsontext.Value {
	if v.Kind() != '{' && v.Kind() != '[' {
		return nil
	}
	var one v3Auth
	if v.Kind() == '[' {
		var list []v3Auth
		if json.Unmarshal(v, &list) != nil {
			r.warn.add("v3 auth", "", 1)
			return nil
		}
		if len(list) == 0 {
			return nil
		}
		if len(list) > 1 {
			r.warn.add("multi auth", scope, 1)
		}
		one = list[0]
	} else if json.Unmarshal(v, &one) != nil {
		r.warn.add("v3 auth", "", 1)
		return nil
	}
	if strings.TrimSpace(text(one.Description)) != "" {
		r.warn.add("auth description", "", 1)
	}
	kind := strings.TrimSpace(one.Type)
	if kind == "" || kind == "inherit" {
		return nil
	}
	if kind == "noauth" {
		return jsontext.Value(`{"type":"noauth"}`)
	}
	var params []map[string]string
	switch one.Credentials.Kind() {
	case '[':
		var list []v3KV
		if json.Unmarshal(one.Credentials, &list) == nil {
			for _, kv := range list {
				params = append(params, map[string]string{"key": kv.Key, "value": text(kv.Value)})
			}
		}
	case '{':
		var values map[string]jsontext.Value
		if json.Unmarshal(one.Credentials, &values) == nil {
			for _, key := range slices.Sorted(maps.Keys(values)) {
				params = append(params, map[string]string{"key": key, "value": text(values[key])})
			}
		}
	}
	data, err := json.Marshal(map[string]any{"type": kind, kind: params})
	if err != nil {
		return nil
	}
	return jsontext.Value(data)
}

// events maps v3 scripts to the v2.1 event shape. HTTP request scripts use
// beforeRequest/afterResponse, GraphQL request scripts use
// beforeQuery/afterResponse, and collection/folder scripts prefix the
// protocol. A protocol-prefixed GraphQL script is not merged into the shared
// HTTP scripts: that would run it before every HTTP request.
func (r *v3reader) events(scripts []v3Script, scope string) []pmEvent {
	var out []pmEvent
	for _, script := range scripts {
		code := strings.TrimSpace(script.Code)
		if code == "" {
			continue
		}
		listen := ""
		switch scope {
		case "request":
			switch script.Type {
			case "beforeRequest":
				listen = "prerequest"
			case "afterResponse":
				listen = "test"
			default:
				r.warn.add("script event", script.Type, 1)
				continue
			}
		case "graphql-request":
			switch script.Type {
			case "beforeQuery", "beforeRequest":
				listen = "prerequest"
			case "afterResponse":
				listen = "test"
			default:
				r.warn.add("script event", script.Type, 1)
				continue
			}
		case "collection", "folder":
			switch script.Type {
			case "http:beforeRequest":
				listen = "prerequest"
			case "http:afterResponse":
				listen = "test"
			case "graphql:beforeQuery", "graphql:afterResponse",
				"grpc:beforeInvoke", "grpc:onIncomingMessage", "grpc:afterResponse",
				"kafka:beforeConsume", "kafka:onIncomingMessage", "kafka:afterConsume",
				"kafka:beforeProduce", "kafka:afterProduce":
				r.warn.add("protocol script", script.Type, 1)
				continue
			default:
				r.warn.add("script event", script.Type, 1)
				continue
			}
		}
		if script.Language != "" && script.Language != "text/javascript" {
			r.warn.add("script language", script.Language, 1)
			continue
		}
		if script.Packages.Kind() == '{' {
			r.warn.add("script package", "", 1)
		}
		if script.Requests.Kind() == '{' {
			r.warn.add("script package", "", 1)
		}
		exec, err := json.Marshal(code)
		if err != nil {
			continue
		}
		event := pmEvent{Listen: listen}
		event.Script.Exec = jsontext.Value(exec)
		out = append(out, event)
	}
	return out
}

// variables maps v3 variables. A plain value is Initial, as in v2.1. A secret
// value has no shared equivalent: it becomes a local Current value, and a
// vault reference that cannot be resolved is skipped.
func (r *v3reader) variables(vars []v3Variable) []pmVariable {
	var out []pmVariable
	for _, variable := range vars {
		name := strings.TrimSpace(variable.Key)
		if name == "" {
			continue
		}
		if strings.TrimSpace(text(variable.Description)) != "" {
			r.warn.add("variable description", "", 1)
		}
		if variable.Secret {
			if text(variable.Value) == "" {
				r.warn.add("secret variable", "vault", 1)
				continue
			}
			r.warn.add("secret variable", "local", 1)
			out = append(out, pmVariable{Key: name, Value: variable.Value, Disabled: variable.Disabled, Secret: true})
			continue
		}
		out = append(out, pmVariable{Key: name, Value: variable.Value, Disabled: variable.Disabled})
	}
	return out
}

// warnSettings counts settings the app cannot hold. strictSSL is mapped for
// protocols; GraphQL's disabledSystemHeaders has no equivalent.
func (r *v3reader) warnSettings(v jsontext.Value, protocol string) {
	if v.Kind() == 0 || v.Kind() == 'n' {
		return
	}
	if v.Kind() != '{' {
		r.warn.add("settings", "", 1)
		return
	}
	var values map[string]jsontext.Value
	if json.Unmarshal(v, &values) != nil {
		r.warn.add("settings", "", 1)
		return
	}
	for key := range values {
		allowed := key == "strictSSL" && (protocol == "grpc" || protocol == "websocket" || protocol == "socketio" || protocol == "mqtt")
		if protocol == "socketio" && (key == "version" || key == "path") {
			allowed = true
		}
		if protocol == "mqtt" && (key == "cleanSession" || key == "keepAlive" || key == "autoReconnect" || key == "connectionTimeout") {
			allowed = true
		}
		if !allowed {
			r.warn.add("settings", "", 1)
		}
	}
}

// protocolSettings maps strictSSL and counts the rest.
func (r *v3reader) protocolSettings(v jsontext.Value, protocol string) *request.Settings {
	out := &request.Settings{}
	r.warnSettings(v, protocol)
	if v.Kind() != '{' {
		return out
	}
	var settings struct {
		StrictSSL *bool  `json:"strictSSL"`
		Version   string `json:"version"`
		Path      string `json:"path"`
	}
	if json.Unmarshal(v, &settings) == nil {
		out.VerifyTLS = settings.StrictSSL
		if protocol == "socketio" {
			if settings.Version != "" && settings.Version != "4" {
				r.warn.add("socketio settings", "version", 1)
			}
			if settings.Path != "" && settings.Path != "/socket.io/" {
				r.warn.add("socketio settings", "path", 1)
			}
		}
	}
	return out
}

func schemaSource(v jsontext.Value) string {
	var schema v3Schema
	if v.Kind() != '{' || json.Unmarshal(v, &schema) != nil {
		return ""
	}
	return schema.Source
}

// graphqlSchema imports a local SDL file. Remote and API-backed schemas are
// counted; the app can load or introspect them itself.
func (r *v3reader) graphqlSchema(v jsontext.Value, requestFile string) (string, string) {
	var schema v3Schema
	if v.Kind() != '{' || json.Unmarshal(v, &schema) != nil {
		return "", ""
	}
	switch schema.Source {
	case "", "none", "auto":
		return "", ""
	case "file":
		path, ok := r.resolve(requestFile, schema.Location)
		if !ok {
			r.warn.add("graphql schema", "", 1)
			return "", ""
		}
		data, err := r.readLimit(path, v3MaxSchema, "GraphQL schema size")
		if err != nil {
			var limit v3LimitError
			if errors.As(err, &limit) {
				r.fileProblem(err, path)
			} else {
				r.warn.add("graphql schema", "", 1)
			}
			return "", ""
		}
		return string(data), schema.Location
	default:
		r.warn.add("graphql schema", schema.Source, 1)
		return "", ""
	}
}

// examples converts saved examples through the v2.1 response shape. HTTP and
// GraphQL examples are HTTP exchanges; stream and gRPC examples are counted.
func (r *v3reader) examples(req v3Request, requestFile, stem string) []jsontext.Value {
	var list []v3Example
	if req.Examples.Kind() == '[' {
		var raw []jsontext.Value
		if json.Unmarshal(req.Examples, &raw) == nil {
			for _, value := range raw {
				var example v3Example
				if json.Unmarshal(value, &example) == nil {
					list = append(list, example)
				} else {
					r.warn.add("example", "", 1)
				}
			}
		}
	} else {
		ref := ""
		if req.Examples.Kind() == '"' {
			ref = text(req.Examples)
		}
		list = r.exampleFiles(requestFile, ref, stem)
	}
	var out []jsontext.Value
	for _, example := range list {
		response, ok := r.pmExample(example)
		if !ok {
			continue
		}
		data, err := json.Marshal(response)
		if err != nil {
			r.warn.add("example", "", 1)
			continue
		}
		out = append(out, jsontext.Value(data))
	}
	return out
}

func (r *v3reader) pmExample(example v3Example) (pmResponse, bool) {
	r.exampleMetadata(example)
	switch example.Kind {
	case "", "http-example":
		return r.httpExample(example)
	case "graphql-example":
		return r.graphqlExample(example)
	default:
		r.warn.add("stream example", example.Kind, 1)
		return pmResponse{}, false
	}
}

// exampleMetadata counts fields a saved example has that the app's HTTP
// example shape cannot hold.
func (r *v3reader) exampleMetadata(example v3Example) {
	n := 0
	if strings.TrimSpace(text(example.Description)) != "" {
		n++
	}
	if example.Auth.Kind() == '{' || example.Auth.Kind() == '[' {
		n++
	}
	if example.Settings.Kind() == '{' {
		n++
	}
	if example.Schema.Kind() == '{' {
		n++
	}
	r.warn.add("example metadata", "", n)
}

// exampleRequestMetadata counts descriptions and types on a saved example's
// request fields, and the GraphQL operation type the app does not store.
func (r *v3reader) exampleRequestMetadata(req *v3ExampleRequest) {
	if req == nil {
		return
	}
	r.paramMetadata(req.QueryParams)
	r.paramMetadata(req.PathVariables)
	var rows []v3KV
	if json.Unmarshal(req.Headers, &rows) == nil {
		r.paramMetadata(rows)
	}
	if req.OperationType != "" {
		r.warn.add("example metadata", "", 1)
	}
}

// headerMetadata counts descriptions and types on a header array. Object-form
// headers carry no metadata.
func (r *v3reader) headerMetadata(v jsontext.Value) {
	var rows []v3KV
	if json.Unmarshal(v, &rows) == nil {
		r.paramMetadata(rows)
	}
}

func (r *v3reader) httpExample(example v3Example) (pmResponse, bool) {
	if example.Response == nil {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	body := ""
	if example.Response.Body != nil {
		body = text(example.Response.Body.Content)
	}
	r.paramMetadataBody(example.Response.Body)
	r.headerMetadata(example.Response.Headers)
	if len(body) > request.MaxExampleBody {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	original := pmRequest{Method: "GET"}
	if example.Request != nil {
		r.exampleRequestMetadata(example.Request)
		original.Method = example.Request.Method
		original.Header = exampleHeaders(example.Request.Headers)
		original.URL = marshalURL(example.Request.URL, example.Request.QueryParams, example.Request.PathVariables)
		original.Body, _ = v3BodyToPM(example.Request.Body)
	}
	data, err := json.Marshal(original)
	if err != nil {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	return pmResponse{
		ID: example.ID, Name: example.Name, Code: example.Response.StatusCode,
		Header: exampleHeaders(example.Response.Headers), Body: jsonString(body),
		OriginalRequest: jsontext.Value(data),
	}, true
}

func (r *v3reader) graphqlExample(example v3Example) (pmResponse, bool) {
	if example.Response == nil {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	status := 200
	if line := strings.TrimSpace(example.Response.HTTP.Status); line != "" {
		if fields := strings.Fields(line); len(fields) > 0 {
			if code, err := strconv.Atoi(fields[0]); err == nil {
				status = code
			}
		}
	}
	body := example.Response.HTTP.Body
	if body == "" && len(example.Response.Events) > 0 {
		body = example.Response.Events[0].Content
		if len(example.Response.Events) > 1 {
			r.warn.add("graphql example events", "", 1)
		}
	} else if body != "" && len(example.Response.Events) > 0 {
		r.warn.add("graphql example events", "", 1)
	}
	if len(body) > request.MaxExampleBody {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	r.headerMetadata(example.Response.HTTP.Headers)
	original := pmRequest{Method: "POST"}
	if example.Request != nil {
		r.exampleRequestMetadata(example.Request)
		original.URL = marshalURL(example.Request.URL, example.Request.QueryParams, example.Request.PathVariables)
		original.Header = exampleHeaders(example.Request.Headers)
		if body := graphqlExampleBody(example.Request); body != "" {
			original.Body = &pmBody{Mode: "raw", Raw: body}
		}
	}
	data, err := json.Marshal(original)
	if err != nil {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	return pmResponse{
		ID: example.ID, Name: example.Name, Code: status,
		Header: exampleHeaders(example.Response.HTTP.Headers), Body: jsonString(body),
		OriginalRequest: jsontext.Value(data),
	}, true
}

// graphqlExampleBody keeps the example's original GraphQL request as the JSON
// envelope an HTTP send would use. The app's example request has one body
// field, so this is the only place the query and variables can survive.
func graphqlExampleBody(req *v3ExampleRequest) string {
	if req == nil || strings.TrimSpace(req.Query) == "" {
		return ""
	}
	envelope := map[string]any{"query": req.Query}
	var variables any
	switch req.Variables.Kind() {
	case '"':
		text := text(req.Variables)
		if json.Unmarshal([]byte(text), &variables) != nil {
			variables = text
		}
	case 0, 'n':
	default:
		_ = json.Unmarshal(req.Variables, &variables)
	}
	if variables != nil {
		envelope["variables"] = variables
	}
	if req.OperationName != "" {
		envelope["operationName"] = req.OperationName
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return ""
	}
	return string(data)
}

// exampleHeaders accepts the array the schema documents and the object the
// Postman CLI has written in migrated collections.
func exampleHeaders(v jsontext.Value) jsontext.Value {
	switch v.Kind() {
	case '[':
		return v
	case '{':
		var values map[string]jsontext.Value
		if json.Unmarshal(v, &values) != nil {
			return nil
		}
		type row struct {
			Key   string         `json:"key"`
			Value jsontext.Value `json:"value"`
		}
		var out []row
		for _, key := range slices.Sorted(maps.Keys(values)) {
			out = append(out, row{Key: key, Value: values[key]})
		}
		data, err := json.Marshal(out)
		if err != nil {
			return nil
		}
		return jsontext.Value(data)
	}
	return nil
}

func jsonString(s string) jsontext.Value {
	data, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return jsontext.Value(data)
}

// exampleFiles reads the example directory a request points at, or the
// conventional sibling directory when it has no examples field.
func (r *v3reader) exampleFiles(requestFile, ref, stem string) []v3Example {
	dir := r.resourceDir(requestFile, ref, stem, "examples")
	if dir == "" {
		return nil
	}
	var list []v3Example
	entries, err := r.readDir(dir)
	if err != nil {
		r.fileProblem(err, dir)
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			r.warn.add("v3 path", "", 1)
			continue
		}
		if entry.IsDir() || !isExampleFile(name) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := r.read(path)
		if err != nil {
			r.fileProblem(err, path)
			continue
		}
		var example v3Example
		if err := unmarshalV3(data, &example); err != nil {
			if !r.yamlLimit(path, err) {
				r.warn.add("example", "", 1)
			}
			continue
		}
		list = append(list, example)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if (list[i].Order == nil) != (list[j].Order == nil) {
			return list[i].Order != nil
		}
		if list[i].Order != nil && *list[i].Order != *list[j].Order {
			return *list[i].Order < *list[j].Order
		}
		return list[i].Name < list[j].Name
	})
	return list
}

func isExampleFile(name string) bool {
	return strings.HasSuffix(name, ".example.yaml") || strings.HasSuffix(name, ".example.yml")
}

// resourceDir resolves the explicit resource path or the conventional
// .resources/<request>.resources/<kind> sibling. It only returns a real
// directory: a FIFO or a symlink must not be opened as a resource directory.
func (r *v3reader) resourceDir(requestFile, ref, stem, kind string) string {
	explicit := ref != ""
	pathRef := ref
	if !explicit {
		pathRef = filepath.Join(".resources", stem+".resources", kind)
	}
	path, ok := r.resolve(requestFile, pathRef)
	if !ok {
		if explicit {
			r.warn.add("v3 path", "", 1)
		} else if _, err := os.Lstat(filepath.Clean(filepath.Join(filepath.Dir(requestFile), filepath.FromSlash(pathRef)))); err == nil {
			// The conventional path is optional, but one that exists and is
			// still unusable (a symlink, FIFO or escape) must not vanish.
			r.warn.add("v3 path", "", 1)
		}
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		r.warn.add("v3 path", "", 1)
		return ""
	}
	return path
}

// firstMessage reads the first saved WebSocket or Socket.IO message.
func (r *v3reader) firstMessage(req v3Request, requestFile string) (v3Message, bool) {
	var list []v3Message
	if req.Messages.Kind() == '[' {
		var raw []jsontext.Value
		if json.Unmarshal(req.Messages, &raw) == nil {
			for _, value := range raw {
				var message v3Message
				if json.Unmarshal(value, &message) == nil {
					list = append(list, message)
				}
			}
		}
	} else {
		ref := ""
		if req.Messages.Kind() == '"' {
			ref = text(req.Messages)
		}
		dir := r.resourceDir(requestFile, ref, requestStem(filepath.Base(requestFile)), "messages")
		if dir != "" {
			entries, err := r.readDir(dir)
			if err != nil {
				r.fileProblem(err, dir)
			} else {
				for _, entry := range entries {
					name := entry.Name()
					if entry.Type()&os.ModeSymlink != 0 {
						r.warn.add("v3 path", "", 1)
						continue
					}
					if entry.IsDir() || !isMessageFile(name) {
						continue
					}
					path := filepath.Join(dir, name)
					data, err := r.read(path)
					if err != nil {
						r.fileProblem(err, path)
						continue
					}
					var message v3Message
					if err := unmarshalV3(data, &message); err != nil {
						r.unreadable(path, err)
						continue
					}
					list = append(list, message)
				}
			}
		}
	}
	if len(list) == 0 {
		return v3Message{}, false
	}
	if len(list) > 1 {
		r.warn.add("stream messages", "", len(list)-1)
	}
	if strings.TrimSpace(list[0].Description) != "" {
		r.warn.add("stream message description", "", 1)
	}
	return list[0], true
}

func isMessageFile(name string) bool {
	return strings.HasSuffix(name, ".message.yaml") || strings.HasSuffix(name, ".message.yml")
}

func (r *v3reader) streamBody(req v3Request, requestFile string) (string, bool) {
	message, ok := r.firstMessage(req, requestFile)
	if !ok {
		return "", false
	}
	switch message.ContentType {
	case "", "text", "json", "xml", "html":
		return message.Content, true
	default:
		r.warn.add("stream messages", "binary", 1)
		return "", false
	}
}

func socketIOArgs(args []v3MessageArg) string {
	if len(args) == 0 {
		return ""
	}
	values := make([]any, 0, len(args))
	for _, arg := range args {
		if arg.ContentType == "json" {
			var value any
			if json.Unmarshal([]byte(arg.Content), &value) == nil {
				values = append(values, value)
				continue
			}
		}
		values = append(values, arg.Content)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

// resolve turns a v3 path relative to requestFile into a path inside the
// collection root, refusing absolute paths, traversal and symbolic links.
func (r *v3reader) resolve(requestFile, ref string) (string, bool) {
	if strings.TrimSpace(ref) == "" || filepath.IsAbs(ref) {
		return "", false
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(requestFile), filepath.FromSlash(ref)))
	rel, err := filepath.Rel(r.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	current := r.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
	}
	return path, true
}

// unmarshalV3 reads YAML with the JSON tags the rest of this package uses.
// Postman never writes anchors, so an alias is refused rather than expanded.
func unmarshalV3(data []byte, out any) error {
	jsonData, err := yamlguard.ToJSON(data, yamlguard.Options{RejectAliases: true})
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonData, out)
}
