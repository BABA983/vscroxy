package postman_test

import (
	"encoding/hex"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
)

const shopCollection = `{
  "info": {
    "_postman_id": "c0ffee00-0000-4000-8000-000000000001",
    "name": "Shop API",
    "description": "Storefront endpoints.",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}", "type": "string"}]},
  "event": [
    {"listen": "prerequest", "script": {"type": "text/javascript", "exec": ["pm.variables.set('ts', Date.now());", "console.log('root');"]}},
    {"listen": "test", "script": {"type": "text/javascript", "exec": "pm.test('ok', () => pm.response.to.be.ok);"}}
  ],
  "variable": [
    {"key": "baseUrl", "value": "https://shop.example.com", "type": "string"},
    {"key": "pageSize", "value": 20, "type": "number"},
    {"key": "legacy", "value": "v1", "disabled": true}
  ],
  "item": [
    {
      "name": "Orders/Returns",
      "description": {"content": "Order and return flows.", "type": "text/markdown"},
      "auth": {"type": "noauth"},
      "event": [
        {"listen": "prerequest", "script": {"exec": "console.log('orders');"}},
        {"listen": "test", "disabled": true, "script": {"exec": ["skipped();"]}}
      ],
      "variable": [{"key": "region", "value": "eu"}],
      "item": [
        {
          "id": "req-list-orders",
          "name": "List orders",
          "request": {
            "method": "GET",
            "header": [
              {"key": "Accept", "value": "application/json"},
              {"key": "X-Debug", "value": "1", "disabled": true}
            ],
            "url": {
              "raw": "{{baseUrl}}/orders?status=open",
              "host": ["{{baseUrl}}"],
              "path": ["orders"],
              "query": [{"key": "status", "value": "open"}, {"key": "page", "value": "2", "disabled": true}]
            }
          },
          "response": [{"name": "200 OK", "status": "OK", "code": 200, "body": "[]"}]
        },
        {
          "name": "Archive",
          "item": [
            {
              "name": "Get order",
              "request": {
                "method": "get",
                "header": [],
                "url": {
                  "raw": "{{baseUrl}}/orders/:orderId/items/:itemId",
                  "host": ["{{baseUrl}}"],
                  "path": ["orders", ":orderId", "items", ":itemId"],
                  "variable": [{"key": "orderId", "value": "42"}, {"key": "itemId", "value": ""}]
                },
                "auth": {"type": "apikey", "apikey": [
                  {"key": "in", "value": "query", "type": "string"},
                  {"key": "key", "value": "api_key", "type": "string"},
                  {"key": "value", "value": "{{apiKey}}", "type": "string"}
                ]}
              }
            }
          ]
        }
      ]
    },
    {
      "name": "Auth",
      "auth": {"type": "digest", "digest": [{"key": "username", "value": "shop"}]},
      "item": [
        {
          "id": "req-login",
          "name": "Login",
          "event": [{"listen": "test", "script": {"exec": ["const t = pm.response.json().token;", "pm.collectionVariables.set('token', t);"]}}],
          "request": {
            "method": "POST",
            "description": "Logs in.",
            "header": [],
            "body": {"mode": "raw", "raw": "{\"user\":\"{{user}}\"}", "options": {"raw": {"language": "json"}}},
            "url": "{{baseUrl}}/login",
            "auth": {"type": "noauth"}
          }
        },
        {
          "name": "Token",
          "request": {
            "method": "POST",
            "header": [{"key": "content-type", "value": "application/x-www-form-urlencoded"}],
            "body": {"mode": "urlencoded", "urlencoded": [
              {"key": "grant_type", "value": "password", "type": "text"},
              {"key": "scope", "value": "admin", "type": "text", "disabled": true}
            ]},
            "url": {"raw": "{{baseUrl}}/oauth/token"},
            "auth": {"type": "digest", "digest": [{"key": "realm", "value": "shop"}]}
          }
        }
      ]
    },
    {
      "name": "Upload avatar",
      "protocolProfileBehavior": {"disableBodyPruning": true},
      "request": {
        "method": "PUT",
        "body": {"mode": "formdata", "formdata": [
          {"key": "name", "value": "me", "type": "text"},
          {"key": "avatar", "type": "file", "src": "/tmp/avatar.png"}
        ]},
        "url": "{{baseUrl}}/me/avatar"
      }
    },
    {
      "name": "Upload firmware",
      "request": {
        "method": "POST",
        "body": {"mode": "file", "file": {"src": "/tmp/firmware.bin"}},
        "url": "{{baseUrl}}/firmware"
      }
    },
    {
      "name": "Search",
      "variable": [{"key": "q", "value": "boots"}],
      "request": {
        "method": "POST",
        "body": {"mode": "graphql", "graphql": {"query": "query($q: String) { search(q: $q) { id } }", "variables": "{\n  \"q\": \"shoes\"\n}"}},
        "url": {"raw": "{{baseUrl}}/graphql"}
      }
    },
    {"name": "Ping", "request": "https://shop.example.com/ping"},
    {
      "name": "Ping",
      "request": {
        "method": "HEAD",
        "header": "X-Trace: on\nAccept: */*",
        "url": {
          "protocol": "https",
          "host": ["shop", "example", "com"],
          "path": ["ping"],
          "query": [{"key": "deep", "value": "1"}, {"key": "verbose", "value": "true", "disabled": true}]
        }
      }
    }
  ]
}`

func TestReadCollection(t *testing.T) {
	got, err := postman.ReadCollection([]byte(shopCollection))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Shop API" {
		t.Errorf("Name = %q, want Shop API", got.Name)
	}
	inherit := request.Auth{Type: request.AuthInherit}
	orders := "Shop API/Orders∕Returns"
	want := []request.Request{
		{
			ID: "req-list-orders", Name: "List orders", Collection: orders, Method: "GET",
			URL:        "{{baseUrl}}/orders?status=open",
			HeaderList: []request.Field{{Name: "Accept", Value: "application/json"}, {Name: "X-Debug", Value: "1", Disabled: true}},
			Headers:    "Accept: application/json", Auth: inherit,
		},
		{
			Name: "Get order", Collection: orders + "/Archive", Method: "GET",
			URL:  "{{baseUrl}}/orders/42/items/{{itemId}}",
			Auth: request.Auth{Type: "API key", Key: "api_key", Value: "{{apiKey}}", Location: "Query"},
		},
		{
			ID: "req-login", Name: "Login", Collection: "Shop API/Auth", Method: "POST", URL: "{{baseUrl}}/login",
			BodyMode: "Raw", Body: `{"user":"{{user}}"}`,
			HeaderList: []request.Field{{Name: "Content-Type", Value: "application/json"}},
			Headers:    "Content-Type: application/json",
			Post:       "const t = pm.response.json().token;\npm.collectionVariables.set('token', t);",
		},
		{
			Name: "Token", Collection: "Shop API/Auth", Method: "POST", URL: "{{baseUrl}}/oauth/token",
			HeaderList: []request.Field{{Name: "content-type", Value: "application/x-www-form-urlencoded"}},
			Headers:    "content-type: application/x-www-form-urlencoded",
			BodyMode:   "URL encoded",
			Form:       []request.Field{{Name: "grant_type", Value: "password"}, {Name: "scope", Value: "admin", Disabled: true}},
		},
		{
			Name: "Upload avatar", Collection: "Shop API", Method: "PUT", URL: "{{baseUrl}}/me/avatar",
			BodyMode: "Multipart",
			Form:     []request.Field{{Name: "name", Value: "me"}, {Name: "avatar", Value: "/tmp/avatar.png", File: true}},
			Auth:     inherit,
		},
		{Name: "Upload firmware", Collection: "Shop API", Method: "POST", URL: "{{baseUrl}}/firmware", Auth: inherit},
		{
			Name: "Search", Collection: "Shop API", Method: "POST", URL: "{{baseUrl}}/graphql",
			BodyMode: "Raw", Body: `{"query":"query($q: String) { search(q: $q) { id } }","variables":{"q":"shoes"}}`,
			HeaderList: []request.Field{{Name: "Content-Type", Value: "application/json"}},
			Headers:    "Content-Type: application/json", Auth: inherit,
		},
		{Name: "Ping", Collection: "Shop API", Method: "GET", URL: "https://shop.example.com/ping", Auth: inherit},
		{
			Name: "Ping", Collection: "Shop API", Method: "HEAD", URL: "https://shop.example.com/ping?deep=1",
			HeaderList: []request.Field{{Name: "X-Trace", Value: "on"}, {Name: "Accept", Value: "*/*"}},
			Headers:    "X-Trace: on\nAccept: */*", Auth: inherit,
		},
	}
	if len(got.Requests) != len(want) {
		t.Fatalf("got %d requests, want %d: %+v", len(got.Requests), len(want), got.Requests)
	}
	again, err := postman.ReadCollection([]byte(shopCollection))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for i, r := range got.Requests {
		if want[i].ID == "" {
			// Generated: stable across imports, distinct, and the documented shape.
			if again.Requests[i].ID != r.ID {
				t.Errorf("request %d: ID %q changed to %q on re-import", i, r.ID, again.Requests[i].ID)
			}
			hexID, ok := strings.CutPrefix(r.ID, "postman-")
			if _, err := hex.DecodeString(hexID); !ok || err != nil || len(hexID) != 64 {
				t.Errorf("request %d: generated ID %q is not postman-<sha256 hex>", i, r.ID)
			}
			r.ID = ""
		}
		if ids[got.Requests[i].ID] {
			t.Errorf("request %d: duplicate ID %q", i, got.Requests[i].ID)
		}
		ids[got.Requests[i].ID] = true
		if !reflect.DeepEqual(r, want[i]) {
			t.Errorf("request %d:\n got %+v\nwant %+v", i, r, want[i])
		}
	}

	wantFolders := []request.Folder{
		{
			Path: "Shop API", Description: "Storefront endpoints.",
			Auth: request.Auth{Type: "Bearer", Token: "{{token}}"},
			Pre:  "pm.variables.set('ts', Date.now());\nconsole.log('root');",
			Post: "pm.test('ok', () => pm.response.to.be.ok);",
			Variables: []request.Field{
				{Name: "baseUrl", Value: "https://shop.example.com"},
				{Name: "pageSize", Value: "20"},
				{Name: "legacy", Value: "v1", Disabled: true},
			},
		},
		{
			Path: orders, Description: "Order and return flows.", Auth: request.Auth{Type: "None"},
			Pre: "console.log('orders');", Variables: []request.Field{{Name: "region", Value: "eu"}},
		},
		{Path: "Shop API/Auth", Auth: request.Auth{Type: "None"}},
	}
	if !reflect.DeepEqual(got.Folders, wantFolders) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, wantFolders)
	}

	wantWarnings := []string{
		"1 folder uses digest auth; imported as no auth",
		"1 request uses a binary file body; imported without a body",
		"1 request uses digest auth; imported as no auth",
		"Dropped 1 request description",
		"Dropped 1 request variable",
		"Dropped 1 saved example",
		"Dropped 2 disabled query parameters",
		"Dropped protocol profile, certificate or proxy settings on 1 item",
		"Skipped 1 disabled script",
	}
	if !slices.Equal(got.Warnings, wantWarnings) {
		t.Errorf("Warnings:\n got %q\nwant %q", got.Warnings, wantWarnings)
	}
}

func TestReadCollectionV20(t *testing.T) {
	// v2.0 auth parameters are objects, and the collection's auth is set.
	got, err := postman.ReadCollection([]byte(`{
  "info": {"name": "Legacy", "schema": "https://schema.getpostman.com/json/collection/v2.0.0/collection.json"},
  "auth": {"type": "basic", "basic": {"username": "admin", "password": "secret"}},
  "item": [{"id": "1", "name": "Who", "request": {"url": "https://example.com/who", "auth": {"type": "bearer", "bearer": {"token": "abc"}}}}]
}`))
	if err != nil {
		t.Fatal(err)
	}
	wantFolders := []request.Folder{{Path: "Legacy", Auth: request.Auth{Type: "Basic", Username: "admin", Password: "secret"}}}
	if !reflect.DeepEqual(got.Folders, wantFolders) {
		t.Errorf("Folders = %+v, want %+v", got.Folders, wantFolders)
	}
	want := []request.Request{{ID: "1", Name: "Who", Collection: "Legacy", Method: "GET", URL: "https://example.com/who", Auth: request.Auth{Type: "Bearer", Token: "abc"}}}
	if !reflect.DeepEqual(got.Requests, want) {
		t.Errorf("Requests = %+v, want %+v", got.Requests, want)
	}
}

func TestReadCollectionRejects(t *testing.T) {
	for _, tt := range []struct{ name, data, want string }{
		{"v1", `{"id": "x", "name": "Old", "order": [], "requests": [{"id": "r", "url": "https://example.com"}]}`, "v1 is not supported"},
		{"environment", `{"name": "Staging", "values": []}`, "not a Postman collection"},
		{"unknown schema", `{"info": {"name": "X", "schema": "https://schema.getpostman.com/json/collection/v3.0.0/collection.json"}, "item": []}`, "unsupported Postman collection schema"},
		{"not JSON", `name: x`, "not a Postman collection"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := postman.ReadCollection([]byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestCollectionRoundTrip(t *testing.T) {
	folders := []request.Folder{
		{
			Path: "Shop", Description: "Root notes.", Auth: request.Auth{Type: "Basic", Username: "u", Password: "p"},
			Pre: "first();\nsecond();", Variables: []request.Field{{Name: "host", Value: "example.com"}, {Name: "off", Value: "1", Disabled: true}},
		},
		{Path: "Shop/Orders∕Returns", Auth: request.Auth{Type: "None"}, Post: "after();"},
		{Path: "Shop/Orders∕Returns/Deep", Description: "Deep.", Auth: request.Auth{Type: "API key", Key: "X-Key", Value: "{{key}}", Location: "Header"}},
		{Path: "Shopping", Description: "Another collection."},
	}
	requests := []request.Request{
		{
			ID: "r1", Name: "List", Collection: "Shop/Orders∕Returns", Method: "GET",
			URL:        "https://{{host}}:8443/orders/{{id}}?a=1&b#frag",
			HeaderList: []request.Field{{Name: "Accept", Value: "*/*"}, {Name: "X-Off", Value: "1", Disabled: true}},
			Headers:    "Accept: */*", Auth: request.Auth{Type: request.AuthInherit},
		},
		{
			ID: "r2", Name: "Create", Collection: "Shop", Method: "POST", URL: "{{base}}/orders",
			BodyMode: "Raw", Body: `{"a": 1}`,
			HeaderList: []request.Field{{Name: "Content-Type", Value: "application/json"}},
			Headers:    "Content-Type: application/json", Pre: "pre();", Post: "post();\ncheck();",
		},
		{
			ID: "r3", Name: "Form", Collection: "Shop/Orders∕Returns/Deep", Method: "POST", URL: "{{base}}/form",
			BodyMode: "URL encoded", Form: []request.Field{{Name: "a", Value: "1"}, {Name: "b", Value: "2", Disabled: true}},
			Auth: request.Auth{Type: "Bearer", Token: "t"},
		},
		{
			ID: "r4", Name: "Upload", Collection: "Shop/Empty/Inner", Method: "PUT", URL: "{{base}}/upload",
			BodyMode: "Multipart", Form: []request.Field{{Name: "f", Value: "/tmp/x.png", File: true}, {Name: "t", Value: "v", Disabled: true}},
			Auth: request.Auth{Type: "Basic", Username: "a", Password: "b"},
		},
		{
			ID: "r5", Name: "Text", Collection: "Shop", Method: "POST", URL: "http://localhost/echo",
			BodyMode: "Raw", Body: "plain text",
			HeaderList: []request.Field{{Name: "Content-Type", Value: "text/plain"}},
			Headers:    "Content-Type: text/plain",
			Auth:       request.Auth{Type: "API key", Key: "k", Value: "v", Location: "Query"},
		},
		{
			ID: "r6", Name: "Checked", Collection: "Shop", Method: "DELETE", URL: "{{base}}/x", Headers: "A: 1\nB: 2",
			Checks: request.Checks{Status: "204"},
		},
		{ID: "other", Name: "Elsewhere", Collection: "Shopping", Method: "GET", URL: "https://example.com"},
	}
	data, warnings, err := postman.WriteCollection("Shop", requests, folders)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Declarative checks on 1 request are not exported"}; !slices.Equal(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
	for _, s := range []string{"{\n  \"info\": {", `"name": "Orders/Returns"`, `"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"`} {
		if !strings.Contains(string(data), s) {
			t.Errorf("export lacks %q:\n%s", s, data)
		}
	}
	if postman.Detect(data) != postman.KindCollection {
		t.Errorf("Detect(export) = %q", postman.Detect(data))
	}

	got, err := postman.ReadCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Shop" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("re-import warnings = %q", got.Warnings)
	}
	// Folders come first at each level, sorted by name; requests keep their order.
	var order []string
	for _, r := range got.Requests {
		order = append(order, r.ID)
	}
	if want := []string{"r4", "r3", "r1", "r2", "r5", "r6"}; !slices.Equal(order, want) {
		t.Errorf("request order = %q, want %q", order, want)
	}
	want := map[string]request.Request{}
	for _, r := range requests[:6] {
		want[r.ID] = r
	}
	r6 := want["r6"]
	r6.HeaderList, r6.Checks = []request.Field{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}}, request.Checks{}
	want["r6"] = r6
	for _, r := range got.Requests {
		if !reflect.DeepEqual(r, want[r.ID]) {
			t.Errorf("request %s:\n got %+v\nwant %+v", r.ID, r, want[r.ID])
		}
	}
	if !reflect.DeepEqual(got.Folders, folders[:3]) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, folders[:3])
	}
}

func TestWriteCollectionRejectsEmptyName(t *testing.T) {
	if _, _, err := postman.WriteCollection("", nil, nil); err == nil {
		t.Error("WriteCollection with no name succeeded")
	}
}

func TestEnvironmentRoundTrip(t *testing.T) {
	for _, globals := range []bool{false, true} {
		values := map[string]string{"token": "abc", "baseUrl": "https://example.com", "empty": ""}
		data, err := postman.WriteEnvironment("Staging", values, globals)
		if err != nil {
			t.Fatal(err)
		}
		wantKind := postman.KindEnvironment
		if globals {
			wantKind = postman.KindGlobals
		}
		if kind := postman.Detect(data); kind != wantKind {
			t.Errorf("Detect = %q, want %q", kind, wantKind)
		}
		if b, e := strings.Index(string(data), `"baseUrl"`), strings.Index(string(data), `"token"`); b < 0 || b > e {
			t.Errorf("values are not sorted by key:\n%s", data)
		}
		got, err := postman.ReadEnvironment(data)
		if err != nil {
			t.Fatal(err)
		}
		want := postman.Environment{Name: "Staging", Globals: globals, Values: values}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestReadEnvironment(t *testing.T) {
	got, err := postman.ReadEnvironment([]byte(`{
  "id": "5f1d",
  "name": "Production",
  "values": [
    {"key": "host", "value": "api.example.com", "type": "default", "enabled": true},
    {"key": "retries", "value": 3, "type": "default", "enabled": true},
    {"key": "debug", "value": false},
    {"key": "password", "value": "hunter2", "type": "secret", "enabled": true},
    {"key": "old", "value": "x", "enabled": false}
  ],
  "_postman_variable_scope": "environment"
}`))
	if err != nil {
		t.Fatal(err)
	}
	want := postman.Environment{
		Name:     "Production",
		Values:   map[string]string{"host": "api.example.com", "retries": "3", "debug": "false", "password": "hunter2"},
		Warnings: []string{"Skipped 1 disabled variable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := postman.ReadEnvironment([]byte(shopCollection)); err == nil {
		t.Error("ReadEnvironment accepted a collection")
	}
}

func TestDetect(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		want       postman.Kind
	}{
		{"v2.1 collection", shopCollection, postman.KindCollection},
		{"v2.0 collection", `{"info": {"name": "A", "schema": "https://schema.getpostman.com/json/collection/v2.0.0/collection.json"}}`, postman.KindCollection},
		{"items without schema", `{"info": {"name": "A"}, "item": []}`, postman.KindCollection},
		{"environment", `{"name": "Staging", "values": [{"key": "a", "value": "1"}]}`, postman.KindEnvironment},
		{"environment scope", `{"name": "Staging", "values": [], "_postman_variable_scope": "environment"}`, postman.KindEnvironment},
		{"globals", `{"name": "Globals", "values": [], "_postman_variable_scope": "globals"}`, postman.KindGlobals},
		{"v1 collection", `{"id": "x", "name": "Old", "requests": []}`, ""},
		{"OpenAPI", `{"openapi": "3.1.0", "info": {"title": "A"}}`, ""},
		{"array", `[]`, ""},
		{"not JSON", `values:`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := postman.Detect([]byte(tt.data)); got != tt.want {
				t.Errorf("Detect = %q, want %q", got, tt.want)
			}
		})
	}
}
