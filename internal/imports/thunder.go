package imports

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// ReadThunder reads a Thunder Client collection export: one collection object
// or an array of them. Thunder's JSON is close to the app's model, so headers,
// params, bodies, scripts and the common auth types convert directly.
func ReadThunder(data []byte) (Result, error) {
	if len(data) > 16<<20 {
		return Result{}, fmt.Errorf("Thunder Client exports are limited to 16 MiB")
	}
	raw := jsontext.Value(data)
	var collections []thunderCollection
	switch raw.Kind() {
	case '[':
		if err := json.Unmarshal(data, &collections); err != nil {
			return Result{}, fmt.Errorf("read Thunder Client collections: %w", err)
		}
	case '{':
		var one thunderCollection
		if err := json.Unmarshal(data, &one); err != nil {
			return Result{}, fmt.Errorf("read Thunder Client collection: %w", err)
		}
		collections = []thunderCollection{one}
	default:
		return Result{}, fmt.Errorf("not a Thunder Client JSON export")
	}
	var result Result
	for _, collection := range collections {
		converted := thunderCollectionRequests(collection)
		if len(converted.Requests) == 0 && len(converted.Folders) == 0 {
			continue
		}
		result.Collections = append(result.Collections, converted)
	}
	if len(result.Collections) == 0 {
		return Result{}, fmt.Errorf("the export has no HTTP requests")
	}
	return result, nil
}

type thunderCollection struct {
	Client         string            `json:"client"`
	CollectionName string            `json:"collectionName"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Folders        []thunderFolder   `json:"folders"`
	Requests       []thunderRequest  `json:"requests"`
	Data           []thunderVariable `json:"data"`
}

type thunderFolder struct {
	ID          string  `json:"_id"`
	ContainerID string  `json:"containerId"`
	Name        string  `json:"name"`
	SortNum     float64 `json:"sortNum"`
}

type thunderVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type thunderRequest struct {
	ID          string         `json:"_id"`
	ContainerID string         `json:"containerId"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Method      string         `json:"method"`
	SortNum     float64        `json:"sortNum"`
	Headers     []thunderPair  `json:"headers"`
	Params      []thunderPair  `json:"params"`
	Body        *thunderBody   `json:"body"`
	Auth        jsontext.Value `json:"auth"`
	PreScripts  []string       `json:"preScripts"`
	PostScripts []string       `json:"postScripts"`
	Tests       []thunderTest  `json:"tests"`
}

type thunderPair struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	IsDisabled bool   `json:"isDisabled"`
}

type thunderBody struct {
	Type    string        `json:"type"`
	Raw     string        `json:"raw"`
	Form    []thunderPair `json:"form"`
	GraphQL *thunderGraph `json:"graphql"`
}

type thunderGraph struct {
	Query     string `json:"query"`
	Variables string `json:"variables"`
}

type thunderTest struct {
	Type   string `json:"type"`
	Custom string `json:"custom"`
	Action string `json:"action"`
	Value  string `json:"value"`
}

func thunderCollectionRequests(c thunderCollection) Collection {
	name := Segment(cmp.Or(strings.TrimSpace(c.CollectionName), strings.TrimSpace(c.Name), "Thunder Client"))
	out := Collection{Name: name}
	folders := map[string]thunderFolder{}
	for _, folder := range c.Folders {
		folders[folder.ID] = folder
	}
	pathOf := func(container string) string {
		var parts []string
		seen := map[string]bool{}
		for container != "" && !seen[container] {
			seen[container] = true
			folder, ok := folders[container]
			if !ok {
				break
			}
			parts = append([]string{Segment(folder.Name)}, parts...)
			container = folder.ContainerID
		}
		return name + "/" + strings.Join(parts, "/")
	}
	paths := map[string]bool{}
	for _, folder := range c.Folders {
		path := strings.TrimSuffix(pathOf(folder.ID), "/")
		if path == name || paths[path] {
			continue
		}
		paths[path] = true
		out.Folders = append(out.Folders, request.Folder{Path: path})
	}
	if len(c.Data) > 0 {
		var vars []request.Variable
		for _, v := range c.Data {
			if v.Name != "" {
				vars = append(vars, request.Variable{Name: v.Name, Initial: v.Value})
			}
		}
		out.Folders = append(out.Folders, request.Folder{Path: name, Variables: vars})
	}
	var warnings []string
	for _, tr := range c.Requests {
		collection := pathOf(tr.ContainerID)
		r := request.Request{ID: tr.ID, Name: cmp.Or(strings.TrimSpace(tr.Name), "Thunder request"), Collection: collection,
			Method: strings.ToUpper(cmp.Or(strings.TrimSpace(tr.Method), "GET")), URL: strings.TrimSpace(tr.URL)}
		for _, h := range tr.Headers {
			if h.Name != "" {
				r.HeaderList = append(r.HeaderList, request.Field{Name: h.Name, Value: h.Value, Disabled: h.IsDisabled})
			}
		}
		var query []string
		for _, p := range tr.Params {
			if p.Name == "" || p.IsDisabled {
				continue
			}
			query = append(query, url.QueryEscape(p.Name)+"="+url.QueryEscape(p.Value))
		}
		if len(query) > 0 {
			sep := "?"
			if strings.Contains(r.URL, "?") {
				sep = "&"
			}
			r.URL += sep + strings.Join(query, "&")
		}
		if tr.Body != nil {
			switch strings.ToLower(tr.Body.Type) {
			case "", "none":
			case "json", "text", "xml":
				r.BodyMode, r.Body = "Raw", tr.Body.Raw
				if !hasHeader(r.HeaderList, "Content-Type") {
					switch strings.ToLower(tr.Body.Type) {
					case "json":
						SetHeader(&r, "Content-Type", "application/json")
					case "xml":
						SetHeader(&r, "Content-Type", "application/xml")
					default:
						SetHeader(&r, "Content-Type", "text/plain")
					}
				}
			case "formencoded", "formdata":
				var fields []request.Field
				for _, f := range tr.Body.Form {
					if f.Name != "" {
						fields = append(fields, request.Field{Name: f.Name, Value: f.Value, Disabled: f.IsDisabled})
					}
				}
				SetFormBody(&r, strings.EqualFold(tr.Body.Type, "formdata"), fields)
			case "graphql":
				r.BodyMode = "GraphQL"
				if tr.Body.GraphQL != nil {
					r.GraphQL = request.GraphQLBody{Query: tr.Body.GraphQL.Query, Variables: tr.Body.GraphQL.Variables}
				}
			case "binary":
				warnings = append(warnings, "a binary body was not imported")
			default:
				warnings = append(warnings, "a "+tr.Body.Type+" body was not imported")
			}
		}
		FinishHeaders(&r)
		warnings = append(warnings, thunderAuth(&r, tr.Auth)...)
		r.Pre = strings.Join(tr.PreScripts, "\n")
		r.Post = strings.Join(tr.PostScripts, "\n")
		for _, test := range tr.Tests {
			switch strings.ToLower(test.Type) {
			case "res-code", "status":
				if test.Action == "equal" && test.Value != "" {
					r.Checks.Status = test.Value
				}
			case "set-env-var", "set-env":
				warnings = append(warnings, "Thunder Client environment-setting tests were not imported")
			}
		}
		out.Requests = append(out.Requests, r)
	}
	slices.SortStableFunc(out.Requests, func(a, b request.Request) int {
		return cmp.Compare(a.Collection+"\x00"+a.Name, b.Collection+"\x00"+b.Name)
	})
	out.Warnings = warnings
	return out
}

func hasHeader(rows []request.Field, name string) bool {
	return slices.ContainsFunc(rows, func(f request.Field) bool {
		return !f.Disabled && strings.EqualFold(strings.TrimSpace(f.Name), name)
	})
}

// thunderAuth maps Thunder's auth object. Thunder uses flat fields for basic
// and bearer and nested objects for the rest; both shapes are accepted.
func thunderAuth(r *request.Request, raw jsontext.Value) []string {
	if raw.Kind() != '{' {
		return nil
	}
	var m map[string]jsontext.Value
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	var warnings []string
	kind := strings.ToLower(textValue(m["type"]))
	field := func(names ...string) string {
		for _, name := range names {
			if v, ok := m[name]; ok {
				if s := textValue(v); s != "" {
					return s
				}
			}
		}
		return ""
	}
	switch kind {
	case "", "none", "inherit":
	case "bearer":
		r.Auth = request.Auth{Type: "Bearer", Token: field("bearer", "token")}
	case "basic":
		r.Auth = request.Auth{Type: "Basic", Username: field("username", "user"), Password: field("password")}
	case "apikey", "api-key":
		location := "Header"
		if strings.EqualFold(field("placement", "in"), "query") {
			location = "Query"
		}
		r.Auth = request.Auth{Type: "API key", Key: field("key", "name"), Value: field("value"), Location: location}
	case "digest":
		r.Auth = request.Auth{Type: request.AuthDigest, Username: field("username"), Password: field("password")}
	case "oauth2":
		r.Auth = request.Auth{Type: request.AuthOAuth2, OAuth2: request.OAuth2{
			Grant: request.GrantAuthorizationCodePKCE, AuthURL: field("authUrl", "authorizationUrl"),
			TokenURL: field("accessTokenUrl", "tokenUrl"), ClientID: field("clientId"), ClientSecret: field("clientSecret"),
			Scope: field("scope"), Username: field("username"), Password: field("password"),
		}}
	case "awsv4":
		r.Auth = request.Auth{Type: request.AuthAWS, AWS: request.AWSAuth{
			AccessKey: field("accessKey"), SecretKey: field("secretKey"), Region: field("region"),
			Service: field("service"), SessionToken: field("sessionToken"),
		}}
	case "ntlm":
		r.Auth = request.Auth{Type: request.AuthNTLM, NTLM: request.NTLMAuth{
			Username: field("username"), Password: field("password"), Domain: field("domain"), Workstation: field("workstation"),
		}}
	default:
		warnings = append(warnings, "Thunder Client "+kind+" auth was imported as no auth")
	}
	return warnings
}

// textValue reads a JSON string, or a scalar's compact text.
func textValue(v jsontext.Value) string {
	if v.Kind() == '"' {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	return strings.TrimSpace(string(v))
}
