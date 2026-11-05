package request

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSocketIOPrepareScopesAndVault(t *testing.T) {
	r := Request{Protocol: "Socket.IO", URL: "https://{{host}}/socket.io/", Body: `["{{value}}"]`, BodyMode: "Binary", BodyFile: "{{unused-file}}", Auth: Auth{Type: AuthInherit}, SocketIO: SocketIOConfig{Namespace: "/{{ns}}", Auth: `{"token":"{{vault:socket-token}}"}`, Event: "{{event}}", Attachments: `["{{bytes}}"]`}}
	if got := r.Variables(); !slices.Equal(got, []string{"bytes", "event", "host", "ns", "value", "vault:socket-token"}) {
		t.Fatalf("Socket.IO arguments must ignore the former HTTP body mode: %v", got)
	}
	in := Context{Folders: []Folder{{Auth: Auth{Type: "Bearer", Token: "{{token}}"}, Variables: []Variable{{Name: "ns", Initial: "admin"}}}}, Globals: map[string]string{"value": "global"}, Vault: func(name, host string) (string, error) {
		if name != "socket-token" || host != "api.example.test" {
			return "", fmt.Errorf("host denied")
		}
		return "local-secret", nil
	}}
	vars := map[string]string{"host": "api.example.test", "value": "environment", "event": "bytes", "bytes": "AP9J", "token": "header-token"}
	p, cfg, err := r.PrepareSocketIO(in, vars)
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != "https://api.example.test/socket.io/" || p.Header.Get("Authorization") != "Bearer header-token" || p.Body != `["environment"]` || cfg.Namespace != "/admin" || cfg.Auth != `{"token":"local-secret"}` || cfg.Event != "bytes" || cfg.Attachments != `["AP9J"]` {
		t.Fatalf("prepared %+v %+v", p, cfg)
	}
	if vars["vault:socket-token"] != "" || r.SocketIO.Auth != `{"token":"{{vault:socket-token}}"}` {
		t.Fatal("preparation mutated saved templates or environment")
	}
	r.URL = "https://wrong.example/socket.io/"
	if _, _, err := r.PrepareSocketIO(in, vars); err == nil || !strings.Contains(err.Error(), "host denied") {
		t.Fatal("Vault host restriction bypassed")
	}
	for _, r := range []Request{{Protocol: "Socket.IO", URL: "https://api.test", Pre: "throw 1"}, {Protocol: "Socket.IO", URL: "https://api.test", SocketIO: SocketIOConfig{Auth: `{"token":"{{missing}}"}`}}} {
		if _, _, err := r.PrepareSocketIO(Context{}, nil); err == nil {
			t.Fatal("unsupported scripts or unresolved auth silently accepted")
		}
	}
	if _, err := r.Prepare(nil); err == nil {
		t.Fatal("Socket.IO routed through HTTP")
	}
}
