package postman

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// Collection is a Postman collection as the app holds it.
type Collection struct {
	// Name is the collection's name and the root of every request's path.
	Name     string
	Requests []request.Request
	// Folders are the collection and the folders in it that carry settings:
	// auth, scripts, variables or a description.
	Folders []request.Folder
	// Warnings say, counted across the file, what the import dropped.
	Warnings []string
}

type pmItem struct {
	ID                      string           `json:"id"`
	Name                    string           `json:"name"`
	Description             jsontext.Value   `json:"description"`
	Item                    *[]pmItem        `json:"item"`
	Request                 jsontext.Value   `json:"request"`
	Response                []jsontext.Value `json:"response"`
	Event                   []pmEvent        `json:"event"`
	Variable                []pmVariable     `json:"variable"`
	Auth                    jsontext.Value   `json:"auth"`
	ProtocolProfileBehavior jsontext.Value   `json:"protocolProfileBehavior"`
}

type pmEvent struct {
	Listen   string `json:"listen"`
	Disabled bool   `json:"disabled"`
	Script   struct {
		Exec jsontext.Value `json:"exec"`
	} `json:"script"`
}

type pmVariable struct {
	ID       string         `json:"id"`
	Key      string         `json:"key"`
	Value    jsontext.Value `json:"value"`
	Disabled bool           `json:"disabled"`
}

type pmRequest struct {
	Method      string         `json:"method"`
	Header      jsontext.Value `json:"header"`
	Body        *pmBody        `json:"body"`
	URL         jsontext.Value `json:"url"`
	Auth        jsontext.Value `json:"auth"`
	Description jsontext.Value `json:"description"`
	Proxy       jsontext.Value `json:"proxy"`
	Certificate jsontext.Value `json:"certificate"`
}

type pmKeyValue struct {
	Key      string         `json:"key"`
	Value    jsontext.Value `json:"value"`
	Disabled bool           `json:"disabled"`
}

type pmBody struct {
	Mode       string        `json:"mode"`
	Disabled   bool          `json:"disabled"`
	Raw        string        `json:"raw"`
	URLEncoded []pmFormField `json:"urlencoded"`
	FormData   []pmFormField `json:"formdata"`
	GraphQL    struct {
		Query     string         `json:"query"`
		Variables jsontext.Value `json:"variables"`
	} `json:"graphql"`
	Options struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
}

type pmFormField struct {
	Key      string         `json:"key"`
	Value    jsontext.Value `json:"value"`
	Type     string         `json:"type"`
	Src      jsontext.Value `json:"src"`
	Disabled bool           `json:"disabled"`
}

type pmURL struct {
	Raw      string         `json:"raw"`
	Protocol string         `json:"protocol"`
	Host     jsontext.Value `json:"host"`
	Port     jsontext.Value `json:"port"`
	Path     jsontext.Value `json:"path"`
	Query    []pmKeyValue   `json:"query"`
	Hash     string         `json:"hash"`
	Variable []pmKeyValue   `json:"variable"`
}

// ReadCollection reads a Postman Collection v2.0 or v2.1 file. Folders
// become paths under the collection's name; what the app cannot hold is
// dropped and counted in Warnings.
func ReadCollection(data []byte) (Collection, error) {
	var probe struct {
		Info     jsontext.Value `json:"info"`
		Item     jsontext.Value `json:"item"`
		Requests jsontext.Value `json:"requests"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Collection{}, fmt.Errorf("not a Postman collection: %w", err)
	}
	if probe.Info.Kind() != '{' {
		if probe.Requests.Kind() == '[' {
			return Collection{}, errors.New("Postman Collection v1 is not supported; export it from Postman as Collection v2.1")
		}
		return Collection{}, errors.New("not a Postman collection: it has no info object")
	}
	var file struct {
		Info struct {
			PostmanID   string         `json:"_postman_id"`
			Name        string         `json:"name"`
			Description jsontext.Value `json:"description"`
			Schema      string         `json:"schema"`
		} `json:"info"`
		Item                    []pmItem       `json:"item"`
		Auth                    jsontext.Value `json:"auth"`
		Event                   []pmEvent      `json:"event"`
		Variable                []pmVariable   `json:"variable"`
		ProtocolProfileBehavior jsontext.Value `json:"protocolProfileBehavior"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return Collection{}, fmt.Errorf("read Postman collection: %w", err)
	}
	switch schema := file.Info.Schema; {
	case strings.Contains(schema, "v2.0.0"), strings.Contains(schema, "v2.1.0"):
	case strings.Contains(schema, "v1."):
		return Collection{}, errors.New("Postman Collection v1 is not supported; export it from Postman as Collection v2.1")
	case schema == "" && probe.Item.Kind() == '[':
	case schema == "":
		return Collection{}, errors.New("not a Postman collection: it has no schema and no items")
	default:
		return Collection{}, fmt.Errorf("unsupported Postman collection schema %q; export it from Postman as Collection v2.1", schema)
	}
	name := segment(cmp.Or(strings.TrimSpace(file.Info.Name), "Postman collection"))
	r := &reader{base: cmp.Or(file.Info.PostmanID, name), warn: tally{}}
	r.out.Name = name
	r.folder(name, file.Info.Description, file.Auth, file.Event, file.Variable, "collection auth")
	r.settings(file.ProtocolProfileBehavior)
	if err := r.items(file.Item, name); err != nil {
		return Collection{}, err
	}
	r.out.Warnings = r.warn.list()
	return r.out, nil
}

// reader carries an import's results and counts.
type reader struct {
	base string // _postman_id or the name, seeding generated request IDs
	out  Collection
	warn tally
}

// items reads one level of items under parent, folders depth first.
func (r *reader) items(items []pmItem, parent string) error {
	seen := map[string]int{}
	for _, it := range items {
		if it.Item != nil {
			r.settings(it.ProtocolProfileBehavior)
			path := parent + "/" + segment(cmp.Or(it.Name, "Untitled folder"))
			r.folder(path, it.Description, it.Auth, it.Event, it.Variable, "folder auth")
			if err := r.items(*it.Item, path); err != nil {
				return err
			}
			continue
		}
		if kind := it.Request.Kind(); kind != '{' && kind != '"' {
			continue
		}
		ordinal := seen[it.Name]
		seen[it.Name]++
		if err := r.request(it, parent, ordinal); err != nil {
			return err
		}
	}
	return nil
}

// settings counts dropped protocol profile, certificate or proxy settings.
func (r *reader) settings(values ...jsontext.Value) {
	for _, v := range values {
		if k := v.Kind(); k != 0 && k != 'n' {
			r.warn.add("settings", "", 1)
			return
		}
	}
}

// folder records a level's settings when it has any.
func (r *reader) folder(path string, description, auth jsontext.Value, events []pmEvent, vars []pmVariable, authWarning string) {
	f := request.Folder{Path: path, Description: describe(description), Variables: variables(vars)}
	f.Pre, f.Post = r.scripts(events)
	if a, set := r.auth(auth, authWarning); set {
		f.Auth = a
	}
	if f.Description != "" || f.Auth.Type != "" || f.Pre != "" || f.Post != "" || len(f.Variables) > 0 {
		r.out.Folders = append(r.out.Folders, f)
	}
}

// request reads a request item filed under parent; ordinal counts the
// earlier siblings with the same name.
func (r *reader) request(it pmItem, parent string, ordinal int) error {
	out := request.Request{ID: it.ID, Name: it.Name, Collection: parent, Method: "GET", Auth: request.Auth{Type: request.AuthInherit}}
	if out.ID == "" {
		sum := sha256.Sum256([]byte(strings.Join([]string{r.base, parent, it.Name, strconv.Itoa(ordinal)}, "\x00")))
		out.ID = "postman-" + hex.EncodeToString(sum[:])
	}
	out.Pre, out.Post = r.scripts(it.Event)
	out.Examples = r.examples(it.Response, out.ID)
	r.warn.add("request variable", "", len(it.Variable))
	hasDescription := describe(it.Description) != ""
	if it.Request.Kind() == '"' {
		out.URL = text(it.Request)
		r.settings(it.ProtocolProfileBehavior)
	} else {
		var req pmRequest
		if err := json.Unmarshal(it.Request, &req); err != nil {
			return fmt.Errorf("read request %q in %s: %w", it.Name, parent, err)
		}
		out.Method = strings.ToUpper(cmp.Or(strings.TrimSpace(req.Method), "GET"))
		out.URL = r.url(req.URL)
		out.HeaderList = readHeaders(req.Header)
		r.body(&out, req.Body)
		if a, set := r.auth(req.Auth, "request auth"); set {
			if out.Auth = a; a.Type == "None" {
				out.Auth = request.Auth{}
			}
		}
		hasDescription = hasDescription || describe(req.Description) != ""
		r.settings(it.ProtocolProfileBehavior, req.Proxy, req.Certificate)
	}
	if hasDescription {
		r.warn.add("description", "", 1)
	}
	out.Headers = headerBlock(out.HeaderList)
	r.out.Requests = append(r.out.Requests, out)
	return nil
}

// pmResponse is a saved example: a response and the request that got it.
type pmResponse struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	OriginalRequest jsontext.Value `json:"originalRequest"`
	Code            int            `json:"code"`
	Header          jsontext.Value `json:"header"`
	Body            jsontext.Value `json:"body"`
}

// examples reads a request's saved examples. One that is not an example,
// or whose body is over the size an example keeps, is dropped and counted.
func (r *reader) examples(list []jsontext.Value, requestID string) []request.Example {
	var out []request.Example
	for i, v := range list {
		var res pmResponse
		if json.Unmarshal(v, &res) != nil || len(text(res.Body)) > request.MaxExampleBody {
			r.warn.add("example", "", 1)
			continue
		}
		e := request.Example{ID: res.ID, Name: res.Name, Status: res.Code, Headers: headerBlock(readHeaders(res.Header)), Body: text(res.Body)}
		if e.ID == "" {
			sum := sha256.Sum256([]byte(requestID + "\x00example\x00" + strconv.Itoa(i)))
			e.ID = "postman-" + hex.EncodeToString(sum[:])
		}
		if e.Name == "" {
			e.Name = strings.TrimSpace(strconv.Itoa(e.Status) + " " + http.StatusText(e.Status))
		}
		e.Request.Method = "GET"
		if res.OriginalRequest.Kind() == '"' {
			e.Request.URL = text(res.OriginalRequest)
		} else if res.OriginalRequest.Kind() == '{' {
			var req pmRequest
			if json.Unmarshal(res.OriginalRequest, &req) == nil {
				e.Request.Method = strings.ToUpper(cmp.Or(strings.TrimSpace(req.Method), "GET"))
				e.Request.URL = r.url(req.URL)
				e.Request.Headers = headerBlock(readHeaders(req.Header))
				if req.Body != nil && req.Body.Mode == "raw" {
					e.Request.Body = req.Body.Raw
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// describe is a description's text, given as a string or {content, type}.
func describe(v jsontext.Value) string {
	if v.Kind() == '{' {
		var d struct {
			Content jsontext.Value `json:"content"`
		}
		if json.Unmarshal(v, &d) != nil {
			return ""
		}
		v = d.Content
	}
	return text(v)
}

// variables reads collection or folder variables.
func variables(vars []pmVariable) []request.Field {
	var out []request.Field
	for _, v := range vars {
		out = append(out, request.Field{Name: cmp.Or(v.Key, v.ID), Value: text(v.Value), Disabled: v.Disabled})
	}
	return out
}

// scripts joins the enabled pre-request and test scripts.
func (r *reader) scripts(events []pmEvent) (pre, post string) {
	var pres, posts []string
	for _, e := range events {
		if e.Listen != "prerequest" && e.Listen != "test" {
			continue
		}
		if e.Disabled {
			r.warn.add("disabled script", "", 1)
			continue
		}
		var source string
		if e.Script.Exec.Kind() == '[' {
			var lines []jsontext.Value
			if json.Unmarshal(e.Script.Exec, &lines) == nil {
				parts := make([]string, len(lines))
				for i, line := range lines {
					parts[i] = text(line)
				}
				source = strings.Join(parts, "\n")
			}
		} else {
			source = text(e.Script.Exec)
		}
		if e.Listen == "prerequest" {
			pres = append(pres, source)
		} else {
			posts = append(posts, source)
		}
	}
	return strings.Join(pres, "\n"), strings.Join(posts, "\n")
}

// auth maps a Postman auth object. set is false when there is none.
// No auth is Type "None"; auth the app cannot send is None too, counted
// under the warning named unsupported.
func (r *reader) auth(v jsontext.Value, unsupported string) (auth request.Auth, set bool) {
	if v.Kind() != '{' {
		return request.Auth{}, false
	}
	var obj map[string]jsontext.Value
	if json.Unmarshal(v, &obj) != nil {
		return request.Auth{}, false
	}
	kind := text(obj["type"])
	params := map[string]string{}
	switch p := obj[kind]; p.Kind() {
	case '[': // v2.1: [{key, value, type}]
		var list []pmKeyValue
		if json.Unmarshal(p, &list) == nil {
			for _, kv := range list {
				params[kv.Key] = text(kv.Value)
			}
		}
	case '{': // v2.0: {key: value}
		var m map[string]jsontext.Value
		if json.Unmarshal(p, &m) == nil {
			for k, value := range m {
				params[k] = text(value)
			}
		}
	}
	switch kind {
	case "":
		return request.Auth{}, false
	case "noauth":
		return request.Auth{Type: "None"}, true
	case "bearer":
		return request.Auth{Type: "Bearer", Token: params["token"]}, true
	case "basic":
		return request.Auth{Type: "Basic", Username: params["username"], Password: params["password"]}, true
	case "apikey":
		location := "Header"
		if strings.EqualFold(params["in"], "query") {
			location = "Query"
		}
		return request.Auth{Type: "API key", Key: params["key"], Value: params["value"], Location: location}, true
	case "oauth2":
		o, dropped := r.oauth2(params)
		if dropped == "" {
			return request.Auth{Type: request.AuthOAuth2, OAuth2: o}, true
		}
		r.warn.add(unsupported, dropped, 1)
		return request.Auth{Type: "None"}, true
	case "digest":
		// realm, nonce, algorithm, qop, nc, cnonce and opaque come from the
		// server's challenge at each send.
		if params["disableRetryRequest"] == "true" {
			r.warn.add("digest retry", "", 1)
		}
		return request.Auth{Type: request.AuthDigest, Username: params["username"], Password: params["password"]}, true
	case "awsv4":
		if params["addAuthDataToQuery"] == "true" {
			r.warn.add("aws query", "", 1)
		}
		return request.Auth{Type: request.AuthAWS, AWS: request.AWSAuth{
			AccessKey: params["accessKey"], SecretKey: params["secretKey"], Region: params["region"],
			Service: params["service"], SessionToken: params["sessionToken"],
		}}, true
	case "jwt":
		if alg := params["algorithm"]; alg != "" && !slices.Contains(request.JWTAlgorithms, alg) {
			r.warn.add(unsupported, "a JWT signed with "+alg, 1)
			return request.Auth{Type: "None"}, true
		}
		j := request.JWTAuth{
			Algorithm: params["algorithm"], Secret: params["secret"], SecretBase64: params["isSecretBase64Encoded"] == "true",
			PrivateKey: params["privateKey"], Payload: params["payload"], Header: params["header"],
			Prefix: params["headerPrefix"], QueryParam: params["queryParamKey"],
		}
		if params["addTokenTo"] == "queryParam" {
			j.AddTo = "Query"
		}
		return request.Auth{Type: request.AuthJWT, JWT: j}, true
	}
	r.warn.add(unsupported, kind+" auth", 1)
	return request.Auth{Type: "None"}, true
}

// oauth2Grants are the app's grants by Postman grant_type.
var oauth2Grants = map[string]string{
	"authorization_code_with_pkce": request.GrantAuthorizationCodePKCE,
	"authorization_code":           request.GrantAuthorizationCode,
	"client_credentials":           request.GrantClientCredentials,
	"password_credentials":         request.GrantPassword,
}

// oauth2 maps Postman's oauth2 parameters. dropped names a grant the app
// cannot get a token with. Postman's token manager data, the access token
// among it, stays behind.
func (r *reader) oauth2(params map[string]string) (o request.OAuth2, dropped string) {
	grantType := cmp.Or(params["grant_type"], "authorization_code")
	o.Grant = oauth2Grants[grantType]
	if o.Grant == "" {
		return o, "the OAuth 2.0 " + strings.ReplaceAll(grantType, "_", " ") + " grant"
	}
	o.AuthURL, o.TokenURL = params["authUrl"], params["accessTokenUrl"]
	o.ClientID, o.ClientSecret, o.Scope = params["clientId"], params["clientSecret"], params["scope"]
	o.Username, o.Password, o.HeaderPrefix = params["username"], params["password"], params["headerPrefix"]
	if strings.EqualFold(params["client_authentication"], "body") {
		o.ClientAuth = "Body"
	}
	if redirect := params["redirect_uri"]; loopback(redirect) {
		o.RedirectURL = redirect
	} else if redirect != "" && (o.Grant == request.GrantAuthorizationCodePKCE || o.Grant == request.GrantAuthorizationCode) {
		r.warn.add("oauth2 callback", "", 1)
	}
	if params["addTokenTo"] == "queryParams" {
		r.warn.add("oauth2 query", "", 1)
	}
	return o, ""
}

// loopback says whether raw is an http URL on 127.0.0.1, localhost or [::1],
// the only callbacks the app can listen on.
func loopback(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}

// readHeaders reads a header array, or the "Key: Value" block old exports use.
func readHeaders(v jsontext.Value) []request.Field {
	switch v.Kind() {
	case '"':
		return headerRows(text(v))
	case '[':
		var list []pmKeyValue
		if json.Unmarshal(v, &list) != nil {
			return nil
		}
		var rows []request.Field
		for _, h := range list {
			rows = append(rows, request.Field{Name: h.Key, Value: text(h.Value), Disabled: h.Disabled})
		}
		return rows
	}
	return nil
}

// url reads a string or object URL: raw when present, else rebuilt from its
// parts, with path variables filled in.
func (r *reader) url(v jsontext.Value) string {
	if v.Kind() != '{' {
		return text(v)
	}
	var u pmURL
	if json.Unmarshal(v, &u) != nil {
		return ""
	}
	disabled := 0
	for _, q := range u.Query {
		if q.Disabled {
			disabled++
		}
	}
	r.warn.add("disabled query", "", disabled)
	raw := u.Raw
	if raw == "" {
		raw = rebuildURL(u)
	}
	if len(u.Variable) == 0 {
		return raw
	}
	vars := map[string]string{}
	for _, pv := range u.Variable {
		vars[pv.Key] = text(pv.Value)
	}
	return fillPathVariables(raw, vars)
}

// rebuildURL writes a URL object without raw from its parts.
func rebuildURL(u pmURL) string {
	var b strings.Builder
	if u.Protocol != "" {
		b.WriteString(u.Protocol + "://")
	}
	b.WriteString(joinParts(u.Host, "."))
	if port := text(u.Port); port != "" {
		b.WriteString(":" + port)
	}
	if path := joinParts(u.Path, "/"); path != "" {
		if !strings.HasPrefix(path, "/") {
			b.WriteString("/")
		}
		b.WriteString(path)
	}
	var query []string
	for _, q := range u.Query {
		if q.Disabled {
			continue
		}
		if q.Value.Kind() == 0 || q.Value.Kind() == 'n' {
			query = append(query, q.Key)
		} else {
			query = append(query, q.Key+"="+text(q.Value))
		}
	}
	if len(query) > 0 {
		b.WriteString("?" + strings.Join(query, "&"))
	}
	if u.Hash != "" {
		b.WriteString("#" + u.Hash)
	}
	return b.String()
}

// joinParts joins a host or path given as a string or an array of strings
// (or, for paths, {type, value} objects).
func joinParts(v jsontext.Value, sep string) string {
	if v.Kind() != '[' {
		return text(v)
	}
	var list []jsontext.Value
	if json.Unmarshal(v, &list) != nil {
		return ""
	}
	parts := make([]string, len(list))
	for i, part := range list {
		if part.Kind() == '{' {
			var p struct {
				Value jsontext.Value `json:"value"`
			}
			if json.Unmarshal(part, &p) == nil {
				part = p.Value
			}
		}
		parts[i] = text(part)
	}
	return strings.Join(parts, sep)
}

// fillPathVariables replaces each ":key" path segment of raw with the
// variable's value, or {{key}} when the value is empty.
func fillPathVariables(raw string, vars map[string]string) string {
	start := 0
	if scheme, _, ok := strings.Cut(raw, "://"); ok && !strings.ContainsAny(scheme, "/?#") {
		start = len(scheme) + len("://")
	}
	slash := strings.IndexByte(raw[start:], '/')
	if slash < 0 {
		return raw
	}
	begin, end := start+slash, len(raw)
	if i := strings.IndexAny(raw[begin:], "?#"); i >= 0 {
		end = begin + i
	}
	segments := strings.Split(raw[begin:end], "/")
	for i, seg := range segments {
		if key, ok := strings.CutPrefix(seg, ":"); ok {
			if value, ok := vars[key]; ok {
				segments[i] = cmp.Or(value, "{{"+key+"}}")
			}
		}
	}
	return raw[:begin] + strings.Join(segments, "/") + raw[end:]
}

// rawContentTypes are the Content-Type headers Postman sends for a raw
// body's language.
var rawContentTypes = map[string]string{
	"json":       "application/json",
	"xml":        "application/xml",
	"html":       "text/html",
	"javascript": "application/javascript",
	"text":       "text/plain",
}

// body reads a request body into out; out.HeaderList may gain the
// Content-Type Postman would send.
func (r *reader) body(out *request.Request, body *pmBody) {
	if body == nil || body.Disabled {
		return
	}
	switch body.Mode {
	case "":
	case "raw":
		out.BodyMode, out.Body = "Raw", body.Raw
		if contentType := rawContentTypes[body.Options.Raw.Language]; contentType != "" && body.Raw != "" {
			addContentType(out, contentType)
		}
	case "urlencoded":
		out.BodyMode = "URL encoded"
		for _, f := range body.URLEncoded {
			out.Form = append(out.Form, request.Field{Name: f.Key, Value: text(f.Value), Disabled: f.Disabled})
		}
	case "formdata":
		out.BodyMode = "Multipart"
		for _, f := range body.FormData {
			field := request.Field{Name: f.Key, Value: text(f.Value), Disabled: f.Disabled}
			if f.Type == "file" {
				field.File, field.Value = true, text(f.Src)
				if f.Src.Kind() == '[' {
					var srcs []jsontext.Value
					field.Value = ""
					if json.Unmarshal(f.Src, &srcs) == nil && len(srcs) > 0 {
						field.Value = text(srcs[0])
					}
					if len(srcs) > 1 {
						r.warn.add("form files", "", 1)
					}
				}
			}
			out.Form = append(out.Form, field)
		}
	case "graphql":
		variables := jsontext.Value(`{}`)
		switch v := body.GraphQL.Variables; v.Kind() {
		case '{':
			variables = v
		case '"':
			if parsed := jsontext.Value(strings.TrimSpace(text(v))); parsed.Kind() == '{' && parsed.IsValid() {
				variables = parsed
			}
		}
		variables = variables.Clone()
		if variables.Compact() != nil {
			variables = jsontext.Value(`{}`)
		}
		data, err := json.Marshal(struct {
			Query     string         `json:"query"`
			Variables jsontext.Value `json:"variables"`
		}{body.GraphQL.Query, variables})
		if err != nil {
			r.warn.add("body", "GraphQL", 1)
			return
		}
		out.BodyMode, out.Body = "Raw", string(data)
		addContentType(out, "application/json")
	case "file":
		r.warn.add("body", "binary file", 1)
	default:
		r.warn.add("body", body.Mode, 1)
	}
}

// addContentType appends a Content-Type row unless an enabled one exists.
func addContentType(out *request.Request, value string) {
	for _, h := range out.HeaderList {
		if !h.Disabled && strings.EqualFold(strings.TrimSpace(h.Name), "Content-Type") {
			return
		}
	}
	out.HeaderList = append(out.HeaderList, request.Field{Name: "Content-Type", Value: value})
}
