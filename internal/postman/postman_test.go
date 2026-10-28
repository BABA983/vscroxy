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
          "response": [
            {
              "id": "ex-open", "name": "Open orders", "status": "OK", "code": 200,
              "originalRequest": {"method": "get", "header": [{"key": "Accept", "value": "application/json"}], "url": {"raw": "{{baseUrl}}/orders?status=open"}},
              "header": [{"key": "Content-Type", "value": "application/json"}],
              "body": "[]"
            },
            {"code": 404, "originalRequest": "{{baseUrl}}/orders?status=gone", "body": "missing"},
            "not an example"
          ]
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
            "auth": {"type": "hawk", "hawk": [{"key": "authId", "value": "shop"}]}
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
			Examples: []request.Example{
				{
					ID: "ex-open", Name: "Open orders", Status: 200, Headers: "Content-Type: application/json", Body: "[]",
					Request: request.ExampleRequest{Method: "GET", URL: "{{baseUrl}}/orders?status=open", Headers: "Accept: application/json"},
				},
				{Name: "404 Not Found", Status: 404, Body: "missing", Request: request.ExampleRequest{Method: "GET", URL: "{{baseUrl}}/orders?status=gone"}},
			},
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
		r.Examples = slices.Clone(r.Examples)
		for j, e := range r.Examples {
			if j < len(want[i].Examples) && want[i].Examples[j].ID == "" {
				if hexID, ok := strings.CutPrefix(e.ID, "postman-"); !ok || len(hexID) != 64 || again.Requests[i].Examples[j].ID != e.ID {
					t.Errorf("request %d example %d: generated ID %q is not stable postman-<sha256 hex>", i, j, e.ID)
				}
				r.Examples[j].ID = ""
			}
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
		{Path: "Shop API/Auth", Auth: request.Auth{Type: request.AuthDigest, Username: "shop"}},
	}
	if !reflect.DeepEqual(got.Folders, wantFolders) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, wantFolders)
	}

	wantWarnings := []string{
		"1 request uses a binary file body; imported without a body",
		"1 request uses hawk auth; imported as no auth",
		"Dropped 1 request description",
		"Dropped 1 request variable",
		"Dropped 1 saved example that is unreadable or over 5 MiB",
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

func TestReadCollectionOAuth2(t *testing.T) {
	got, err := postman.ReadCollection([]byte(`{
  "info": {"name": "Identity", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "auth": {"type": "oauth2", "oauth2": [
    {"key": "authUrl", "value": "https://id.example.com/authorize", "type": "string"},
    {"key": "accessTokenUrl", "value": "https://id.example.com/token", "type": "string"},
    {"key": "clientId", "value": "web", "type": "string"},
    {"key": "redirect_uri", "value": "https://oauth.pstmn.io/v1/callback", "type": "string"},
    {"key": "useBrowser", "value": true, "type": "boolean"},
    {"key": "tokenName", "value": "web token", "type": "string"},
    {"key": "addTokenTo", "value": "header", "type": "string"}
  ]},
  "item": [
    {
      "name": "Partners",
      "auth": {"type": "oauth2", "oauth2": [
        {"key": "grant_type", "value": "client_credentials", "type": "string"},
        {"key": "accessTokenUrl", "value": "https://id.example.com/token", "type": "string"},
        {"key": "clientId", "value": "partner", "type": "string"},
        {"key": "clientSecret", "value": "{{partnerSecret}}", "type": "string"},
        {"key": "scope", "value": "orders:read", "type": "string"},
        {"key": "client_authentication", "value": "body", "type": "string"},
        {"key": "redirect_uri", "value": "https://oauth.pstmn.io/v1/callback", "type": "string"},
        {"key": "addTokenTo", "value": "queryParams", "type": "string"},
        {"key": "tokenRequestParams", "value": [{"key": "audience", "value": "orders"}], "type": "any"}
      ]},
      "item": [{"id": "partner", "name": "Partner orders", "request": {"url": "https://api.example.com/partner"}}]
    },
    {
      "name": "Legacy",
      "auth": {"type": "oauth2", "oauth2": [{"key": "grant_type", "value": "implicit"}, {"key": "authUrl", "value": "https://id.example.com/authorize"}]},
      "item": [{"id": "legacy", "name": "Legacy me", "request": {"url": "https://api.example.com/me"}}]
    },
    {
      "id": "pkce", "name": "Me (PKCE)",
      "request": {"url": "https://api.example.com/me", "auth": {"type": "oauth2", "oauth2": [
        {"key": "grant_type", "value": "authorization_code_with_pkce", "type": "string"},
        {"key": "authUrl", "value": "https://id.example.com/authorize", "type": "string"},
        {"key": "accessTokenUrl", "value": "https://id.example.com/token", "type": "string"},
        {"key": "clientId", "value": "cli", "type": "string"},
        {"key": "clientSecret", "value": "", "type": "string"},
        {"key": "scope", "value": "openid profile", "type": "string"},
        {"key": "redirect_uri", "value": "http://localhost:8080/callback", "type": "string"},
        {"key": "client_authentication", "value": "header", "type": "string"},
        {"key": "headerPrefix", "value": "Token", "type": "string"},
        {"key": "challengeAlgorithm", "value": "S256", "type": "string"},
        {"key": "code_verifier", "value": "abc", "type": "string"},
        {"key": "state", "value": "xyz", "type": "string"},
        {"key": "accessToken", "value": "eyJ.leaked.token", "type": "string"}
      ]}}
    },
    {
      "id": "code-https", "name": "Me (code)",
      "request": {"url": "https://api.example.com/me", "auth": {"type": "oauth2", "oauth2": [
        {"key": "grant_type", "value": "authorization_code"},
        {"key": "accessTokenUrl", "value": "https://id.example.com/token"},
        {"key": "clientId", "value": "cli"},
        {"key": "redirect_uri", "value": "https://127.0.0.1/callback"}
      ]}}
    },
    {
      "id": "password", "name": "Me (password, v2.0 object form)",
      "request": {"url": "https://api.example.com/me", "auth": {"type": "oauth2", "oauth2": {
        "grant_type": "password_credentials", "accessTokenUrl": "https://id.example.com/token", "clientId": "cli",
        "username": "{{user}}", "password": "{{password}}", "redirect_uri": "http://[::1]:9000/cb"
      }}}
    },
    {
      "id": "implicit", "name": "Me (implicit)",
      "request": {"url": "https://api.example.com/me", "auth": {"type": "oauth2", "oauth2": [{"key": "grant_type", "value": "implicit"}]}}
    }
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	oauth2 := func(o request.OAuth2) request.Auth { return request.Auth{Type: request.AuthOAuth2, OAuth2: o} }
	wantFolders := []request.Folder{
		{Path: "Identity", Auth: oauth2(request.OAuth2{
			Grant: request.GrantAuthorizationCode, AuthURL: "https://id.example.com/authorize",
			TokenURL: "https://id.example.com/token", ClientID: "web",
		})},
		{Path: "Identity/Partners", Auth: oauth2(request.OAuth2{
			Grant: request.GrantClientCredentials, TokenURL: "https://id.example.com/token", ClientID: "partner",
			ClientSecret: "{{partnerSecret}}", Scope: "orders:read", ClientAuth: "Body",
		})},
		{Path: "Identity/Legacy", Auth: request.Auth{Type: "None"}},
	}
	if !reflect.DeepEqual(got.Folders, wantFolders) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, wantFolders)
	}
	wantAuth := map[string]request.Auth{
		"partner": {Type: request.AuthInherit},
		"legacy":  {Type: request.AuthInherit},
		"pkce": oauth2(request.OAuth2{
			Grant: request.GrantAuthorizationCodePKCE, AuthURL: "https://id.example.com/authorize",
			TokenURL: "https://id.example.com/token", ClientID: "cli", Scope: "openid profile",
			RedirectURL: "http://localhost:8080/callback", HeaderPrefix: "Token",
		}),
		"code-https": oauth2(request.OAuth2{Grant: request.GrantAuthorizationCode, TokenURL: "https://id.example.com/token", ClientID: "cli"}),
		"password": oauth2(request.OAuth2{
			Grant: request.GrantPassword, TokenURL: "https://id.example.com/token", ClientID: "cli",
			Username: "{{user}}", Password: "{{password}}", RedirectURL: "http://[::1]:9000/cb",
		}),
		"implicit": {},
	}
	if len(got.Requests) != len(wantAuth) {
		t.Fatalf("got %d requests, want %d", len(got.Requests), len(wantAuth))
	}
	for _, r := range got.Requests {
		if want, ok := wantAuth[r.ID]; !ok || !reflect.DeepEqual(r.Auth, want) {
			t.Errorf("request %s: Auth = %+v, want %+v", r.ID, r.Auth, want)
		}
	}
	// The collection's and the code request's callbacks are not loopback
	// addresses; the client credentials folder never opens a browser.
	wantWarnings := []string{
		"1 OAuth 2.0 configuration adds the token to the query; vscroxy sends it in the Authorization header",
		"1 folder uses the OAuth 2.0 implicit grant; imported as no auth",
		"1 request uses the OAuth 2.0 implicit grant; imported as no auth",
		"2 OAuth 2.0 callback URLs are not loopback addresses; vscroxy signs in through http://127.0.0.1 instead",
	}
	if !slices.Equal(got.Warnings, wantWarnings) {
		t.Errorf("Warnings:\n got %q\nwant %q", got.Warnings, wantWarnings)
	}

	collection, err := postman.ReadCollection([]byte(`{"info": {"name": "Old", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "auth": {"type": "oauth2", "oauth2": [{"key": "grant_type", "value": "implicit"}]}, "item": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"The collection uses the OAuth 2.0 implicit grant; imported as no auth"}; !slices.Equal(collection.Warnings, want) {
		t.Errorf("collection Warnings = %q, want %q", collection.Warnings, want)
	}
}

func TestReadCollectionDigestAWSJWT(t *testing.T) {
	got, err := postman.ReadCollection([]byte(`{
  "info": {"name": "Signed", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "auth": {"type": "jwt", "jwt": {"algorithm": "HS512", "secret": "c2VjcmV0", "isSecretBase64Encoded": true, "payload": "{\"sub\": \"{{user}}\"}"}},
  "item": [
    {
      "name": "Legacy",
      "auth": {"type": "digest", "digest": [
        {"key": "username", "value": "{{user}}", "type": "string"},
        {"key": "password", "value": "{{vault:pw}}", "type": "string"},
        {"key": "realm", "value": "shop", "type": "string"},
        {"key": "nonce", "value": "abc", "type": "string"},
        {"key": "algorithm", "value": "SHA-256", "type": "string"},
        {"key": "qop", "value": "auth", "type": "string"},
        {"key": "nc", "value": "00000002", "type": "string"},
        {"key": "cnonce", "value": "xyz", "type": "string"},
        {"key": "opaque", "value": "op", "type": "string"},
        {"key": "disableRetryRequest", "value": true, "type": "boolean"}
      ]},
      "item": [{"id": "digest", "name": "Who", "request": {"url": "https://example.com/who"}}]
    },
    {
      "id": "aws", "name": "List", "request": {"url": "https://iam.amazonaws.com/?Action=ListUsers", "auth": {"type": "awsv4", "awsv4": [
        {"key": "accessKey", "value": "{{awsKey}}", "type": "string"},
        {"key": "secretKey", "value": "{{vault:aws}}", "type": "string"},
        {"key": "region", "value": "eu-west-1", "type": "string"},
        {"key": "service", "value": "iam", "type": "string"},
        {"key": "sessionToken", "value": "tok", "type": "string"},
        {"key": "addAuthDataToQuery", "value": true, "type": "boolean"}
      ]}}
    },
    {
      "id": "jwt", "name": "Token", "request": {"url": "https://example.com/me", "auth": {"type": "jwt", "jwt": [
        {"key": "algorithm", "value": "RS256", "type": "string"},
        {"key": "isSecretBase64Encoded", "value": false, "type": "boolean"},
        {"key": "privateKey", "value": "{{key}}", "type": "string"},
        {"key": "payload", "value": "{\"iss\": \"me\"}", "type": "string"},
        {"key": "addTokenTo", "value": "queryParam", "type": "string"},
        {"key": "headerPrefix", "value": "JWT", "type": "string"},
        {"key": "queryParamKey", "value": "jwt", "type": "string"},
        {"key": "header", "value": "{\"kid\": \"1\"}", "type": "string"}
      ]}}
    },
    {"id": "eddsa", "name": "Ed", "request": {"url": "https://example.com/ed", "auth": {"type": "jwt", "jwt": [{"key": "algorithm", "value": "EdDSA"}]}}}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	wantFolders := []request.Folder{
		{Path: "Signed", Auth: request.Auth{Type: request.AuthJWT, JWT: request.JWTAuth{Algorithm: "HS512", Secret: "c2VjcmV0", SecretBase64: true, Payload: `{"sub": "{{user}}"}`}}},
		{Path: "Signed/Legacy", Auth: request.Auth{Type: request.AuthDigest, Username: "{{user}}", Password: "{{vault:pw}}"}},
	}
	if !reflect.DeepEqual(got.Folders, wantFolders) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, wantFolders)
	}
	wantAuth := map[string]request.Auth{
		"digest": {Type: request.AuthInherit},
		"aws":    {Type: request.AuthAWS, AWS: request.AWSAuth{AccessKey: "{{awsKey}}", SecretKey: "{{vault:aws}}", Region: "eu-west-1", Service: "iam", SessionToken: "tok"}},
		"jwt": {Type: request.AuthJWT, JWT: request.JWTAuth{Algorithm: "RS256", PrivateKey: "{{key}}", Payload: `{"iss": "me"}`,
			Header: `{"kid": "1"}`, Prefix: "JWT", AddTo: "Query", QueryParam: "jwt"}},
		"eddsa": {},
	}
	if len(got.Requests) != len(wantAuth) {
		t.Fatalf("got %d requests, want %d", len(got.Requests), len(wantAuth))
	}
	for _, r := range got.Requests {
		if want, ok := wantAuth[r.ID]; !ok || !reflect.DeepEqual(r.Auth, want) {
			t.Errorf("request %s auth:\n got %+v\nwant %+v", r.ID, r.Auth, want)
		}
	}
	wantWarnings := []string{
		"1 AWS Signature configuration adds the signature to the query; vscroxy sends it in headers",
		"1 Digest auth configuration does not retry after the server's challenge; vscroxy always answers the challenge",
		"1 request uses a JWT signed with EdDSA; imported as no auth",
	}
	if !slices.Equal(got.Warnings, wantWarnings) {
		t.Errorf("Warnings:\n got %q\nwant %q", got.Warnings, wantWarnings)
	}
}

func TestDigestAWSJWTRoundTrip(t *testing.T) {
	folders := []request.Folder{
		{Path: "Signed", Auth: request.Auth{Type: request.AuthDigest, Username: "{{user}}", Password: "{{vault:pw}}"}},
	}
	jwt := request.JWTAuth{Algorithm: "ES384", Secret: "c2VjcmV0", SecretBase64: true, PrivateKey: "{{vault:jwt-key}}",
		Payload: "{\n  \"sub\": \"{{user}}\"\n}", Header: `{"kid":"k1"}`, Prefix: "JWT", AddTo: "Query", QueryParam: "jwt"}
	requests := []request.Request{
		{ID: "aws", Name: "AWS", Collection: "Signed", Method: "GET", URL: "https://s3.amazonaws.com/b", Auth: request.Auth{Type: request.AuthAWS, AWS: request.AWSAuth{
			AccessKey: "{{key}}", SecretKey: "{{vault:aws}}", Region: "eu-west-1", Service: "s3", SessionToken: "tok"}}},
		{ID: "jwt", Name: "JWT", Collection: "Signed", Method: "GET", URL: "https://example.com/", Auth: request.Auth{Type: request.AuthJWT, JWT: jwt}},
		// Empty JWT fields are written as the values they mean.
		{ID: "jwt-defaults", Name: "JWT defaults", Collection: "Signed", Method: "GET", URL: "https://example.com/", Auth: request.Auth{Type: request.AuthJWT}},
	}
	data, warnings, err := postman.WriteCollection("Signed", requests, folders)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("%v, warnings %q", err, warnings)
	}
	compact := strings.Join(strings.Fields(string(data)), "")
	for _, s := range []string{`"type":"digest"`, `"type":"awsv4"`, `"key":"isSecretBase64Encoded","value":true,"type":"boolean"`, `"key":"addTokenTo","value":"queryParam"`} {
		if !strings.Contains(compact, s) {
			t.Errorf("export lacks %q:\n%s", s, data)
		}
	}
	got, err := postman.ReadCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("re-import warnings = %q", got.Warnings)
	}
	if !reflect.DeepEqual(got.Folders, folders) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, folders)
	}
	requests[2].Auth.JWT = request.JWTAuth{Algorithm: "HS256", Payload: "{}", Header: "{}", Prefix: "Bearer", QueryParam: "token"}
	if !reflect.DeepEqual(got.Requests, requests) {
		t.Errorf("Requests:\n got %+v\nwant %+v", got.Requests, requests)
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
		{Path: "Shop/Partners", Auth: request.Auth{Type: request.AuthOAuth2, OAuth2: request.OAuth2{
			Grant: request.GrantClientCredentials, TokenURL: "https://id.example.com/token", ClientID: "svc",
			ClientSecret: "{{vault:partner-secret}}", Scope: "read write", ClientAuth: "Body",
		}}},
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
			Examples: []request.Example{{
				ID: "e1", Name: "Created", Status: 201, Headers: "Content-Type: application/json\nLocation: /orders/7", Body: `{"id":7}`,
				Request: request.ExampleRequest{Method: "POST", URL: "{{base}}/orders", Headers: "Content-Type: application/json", Body: `{"a": 1}`},
			}},
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
		{ID: "r7", Name: "Partner orders", Collection: "Shop/Partners", Method: "GET", URL: "{{base}}/partner", Auth: request.Auth{Type: request.AuthInherit}},
		{
			ID: "r8", Name: "PKCE default", Collection: "Shop", Method: "GET", URL: "{{base}}/me",
			Auth: request.Auth{Type: request.AuthOAuth2, Token: "transient-access-token", OAuth2: request.OAuth2{
				AuthURL: "https://id.example.com/authorize", TokenURL: "https://id.example.com/token", ClientID: "app",
				Scope: "openid", RedirectURL: "http://127.0.0.1:8765/callback", HeaderPrefix: "Token",
			}},
		},
		{
			ID: "r9", Name: "Code", Collection: "Shop", Method: "GET", URL: "{{base}}/me",
			Auth: request.Auth{Type: request.AuthOAuth2, OAuth2: request.OAuth2{
				Grant: request.GrantAuthorizationCode, AuthURL: "https://id.example.com/authorize", TokenURL: "https://id.example.com/token",
				ClientID: "app", ClientSecret: "s3cret",
			}},
		},
		{
			ID: "r10", Name: "Password", Collection: "Shop", Method: "GET", URL: "{{base}}/me",
			Auth: request.Auth{Type: request.AuthOAuth2, OAuth2: request.OAuth2{
				Grant: request.GrantPassword, TokenURL: "https://id.example.com/token", ClientID: "app",
				Username: "{{user}}", Password: "{{password}}", ClientAuth: "Body",
			}},
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
	if strings.Contains(string(data), "transient-access-token") {
		t.Errorf("export contains the OAuth 2.0 access token:\n%s", data)
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
	if want := []string{"r4", "r3", "r1", "r7", "r2", "r5", "r6", "r8", "r9", "r10"}; !slices.Equal(order, want) {
		t.Errorf("request order = %q, want %q", order, want)
	}
	want := map[string]request.Request{}
	for _, r := range requests[:10] {
		want[r.ID] = r
	}
	// The access token is never exported, and the default grant comes back named.
	r8 := want["r8"]
	r8.Auth.Token, r8.Auth.OAuth2.Grant = "", request.GrantAuthorizationCodePKCE
	want["r8"] = r8
	r6 := want["r6"]
	r6.HeaderList, r6.Checks = []request.Field{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}}, request.Checks{}
	want["r6"] = r6
	for _, r := range got.Requests {
		if !reflect.DeepEqual(r, want[r.ID]) {
			t.Errorf("request %s:\n got %+v\nwant %+v", r.ID, r, want[r.ID])
		}
	}
	if !reflect.DeepEqual(got.Folders, folders[:4]) {
		t.Errorf("Folders:\n got %+v\nwant %+v", got.Folders, folders[:4])
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
