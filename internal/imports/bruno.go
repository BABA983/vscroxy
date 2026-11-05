package imports

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

// ReadBrunoDir reads a Bruno .bru collection directory. It never executes a
// script, reads a referenced upload or follows an extends outside the
// collection's environments folder.
func ReadBrunoDir(dir string) (Result, error) {
	configPath := filepath.Join(dir, "bruno.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Result{}, fmt.Errorf("choose a Bruno collection folder (bruno.json was not found): %w", err)
	}
	var config brunoConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return Result{}, fmt.Errorf("read bruno.json: %w", err)
	}
	if config.Type != "" && !strings.EqualFold(config.Type, "collection") {
		return Result{}, fmt.Errorf("bruno.json describes a %q, not a collection", config.Type)
	}
	name := Segment(cmp.Or(strings.TrimSpace(config.Name), filepath.Base(dir)))
	root := &brunoFolder{path: name}
	var warnings []string
	if config.Proxy.Kind() != 0 || config.ClientCertificates.Kind() != 0 || config.Scripts.Kind() != 0 || config.Presets.Kind() != 0 {
		warnings = append(warnings, "collection proxy, client certificates or collection scripts were not imported")
	}
	if file, err := os.ReadFile(filepath.Join(dir, "collection.bru")); err == nil {
		blocks, _, err := parseBru(file)
		if err != nil {
			return Result{}, fmt.Errorf("read collection.bru: %w", err)
		}
		root.settings, root.headers = brunoFolderSettings(blocks, name, &warnings)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("read collection.bru: %w", err)
	}
	if err := readBrunoTree(dir, dir, root, &warnings); err != nil {
		return Result{}, err
	}
	collection := Collection{Name: name}
	collectBrunoRequests(root, nil, &collection, &warnings)
	collection.Warnings = dedupe(warnings)
	if len(root.settings.Variables) > 0 || root.settings.Auth.Type != "" || root.settings.Description != "" {
		collection.Folders = append(collection.Folders, root.settings)
	}
	environments, _, err := readBrunoEnvironments(filepath.Join(dir, "environments"))
	if err != nil {
		return Result{}, err
	}
	return Result{Collections: []Collection{collection}, Environments: environments}, nil
}

type brunoConfig struct {
	Version            string         `json:"version"`
	Name               string         `json:"name"`
	Type               string         `json:"type"`
	Ignore             []string       `json:"ignore"`
	Proxy              jsontext.Value `json:"proxy"`
	ClientCertificates jsontext.Value `json:"clientCertificates"`
	Scripts            jsontext.Value `json:"scripts"`
	Presets            jsontext.Value `json:"presets"`
}

type brunoFolder struct {
	path     string
	settings request.Folder
	headers  []request.Field
	children []*brunoFolder
	requests []brunoRequestFile
}

type brunoRequestFile struct {
	file  string
	seq   int
	order int
	req   request.Request
}

// readBrunoTree reads one directory's folders and request files.
func readBrunoTree(root, dir string, folder *brunoFolder, warnings *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	var subdirs []os.DirEntry
	var files []os.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "environments" && dir == root {
			continue
		}
		if entry.IsDir() {
			subdirs = append(subdirs, entry)
		} else {
			files = append(files, entry)
		}
	}
	for _, entry := range subdirs {
		child := &brunoFolder{}
		folderPath := filepath.Join(dir, entry.Name())
		childName := entry.Name()
		var blocks bruBlocks
		if data, err := os.ReadFile(filepath.Join(folderPath, "folder.bru")); err == nil {
			parsed, _, err := parseBru(data)
			if err != nil {
				return fmt.Errorf("read %s: %w", filepath.Join(folderPath, "folder.bru"), err)
			}
			blocks = indexBru(parsed)
			meta := readBruMeta(blocks)
			childName = cmp.Or(meta.name, childName)
			child.settings, child.headers = brunoFolderSettings(parsed, "", warnings)
			child.settings.Path = folder.path + "/" + Segment(childName)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read folder.bru: %w", err)
		} else {
			child.settings.Path = folder.path + "/" + Segment(childName)
		}
		child.path = child.settings.Path
		if err := readBrunoTree(root, folderPath, child, warnings); err != nil {
			return err
		}
		folder.children = append(folder.children, child)
	}
	for _, entry := range files {
		if !strings.HasSuffix(entry.Name(), ".bru") || entry.Name() == "collection.bru" || entry.Name() == "folder.bru" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		blocks, _, err := parseBru(data)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		indexed := indexBru(blocks)
		meta := readBruMeta(indexed)
		if meta.kind != "" && meta.kind != "http" && meta.kind != "graphql" {
			*warnings = append(*warnings, "gRPC, WebSocket and app requests were skipped")
			continue
		}
		method, values, ok := bruMethod(indexed)
		if !ok {
			*warnings = append(*warnings, "requests without a method block were skipped")
			continue
		}
		req, err := brunoRequest(indexed, method, values, meta, path, folder.path, warnings)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		folder.requests = append(folder.requests, brunoRequestFile{file: path, seq: meta.seq, req: req})
	}
	sort.SliceStable(folder.children, func(i, j int) bool { return folder.children[i].path < folder.children[j].path })
	sort.SliceStable(folder.requests, func(i, j int) bool {
		if folder.requests[i].seq != folder.requests[j].seq {
			return folder.requests[i].seq < folder.requests[j].seq
		}
		return folder.requests[i].file < folder.requests[j].file
	})
	return nil
}

func brunoFolderSettings(blocks []bruBlock, path string, warnings *[]string) (request.Folder, []request.Field) {
	indexed := indexBru(blocks)
	folder := request.Folder{Path: path, Description: indexed.text("docs")}
	folder.Variables = bruVariables(indexed.pairs("vars:pre-request"))
	if mode := bruMap(indexed.pairs("auth"))["mode"]; mode != "" && !strings.EqualFold(mode, "inherit") {
		folder.Auth = bruAuth(mode, indexed, warnings)
	}
	if indexed.has("vars:post-response") {
		*warnings = append(*warnings, "post-response variables use Bruno expressions and were not imported")
	}
	if indexed.has("script:pre-request") || indexed.has("script:post-response") || indexed.has("tests") {
		*warnings = append(*warnings, "collection or folder scripts and tests use Bruno's API and were not imported")
	}
	return folder, bruFields(indexed.pairs("headers"))
}

func brunoRequest(indexed bruBlocks, method string, values map[string]string, meta bruMeta, file, parent string, warnings *[]string) (request.Request, error) {
	out := request.Request{
		ID:         StableID("bruno", parent, filepath.Base(file)),
		Name:       cmp.Or(strings.TrimSpace(meta.name), strings.TrimSuffix(filepath.Base(file), ".bru")),
		Collection: parent,
		Method:     method,
		URL:        cmp.Or(values["url"], ""),
	}
	if out.URL == "" {
		return request.Request{}, errors.New("the request has no URL")
	}
	var pathValues map[string]string
	for _, pair := range indexed.pairs("params:path") {
		if pathValues == nil {
			pathValues = map[string]string{}
		}
		if !pair.disabled {
			pathValues[pair.key] = pair.value
		}
	}
	out.URL = normalizeTags(pathVariables(out.URL, pathValues))
	var query []requestField
	for _, pair := range indexed.pairs("params:query") {
		query = append(query, requestField{Name: normalizeTags(pair.key), Value: normalizeTags(pair.value), Disabled: pair.disabled})
	}
	out.URL = appendQuery(out.URL, query)
	out.HeaderList = bruFields(indexed.pairs("headers"))
	if authMode := values["auth"]; authMode != "" {
		out.Auth = bruAuth(authMode, indexed, warnings)
	}
	if out.Auth.Type == "" {
		out.Auth.Type = request.AuthInherit
	}
	brunoBody(&out, values["body"], indexed, warnings)
	if script := VariableScript(bruVariables(indexed.pairs("vars:pre-request"))); script != "" {
		out.Pre = JoinScript(script, out.Pre)
	}
	if indexed.has("vars:post-response") {
		*warnings = append(*warnings, "post-response variables use Bruno expressions and were not imported")
	}
	if indexed.has("script:pre-request") || indexed.has("script:post-response") || indexed.has("tests") {
		*warnings = append(*warnings, "request scripts and tests use Bruno's API and were not imported")
	}
	if indexed.text("docs") != "" {
		*warnings = append(*warnings, "request descriptions were dropped")
	}
	if indexed.has("assert") {
		brunoChecks(&out, indexed.pairs("assert"), warnings)
	}
	out.Redirects = brunoRedirects(indexed, warnings)
	FinishHeaders(&out)
	return out, nil
}

func bruFields(pairs []bruPair) []request.Field {
	var fields []request.Field
	for _, pair := range pairs {
		fields = append(fields, request.Field{Name: normalizeTags(pair.key), Value: normalizeTags(pair.value), Disabled: pair.disabled})
	}
	return fields
}

func bruVariables(pairs []bruPair) []request.Field {
	var fields []request.Field
	for _, pair := range pairs {
		name := strings.TrimPrefix(pair.key, "@")
		if name == "" {
			continue
		}
		fields = append(fields, request.Field{Name: name, Value: pair.value, Disabled: pair.disabled})
	}
	return fields
}

func brunoBody(out *request.Request, mode string, indexed bruBlocks, warnings *[]string) {
	if mode == "" && indexed.has("body") {
		SetRawBody(out, "application/json", indexed.text("body"))
		return
	}
	if mode == "" || strings.EqualFold(mode, "none") {
		return
	}
	switch mode {
	case "json":
		SetRawBody(out, "application/json", indexed.text("body:json"))
	case "text":
		SetRawBody(out, "text/plain", indexed.text("body:text"))
	case "xml":
		SetRawBody(out, "application/xml", indexed.text("body:xml"))
	case "sparql":
		SetRawBody(out, "application/sparql-query", indexed.text("body:sparql"))
	case "graphql":
		SetGraphQLBody(out, indexed.text("body:graphql"), indexed.text("body:graphql:vars"))
	case "formUrlEncoded":
		SetFormBody(out, false, bruFields(indexed.pairs("body:form-urlencoded")))
	case "multipartForm":
		var fields []request.Field
		for _, pair := range indexed.pairs("body:multipart-form") {
			field := request.Field{Name: pair.key, Value: pair.value, Disabled: pair.disabled}
			if path, ok := brunoFileValue(pair.value); ok {
				field.File, field.Value = true, path
				if strings.Contains(pair.value, "|") {
					*warnings = append(*warnings, "multipart fields with several files imported the first one")
				}
			}
			if pair.contentType != "" {
				*warnings = append(*warnings, "per-part multipart content types were dropped")
			}
			fields = append(fields, field)
		}
		SetFormBody(out, true, fields)
	case "file":
		selected := 0
		for _, pair := range indexed.pairs("body:file") {
			if pair.disabled {
				continue
			}
			selected++
			if selected > 1 {
				continue
			}
			path, ok := brunoFileValue(pair.value)
			if !ok {
				path = pair.value
			}
			out.BodyMode, out.BodyFile = "Binary", path
			if pair.contentType != "" {
				AddHeader(out, "Content-Type", pair.contentType)
			}
		}
		if selected > 1 {
			*warnings = append(*warnings, "a file body with several files imported the first one")
		}
	case "grpc", "ws":
		*warnings = append(*warnings, "gRPC and WebSocket bodies were skipped")
	default:
		*warnings = append(*warnings, "use of the "+mode+" body was skipped")
	}
}

// brunoFileValue reads @file(path) and returns the first path.
func brunoFileValue(value string) (string, bool) {
	at := strings.Index(value, "@file(")
	if at < 0 {
		return "", false
	}
	end := strings.Index(value[at:], ")")
	if end < 0 {
		return "", false
	}
	paths := strings.Split(value[at+len("@file("):at+end], "|")
	return strings.TrimSpace(paths[0]), true
}

func bruAuth(mode string, indexed bruBlocks, warnings *[]string) request.Auth {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "":
		return request.Auth{}
	case "none":
		return AuthNone()
	case "inherit":
		return request.Auth{Type: request.AuthInherit}
	case "bearer":
		return request.Auth{Type: "Bearer", Token: bruMap(indexed.pairs("auth:bearer"))["token"]}
	case "basic":
		values := bruMap(indexed.pairs("auth:basic"))
		return request.Auth{Type: "Basic", Username: values["username"], Password: values["password"]}
	case "apikey":
		values := bruMap(indexed.pairs("auth:apikey"))
		location := "Header"
		if strings.EqualFold(values["placement"], "queryparams") {
			location = "Query"
		}
		return request.Auth{Type: "API key", Key: values["key"], Value: values["value"], Location: location}
	case "digest":
		values := bruMap(indexed.pairs("auth:digest"))
		return request.Auth{Type: request.AuthDigest, Username: values["username"], Password: values["password"]}
	case "awsv4":
		values := bruMap(indexed.pairs("auth:awsv4"))
		return request.Auth{Type: request.AuthAWS, AWS: request.AWSAuth{
			AccessKey: values["accessKeyId"], SecretKey: values["secretAccessKey"], SessionToken: values["sessionToken"],
			Service: values["service"], Region: values["region"],
		}}
	case "oauth2":
		return brunoOAuth2(bruMap(indexed.pairs("auth:oauth2")), warnings)
	default:
		*warnings = append(*warnings, "use of "+mode+" auth was imported as no auth")
		return AuthNone()
	}
}

func brunoOAuth2(values map[string]string, warnings *[]string) request.Auth {
	o := request.OAuth2{
		AuthURL: values["authorization_url"], TokenURL: values["access_token_url"],
		ClientID: values["client_id"], ClientSecret: values["client_secret"], Scope: values["scope"],
		Username: values["username"], Password: values["password"], HeaderPrefix: values["token_header_prefix"],
	}
	switch strings.ToLower(values["grant_type"]) {
	case "", "authorization_code":
		if strings.EqualFold(values["pkce"], "true") {
			o.Grant = request.GrantAuthorizationCodePKCE
		} else {
			o.Grant = request.GrantAuthorizationCode
		}
	case "client_credentials":
		o.Grant = request.GrantClientCredentials
	case "password":
		o.Grant = request.GrantPassword
	default:
		*warnings = append(*warnings, "use of the OAuth 2.0 "+values["grant_type"]+" grant was imported as no auth")
		return AuthNone()
	}
	if strings.EqualFold(values["credentials_placement"], "body") {
		o.ClientAuth = "Body"
	}
	if redirect := values["callback_url"]; redirect != "" && (o.Grant == request.GrantAuthorizationCodePKCE || o.Grant == request.GrantAuthorizationCode) {
		if loopback(redirect) {
			o.RedirectURL = redirect
		} else {
			*warnings = append(*warnings, "use of an OAuth 2.0 callback that is not a loopback address; vscroxy uses its own loopback callback")
		}
	}
	return request.Auth{Type: request.AuthOAuth2, OAuth2: o}
}

func brunoRedirects(indexed bruBlocks, warnings *[]string) capture.RedirectPolicy {
	values := bruMap(indexed.pairs("settings"))
	if len(values) == 0 {
		return capture.RedirectPolicy{}
	}
	for key := range values {
		if key != "followRedirects" {
			*warnings = append(*warnings, "request settings (timeouts, redirect limits, URL encoding) were not imported")
			break
		}
	}
	if strings.EqualFold(values["followRedirects"], "false") {
		return capture.RedirectPolicy{Mode: capture.RedirectManual}
	}
	return capture.RedirectPolicy{}
}

// brunoChecks maps the simple status and response-header assertions; other
// Bruno assertions are reported.
func brunoChecks(out *request.Request, pairs []bruPair, warnings *[]string) {
	for _, pair := range pairs {
		if pair.disabled {
			continue
		}
		key := strings.TrimPrefix(pair.key, "$")
		key = strings.TrimPrefix(key, "res.")
		value := strings.TrimSpace(pair.value)
		value = strings.TrimPrefix(value, "eq ")
		value = strings.TrimSpace(value)
		switch {
		case key == "status":
			out.Checks.Status = value
		case strings.HasPrefix(key, "headers."):
			name := strings.TrimPrefix(key, "headers.")
			if out.Checks.Headers != "" {
				out.Checks.Headers += "\n"
			}
			out.Checks.Headers += name + ": " + value
		default:
			*warnings = append(*warnings, "response assertions other than status and headers were not imported")
		}
	}
}

func collectBrunoRequests(folder *brunoFolder, inherited []request.Field, out *Collection, warnings *[]string) {
	headers := mergeBrunoHeaders(inherited, folder.headers)
	if len(folder.headers) > 0 {
		*warnings = append(*warnings, "collection and folder headers were copied into each request; they cannot be edited at the folder level here")
	}
	if folder.path != "" && folder.path != out.Name && (folder.settings.Description != "" || folder.settings.Auth.Type != "" || len(folder.settings.Variables) > 0) {
		out.Folders = append(out.Folders, folder.settings)
	}
	for _, file := range folder.requests {
		req := file.req
		req.HeaderList = mergeBrunoHeaders(headers, req.HeaderList)
		req.Headers = HeaderBlock(req.HeaderList)
		out.Requests = append(out.Requests, req)
	}
	for _, child := range folder.children {
		collectBrunoRequests(child, headers, out, warnings)
	}
}

func mergeBrunoHeaders(inherited, own []request.Field) []request.Field {
	if len(inherited) == 0 {
		return own
	}
	var out []request.Field
	for _, row := range inherited {
		if !row.Disabled {
			out = append(out, row)
		}
	}
	for _, row := range own {
		if !row.Disabled {
			out = slices.DeleteFunc(out, func(existing request.Field) bool {
				return strings.EqualFold(existing.Name, row.Name)
			})
		}
		out = append(out, row)
	}
	return out
}

func readBrunoEnvironments(dir string) ([]Environment, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read Bruno environments: %w", err)
	}
	type rawEnv struct {
		name    string
		values  map[string]string
		extends string
		secrets []string
	}
	var raw []rawEnv
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bru") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		blocks, scalars, err := parseBru(data)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		indexed := indexBru(blocks)
		values := map[string]string{}
		for _, pair := range indexed.pairs("vars") {
			if !pair.disabled && pair.key != "" {
				values[pair.key] = pair.value
			}
		}
		env := rawEnv{name: strings.TrimSuffix(entry.Name(), ".bru"), values: values, extends: scalars["extends"]}
		if env.extends == "" {
			if list := indexed["extends"]; len(list) > 0 {
				env.extends = strings.Join(list[0].items, ",")
			}
		}
		for _, block := range indexed["vars:secret"] {
			env.secrets = append(env.secrets, block.items...)
		}
		raw = append(raw, env)
	}
	byName := map[string]rawEnv{}
	for _, env := range raw {
		byName[env.name] = env
	}
	var out []Environment
	var warnings []string
	for _, env := range raw {
		values := map[string]string{}
		if base, ok := byName[env.extends]; ok {
			for name, value := range base.values {
				values[name] = value
			}
		}
		for name, value := range env.values {
			values[name] = value
		}
		imported := Environment{Name: env.name, Values: values}
		if len(env.secrets) > 0 {
			imported.Warnings = append(imported.Warnings, fmt.Sprintf("%d secret variable names have values outside the collection and were not imported", len(env.secrets)))
			warnings = append(warnings, "Bruno secret variables have values outside the collection and were not imported")
		}
		out = append(out, imported)
	}
	return out, warnings, nil
}

func dedupe(list []string) []string {
	var out []string
	for _, item := range list {
		if item == "" || slices.Contains(out, item) {
			continue
		}
		out = append(out, item)
	}
	return out
}
