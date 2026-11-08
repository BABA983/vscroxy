package imports

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/yamlguard"
)

// ReadInsomnia reads an Insomnia v4 JSON export or a v5 YAML/JSON export.
// v4 is a flat resources array linked by parentId; v5 is a nested collection
// tree. Both use the same body and authentication models.
func ReadInsomnia(data []byte) (Result, error) {
	if len(data) > 16<<20 {
		return Result{}, fmt.Errorf("Insomnia exports are limited to 16 MiB")
	}
	jsonData := data
	if !jsontext.Value(data).IsValid() {
		var err error
		jsonData, err = yamlguard.ToJSON(data, yamlguard.Options{})
		if err != nil {
			return Result{}, fmt.Errorf("read Insomnia export: %w", err)
		}
	}
	var file insomniaFile
	if err := json.Unmarshal(jsonData, &file); err != nil {
		return Result{}, fmt.Errorf("read Insomnia export: %w", err)
	}
	if file.ExportFormat == 4 && len(file.Resources) > 0 {
		return readInsomniaV4(file)
	}
	if strings.HasPrefix(file.Type, "collection.insomnia.rest/") || strings.HasPrefix(file.Type, "spec.insomnia.rest/") ||
		strings.HasPrefix(file.Type, "environment.insomnia.rest/") {
		return readInsomniaV5(file)
	}
	return Result{}, fmt.Errorf("not an Insomnia v4 or v5 export")
}

type insomniaFile struct {
	Type         string              `json:"type"`
	Name         string              `json:"name"`
	Meta         insomniaMeta        `json:"meta"`
	Collection   []insomniaItem      `json:"collection"`
	Environments insomniaEnvironment `json:"environments"`
	Resources    []insomniaResource  `json:"resources"`
	ExportFormat int                 `json:"__export_format"`
	CookieJar    jsontext.Value      `json:"cookieJar"`
	Spec         jsontext.Value      `json:"spec"`
	TestSuites   jsontext.Value      `json:"testSuites"`
	Certificates jsontext.Value      `json:"certificates"`
}

type insomniaMeta struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	SortKey     float64 `json:"sortKey"`
}

type insomniaItem struct {
	URL                 string           `json:"url"`
	Name                string           `json:"name"`
	Method              string           `json:"method"`
	Meta                insomniaMeta     `json:"meta"`
	Body                *insomniaBody    `json:"body"`
	Parameters          []insomniaPair   `json:"parameters"`
	Headers             []insomniaPair   `json:"headers"`
	Authentication      map[string]any   `json:"authentication"`
	Scripts             insomniaScripts  `json:"scripts"`
	PreRequestScript    string           `json:"preRequestScript"`
	AfterResponseScript string           `json:"afterResponseScript"`
	Settings            insomniaSettings `json:"settings"`
	PathParameters      []insomniaPair   `json:"pathParameters"`
	Environment         map[string]any   `json:"environment"`
	Children            *[]insomniaItem  `json:"children"`
	Disabled            bool             `json:"disabled"`
}

type insomniaScripts struct {
	PreRequest    string `json:"preRequest"`
	AfterResponse string `json:"afterResponse"`
}

type insomniaSettings struct {
	FollowRedirects          string `json:"followRedirects"`
	RenderRequestBody        *bool  `json:"renderRequestBody"`
	EncodeURL                *bool  `json:"encodeUrl"`
	RebuildPath              *bool  `json:"rebuildPath"`
	StoreCookies             *bool  `json:"storeCookies"`
	SendCookies              *bool  `json:"sendCookies"`
	DisableRenderRequestBody *bool  `json:"disableRenderRequestBody"`
	Cookies                  struct {
		Send  *bool `json:"send"`
		Store *bool `json:"store"`
	} `json:"cookies"`
}

type insomniaBody struct {
	MimeType string         `json:"mimeType"`
	Text     string         `json:"text"`
	FileName string         `json:"fileName"`
	Params   []insomniaPair `json:"params"`
}

type insomniaPair struct {
	Name     string `json:"name"`
	Value    any    `json:"value"`
	Disabled bool   `json:"disabled"`
	FileName string `json:"fileName"`
	Type     string `json:"type"`
}

type insomniaEnvironment struct {
	Name             string                `json:"name"`
	Meta             insomniaMeta          `json:"meta"`
	Data             map[string]any        `json:"data"`
	SubEnvironments  []insomniaEnvironment `json:"subEnvironments"`
	KVPairData       []insomniaKVPair      `json:"kvPairData"`
	EnvironmentType  string                `json:"environmentType"`
	ConfidentialKeys []string              `json:"confidentialKeys"`
}

type insomniaKVPair struct {
	Name           string `json:"name"`
	Value          any    `json:"value"`
	Type           string `json:"type"`
	Enabled        bool   `json:"enabled"`
	IsConfidential bool   `json:"isConfidential"`
}

type insomniaResource struct {
	ID                  string         `json:"_id"`
	Type                string         `json:"_type"`
	ParentID            string         `json:"parentId"`
	Name                string         `json:"name"`
	Description         string         `json:"description"`
	URL                 string         `json:"url"`
	Method              string         `json:"method"`
	Body                *insomniaBody  `json:"body"`
	Parameters          []insomniaPair `json:"parameters"`
	PathParameters      []insomniaPair `json:"pathParameters"`
	Headers             []insomniaPair `json:"headers"`
	Authentication      map[string]any `json:"authentication"`
	PreRequestScript    string         `json:"preRequestScript"`
	AfterResponseScript string         `json:"afterResponseScript"`
	Environment         map[string]any `json:"environment"`
	Data                map[string]any `json:"data"`
	MetaSortKey         float64        `json:"metaSortKey"`
	Scope               string         `json:"scope"`
	FileName            string         `json:"fileName"`
	Contents            string         `json:"contents"`
	IsPrivate           bool           `json:"isPrivate"`
	Disabled            bool           `json:"disabled"`

	SettingFollowRedirects          string `json:"settingFollowRedirects"`
	SettingStoreCookies             *bool  `json:"settingStoreCookies"`
	SettingSendCookies              *bool  `json:"settingSendCookies"`
	SettingDisableRenderRequestBody *bool  `json:"settingDisableRenderRequestBody"`
	SettingEncodeURL                *bool  `json:"settingEncodeUrl"`
	SettingRebuildPath              *bool  `json:"settingRebuildPath"`
}

func readInsomniaV4(file insomniaFile) (Result, error) {
	byID := map[string]*insomniaResource{}
	var resources []*insomniaResource
	for i := range file.Resources {
		resource := &file.Resources[i]
		if resource.ID != "" {
			byID[resource.ID] = resource
		}
		resources = append(resources, resource)
	}
	sort.SliceStable(resources, func(i, j int) bool { return resources[i].MetaSortKey < resources[j].MetaSortKey })
	var requestSkips, folderSkips Tally
	var unsupported int
	workspaces := map[string]string{}
	var workspaceOrder []string
	for _, resource := range resources {
		if resource.Type == "workspace" {
			name := Segment(cmp.Or(resource.Name, "Insomnia"))
			workspaces[resource.ID] = name
			workspaceOrder = append(workspaceOrder, resource.ID)
		}
	}
	if len(workspaces) == 0 {
		name := cmp.Or(Segment(file.Name), "Insomnia")
		workspaces[""] = name
		workspaceOrder = append(workspaceOrder, "")
	}
	collections := map[string]*Collection{}
	for _, id := range workspaceOrder {
		name := workspaces[id]
		collections[name] = &Collection{Name: name}
	}
	pathOf := func(resource *insomniaResource) string {
		var parts []string
		seen := map[string]bool{}
		for current := resource; current != nil; {
			if current.Type == "workspace" {
				parts = append(parts, workspaces[current.ID])
				break
			}
			if current.Type == "request_group" {
				parts = append(parts, Segment(cmp.Or(current.Name, "Untitled folder")))
			}
			if current.ParentID == "" || seen[current.ParentID] {
				break
			}
			seen[current.ParentID] = true
			current = byID[current.ParentID]
		}
		slices.Reverse(parts)
		if len(parts) == 0 {
			return workspaces[""]
		}
		return strings.Join(parts, "/")
	}
	for _, resource := range resources {
		if resource.Type == "workspace" && resource.Description != "" {
			name := workspaces[resource.ID]
			collections[name].Folders = append(collections[name].Folders, request.Folder{Path: name, Description: resource.Description})
		}
	}
	for _, resource := range resources {
		if resource.Type != "request_group" {
			continue
		}
		path := pathOf(resource)
		folder := request.Folder{Path: path, Description: resource.Description, Variables: fieldsFromMap(resource.Environment)}
		folder.Auth = authFromInsomnia(resource.Authentication, &folderSkips)
		if resource.PreRequestScript != "" || resource.AfterResponseScript != "" {
			folderSkips.Add("with a script that uses the Insomnia API; it was not imported")
		}
		if len(resource.Headers) > 0 {
			folderSkips.Add("has headers, which were dropped")
		}
		if folder.Description != "" || folder.Auth.Type != "" || len(folder.Variables) > 0 {
			if collection := collections[collectionRoot(path)]; collection != nil {
				collection.Folders = append(collection.Folders, folder)
			}
		}
	}
	for _, resource := range resources {
		switch resource.Type {
		case "workspace", "request_group", "environment", "cookie_jar", "api_spec", "unit_test_suite", "unit_test", "proto_file", "proto_directory":
			continue
		case "request":
		default:
			unsupported++
			continue
		}
		path := pathOf(resource)
		collection := collections[collectionRoot(path)]
		if collection == nil || resource.Disabled {
			continue
		}
		out := request.Request{
			ID:         StableID("insomnia", resource.ID, collection.Name),
			Name:       cmp.Or(strings.TrimSpace(resource.Name), "Untitled request"),
			Collection: path,
			Method:     strings.ToUpper(cmp.Or(strings.TrimSpace(resource.Method), "GET")),
			URL:        insomniaURL(resource.URL, resource.PathParameters, resource.Parameters),
		}
		out.HeaderList = pairFields(resource.Headers)
		out.Auth = authFromInsomnia(resource.Authentication, &requestSkips)
		if out.Auth.Type == "" {
			out.Auth.Type = request.AuthInherit
		}
		insomniaBodyRequest(&out, resource.Body, &requestSkips)
		if resource.PreRequestScript != "" || resource.AfterResponseScript != "" {
			requestSkips.Add("with a script that uses the Insomnia API; it was not imported")
		}
		if resource.Description != "" {
			requestSkips.Add("with a description that was dropped")
		}
		if insomniaSettingsDiffer(resource) {
			requestSkips.Add("with request settings that were not imported")
		}
		out.Settings.FollowRedirects = insomniaRedirects(insomniaSettings{FollowRedirects: resource.SettingFollowRedirects})
		if out.URL == "" {
			requestSkips.Add("with no URL; skipped")
			continue
		}
		FinishHeaders(&out)
		collection.Requests = append(collection.Requests, out)
	}
	var requests int
	for _, id := range workspaceOrder {
		requests += len(collections[workspaces[id]].Requests)
	}
	if requests == 0 {
		return Result{}, fmt.Errorf("the export has no HTTP requests")
	}
	warnings := append(requestSkips.Lines(), folderSkips.LinesOf("folder", "folders")...)
	if unsupported > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s of other types (API specs, cookies, tests, gRPC/WebSocket requests) were skipped", unsupported, plural(unsupported, "resource", "resources")))
	}
	tagged := false
	for _, collection := range collections {
		tagged = tagged || hasInsomniaTags(collection.Requests, collection.Folders)
	}
	if tagged {
		warnings = append(warnings, "Insomnia template tags ({% ... %}) were imported literally")
	}
	var result Result
	for _, id := range workspaceOrder {
		collection := collections[workspaces[id]]
		collection.Warnings = slices.Clone(warnings)
		if len(collection.Requests) > 0 || len(collection.Folders) > 0 {
			result.Collections = append(result.Collections, *collection)
		}
	}
	if hasCookieJar(file.Resources) {
		result.Warnings = append(result.Warnings, "cookie jars were not imported; cookies are kept in memory only")
	}
	result.Environments = insomniaV4Environments(file.Resources)
	if environmentsTagged(result.Environments) {
		result.Warnings = append(result.Warnings, "Insomnia template tags ({% ... %}) in environment variables were imported literally")
	}
	return result, nil
}

func readInsomniaV5(file insomniaFile) (Result, error) {
	var result Result
	var requestSkips, folderSkips Tally
	if strings.HasPrefix(file.Type, "environment.insomnia.rest/") {
		result.Environments, _ = insomniaV5Environments(file.Environments)
		if len(result.Environments) == 0 {
			return Result{}, fmt.Errorf("the environment export has no environments")
		}
		if environmentsTagged(result.Environments) {
			result.Warnings = append(result.Warnings, "Insomnia template tags ({% ... %}) in environment variables were imported literally")
		}
		return result, nil
	}
	name := Segment(cmp.Or(file.Name, "Insomnia"))
	collection := Collection{Name: name}
	if file.Meta.Description != "" {
		collection.Folders = append(collection.Folders, request.Folder{Path: name, Description: file.Meta.Description})
	}
	var walk func(items []insomniaItem, parent string)
	walk = func(items []insomniaItem, parent string) {
		for _, item := range items {
			folder := item.Children != nil || item.Method == "" && item.URL == "" && item.Body == nil
			if folder {
				path := parent + "/" + Segment(cmp.Or(item.Name, "Untitled folder"))
				settings := request.Folder{Path: path, Description: item.Meta.Description, Variables: fieldsFromMap(item.Environment)}
				settings.Auth = authFromInsomnia(item.Authentication, &folderSkips)
				if item.Scripts.PreRequest != "" || item.Scripts.AfterResponse != "" {
					folderSkips.Add("with a script that uses the Insomnia API; it was not imported")
				}
				if len(item.Headers) > 0 {
					folderSkips.Add("has headers, which were dropped")
				}
				if settings.Description != "" || settings.Auth.Type != "" || len(settings.Variables) > 0 {
					collection.Folders = append(collection.Folders, settings)
				}
				if item.Children != nil {
					walk(*item.Children, path)
				}
				continue
			}
			if item.Method == "" || item.URL == "" {
				requestSkips.Add("with no HTTP method; skipped")
				continue
			}
			if item.Disabled {
				requestSkips.Add("disabled; skipped")
				continue
			}
			out := request.Request{
				ID:         StableID("insomnia", item.Meta.ID, name, parent),
				Name:       cmp.Or(strings.TrimSpace(item.Name), "Untitled request"),
				Collection: parent,
				Method:     strings.ToUpper(cmp.Or(strings.TrimSpace(item.Method), "GET")),
				URL:        insomniaURL(item.URL, item.PathParameters, item.Parameters),
			}
			out.HeaderList = pairFields(item.Headers)
			out.Auth = authFromInsomnia(item.Authentication, &requestSkips)
			if out.Auth.Type == "" {
				out.Auth.Type = request.AuthInherit
			}
			insomniaBodyRequest(&out, item.Body, &requestSkips)
			if item.Scripts.PreRequest != "" || item.Scripts.AfterResponse != "" || item.PreRequestScript != "" || item.AfterResponseScript != "" {
				requestSkips.Add("with a script that uses the Insomnia API; it was not imported")
			}
			if item.Meta.Description != "" {
				requestSkips.Add("with a description that was dropped")
			}
			if insomniaSettingsDifferV5(item.Settings) {
				requestSkips.Add("with request settings that were not imported")
			}
			out.Settings.FollowRedirects = insomniaRedirects(item.Settings)
			if out.URL == "" {
				requestSkips.Add("with no URL; skipped")
				continue
			}
			FinishHeaders(&out)
			collection.Requests = append(collection.Requests, out)
		}
	}
	walk(file.Collection, name)
	if len(collection.Requests) == 0 {
		return Result{}, fmt.Errorf("the export has no HTTP requests")
	}
	collection.Warnings = append(requestSkips.Lines(), folderSkips.LinesOf("folder", "folders")...)
	if len(file.CookieJar) > 0 && string(file.CookieJar) != "null" {
		collection.Warnings = append(collection.Warnings, "the cookie jar was not imported; cookies are kept in memory only")
	}
	if len(file.Spec) > 0 && string(file.Spec) != "null" {
		collection.Warnings = append(collection.Warnings, "the embedded API spec was not imported")
	}
	if len(file.TestSuites) > 0 && string(file.TestSuites) != "null" {
		collection.Warnings = append(collection.Warnings, "test suites were not imported")
	}
	if len(file.Certificates) > 0 && string(file.Certificates) != "null" {
		collection.Warnings = append(collection.Warnings, "client certificates were not imported")
	}
	if hasInsomniaTags(collection.Requests, collection.Folders) {
		collection.Warnings = append(collection.Warnings, "Insomnia template tags ({% ... %}) were imported literally")
	}
	result.Collections = []Collection{collection}
	result.Environments, _ = insomniaV5Environments(file.Environments)
	if environmentsTagged(result.Environments) {
		result.Warnings = append(result.Warnings, "Insomnia template tags ({% ... %}) in environment variables were imported literally")
	}
	return result, nil
}

func insomniaURL(raw string, pathParams, queryParams []insomniaPair) string {
	raw = normalizeTags(raw)
	values := map[string]string{}
	for _, param := range pathParams {
		if param.Name != "" {
			values[param.Name] = valueText(param.Value)
		}
	}
	raw = pathVariables(raw, values)
	var params []requestField
	for _, param := range queryParams {
		params = append(params, requestField{Name: param.Name, Value: valueText(param.Value), Disabled: param.Disabled})
	}
	return appendQuery(raw, params)
}

func pairFields(pairs []insomniaPair) []request.Field {
	var fields []request.Field
	for _, pair := range pairs {
		fields = append(fields, request.Field{Name: pair.Name, Value: valueText(pair.Value), Disabled: pair.Disabled})
	}
	return fields
}

func fieldsFromMap(values map[string]any) []request.Variable {
	var vars []request.Variable
	for _, name := range sortedKeys(values) {
		vars = append(vars, request.Variable{Name: name, Initial: valueText(values[name])})
	}
	return vars
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func insomniaBodyRequest(out *request.Request, body *insomniaBody, skips *Tally) {
	if body == nil || body.MimeType == "" {
		return
	}
	switch body.MimeType {
	case "application/json", "text/plain", "application/xml", "text/yaml", "application/edn":
		SetRawBody(out, body.MimeType, normalizeTags(body.Text))
	case "application/graphql":
		text := normalizeTags(body.Text)
		var graphql struct {
			Query         string `json:"query"`
			Variables     any    `json:"variables"`
			OperationName string `json:"operationName"`
		}
		if json.Unmarshal([]byte(text), &graphql) == nil && graphql.Query != "" {
			variables := ""
			if graphql.Variables != nil {
				if data, err := json.Marshal(graphql.Variables); err == nil {
					variables = string(data)
				}
			}
			SetGraphQLBody(out, graphql.Query, variables, graphql.OperationName)
		} else {
			SetRawBody(out, body.MimeType, text)
			skips.Add("with a GraphQL body that is not a query/variables object; imported as raw text")
		}
	case "application/x-www-form-urlencoded":
		SetFormBody(out, false, pairFields(body.Params))
	case "multipart/form-data":
		var fields []request.Field
		for _, param := range body.Params {
			field := request.Field{Name: param.Name, Value: valueText(param.Value), Disabled: param.Disabled}
			if param.Type == "file" {
				field.File, field.Value = true, cmp.Or(param.FileName, valueText(param.Value))
				if field.Value == "" {
					skips.Add("with a multipart file field with no local path; choose files in the request")
				}
			}
			fields = append(fields, field)
		}
		SetFormBody(out, true, fields)
	case "application/octet-stream":
		out.BodyMode, out.BodyFile = "Binary", body.FileName
		if body.FileName == "" {
			skips.Add("with a binary body and no local file; imported without a body")
		}
	default:
		SetRawBody(out, body.MimeType, normalizeTags(body.Text))
		skips.Add("with a " + body.MimeType + " body; imported as raw text")
	}
}

// hasInsomniaTags says whether any imported string still contains an
// Insomnia template tag, which this app cannot evaluate.
func hasInsomniaTags(requests []request.Request, folders []request.Folder) bool {
	for _, r := range requests {
		if insomniaRequestTagged(r) {
			return true
		}
	}
	for _, f := range folders {
		if insomniaFolderTagged(f) {
			return true
		}
	}
	return false
}

func insomniaRequestTagged(r request.Request) bool {
	if anyInsomniaTag(r.URL, r.Body, r.Headers, r.BodyFile, r.GraphQL.Query, r.GraphQL.Variables,
		r.Name, r.Collection, r.Pre, r.Post, r.Checks.Status, r.Checks.Headers, r.Checks.JSON) {
		return true
	}
	for _, row := range r.HeaderList {
		if anyInsomniaTag(row.Name, row.Value) {
			return true
		}
	}
	for _, row := range r.Form {
		if anyInsomniaTag(row.Name, row.Value) {
			return true
		}
	}
	return insomniaAuthTagged(r.Auth)
}

func insomniaFolderTagged(f request.Folder) bool {
	if anyInsomniaTag(f.Path, f.Description, f.Pre, f.Post) || insomniaAuthTagged(f.Auth) {
		return true
	}
	for _, row := range f.Variables {
		if anyInsomniaTag(row.Name, row.Initial, row.Current) {
			return true
		}
	}
	return false
}

func insomniaAuthTagged(a request.Auth) bool {
	return anyInsomniaTag(
		a.Type, a.Token, a.Username, a.Password, a.Key, a.Value, a.Location,
		a.OAuth2.Grant, a.OAuth2.AuthURL, a.OAuth2.TokenURL, a.OAuth2.ClientID,
		a.OAuth2.ClientSecret, a.OAuth2.Scope, a.OAuth2.Username, a.OAuth2.Password,
		a.OAuth2.RedirectURL, a.OAuth2.ClientAuth, a.OAuth2.HeaderPrefix,
		a.AWS.AccessKey, a.AWS.SecretKey, a.AWS.Region, a.AWS.Service, a.AWS.SessionToken,
		a.JWT.Algorithm, a.JWT.Secret, a.JWT.PrivateKey, a.JWT.Payload, a.JWT.Header,
		a.JWT.Prefix, a.JWT.AddTo, a.JWT.QueryParam,
		a.OAuth1.ConsumerKey, a.OAuth1.ConsumerSecret, a.OAuth1.Token, a.OAuth1.TokenSecret,
		a.OAuth1.PrivateKey, a.OAuth1.SignatureMethod, a.OAuth1.Timestamp, a.OAuth1.Nonce,
		a.OAuth1.Version, a.OAuth1.Realm, a.OAuth1.AddTo, a.OAuth1.Callback, a.OAuth1.Verifier,
		a.Hawk.AuthID, a.Hawk.AuthKey, a.Hawk.Algorithm, a.Hawk.User, a.Hawk.Nonce,
		a.Hawk.Timestamp, a.Hawk.ExtraData, a.Hawk.App, a.Hawk.Delegation,
		a.EdgeGrid.AccessToken, a.EdgeGrid.ClientToken, a.EdgeGrid.ClientSecret,
		a.EdgeGrid.BaseURL, a.EdgeGrid.Nonce, a.EdgeGrid.Timestamp, a.EdgeGrid.HeadersToSign,
	)
}

func environmentsTagged(envs []Environment) bool {
	for _, env := range envs {
		if anyInsomniaTag(env.Name) {
			return true
		}
		for _, value := range env.Values {
			if anyInsomniaTag(value) {
				return true
			}
		}
	}
	return false
}

func anyInsomniaTag(values ...string) bool {
	for _, value := range values {
		if strings.Contains(value, "{%") {
			return true
		}
	}
	return false
}

func insomniaSettingsDiffer(resource *insomniaResource) bool {
	return resource.SettingStoreCookies != nil && !*resource.SettingStoreCookies ||
		resource.SettingSendCookies != nil && !*resource.SettingSendCookies ||
		resource.SettingDisableRenderRequestBody != nil && *resource.SettingDisableRenderRequestBody ||
		resource.SettingEncodeURL != nil && !*resource.SettingEncodeURL ||
		resource.SettingRebuildPath != nil && !*resource.SettingRebuildPath
}

func insomniaSettingsDifferV5(settings insomniaSettings) bool {
	return settings.StoreCookies != nil && !*settings.StoreCookies ||
		settings.SendCookies != nil && !*settings.SendCookies ||
		settings.Cookies.Send != nil && !*settings.Cookies.Send ||
		settings.Cookies.Store != nil && !*settings.Cookies.Store ||
		settings.DisableRenderRequestBody != nil && *settings.DisableRenderRequestBody ||
		settings.EncodeURL != nil && !*settings.EncodeURL ||
		settings.RebuildPath != nil && !*settings.RebuildPath ||
		settings.RenderRequestBody != nil && !*settings.RenderRequestBody
}

func insomniaRedirects(settings insomniaSettings) *bool {
	if strings.EqualFold(settings.FollowRedirects, "off") {
		follow := false
		return &follow
	}
	return nil
}

// authFromInsomnia maps the shared v4/v5 authentication object.
func authFromInsomnia(auth map[string]any, skips *Tally) request.Auth {
	if len(auth) == 0 {
		return request.Auth{}
	}
	kind := strings.ToLower(valueText(auth["type"]))
	switch kind {
	case "", "none":
		return AuthNone()
	case "basic":
		return request.Auth{Type: "Basic", Username: valueText(auth["username"]), Password: valueText(auth["password"])}
	case "bearer":
		token, prefix := valueText(auth["token"]), strings.TrimSpace(valueText(auth["prefix"]))
		switch {
		case strings.EqualFold(prefix, "NO_PREFIX"):
			skips.Add("with NO_PREFIX bearer auth; imported as an Authorization API key")
			return request.Auth{Type: "API key", Key: "Authorization", Value: token, Location: "Header"}
		case prefix != "" && !strings.EqualFold(prefix, "bearer"):
			skips.Add("with a custom bearer prefix; imported as an Authorization API key")
			return request.Auth{Type: "API key", Key: "Authorization", Value: prefix + " " + token, Location: "Header"}
		default:
			return request.Auth{Type: "Bearer", Token: token}
		}
	case "apikey":
		key, value := valueText(auth["key"]), valueText(auth["value"])
		switch strings.ToLower(valueText(auth["addTo"])) {
		case "query", "queryparams":
			return request.Auth{Type: "API key", Key: key, Value: value, Location: "Query"}
		case "cookie":
			skips.Add("with a cookie API key; imported as a Cookie header")
			return request.Auth{Type: "API key", Key: "Cookie", Value: key + "=" + value, Location: "Header"}
		default:
			return request.Auth{Type: "API key", Key: key, Value: value, Location: "Header"}
		}
	case "digest":
		return request.Auth{Type: request.AuthDigest, Username: valueText(auth["username"]), Password: valueText(auth["password"])}
	case "oauth2":
		return insomniaOAuth2(auth, skips)
	case "iam":
		return request.Auth{Type: request.AuthAWS, AWS: request.AWSAuth{
			AccessKey: valueText(auth["accessKeyId"]), SecretKey: valueText(auth["secretAccessKey"]),
			SessionToken: valueText(auth["sessionToken"]), Region: valueText(auth["region"]), Service: valueText(auth["service"]),
		}}
	case "singleToken":
		return request.Auth{Type: "Bearer", Token: valueText(auth["token"])}
	default:
		skips.Add("with " + kind + " auth; imported as no auth")
		return AuthNone()
	}
}

func insomniaOAuth2(auth map[string]any, skips *Tally) request.Auth {
	grant := strings.ToLower(valueText(auth["grantType"]))
	o := request.OAuth2{
		AuthURL: valueText(auth["authorizationUrl"]), TokenURL: valueText(auth["accessTokenUrl"]),
		ClientID: valueText(auth["clientId"]), ClientSecret: valueText(auth["clientSecret"]),
		Scope: valueText(auth["scope"]), Username: valueText(auth["username"]), Password: valueText(auth["password"]),
		HeaderPrefix: valueText(auth["tokenPrefix"]),
	}
	switch grant {
	case "", "authorization_code":
		if valueBool(auth["usePkce"]) {
			o.Grant = request.GrantAuthorizationCodePKCE
		} else {
			o.Grant = request.GrantAuthorizationCode
		}
	case "client_credentials":
		o.Grant = request.GrantClientCredentials
	case "password":
		o.Grant = request.GrantPassword
	default:
		skips.Add("with the OAuth 2.0 " + strings.ReplaceAll(grant, "_", " ") + " grant; imported as no auth")
		return AuthNone()
	}
	if valueBool(auth["credentialsInBody"]) {
		o.ClientAuth = "Body"
	}
	if redirect := valueText(auth["redirectUrl"]); redirect != "" && (o.Grant == request.GrantAuthorizationCodePKCE || o.Grant == request.GrantAuthorizationCode) {
		if loopback(redirect) {
			o.RedirectURL = redirect
		} else {
			skips.Add("with an OAuth 2.0 callback that is not a loopback address; vscroxy uses its own loopback callback")
		}
	}
	return request.Auth{Type: request.AuthOAuth2, OAuth2: o}
}

func loopback(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}

func insomniaV4Environments(resources []insomniaResource) []Environment {
	byID := map[string]*insomniaResource{}
	for i := range resources {
		if resources[i].ID != "" {
			byID[resources[i].ID] = &resources[i]
		}
	}
	var out []Environment
	for _, resource := range resources {
		if resource.Type != "environment" {
			continue
		}
		values := map[string]any{}
		if parent := byID[resource.ParentID]; parent != nil && parent.Type == "environment" {
			for name, value := range parent.Data {
				values[name] = value
			}
		}
		for name, value := range resource.Data {
			values[name] = value
		}
		env := Environment{Name: cmp.Or(strings.TrimSpace(resource.Name), "Base Environment")}
		env.Values = mapStringString(values)
		out = append(out, env)
	}
	return out
}

func insomniaV5Environments(base insomniaEnvironment) ([]Environment, []string) {
	var out []Environment
	var warnings []string
	baseValues, baseCurrent, baseWarning := insomniaEnvironmentValues(base)
	if baseWarning != "" {
		warnings = append(warnings, baseWarning)
	}
	if base.Name != "" || len(baseValues) > 0 || len(baseCurrent) > 0 {
		out = append(out, Environment{Name: cmp.Or(strings.TrimSpace(base.Name), "Base Environment"), Values: baseValues, Current: baseCurrent, Credentials: credentialMap(baseCurrent), Warnings: warnings})
	}
	for _, sub := range base.SubEnvironments {
		values := map[string]string{}
		for name, value := range baseValues {
			values[name] = value
		}
		current := map[string]string{}
		for name, value := range baseCurrent {
			current[name] = value
		}
		subValues, subCurrent, subWarning := insomniaEnvironmentValues(sub)
		for name, value := range subValues {
			values[name] = value
		}
		for name, value := range subCurrent {
			current[name] = value
		}
		if len(current) == 0 {
			current = nil
		}
		subWarnings := slices.Clone(warnings)
		if subWarning != "" {
			subWarnings = append(subWarnings, subWarning)
		}
		out = append(out, Environment{Name: cmp.Or(strings.TrimSpace(sub.Name), "Imported environment"), Values: values, Current: current, Credentials: credentialMap(current), Warnings: subWarnings})
	}
	return out, warnings
}

func insomniaEnvironmentValues(env insomniaEnvironment) (values, current map[string]string, warning string) {
	values = mapStringString(env.Data)
	if len(values) == 0 && len(env.KVPairData) > 0 {
		values = map[string]string{}
		for _, pair := range env.KVPairData {
			if !pair.Enabled {
				continue
			}
			if pair.IsConfidential || strings.EqualFold(pair.Type, "secret") {
				if current == nil {
					current = map[string]string{}
				}
				current[pair.Name] = valueText(pair.Value)
				continue
			}
			values[pair.Name] = valueText(pair.Value)
		}
	}
	for _, name := range env.ConfidentialKeys {
		value, ok := values[name]
		if !ok {
			continue
		}
		if current == nil {
			current = map[string]string{}
		}
		current[name] = value
		delete(values, name)
	}
	if len(current) > 0 {
		return values, current, "confidential variables were imported as local Current values"
	}
	return values, nil, ""
}

func mapStringString(values map[string]any) map[string]string {
	out := make(map[string]string, len(values))
	for name, value := range values {
		out[name] = valueText(value)
	}
	return out
}

func hasCookieJar(resources []insomniaResource) bool {
	for _, resource := range resources {
		if resource.Type == "cookie_jar" {
			return true
		}
	}
	return false
}

func collectionRoot(path string) string {
	root, _, _ := strings.Cut(strings.Trim(path, "/"), "/")
	return root
}
