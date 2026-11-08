package postman

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/oasdiff/yaml"

	"github.com/baba983/vscroxy/internal/request"
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
	// v3MaxFiles bounds how many files one import reads.
	v3MaxFiles = 10_000
	// v3MaxFile bounds one YAML file. Postman examples are capped at 5 MiB;
	// a definition or request is far smaller, but imports stay generous.
	v3MaxFile = 8 << 20
	// v3MaxSchema bounds an imported GraphQL SDL file.
	v3MaxSchema = 1 << 20
	// v3MaxDepth bounds folder nesting.
	v3MaxDepth = 32
)

var (
	errV3NotRegular = errors.New("not a regular file")
	errV3Symlink    = errors.New("symbolic link")
	errV3TooLarge   = errors.New("file is larger than 8 MiB")
	errV3TooMany    = errors.New("too many files")
)

// LooksLikeCollectionDir reports whether dir is the root of a Postman
// Collection v3 directory. It checks the directory itself and its immediate
// children, so a collection whose requests all live in folders is recognized
// without walking an unrelated tree.
func LooksLikeCollectionDir(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if hasV3Definition(dir) || hasV3RequestFiles(dir) {
		return true
	}
	entries := readDirBounded(dir, 1000)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(dir, entry.Name())
		if hasV3Definition(child) || hasV3RequestFiles(child) {
			return true
		}
	}
	return false
}

func hasV3Definition(dir string) bool {
	for _, name := range []string{"definition.yaml", "definition.yml"} {
		info, err := os.Lstat(filepath.Join(dir, ".resources", name))
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return true
		}
	}
	return false
}

func hasV3RequestFiles(dir string) bool {
	for _, entry := range readDirBounded(dir, 1000) {
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
	r := &v3reader{root: root, paths: map[string]bool{}, reader: reader{warn: tally{}}}
	def, err := r.definition(root)
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
	r.out.Warnings = r.warn.list()
	return r.out, nil
}

// v3reader carries one directory import's results and counts. The embedded
// reader is the v2.1 conversion every request and folder goes through.
type v3reader struct {
	reader
	root  string
	files int
	paths map[string]bool
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

// read reads one regular file, bounded. Missing files are reported as
// os.ErrNotExist so a caller can treat a definition as optional.
func (r *v3reader) read(path string) ([]byte, error) {
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
	r.files++
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, v3MaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > v3MaxFile {
		return nil, errV3TooLarge
	}
	return data, nil
}

// readDir lists a directory with the same file budget as read. A directory
// with more entries than the budget is truncated and reported, so listing it
// cannot allocate without bound.
func (r *v3reader) readDir(dir string) ([]os.DirEntry, error) {
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
		r.warn.add("v3 limit", "files", 1)
		entries = entries[:v3MaxFiles]
	}
	return entries, nil
}

// fileProblem counts why one file could not be read.
func (r *v3reader) fileProblem(err error) {
	switch {
	case errors.Is(err, errV3TooLarge):
		r.warn.add("v3 limit", "file size", 1)
	case errors.Is(err, errV3TooMany):
		r.warn.add("v3 limit", "files", 1)
	default:
		r.warn.add("v3 path", "", 1)
	}
}

// definition reads a collection or folder definition. A malformed definition
// is reported and skipped, so one bad metadata file does not lose requests;
// a file that names another kind is a hard error.
func (r *v3reader) definition(dir string) (v3Definition, error) {
	for _, name := range []string{"definition.yaml", "definition.yml"} {
		path := filepath.Join(dir, ".resources", name)
		data, err := r.read(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			r.fileProblem(err)
			return v3Definition{}, nil
		}
		var def v3Definition
		if err := unmarshalV3(data, &def); err != nil {
			r.warn.add("v3 unreadable", "", 1)
			return v3Definition{}, nil
		}
		if def.Kind != "" && def.Kind != "collection" {
			return v3Definition{}, fmt.Errorf("%s: $kind is %q, want collection", path, def.Kind)
		}
		return def, nil
	}
	return v3Definition{}, nil
}

// addFolder records a level's settings when it has any, through the v2.1
// folder conversion.
func (r *v3reader) addFolder(path string, def v3Definition, authWarning string) {
	r.folder(path, def.Description, r.authValue(def.Auth, strings.TrimSuffix(authWarning, " auth")),
		r.events(def.Scripts, "collection"), r.variables(def.Variables), request.Settings{}, authWarning)
}

// walk reads one directory's folders and request files in Postman's order.
func (r *v3reader) walk(dir, path string, depth int) error {
	if depth > v3MaxDepth {
		r.warn.add("v3 limit", "depth", 1)
		return nil
	}
	entries, err := r.readDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	type child struct {
		order    float64
		hasOrder bool
		name     string
		dir      string
		path     string
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
			def, err := r.definition(full)
			if err != nil {
				return err
			}
			display := cmp.Or(strings.TrimSpace(def.Name), name)
			children = append(children, child{
				order: orderOf(def.Order), hasOrder: def.Order != nil,
				name: name, dir: full, path: r.uniquePath(path + "/" + segment(display)), def: def,
			})
		case info.Mode().IsRegular() && isV3RequestFile(name):
			data, err := r.read(full)
			if err != nil {
				r.fileProblem(err)
				continue
			}
			var req v3Request
			if err := unmarshalV3(data, &req); err != nil {
				r.warn.add("v3 unreadable", "", 1)
				continue
			}
			children = append(children, child{
				order: orderOf(req.Order), hasOrder: req.Order != nil,
				name: name, req: &req, file: full,
			})
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
		it.Event = r.events(req.Scripts, "request")
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
	case "mqtt-request", "kafka-request", "mcp-request", "llm-request", "data-request":
		r.warn.add("v3 protocol", protocolName(req.Kind), 1)
		return nil
	default:
		r.warn.add("v3 request kind", cmp.Or(req.Kind, "no $kind"), 1)
		return nil
	}
	return r.request(it, parent, ordinal)
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
	case "mqtt-request":
		return "MQTT"
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
	u := pmURL{Raw: raw}
	for _, q := range req.QueryParams {
		u.Query = append(u.Query, pmKeyValue{Key: q.Key, Value: q.Value, Disabled: q.Disabled})
	}
	for _, p := range req.PathVariables {
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
	if body == nil || body.Type != "formdata" {
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
			r.warn.add("v3 unreadable", "", 1)
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
		r.warn.add("v3 unreadable", "", 1)
		return nil
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

// events maps v3 scripts to the v2.1 event shape. Request scripts use
// beforeRequest/afterResponse; collection and folder scripts prefix the
// protocol. Scripts for a protocol the app does not run are counted.
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
		case "collection", "folder":
			switch script.Type {
			case "http:beforeRequest", "graphql:beforeQuery":
				listen = "prerequest"
			case "http:afterResponse", "graphql:afterResponse":
				listen = "test"
			case "grpc:beforeInvoke", "grpc:onIncomingMessage", "grpc:afterResponse",
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
		allowed := key == "strictSSL" && (protocol == "grpc" || protocol == "websocket" || protocol == "socketio")
		if protocol == "socketio" && (key == "version" || key == "path") {
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
		data, err := r.read(path)
		if err != nil || len(data) > v3MaxSchema {
			r.warn.add("graphql schema", "", 1)
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

func (r *v3reader) httpExample(example v3Example) (pmResponse, bool) {
	if example.Response == nil {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	body := text(example.Response.Body.Content)
	if len(body) > request.MaxExampleBody {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	original := pmRequest{Method: "GET"}
	if example.Request != nil {
		original.Method = example.Request.Method
		original.Header = exampleHeaders(example.Request.Headers)
		original.URL = r.exampleURL(example.Request)
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
	}
	if len(body) > request.MaxExampleBody {
		r.warn.add("example", "", 1)
		return pmResponse{}, false
	}
	original := pmRequest{Method: "POST"}
	if example.Request != nil {
		original.URL = r.exampleURL(example.Request)
		original.Header = exampleHeaders(example.Request.Headers)
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

func (r *v3reader) exampleURL(req *v3ExampleRequest) jsontext.Value {
	if req == nil {
		return nil
	}
	u := pmURL{Raw: req.URL}
	for _, q := range req.QueryParams {
		u.Query = append(u.Query, pmKeyValue{Key: q.Key, Value: q.Value, Disabled: q.Disabled})
	}
	for _, p := range req.PathVariables {
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
	var list []v3Example
	for _, dir := range r.resourceDirs(requestFile, ref, stem, "examples") {
		entries, err := r.readDir(dir)
		if err != nil {
			r.warn.add("v3 path", "", 1)
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isExampleFile(name) {
				continue
			}
			path := filepath.Join(dir, name)
			data, err := r.read(path)
			if err != nil {
				r.fileProblem(err)
				continue
			}
			var example v3Example
			if err := unmarshalV3(data, &example); err != nil {
				r.warn.add("example", "", 1)
				continue
			}
			list = append(list, example)
		}
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

// resourceDirs resolves the explicit resource path or the conventional
// .resources/<request>.resources/<kind> sibling.
func (r *v3reader) resourceDirs(requestFile, ref, stem, kind string) []string {
	if ref != "" {
		path, ok := r.resolve(requestFile, ref)
		if !ok {
			r.warn.add("v3 path", "", 1)
			return nil
		}
		return []string{path}
	}
	path, ok := r.resolve(requestFile, filepath.Join(".resources", stem+".resources", kind))
	if !ok {
		return nil
	}
	return []string{path}
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
		for _, dir := range r.resourceDirs(requestFile, ref, requestStem(filepath.Base(requestFile)), "messages") {
			entries, err := r.readDir(dir)
			if err != nil {
				r.warn.add("v3 path", "", 1)
				continue
			}
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isMessageFile(name) {
					continue
				}
				data, err := r.read(filepath.Join(dir, name))
				if err != nil {
					r.fileProblem(err)
					continue
				}
				var message v3Message
				if err := unmarshalV3(data, &message); err != nil {
					r.warn.add("v3 unreadable", "", 1)
					continue
				}
				list = append(list, message)
			}
		}
	}
	if len(list) == 0 {
		return v3Message{}, false
	}
	if len(list) > 1 {
		r.warn.add("stream messages", "", len(list)-1)
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
func unmarshalV3(data []byte, out any) error {
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonData, out)
}
