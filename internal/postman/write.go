package postman

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
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
	Item                    []outItem     `json:"item"`
	Auth                    *outAuth      `json:"auth,omitempty"`
	Event                   []outEvent    `json:"event,omitempty"`
	Variable                []outVariable `json:"variable,omitempty"`
	ProtocolProfileBehavior *outProfile   `json:"protocolProfileBehavior,omitempty"`
}

// outItem is a folder, whose Item is never nil, or a request.
type outItem struct {
	ID                      string        `json:"id,omitempty"`
	Name                    string        `json:"name"`
	Description             string        `json:"description,omitempty"`
	Item                    []outItem     `json:"item,omitzero"`
	Request                 *outRequest   `json:"request,omitempty"`
	Auth                    *outAuth      `json:"auth,omitempty"`
	Event                   []outEvent    `json:"event,omitempty"`
	Variable                []outVariable `json:"variable,omitempty"`
	Response                []outResponse `json:"response,omitempty"`
	ProtocolProfileBehavior *outProfile   `json:"protocolProfileBehavior,omitempty"`
}

// outProfile is Postman's protocolProfileBehavior for the settings the app
// holds. Postman's request timeout is an app setting, not part of a
// collection, so it has no field here.
type outProfile struct {
	FollowRedirects *bool `json:"followRedirects,omitempty"`
	MaxRedirects    *int  `json:"maxRedirects,omitempty"`
	StrictSSL       *bool `json:"strictSSL,omitempty"`
}

// outResponse is a saved example.
type outResponse struct {
	ID              string      `json:"id,omitempty"`
	Name            string      `json:"name"`
	OriginalRequest *outRequest `json:"originalRequest"`
	Status          string      `json:"status"`
	Code            int         `json:"code"`
	PreviewLanguage string      `json:"_postman_previewlanguage"`
	Header          []outHeader `json:"header"`
	Cookie          []struct{}  `json:"cookie"`
	Body            string      `json:"body"`
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
	File       *outFile       `json:"file,omitempty"`
	GraphQL    *outGraphQL    `json:"graphql,omitempty"`
	Options    *outOptions    `json:"options,omitempty"`
}

type outFile struct {
	Src string `json:"src"`
}

type outGraphQL struct {
	Query     string `json:"query"`
	Variables string `json:"variables"`
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
	Type     string  `json:"type"`
	Bearer   []outKV `json:"bearer,omitempty"`
	Basic    []outKV `json:"basic,omitempty"`
	APIKey   []outKV `json:"apikey,omitempty"`
	OAuth2   []outKV `json:"oauth2,omitempty"`
	Digest   []outKV `json:"digest,omitempty"`
	AWSv4    []outKV `json:"awsv4,omitempty"`
	JWT      []outKV `json:"jwt,omitempty"`
	OAuth1   []outKV `json:"oauth1,omitempty"`
	Hawk     []outKV `json:"hawk,omitempty"`
	EdgeGrid []outKV `json:"edgegrid,omitempty"`
}

type outKV struct {
	Key string `json:"key"`
	// Value is a string, or a bool for a "boolean" parameter.
	Value any    `json:"value"`
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
	for _, f := range folders {
		if root.at(name, f.Path) == nil {
			continue // another collection's settings are not in this export
		}
		if f.Settings.TimeoutSeconds != nil {
			warn.add("timeout", "", 1)
		}
		if f.Settings.RedirectMode == capture.RedirectCurl {
			warn.add("curl redirects", "", 1)
		}
	}
	for _, r := range requests {
		if n := root.at(name, r.Collection); n != nil {
			if r.Protocol != "" {
				warn.add("protocol request", r.Protocol, 1)
				continue // Collection v2.1 cannot represent gRPC calls.
			}
			n.requests = append(n.requests, r)
			if _, err := r.Checks.Script(); err != nil {
				warn.add("checks", "", 1)
			}
			if len(r.TokenSources) > 0 {
				warn.add("token source", "", 1)
			}
			if r.Settings.TimeoutSeconds != nil {
				warn.add("timeout", "", 1)
			}
			if r.Settings.RedirectMode == capture.RedirectCurl {
				warn.add("curl redirects", "", 1)
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
	out.ProtocolProfileBehavior = writeProfile(root.folder.Settings)
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
			Variable: writeVariables(child.folder.Variables), ProtocolProfileBehavior: writeProfile(child.folder.Settings),
		})
	}
	for _, r := range n.requests {
		items = append(items, outItem{ID: r.ID, Name: r.Name, Description: r.Description, Request: writeRequest(r),
			Event: writeEvents(r.Pre, testScript(r)), Response: writeExamples(r.Examples), ProtocolProfileBehavior: writeProfile(r.Settings)})
	}
	return items
}

// writeProfile writes the transport settings Postman can hold; nil when the
// level sets none.
func writeProfile(s request.Settings) *outProfile {
	out := &outProfile{FollowRedirects: s.FollowRedirects, MaxRedirects: s.MaxRedirects, StrictSSL: s.VerifyTLS}
	if s.RedirectMode == capture.RedirectCurl && out.FollowRedirects == nil {
		follow := true
		out.FollowRedirects = &follow
	}
	if out.FollowRedirects == nil && out.MaxRedirects == nil && out.StrictSSL == nil {
		return nil
	}
	return out
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
	case "GraphQL":
		out.Body = &outBody{Mode: "graphql", GraphQL: &outGraphQL{Query: r.GraphQL.Query, Variables: r.GraphQL.Variables}}
	case "Binary":
		out.Body = &outBody{Mode: "file", File: &outFile{Src: r.BodyFile}}
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

// writeExamples writes a request's examples as Postman saved responses.
func writeExamples(examples []request.Example) []outResponse {
	var out []outResponse
	for _, e := range examples {
		res := outResponse{
			ID: e.ID, Name: e.Name, Status: http.StatusText(e.Status), Code: e.Status, Header: []outHeader{}, Cookie: []struct{}{}, Body: e.Body,
			OriginalRequest: writeRequest(request.Request{Method: e.Request.Method, URL: e.Request.URL, Headers: e.Request.Headers, Body: e.Request.Body, Auth: request.Auth{Type: request.AuthInherit}}),
		}
		contentType := ""
		for _, h := range headerRows(e.Headers) {
			res.Header = append(res.Header, outHeader{Key: h.Name, Value: h.Value, Type: "text"})
			if strings.EqualFold(h.Name, "Content-Type") {
				contentType = strings.ToLower(h.Value)
			}
		}
		switch {
		case jsontext.Value(e.Body).IsValid() && strings.TrimSpace(e.Body) != "":
			res.PreviewLanguage = "json"
		case strings.Contains(contentType, "html"):
			res.PreviewLanguage = "html"
		case strings.Contains(contentType, "xml"):
			res.PreviewLanguage = "xml"
		default:
			res.PreviewLanguage = "text"
		}
		out = append(out, res)
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
	case request.AuthOAuth2:
		return &outAuth{Type: "oauth2", OAuth2: writeOAuth2(a.OAuth2)}
	case request.AuthDigest:
		return &outAuth{Type: "digest", Digest: []outKV{{"username", a.Username, "string"}, {"password", a.Password, "string"}}}
	case request.AuthAWS:
		return &outAuth{Type: "awsv4", AWSv4: []outKV{
			{"accessKey", a.AWS.AccessKey, "string"}, {"secretKey", a.AWS.SecretKey, "string"}, {"region", a.AWS.Region, "string"},
			{"service", a.AWS.Service, "string"}, {"sessionToken", a.AWS.SessionToken, "string"},
		}}
	case request.AuthJWT:
		// An empty field is vscroxy's default; it is written out so Postman uses it too.
		addTo := "header"
		if a.JWT.AddTo == "Query" {
			addTo = "queryParam"
		}
		return &outAuth{Type: "jwt", JWT: []outKV{
			{"algorithm", cmp.Or(a.JWT.Algorithm, "HS256"), "string"}, {"secret", a.JWT.Secret, "string"},
			{"isSecretBase64Encoded", a.JWT.SecretBase64, "boolean"}, {"privateKey", a.JWT.PrivateKey, "string"},
			{"payload", cmp.Or(a.JWT.Payload, "{}"), "string"}, {"addTokenTo", addTo, "string"},
			{"headerPrefix", cmp.Or(a.JWT.Prefix, "Bearer"), "string"}, {"queryParamKey", cmp.Or(a.JWT.QueryParam, "token"), "string"},
			{"header", cmp.Or(a.JWT.Header, "{}"), "string"},
		}}
	case request.AuthOAuth1:
		// Postman has one switch for headers versus the body and URL.
		return &outAuth{Type: "oauth1", OAuth1: []outKV{
			{"consumerKey", a.OAuth1.ConsumerKey, "string"}, {"consumerSecret", a.OAuth1.ConsumerSecret, "string"},
			{"token", a.OAuth1.Token, "string"}, {"tokenSecret", a.OAuth1.TokenSecret, "string"},
			{"privateKey", a.OAuth1.PrivateKey, "string"}, {"signatureMethod", cmp.Or(a.OAuth1.SignatureMethod, "HMAC-SHA1"), "string"},
			{"timestamp", a.OAuth1.Timestamp, "string"}, {"nonce", a.OAuth1.Nonce, "string"},
			{"version", cmp.Or(a.OAuth1.Version, "1.0"), "string"}, {"realm", a.OAuth1.Realm, "string"},
			{"callback", a.OAuth1.Callback, "string"}, {"verifier", a.OAuth1.Verifier, "string"},
			{"addParamsToHeader", a.OAuth1.AddTo == "" || a.OAuth1.AddTo == "Header", "boolean"},
			{"includeBodyHash", a.OAuth1.IncludeBodyHash, "boolean"},
			{"addEmptyParamsToSign", a.OAuth1.AddEmptyParamsToSign, "boolean"},
		}}
	case request.AuthHawk:
		return &outAuth{Type: "hawk", Hawk: []outKV{
			{"authId", a.Hawk.AuthID, "string"}, {"authKey", a.Hawk.AuthKey, "string"},
			{"algorithm", strings.ToLower(cmp.Or(a.Hawk.Algorithm, "SHA256")), "string"},
			{"user", a.Hawk.User, "string"}, {"nonce", a.Hawk.Nonce, "string"}, {"timestamp", a.Hawk.Timestamp, "string"},
			{"extraData", a.Hawk.ExtraData, "string"}, {"app", a.Hawk.App, "string"}, {"delegation", a.Hawk.Delegation, "string"},
			{"includePayloadHash", a.Hawk.IncludePayloadHash, "boolean"},
		}}
	case request.AuthEdgeGrid:
		return &outAuth{Type: "edgegrid", EdgeGrid: []outKV{
			{"accessToken", a.EdgeGrid.AccessToken, "string"}, {"clientToken", a.EdgeGrid.ClientToken, "string"},
			{"clientSecret", a.EdgeGrid.ClientSecret, "string"}, {"baseURL", a.EdgeGrid.BaseURL, "string"},
			{"nonce", a.EdgeGrid.Nonce, "string"}, {"timestamp", a.EdgeGrid.Timestamp, "string"},
			{"headersToSign", a.EdgeGrid.HeadersToSign, "string"},
		}}
	}
	return &outAuth{Type: "noauth"}
}

// postmanGrants are Postman's grant_type values by the app's grant.
var postmanGrants = map[string]string{
	"":                                 "authorization_code_with_pkce",
	request.GrantAuthorizationCodePKCE: "authorization_code_with_pkce",
	request.GrantAuthorizationCode:     "authorization_code",
	request.GrantClientCredentials:     "client_credentials",
	request.GrantPassword:              "password_credentials",
}

// writeOAuth2 is an OAuth 2.0 configuration as Postman's parameters. The
// access token is the app's runtime state and is never written.
func writeOAuth2(o request.OAuth2) []outKV {
	grant := cmp.Or(postmanGrants[o.Grant], "authorization_code_with_pkce")
	params := []outKV{{"grant_type", grant, "string"}}
	add := func(key, value string, always bool) {
		if always || value != "" {
			params = append(params, outKV{key, value, "string"})
		}
	}
	add("authUrl", o.AuthURL, false)
	add("accessTokenUrl", o.TokenURL, true)
	add("clientId", o.ClientID, true)
	add("clientSecret", o.ClientSecret, false)
	add("scope", o.Scope, false)
	if grant == "password_credentials" {
		add("username", o.Username, false)
		add("password", o.Password, false)
	}
	add("redirect_uri", o.RedirectURL, false)
	clientAuth := "header"
	if o.ClientAuth == "Body" {
		clientAuth = "body"
	}
	add("client_authentication", clientAuth, true)
	add("headerPrefix", o.HeaderPrefix, false)
	add("addTokenTo", "header", true)
	return params
}

// testScript is a request's post-response script with its Checks after it,
// as pm.test code: Postman has no declarative checks.
func testScript(r request.Request) string {
	checks, _ := r.Checks.Script() // Invalid checks are left out, with a warning.
	if r.Post == "" || checks == "" {
		return r.Post + checks
	}
	return r.Post + "\n\n" + checks
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

// writeVariables writes a collection's or folder's Initial values as
// Postman's value. Current values are local and never exported.
func writeVariables(vars []request.Variable) []outVariable {
	var out []outVariable
	for _, v := range vars {
		out = append(out, outVariable{Key: v.Name, Value: v.Initial, Type: "string", Disabled: v.Disabled})
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
