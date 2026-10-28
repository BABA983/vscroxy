package postman

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
)

// schemaV21 is the schema an export declares.
const schemaV21 = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

type outCollection struct {
	Info struct {
		PostmanID   string `json:"_postman_id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Schema      string `json:"schema"`
	} `json:"info"`
	Item     []outItem     `json:"item"`
	Auth     *outAuth      `json:"auth,omitempty"`
	Event    []outEvent    `json:"event,omitempty"`
	Variable []outVariable `json:"variable,omitempty"`
}

// outItem is a folder, whose Item is never nil, or a request.
type outItem struct {
	ID          string        `json:"id,omitempty"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Item        []outItem     `json:"item,omitzero"`
	Request     *outRequest   `json:"request,omitempty"`
	Auth        *outAuth      `json:"auth,omitempty"`
	Event       []outEvent    `json:"event,omitempty"`
	Variable    []outVariable `json:"variable,omitempty"`
}

type outRequest struct {
	Method string      `json:"method"`
	Header []outHeader `json:"header"`
	Body   *outBody    `json:"body,omitempty"`
	URL    outURL      `json:"url"`
	Auth   *outAuth    `json:"auth,omitempty"`
}

type outHeader struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled,omitzero"`
}

type outURL struct {
	Raw      string     `json:"raw"`
	Protocol string     `json:"protocol,omitempty"`
	Host     []string   `json:"host,omitempty"`
	Port     string     `json:"port,omitempty"`
	Path     []string   `json:"path,omitempty"`
	Query    []outQuery `json:"query,omitempty"`
	Hash     string     `json:"hash,omitempty"`
}

type outQuery struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type outBody struct {
	Mode       string         `json:"mode"`
	Raw        string         `json:"raw,omitempty"`
	URLEncoded []outFormField `json:"urlencoded,omitempty"`
	FormData   []outFormField `json:"formdata,omitempty"`
	Options    *outOptions    `json:"options,omitempty"`
}

type outOptions struct {
	Raw struct {
		Language string `json:"language"`
	} `json:"raw"`
}

type outFormField struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Type     string `json:"type"`
	Src      string `json:"src,omitempty"`
	Disabled bool   `json:"disabled,omitzero"`
}

type outAuth struct {
	Type   string  `json:"type"`
	Bearer []outKV `json:"bearer,omitempty"`
	Basic  []outKV `json:"basic,omitempty"`
	APIKey []outKV `json:"apikey,omitempty"`
}

type outKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type outEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Type string   `json:"type"`
		Exec []string `json:"exec"`
	} `json:"script"`
}

type outVariable struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled,omitzero"`
}

// WriteCollection writes the collection name as a Postman Collection v2.1
// file: the requests filed under name and the settings of the folders under
// it. warnings say what Postman cannot hold.
func WriteCollection(name string, requests []request.Request, folders []request.Folder) (data []byte, warnings []string, err error) {
	if strings.Trim(name, "/") == "" {
		return nil, nil, errors.New("the collection name is empty")
	}
	root := &node{children: map[string]*node{}}
	for _, f := range folders {
		if n := root.at(name, f.Path); n != nil {
			n.folder = f
		}
	}
	warn := tally{}
	for _, r := range requests {
		if n := root.at(name, r.Collection); n != nil {
			n.requests = append(n.requests, r)
			if r.Checks != (request.Checks{}) {
				warn.add("checks", "", 1)
			}
		}
	}
	var out outCollection
	out.Info.PostmanID = uuid.New().String()
	out.Info.Name = unsegment(name)
	out.Info.Description = root.folder.Description
	out.Info.Schema = schemaV21
	out.Item = root.items()
	out.Auth = folderAuth(root.folder.Auth)
	out.Event = writeEvents(root.folder.Pre, root.folder.Post)
	out.Variable = writeVariables(root.folder.Variables)
	data, err = json.Marshal(out, jsontext.WithIndent("  "))
	if err != nil {
		return nil, nil, err
	}
	return append(data, '\n'), warn.list(), nil
}

// node is one level of the exported tree.
type node struct {
	folder   request.Folder
	children map[string]*node
	requests []request.Request
}

// at is the node for path, made as needed, or nil when path is not in the
// collection name.
func (n *node) at(name, path string) *node {
	rest, ok := strings.CutPrefix(path, name)
	if !ok || rest != "" && rest[0] != '/' {
		return nil
	}
	for seg := range strings.SplitSeq(rest, "/") {
		if seg == "" {
			continue
		}
		child := n.children[seg]
		if child == nil {
			child = &node{children: map[string]*node{}}
			n.children[seg] = child
		}
		n = child
	}
	return n
}

// items are a level's folders by name, then its requests in order.
func (n *node) items() []outItem {
	items := []outItem{}
	for _, seg := range slices.Sorted(maps.Keys(n.children)) {
		child := n.children[seg]
		items = append(items, outItem{
			Name: unsegment(seg), Description: child.folder.Description, Item: child.items(),
			Auth: folderAuth(child.folder.Auth), Event: writeEvents(child.folder.Pre, child.folder.Post),
			Variable: writeVariables(child.folder.Variables),
		})
	}
	for _, r := range n.requests {
		items = append(items, outItem{ID: r.ID, Name: r.Name, Request: writeRequest(r), Event: writeEvents(r.Pre, r.Post)})
	}
	return items
}

func writeRequest(r request.Request) *outRequest {
	out := &outRequest{Method: strings.ToUpper(strings.TrimSpace(r.Method)), Header: []outHeader{}, URL: writeURL(r.URL)}
	if out.Method == "" {
		out.Method = "GET"
	}
	rows := r.HeaderList
	if len(rows) == 0 {
		rows = headerRows(r.Headers)
	}
	for _, h := range rows {
		out.Header = append(out.Header, outHeader{Key: h.Name, Value: h.Value, Type: "text", Disabled: h.Disabled})
	}
	switch r.BodyMode {
	case "", "Raw":
		if r.Body != "" {
			out.Body = &outBody{Mode: "raw", Raw: r.Body, Options: &outOptions{}}
			out.Body.Options.Raw.Language = "text"
			if jsontext.Value(r.Body).IsValid() {
				out.Body.Options.Raw.Language = "json"
			}
		}
	case "URL encoded":
		out.Body = &outBody{Mode: "urlencoded", URLEncoded: []outFormField{}}
		for _, f := range r.Form {
			out.Body.URLEncoded = append(out.Body.URLEncoded, outFormField{Key: f.Name, Value: f.Value, Type: "text", Disabled: f.Disabled})
		}
	case "Multipart":
		out.Body = &outBody{Mode: "formdata", FormData: []outFormField{}}
		for _, f := range r.Form {
			field := outFormField{Key: f.Name, Value: f.Value, Type: "text", Disabled: f.Disabled}
			if f.File {
				field.Value, field.Type, field.Src = "", "file", f.Value
			}
			out.Body.FormData = append(out.Body.FormData, field)
		}
	}
	switch r.Auth.Type {
	case request.AuthInherit:
	case "", "None":
		out.Auth = &outAuth{Type: "noauth"}
	default:
		out.Auth = writeAuth(r.Auth)
	}
	return out
}

// folderAuth is a folder's auth, nil when it sets none.
func folderAuth(a request.Auth) *outAuth {
	switch a.Type {
	case "", request.AuthInherit:
		return nil
	case "None":
		return &outAuth{Type: "noauth"}
	}
	return writeAuth(a)
}

func writeAuth(a request.Auth) *outAuth {
	switch a.Type {
	case "Bearer":
		return &outAuth{Type: "bearer", Bearer: []outKV{{"token", a.Token, "string"}}}
	case "Basic":
		return &outAuth{Type: "basic", Basic: []outKV{{"username", a.Username, "string"}, {"password", a.Password, "string"}}}
	case "API key":
		in := "header"
		if a.Location == "Query" {
			in = "query"
		}
		return &outAuth{Type: "apikey", APIKey: []outKV{{"key", a.Key, "string"}, {"value", a.Value, "string"}, {"in", in, "string"}}}
	}
	return &outAuth{Type: "noauth"}
}

func writeEvents(pre, post string) []outEvent {
	var events []outEvent
	for _, e := range []struct{ listen, source string }{{"prerequest", pre}, {"test", post}} {
		if e.source == "" {
			continue
		}
		var event outEvent
		event.Listen = e.listen
		event.Script.Type = "text/javascript"
		event.Script.Exec = strings.Split(e.source, "\n")
		events = append(events, event)
	}
	return events
}

func writeVariables(vars []request.Field) []outVariable {
	var out []outVariable
	for _, v := range vars {
		out = append(out, outVariable{Key: v.Name, Value: v.Value, Type: "string", Disabled: v.Disabled})
	}
	return out
}

// writeURL splits raw into Postman's parts without judging it: {{var}}
// hosts and missing schemes are fine, and raw stays verbatim.
func writeURL(raw string) outURL {
	u := outURL{Raw: raw}
	rest := strings.TrimSpace(raw)
	rest, u.Hash, _ = strings.Cut(rest, "#")
	rest, query, hasQuery := strings.Cut(rest, "?")
	if hasQuery && query != "" {
		for pair := range strings.SplitSeq(query, "&") {
			key, value, _ := strings.Cut(pair, "=")
			u.Query = append(u.Query, outQuery{Key: key, Value: value})
		}
	}
	if scheme, after, ok := strings.Cut(rest, "://"); ok && !strings.ContainsAny(scheme, "/") {
		u.Protocol, rest = scheme, after
	}
	host, path, hasPath := strings.Cut(rest, "/")
	if before, port, ok := strings.CutLast(host, ":"); ok && isPort(port) {
		host, u.Port = before, port
	}
	if host != "" {
		u.Host = splitOutsideVars(host, '.')
	}
	if hasPath {
		u.Path = splitOutsideVars(path, '/')
	}
	return u
}

// isPort says whether s is a port number or a {{variable}}.
func isPort(s string) bool {
	if strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		return true
	}
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// splitOutsideVars splits s at sep, except inside {{ }}.
func splitOutsideVars(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			depth++
			i++
		case strings.HasPrefix(s[i:], "}}") && depth > 0:
			depth--
			i++
		case s[i] == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}
