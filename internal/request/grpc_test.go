package request

import (
	"fmt"
	"strings"
	"testing"
)

func TestPrepareGRPCScopesVaultAndHTTPBoundaries(t *testing.T) {
	r := Request{Protocol: "gRPC", Collection: "API", URL: "https://{{host}}", Body: `{"name":"{{name}}"}`,
		Headers: "x-tag: {{tag}}", Auth: Auth{Type: AuthInherit}, BodyMode: "Binary", BodyFile: "/not/read"}
	in := Context{Globals: map[string]string{"host": "wrong.example", "name": "global"}, Folders: []Folder{{Path: "API", Auth: Auth{Type: "Bearer", Token: "{{vault:token}}"}, Variables: []Variable{{Name: "tag", Initial: "folder-73"}}}}, Vault: func(name, host string) (string, error) {
		if name != "token" || host != "api.example" {
			return "", fmt.Errorf("host not authorized")
		}
		return "secret-local", nil
	}}
	vars := map[string]string{"host": "api.example", "name": "environment-19"}
	got, err := r.PrepareGRPC(in, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://api.example" || got.Body != `{"name":"environment-19"}` || got.Header.Get("Authorization") != "Bearer secret-local" || got.Header.Get("X-Tag") != "folder-73" {
		t.Fatalf("wrong scopes: %+v", got)
	}
	if _, exists := vars["vault:token"]; exists {
		t.Fatal("vault secret leaked into environment values")
	}
	vars["host"] = "other.example"
	if _, err := r.PrepareGRPC(in, vars); err == nil || !strings.Contains(err.Error(), "host not authorized") {
		t.Fatalf("vault host restriction: %v", err)
	}
	if _, err := r.Prepare(vars); err == nil {
		t.Fatal("HTTP Prepare accepted a gRPC document")
	}
	r.Pre = `throw new Error("must not run");`
	out := r.Execute(t.Context(), nil, in, vars, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "interactive protocol client") || out.Stage != "Request setup failed" {
		t.Fatalf("Runner executed HTTP code on a gRPC document: %+v", out)
	}
	for _, tc := range []struct {
		name string
		r    Request
		in   Context
	}{
		{"missing", Request{Protocol: "gRPC", URL: "https://example", Body: `"{{missing}}"`}, Context{}},
		{"inherited scripts", Request{Protocol: "gRPC", URL: "https://example"}, Context{Folders: []Folder{{Pre: "console.log(1)"}}}},
		{"query auth", Request{Protocol: "gRPC", URL: "https://example", Auth: Auth{Type: "API key", Location: "Query"}}, Context{}},
		{"bad metadata", Request{Protocol: "gRPC", URL: "https://example", Headers: "ignored-secret"}, Context{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.r.PrepareGRPC(tc.in, nil); err == nil {
				t.Fatal("accepted unsupported or malformed input")
			}
		})
	}
}
