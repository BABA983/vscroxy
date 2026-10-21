package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// apiSpec is an independent contract, not another encoding of a collection.
// Source stays verbatim, including YAML, comments and vendor extensions.
type apiSpec struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Collection  string   `json:"collection,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

func loadAPIs(path string) ([]apiSpec, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var specs []apiSpec
	err = json.Unmarshal(data, &specs)
	return specs, err
}

func saveAPIs(path string, specs []apiSpec) error {
	data, err := json.Marshal(specs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".apis-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(data, '\n'))
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), path)
}

// Parsing a standalone contract must never follow a ref onto the network or
// into another local file. Validation also rejects Swagger 2 and unknown versions.
func parseOpenAPI(source string) (*openapi3.T, error) {
	if len(source) > 16<<20 {
		return nil, errors.New("OpenAPI documents are limited to 16 MiB")
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData([]byte(source))
	if err != nil {
		return nil, err
	}
	if version := doc.OpenAPIMajorMinor(); version != "3.0" && version != "3.1" {
		return nil, fmt.Errorf("supported versions are OpenAPI 3.0.x and 3.1.x, not %q", doc.OpenAPI)
	}
	if err := doc.Validate(context.Background(), openapi3.EnableMultiError()); err != nil {
		return nil, err
	}
	return doc, nil
}

func collectionRequests(requests []savedRequest, collection string) []savedRequest {
	var out []savedRequest
	for _, r := range requests {
		if r.Collection == collection {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b savedRequest) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func collectionFingerprint(requests []savedRequest) string {
	data, _ := json.Marshal(requests)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

var requestVariable = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)
var pathVariable = regexp.MustCompile(`\{([^{}]+)\}`)

// generateOpenAPI omits query/header/body values and never guesses response
// codes or required fields. Literal paths still need review before publishing.
func generateOpenAPI(requests []savedRequest, collection string) (apiSpec, error) {
	list := collectionRequests(requests, collection)
	if len(list) == 0 {
		return apiSpec{}, errors.New("save at least one request in this collection first")
	}
	doc := &openapi3.T{
		OpenAPI: "3.0.3", Info: &openapi3.Info{Title: cmp.Or(collection, "Saved requests"), Version: "1.0.0"},
		Paths: openapi3.NewPaths(),
	}
	notes := []string{
		"Draft, not a verified contract. Review path templates, parameter types, required fields, authentication and response schemas.",
		"Query/header/body values, URL credentials and scripts are not copied. Review literal paths for sensitive data. Parameters start as optional strings; path parameters are required by OpenAPI.",
		"Paths stay literal unless they contain {{variables}}. Numeric IDs are not automatically generalized. Duplicate method/path requests are merged; all observed servers are retained.",
		"Responses use an undocumented default, not a fabricated 200. Define the actual status codes and schemas in OpenAPI.",
	}
	for i, r := range list {
		label := fmt.Sprintf("Request %d", i+1)
		method := strings.ToUpper(strings.TrimSpace(r.Method))
		if !slices.Contains([]string{"GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE"}, method) {
			notes = append(notes, label+": skipped unsupported HTTP method")
			continue
		}
		raw := strings.TrimSpace(r.URL)
		if match := requestVariable.FindStringIndex(raw); match != nil && match[0] == 0 {
			raw = cmp.Or(raw[match[1]:], "/")
			notes = append(notes, label+": environment base URL omitted; add a server or set {{base_url}} before sending generated requests.")
		}
		u, err := url.Parse(raw)
		if err != nil || u.Opaque != "" || u.Scheme != "" && (u.Scheme != "http" && u.Scheme != "https" || u.Host == "") || u.Scheme == "" && (u.Host != "" || !strings.HasPrefix(raw, "/")) {
			notes = append(notes, label+": skipped URL that is not HTTP(S) or a relative path")
			continue
		}
		path := requestVariable.ReplaceAllString(cmp.Or(u.Path, "/"), "{$1}")
		item := doc.Paths.Value(path)
		if item == nil {
			item = &openapi3.PathItem{}
			doc.Paths.Set(path, item)
		}
		op := item.GetOperation(method)
		if op == nil {
			op = &openapi3.Operation{Summary: method + " " + path, Responses: openapi3.NewResponses()}
			op.Responses.Set("default", &openapi3.ResponseRef{Value: &openapi3.Response{Description: new("Undocumented response. Define status codes and schemas.")}})
			item.SetOperation(method, op)
		} else {
			notes = append(notes, label+": merged into "+method+" "+path)
		}
		if u.Host != "" && u.Scheme != "" {
			server := u.Scheme + "://" + u.Host
			if op.Servers == nil {
				op.Servers = new(openapi3.Servers{})
			}
			if !slices.ContainsFunc(*op.Servers, func(s *openapi3.Server) bool { return s.URL == server }) {
				*op.Servers = append(*op.Servers, &openapi3.Server{URL: server})
			}
		}
		addParameter := func(in, name string) {
			if op.Parameters.GetByInAndName(in, name) == nil {
				op.AddParameter(&openapi3.Parameter{Name: name, In: in, Required: in == "path", Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}})
			}
		}
		for _, match := range pathVariable.FindAllStringSubmatch(path, -1) {
			addParameter("path", match[1])
		}
		for _, name := range slices.Sorted(maps.Keys(u.Query())) {
			addParameter("query", name)
		}
		headers := parseHeaderBlock(r.Headers)
		for _, name := range slices.Sorted(maps.Keys(headers)) {
			switch strings.ToLower(name) {
			case "authorization", "proxy-authorization", "cookie", "set-cookie", "host", "content-type", "accept", "content-length", "connection", "accept-encoding", "user-agent":
				continue
			}
			addParameter("header", name)
		}
		if r.Body != "" {
			media, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
			if err != nil {
				media = "application/octet-stream"
				notes = append(notes, label+": missing or invalid Content-Type; body is documented as application/octet-stream.")
			}
			if op.RequestBody == nil {
				op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Content: openapi3.Content{}, Description: "Define the payload schema; request values were intentionally omitted."}}
			}
			op.RequestBody.Value.Content[media] = &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{}}}
		}
	}
	if doc.Paths.Len() == 0 {
		return apiSpec{}, errors.New("this collection contains no supported HTTP endpoints")
	}
	data, err := json.Marshal(doc, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return apiSpec{}, err
	}
	if _, err := parseOpenAPI(string(data)); err != nil {
		return apiSpec{}, fmt.Errorf("generated draft: %w", err)
	}
	return apiSpec{Source: string(data), Collection: collection, Fingerprint: collectionFingerprint(list), Notes: notes}, nil
}

type apiOperation struct {
	path   string
	method string
	item   *openapi3.PathItem
	op     *openapi3.Operation
}

func apiOperations(doc *openapi3.T) []apiOperation {
	var out []apiOperation
	if doc.Paths == nil {
		return out
	}
	for _, path := range doc.Paths.Keys() {
		item := doc.Paths.Value(path)
		for _, method := range slices.Sorted(maps.Keys(item.Operations())) {
			out = append(out, apiOperation{path: path, method: method, item: item, op: item.GetOperation(method)})
		}
	}
	return out
}

// requestFromOperation creates an editable, unsent draft. Runtime auth, scripts
// and schema-based payload synthesis are deliberately left to the user.
func requestFromOperation(doc *openapi3.T, endpoint apiOperation) savedRequest {
	servers := doc.Servers
	if len(endpoint.item.Servers) > 0 {
		servers = endpoint.item.Servers
	}
	if endpoint.op.Servers != nil {
		servers = *endpoint.op.Servers
	}
	base := "{{base_url}}"
	if len(servers) > 0 {
		base = servers[0].URL
		for name, variable := range servers[0].Variables {
			base = strings.ReplaceAll(base, "{"+name+"}", variable.Default)
		}
		if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
			base = "{{base_url}}/" + strings.TrimLeft(base, "/")
		}
	}
	r := savedRequest{
		Name: cmp.Or(endpoint.op.Summary, endpoint.op.OperationID, endpoint.method+" "+endpoint.path), Collection: doc.Info.Title,
		Method: endpoint.method, URL: strings.TrimRight(base, "/") + pathVariable.ReplaceAllString(endpoint.path, "{{$1}}"),
	}
	parameters := map[string]*openapi3.Parameter{}
	for _, refs := range []openapi3.Parameters{endpoint.item.Parameters, endpoint.op.Parameters} {
		for _, ref := range refs {
			if ref != nil && ref.Value != nil {
				p := ref.Value
				parameters[p.In+"\x00"+p.Name] = p
			}
		}
	}
	var query []string
	headers := http.Header{}
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		p := parameters[key]
		if !p.Required {
			continue
		}
		switch p.In {
		case "query":
			query = append(query, url.QueryEscape(p.Name)+"={{"+p.Name+"}}")
		case "header":
			headers.Set(p.Name, "{{"+p.Name+"}}")
		}
	}
	if len(query) > 0 {
		r.URL += "?" + strings.Join(query, "&")
	}
	if body := endpoint.op.RequestBody; body != nil && body.Value != nil && len(body.Value.Content) > 0 {
		media := slices.Sorted(maps.Keys(body.Value.Content))[0]
		if body.Value.Content["application/json"] != nil {
			media = "application/json"
		}
		headers.Set("Content-Type", media)
		mt := body.Value.Content[media]
		example := mt.Example
		if example == nil {
			for _, key := range slices.Sorted(maps.Keys(mt.Examples)) {
				if ex := mt.Examples[key]; ex != nil && ex.Value != nil && ex.Value.Value != nil {
					example = ex.Value.Value
					break
				}
			}
		}
		if example != nil {
			if media == "application/json" || strings.HasSuffix(media, "+json") {
				data, err := json.Marshal(example, jsontext.WithIndent("  "))
				if err == nil {
					r.Body = string(data)
				}
			} else if text, ok := example.(string); ok {
				r.Body = text
			}
		}
	}
	r.Headers = headerBlock(headers)
	return r
}
