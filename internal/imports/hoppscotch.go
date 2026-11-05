package imports

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// ReadHoppscotch reads a Hoppscotch collection export: one collection object
// or a workspace array of them. It also accepts the environment export shape
// when the caller knows the file is an environment.
func ReadHoppscotch(data []byte) (Result, error) {
	if len(data) > 16<<20 {
		return Result{}, fmt.Errorf("Hoppscotch exports are limited to 16 MiB")
	}
	var raw jsontext.Value = data
	if !raw.IsValid() {
		return Result{}, fmt.Errorf("not a Hoppscotch JSON export")
	}
	var collections []hoppCollection
	switch raw.Kind() {
	case '[':
		if err := json.Unmarshal(data, &collections); err != nil {
			return Result{}, fmt.Errorf("read Hoppscotch collections: %w", err)
		}
	case '{':
		var one hoppCollection
		if err := json.Unmarshal(data, &one); err != nil {
			return Result{}, fmt.Errorf("read Hoppscotch collection: %w", err)
		}
		collections = []hoppCollection{one}
	default:
		return Result{}, fmt.Errorf("not a Hoppscotch JSON export")
	}
	var result Result
	for _, collection := range collections {
		converted, err := hoppCollectionRequests(collection)
		if err != nil {
			return Result{}, err
		}
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

// ReadHoppscotchEnvironment reads an environment export: one object or an
// array of them.
func ReadHoppscotchEnvironment(data []byte) ([]Environment, error) {
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("Hoppscotch exports are limited to 16 MiB")
	}
	var raw jsontext.Value = data
	if !raw.IsValid() {
		return nil, fmt.Errorf("not a Hoppscotch JSON export")
	}
	var environments []hoppEnvironment
	switch raw.Kind() {
	case '[':
		if err := json.Unmarshal(data, &environments); err != nil {
			return nil, fmt.Errorf("read Hoppscotch environments: %w", err)
		}
	case '{':
		var one hoppEnvironment
		if err := json.Unmarshal(data, &one); err != nil {
			return nil, fmt.Errorf("read Hoppscotch environment: %w", err)
		}
		environments = []hoppEnvironment{one}
	default:
		return nil, fmt.Errorf("not a Hoppscotch JSON export")
	}
	var out []Environment
	for _, environment := range environments {
		values := map[string]string{}
		secret := false
		for _, variable := range environment.Variables {
			if variable.Key == "" {
				continue
			}
			value := cmp.Or(variable.InitialValue, variable.CurrentValue, valueText(variable.Value))
			values[variable.Key] = value
			secret = secret || variable.Secret
		}
		env := Environment{Name: cmp.Or(strings.TrimSpace(environment.Name), "Imported environment"), Values: values}
		if secret {
			env.Warnings = append(env.Warnings, "confidential variables were imported as ordinary local variables")
		}
		out = append(out, env)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the export has no environments")
	}
	return out, nil
}

type hoppCollection struct {
	V                jsontext.Value `json:"v"`
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Folders          []hoppFolder   `json:"folders"`
	Requests         []hoppRequest  `json:"requests"`
	Auth             map[string]any `json:"auth"`
	Headers          []hoppPair     `json:"headers"`
	Variables        []hoppVariable `json:"variables"`
	Description      any            `json:"description"`
	PreRequestScript string         `json:"preRequestScript"`
	TestScript       string         `json:"testScript"`
}

type hoppFolder struct {
	V                jsontext.Value `json:"v"`
	Name             string         `json:"name"`
	Folders          []hoppFolder   `json:"folders"`
	Requests         []hoppRequest  `json:"requests"`
	Auth             map[string]any `json:"auth"`
	Headers          []hoppPair     `json:"headers"`
	Variables        []hoppVariable `json:"variables"`
	Description      any            `json:"description"`
	PreRequestScript string         `json:"preRequestScript"`
	TestScript       string         `json:"testScript"`
}

type hoppRequest struct {
	V                jsontext.Value            `json:"v"`
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Method           string                    `json:"method"`
	Endpoint         string                    `json:"endpoint"`
	URL              string                    `json:"url"`
	Params           []hoppPair                `json:"params"`
	Headers          []hoppPair                `json:"headers"`
	PreRequestScript string                    `json:"preRequestScript"`
	TestScript       string                    `json:"testScript"`
	Auth             map[string]any            `json:"auth"`
	Body             *hoppBody                 `json:"body"`
	RequestVariables []hoppPair                `json:"requestVariables"`
	Responses        map[string]jsontext.Value `json:"responses"`
	Description      any                       `json:"description"`
	Query            string                    `json:"query"`
	Variables        any                       `json:"variables"`
}

type hoppBody struct {
	ContentType any `json:"contentType"`
	Body        any `json:"body"`
}

type hoppPair struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Active      *bool  `json:"active"`
	Description string `json:"description"`
	IsFile      bool   `json:"isFile"`
}

type hoppVariable struct {
	Key          string `json:"key"`
	Value        any    `json:"value"`
	InitialValue string `json:"initialValue"`
	CurrentValue string `json:"currentValue"`
	Secret       bool   `json:"secret"`
}

type hoppEnvironment struct {
	V         jsontext.Value `json:"v"`
	Name      string         `json:"name"`
	Variables []hoppVariable `json:"variables"`
}

func hoppCollectionRequests(collection hoppCollection) (Collection, error) {
	name := Segment(cmp.Or(strings.TrimSpace(collection.Name), "Hoppscotch"))
	out := Collection{Name: name}
	root := request.Folder{Path: name, Description: valueText(collection.Description)}
	root.Variables = hoppVariables(collection.Variables, &out.Warnings)
	root.Auth = hoppAuth(collection.Auth, &out.Warnings)
	if len(collection.Headers) > 0 {
		out.Warnings = append(out.Warnings, "collection headers were dropped")
	}
	if collection.PreRequestScript != "" || collection.TestScript != "" {
		out.Warnings = append(out.Warnings, "collection scripts use the Hoppscotch pw API and were not imported")
	}
	if root.Auth.Type == request.AuthInherit {
		root.Auth = request.Auth{}
	}
	if root.Description != "" || root.Auth.Type != "" || len(root.Variables) > 0 {
		out.Folders = append(out.Folders, root)
	}
	var walk func(folders []hoppFolder, requests []hoppRequest, parent string)
	walk = func(folders []hoppFolder, requests []hoppRequest, parent string) {
		for _, folder := range folders {
			path := parent + "/" + Segment(cmp.Or(folder.Name, "Untitled folder"))
			settings := request.Folder{Path: path, Description: valueText(folder.Description)}
			settings.Variables = hoppVariables(folder.Variables, &out.Warnings)
			settings.Auth = hoppAuth(folder.Auth, &out.Warnings)
			if len(folder.Headers) > 0 {
				out.Warnings = append(out.Warnings, "folder headers were dropped")
			}
			if folder.PreRequestScript != "" || folder.TestScript != "" {
				out.Warnings = append(out.Warnings, "folder scripts use the Hoppscotch pw API and were not imported")
			}
			if settings.Description != "" || settings.Auth.Type != "" || len(settings.Variables) > 0 {
				out.Folders = append(out.Folders, settings)
			}
			walk(folder.Folders, folder.Requests, path)
		}
		for _, request := range requests {
			value := hoppRequestValue(request, parent, len(out.Requests), &out.Warnings)
			out.Requests = append(out.Requests, value)
		}
	}
	walk(collection.Folders, collection.Requests, name)
	out.Warnings = dedupe(out.Warnings)
	return out, nil
}

func hoppRequestValue(input hoppRequest, parent string, index int, warnings *[]string) request.Request {
	out := request.Request{
		ID:         StableID("hoppscotch", input.ID, parent, input.Name, fmt.Sprint(index)),
		Name:       cmp.Or(strings.TrimSpace(input.Name), "Untitled request"),
		Collection: parent,
		Method:     strings.ToUpper(cmp.Or(strings.TrimSpace(input.Method), "GET")),
	}
	if input.ID != "" {
		out.ID = StableID("hoppscotch", input.ID)
	}
	url := normalizeTags(cmp.Or(input.Endpoint, input.URL))
	var params []requestField
	for _, param := range input.Params {
		params = append(params, requestField{Name: normalizeTags(param.Key), Value: valueText(param.Value), Disabled: param.Active != nil && !*param.Active})
	}
	out.URL = appendQuery(url, params)
	for _, header := range input.Headers {
		if header.Active != nil && !*header.Active {
			continue
		}
		out.HeaderList = append(out.HeaderList, request.Field{Name: normalizeTags(header.Key), Value: valueText(header.Value)})
	}
	out.Auth = hoppAuth(input.Auth, warnings)
	if out.Auth.Type == "" {
		out.Auth.Type = request.AuthInherit
	}
	if input.Query != "" {
		out.BodyMode = "GraphQL"
		out.GraphQL.Query = input.Query
		if input.Variables != nil {
			if data, err := json.Marshal(input.Variables); err == nil {
				out.GraphQL.Variables = string(data)
			}
		}
		out.Method = "POST"
	} else {
		hoppBodyRequest(&out, input.Body, warnings)
	}
	if script := VariableScript(hoppPairFields(input.RequestVariables)); script != "" {
		out.Pre = JoinScript(script, out.Pre)
	}
	if valueText(input.Description) != "" {
		*warnings = append(*warnings, "request descriptions were dropped")
	}
	if input.PreRequestScript != "" || input.TestScript != "" {
		*warnings = append(*warnings, "request scripts use the Hoppscotch pw API and were not imported")
	}
	if len(input.Responses) > 0 {
		*warnings = append(*warnings, "saved response examples were not imported")
	}
	FinishHeaders(&out)
	return out
}

func hoppPairFields(pairs []hoppPair) []request.Field {
	var fields []request.Field
	for _, pair := range pairs {
		fields = append(fields, request.Field{Name: pair.Key, Value: valueText(pair.Value), Disabled: pair.Active != nil && !*pair.Active})
	}
	return fields
}

func hoppVariables(variables []hoppVariable, warnings *[]string) []request.Field {
	var fields []request.Field
	for _, variable := range variables {
		if variable.Key == "" {
			continue
		}
		value := cmp.Or(variable.InitialValue, variable.CurrentValue, valueText(variable.Value))
		fields = append(fields, request.Field{Name: variable.Key, Value: value})
		if variable.Secret {
			*warnings = append(*warnings, "confidential variables were imported as ordinary local variables")
		}
	}
	return fields
}

func hoppBodyRequest(out *request.Request, body *hoppBody, warnings *[]string) {
	if body == nil || body.ContentType == nil {
		return
	}
	contentType := valueText(body.ContentType)
	switch contentType {
	case "application/json", "application/ld+json", "application/hal+json", "application/vnd.api+json",
		"application/xml", "text/xml", "text/html", "text/plain", "application/graphql":
		SetRawBody(out, contentType, valueText(body.Body))
	case "application/x-www-form-urlencoded":
		SetFormBody(out, false, hoppBulkForm(valueText(body.Body)))
	case "multipart/form-data":
		var fields []request.Field
		for _, item := range valueList(body.Body) {
			pair := valueMap(item)
			field := request.Field{Name: valueText(pair["key"]), Value: valueText(pair["value"])}
			if active, ok := pair["active"].(bool); ok && !active {
				field.Disabled = true
			}
			if isFile, _ := pair["isFile"].(bool); isFile {
				field.File = true
				field.Value = ""
				*warnings = append(*warnings, "multipart file fields have no local path; choose files in the request")
			}
			fields = append(fields, field)
		}
		SetFormBody(out, true, fields)
	case "application/octet-stream", "binary":
		// A File object serialises to {}; there is no local path to keep.
		out.BodyMode = ""
		*warnings = append(*warnings, "binary bodies have no local file and were imported without a body")
	default:
		SetRawBody(out, contentType, valueText(body.Body))
		*warnings = append(*warnings, "use of the "+contentType+" body was imported as raw text")
	}
}

// hoppBulkForm reads the urlencoded body Hoppscotch shows in its bulk editor:
// one key: value per line, with # marking a disabled row.
func hoppBulkForm(text string) []request.Field {
	var fields []request.Field
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		disabled := strings.HasPrefix(line, "#")
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		}
		fields = append(fields, request.Field{Name: strings.TrimSpace(key), Value: value, Disabled: disabled})
	}
	return fields
}

// hoppAuth maps Hoppscotch's authType objects. Auth it cannot send becomes
// no auth, with a warning.
func hoppAuth(auth map[string]any, warnings *[]string) request.Auth {
	if len(auth) == 0 {
		return request.Auth{}
	}
	kind := strings.ToLower(valueText(auth["authType"]))
	if active, ok := auth["authActive"].(bool); ok && !active && kind != "none" && kind != "inherit" {
		if warnings != nil {
			*warnings = append(*warnings, "disabled auth was imported as no auth")
		}
		return AuthNone()
	}
	switch kind {
	case "", "none":
		return AuthNone()
	case "inherit":
		return request.Auth{Type: request.AuthInherit}
	case "basic":
		return request.Auth{Type: "Basic", Username: valueText(auth["username"]), Password: valueText(auth["password"])}
	case "bearer":
		return request.Auth{Type: "Bearer", Token: valueText(auth["token"])}
	case "api-key":
		location := "Header"
		if strings.EqualFold(valueText(auth["addTo"]), "QUERY_PARAMS") {
			location = "Query"
		}
		return request.Auth{Type: "API key", Key: valueText(auth["key"]), Value: valueText(auth["value"]), Location: location}
	case "digest":
		return request.Auth{Type: request.AuthDigest, Username: valueText(auth["username"]), Password: valueText(auth["password"])}
	case "aws-signature":
		return request.Auth{Type: request.AuthAWS, AWS: request.AWSAuth{
			AccessKey: valueText(auth["accessKey"]), SecretKey: valueText(auth["secretKey"]),
			Region: valueText(auth["region"]), Service: valueText(auth["serviceName"]), SessionToken: valueText(auth["serviceToken"]),
		}}
	case "jwt":
		algorithm := valueText(auth["algorithm"])
		if algorithm != "" && !slices.Contains(request.JWTAlgorithms, algorithm) {
			if warnings != nil {
				*warnings = append(*warnings, "a JWT signed with "+algorithm+" was imported as no auth")
			}
			return AuthNone()
		}
		j := request.JWTAuth{
			Algorithm: algorithm, Secret: valueText(auth["secret"]), SecretBase64: valueBool(auth["isSecretBase64Encoded"]),
			PrivateKey: valueText(auth["privateKey"]), Payload: valueText(auth["payload"]),
			Header: valueText(auth["jwtHeaders"]), Prefix: valueText(auth["headerPrefix"]),
			QueryParam: valueText(auth["paramName"]),
		}
		if strings.EqualFold(valueText(auth["addTo"]), "QUERY_PARAMS") {
			j.AddTo = "Query"
		}
		return request.Auth{Type: request.AuthJWT, JWT: j}
	case "oauth-2":
		return hoppOAuth2(auth, warnings)
	default:
		if warnings != nil {
			*warnings = append(*warnings, "use of "+kind+" auth was imported as no auth")
		}
		return AuthNone()
	}
}

func hoppOAuth2(auth map[string]any, warnings *[]string) request.Auth {
	info := valueMap(auth["grantTypeInfo"])
	o := request.OAuth2{
		AuthURL: valueText(info["authEndpoint"]), TokenURL: valueText(info["tokenEndpoint"]),
		ClientID: valueText(info["clientID"]), ClientSecret: valueText(info["clientSecret"]), Scope: valueText(info["scopes"]),
	}
	switch strings.ToUpper(valueText(info["grantType"])) {
	case "", "AUTHORIZATION_CODE":
		if valueBool(info["isPKCE"]) {
			o.Grant = request.GrantAuthorizationCodePKCE
		} else {
			o.Grant = request.GrantAuthorizationCode
		}
	case "CLIENT_CREDENTIALS":
		o.Grant = request.GrantClientCredentials
	case "PASSWORD":
		o.Grant = request.GrantPassword
	default:
		if warnings != nil {
			*warnings = append(*warnings, "use of the OAuth 2.0 "+strings.ToLower(valueText(info["grantType"]))+" grant was imported as no auth")
		}
		return AuthNone()
	}
	if len(valueList(info["authRequestParams"])) > 0 || len(valueList(info["tokenRequestParams"])) > 0 {
		if warnings != nil {
			*warnings = append(*warnings, "extra OAuth 2.0 request parameters were dropped")
		}
	}
	return request.Auth{Type: request.AuthOAuth2, OAuth2: o}
}
