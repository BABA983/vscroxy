// Package request is an authored request and everything
// that turns it into traffic: {{variable}} expansion, auth and form encoding,
// pre-request and post-response scripts, declarative response checks, and
// HTTP/WebSocket sending through the capture proxy. gRPC documents share
// preparation, but their interactive transport lives in package grpcclient.
//
// It knows nothing about the window: the app stores requests, edits them
// and shows an Execution's result.
package request

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Request is what a user authors and saves. Names and collection paths are
// editable labels, not asset identities: ID is.
type Request struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Collection string `json:"collection,omitempty"`
	// Empty Protocol is HTTP (including the existing ws/wss send). Protocol
	// documents use their interactive clients, not HTTP sends or Runner steps.
	Protocol string         `json:"protocol,omitempty"`
	GRPC     GRPCConfig     `json:"grpc,omitzero"`
	SocketIO SocketIOConfig `json:"socketio,omitzero"`
	Method   string         `json:"method"`
	URL      string         `json:"url"`
	Headers  string         `json:"headers,omitempty"`
	Body     string         `json:"body,omitempty"`
	Auth     Auth           `json:"auth,omitzero"`
	BodyMode string         `json:"bodyMode,omitempty"`
	// BodyFile is a local path for Binary bodies; only the path is saved.
	BodyFile string      `json:"bodyFile,omitempty"`
	GraphQL  GraphQLBody `json:"graphql,omitzero"`
	Form     []Field     `json:"form,omitempty"`
	// HeaderList keeps the header table's rows: order, duplicates, empty
	// values and disabled rows. Headers stays the Key: Value block the send,
	// export and script paths read, written from the enabled rows.
	HeaderList []Field `json:"headerList,omitempty"`
	// Description is Markdown, shown in the collection's documentation view.
	Description string `json:"description,omitempty"`
	// Settings are this request's transport choices; unset fields inherit.
	Settings Settings `json:"settings,omitzero"`
	// Pre runs before the send and may change it; Post sees the response.
	Pre    string `json:"pre,omitempty"`
	Post   string `json:"post,omitempty"`
	Checks Checks `json:"checks,omitzero"`
	// TokenSources invalidate their environment values before checks or any
	// inherited/authored script can fail. Extraction remains in Post.
	TokenSources []TokenSource `json:"tokenSources,omitempty"`
	// Examples are responses kept with the request, as Postman's are.
	Examples []Example `json:"examples,omitempty"`
}

// GRPCConfig keeps compiled descriptors with the document, so it is portable
// without source paths and does not depend on another tab's loaded schema.
type GRPCConfig struct {
	Method         string `json:"method,omitempty"`
	Schema         []byte `json:"schema,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitzero"`
}

type TokenSource struct {
	Environment string `json:"environment"`
	Variable    string `json:"variable"`
}

// GraphQLBody keeps authored templates separate from the JSON envelope sent.
// Variables is JSON text, not a map, so placeholders and large numbers survive.
type GraphQLBody struct {
	Query     string `json:"query,omitempty"`
	Variables string `json:"variables,omitempty"`
}

// Example is a response kept with a request, with the request that got it.
type Example struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Request ExampleRequest `json:"request"`
	Status  int            `json:"status"`
	// Headers is the response's Key: Value block.
	Headers string `json:"headers,omitempty"`
	Body    string `json:"body,omitempty"`
}

// ExampleRequest is the request an example came from as authored, with its
// {{variables}}, so no expanded secret is kept with it.
type ExampleRequest struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Headers string `json:"headers,omitempty"`
	Body    string `json:"body,omitempty"`
}

// MaxExampleBody is the largest response body an example keeps; Postman's
// limit for an example is the same.
const MaxExampleBody = 5 << 20

type Auth struct {
	Type     string `json:"type,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Location string `json:"location,omitempty"`
	OAuth2   OAuth2 `json:"oauth2,omitzero"`
	// AWS, JWT, OAuth 1.0, Hawk and EdgeGrid configure the auth types of
	// those names; Digest uses Username and Password.
	AWS      AWSAuth      `json:"aws,omitzero"`
	JWT      JWTAuth      `json:"jwt,omitzero"`
	OAuth1   OAuth1Auth   `json:"oauth1,omitzero"`
	Hawk     HawkAuth     `json:"hawk,omitzero"`
	EdgeGrid EdgeGridAuth `json:"edgegrid,omitzero"`
}

type Field struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	File     bool   `json:"file,omitzero"`
	Disabled bool   `json:"disabled,omitzero"`
	// Revealed is the table's own state, never saved: a masked credential shows
	// in plain text only when the reader asks for this row.
	Revealed bool `json:"-"`
}

// Prepare is the encoder shared by Send, Runner and cURL export.
// Send/Runner run pre-scripts on templates first, then encode with final vars.
func (r Request) Prepare(vars map[string]string) (*Prepared, error) {
	if r.Protocol != "" {
		return nil, fmt.Errorf("%s documents require their protocol client, not an HTTP send or code export", r.Protocol)
	}
	req := &Prepared{
		Method: strings.ToUpper(strings.TrimSpace(cmp.Or(ExpandVars(r.Method, vars), "GET"))),
		URL:    strings.TrimSpace(ExpandVars(r.URL, vars)), Body: ExpandVars(r.Body, vars),
		Header: ParseHeaderBlock(ExpandVars(r.Headers, vars)),
	}
	if req.URL == "" {
		return nil, fmt.Errorf("the URL is empty")
	}
	switch r.Auth.Type {
	case "", "None", AuthInherit: // Execute resolves Inherit; without a Context there is no parent.
	case "Bearer":
		req.Set("Authorization", "Bearer "+ExpandVars(r.Auth.Token, vars))
	case "Basic":
		value := ExpandVars(r.Auth.Username, vars) + ":" + ExpandVars(r.Auth.Password, vars)
		req.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
	case AuthOAuth2:
		// Execute puts the access token in Token; cURL export, the kept one.
		if r.Auth.Token == "" {
			return nil, ErrSignInNeeded
		}
		req.Set("Authorization", cmp.Or(strings.TrimSpace(ExpandVars(r.Auth.OAuth2.HeaderPrefix, vars)), "Bearer")+" "+r.Auth.Token)
	case "API key":
		key, value := ExpandVars(r.Auth.Key, vars), ExpandVars(r.Auth.Value, vars)
		if key == "" {
			return nil, fmt.Errorf("API key name is empty")
		}
		if r.Auth.Location == "Query" {
			u, err := url.Parse(req.URL)
			if err != nil {
				return nil, err
			}
			q, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				return nil, err
			}
			q.Set(key, value)
			u.RawQuery = q.Encode()
			req.URL = u.String()
		} else {
			req.Set(key, value)
		}
	case AuthDigest: // Execute answers the server's challenge; there is none yet.
	case AuthAWS, AuthOAuth1, AuthHawk, AuthEdgeGrid: // Signed below, after the body.
	case AuthJWT:
		if err := r.Auth.JWT.expand(vars).apply(req); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported auth type %q", r.Auth.Type)
	}
	switch r.BodyMode {
	case "", "Raw":
	case "GraphQL":
		query := ExpandVars(r.GraphQL.Query, vars)
		if strings.TrimSpace(query) == "" {
			return nil, fmt.Errorf("enter a GraphQL query")
		}
		variables := strings.TrimSpace(ExpandVars(r.GraphQL.Variables, vars))
		if variables == "" {
			variables = "{}"
		}
		value := jsontext.Value(variables)
		if value.Kind() != '{' || !value.IsValid() {
			return nil, fmt.Errorf("GraphQL variables must be a JSON object after variable expansion")
		}
		data, err := json.Marshal(struct {
			Query     string         `json:"query"`
			Variables jsontext.Value `json:"variables"`
		}{query, value})
		if err != nil {
			return nil, fmt.Errorf("encode GraphQL body: %w", err)
		}
		req.Body = string(data)
		if req.Header.Get("Content-Type") == "" {
			req.Set("Content-Type", "application/json")
		}
		req.Remove("Content-Length")
	case "Binary":
		path := ExpandVars(r.BodyFile, vars)
		if path == "" {
			return nil, fmt.Errorf("choose a binary body file")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("binary body file: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return nil, fmt.Errorf("binary body files must be regular files up to 16 MiB")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open binary body file: %w", err)
		}
		defer file.Close()
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("binary body files must be regular files up to 16 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		if err != nil {
			return nil, fmt.Errorf("read binary body file: %w", err)
		}
		if len(data) > 16<<20 {
			return nil, fmt.Errorf("binary body exceeds 16 MiB")
		}
		req.Body, req.BodyFile = string(data), path
		if req.Header.Get("Content-Type") == "" {
			req.Set("Content-Type", "application/octet-stream")
		}
		req.Remove("Content-Length")
	case "URL encoded", "Multipart":
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		values := url.Values{}
		for _, field := range r.Form {
			if field.Disabled || field.Name == "" && field.Value == "" {
				continue
			}
			name, value := ExpandVars(field.Name, vars), ExpandVars(field.Value, vars)
			if r.BodyMode == "URL encoded" {
				if field.File {
					return nil, fmt.Errorf("URL encoded bodies cannot contain files")
				}
				values.Add(name, value)
			} else if field.File {
				info, err := os.Stat(value)
				if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
					return nil, fmt.Errorf("form files must be regular files up to 16 MiB")
				}
				file, err := os.Open(value)
				if err != nil {
					return nil, fmt.Errorf("open form file: %w", err)
				}
				part, err := writer.CreateFormFile(name, filepath.Base(value))
				if err == nil {
					_, err = io.Copy(part, io.LimitReader(file, (16<<20)+1))
				}
				file.Close()
				if err != nil {
					return nil, err
				}
			} else if err := writer.WriteField(name, value); err != nil {
				return nil, err
			}
			if body.Len() > 16<<20 {
				return nil, fmt.Errorf("form body exceeds 16 MiB")
			}
		}
		if r.BodyMode == "URL encoded" {
			req.Body = values.Encode()
			req.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			if err := writer.Close(); err != nil {
				return nil, err
			}
			req.Body = body.String()
			req.Set("Content-Type", writer.FormDataContentType())
		}
		if len(req.Body) > 16<<20 {
			return nil, fmt.Errorf("form body exceeds 16 MiB")
		}
		req.Remove("Content-Length")
	default:
		return nil, fmt.Errorf("unsupported body type %q", r.BodyMode)
	}
	switch r.Auth.Type {
	case AuthAWS:
		if err := r.Auth.AWS.expand(vars).sign(req, now()); err != nil {
			return nil, err
		}
	case AuthOAuth1:
		if err := r.Auth.OAuth1.expand(vars).defaults().sign(req, now()); err != nil {
			return nil, err
		}
	case AuthHawk:
		if err := r.Auth.Hawk.expand(vars).sign(req); err != nil {
			return nil, err
		}
	case AuthEdgeGrid:
		if err := r.Auth.EdgeGrid.expand(vars).sign(req); err != nil {
			return nil, err
		}
	}
	return req, nil
}
