package request

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/vault"
)

// One send through a collection and a folder: inherited auth from the vault,
// scopes resolving nearest first, and scripts collection → folder → request.
func TestExecuteInheritsFromCollectionAndFolder(t *testing.T) {
	var got http.Header
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.RequestURI()
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	srv := testProxy(t)
	secrets := vault.Secrets{{Name: "token", Value: "from-vault", Domains: []string{"127.0.0.1"}}}
	in := Context{
		Globals: map[string]string{"base": "http://global.invalid", "region": "global", "only": "g"},
		Folders: []Folder{
			{Path: "Shop", Auth: Auth{Type: "Bearer", Token: "{{vault:token}}"},
				Variables: []Field{{Name: "base", Value: "http://collection.invalid"}, {Name: "region", Value: "eu"}, {Name: "off", Value: "x", Disabled: true}},
				Pre:       `vars.set("order", vars.get("order") + "C")`, Post: `vars.set("post", vars.get("post") + "C")`},
			{Path: "Shop/Orders", Auth: Auth{Type: AuthInherit},
				Variables: []Field{{Name: "base", Value: upstream.URL}},
				Pre:       `vars.set("order", vars.get("order") + "F")`, Post: `vars.set("post", vars.get("post") + "F")`},
		},
		Vault: secrets.Lookup,
	}
	r := Request{Method: "GET", Collection: "Shop/Orders", Auth: Auth{Type: AuthInherit},
		URL:     "{{base}}/orders?a={{$guid}}&b={{$guid}}",
		Headers: "X-Order: {{order}}\nX-Region: {{region}}\nX-Only: {{only}}\nX-Env: {{env}}\nX-Off: {{off}}",
		Pre:     `vars.set("order", vars.get("order") + "R")`, Post: `vars.set("post", vars.get("post") + "R")`}
	out := r.Execute(t.Context(), srv, in, map[string]string{"env": "dev", "region": "env-wins"}, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for name, want := range map[string]string{
		"Authorization": "Bearer from-vault", // Inherit skipped the folder up to the collection
		"X-Order":       "CFR",               // pre-request scripts, collection first
		"X-Region":      "env-wins",          // environment over collection over global
		"X-Only":        "g",                 // globals fill what nothing else defines
		"X-Env":         "dev",
		"X-Off":         "{{off}}", // a disabled variable is not defined
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
	guids := regexp.MustCompile(`a=([0-9a-f-]{36})&b=([0-9a-f-]{36})$`).FindStringSubmatch(path)
	if guids == nil || guids[1] == guids[2] {
		t.Errorf("each {{$guid}} must be a fresh UUID: %q", path)
	}
	if out.Vars["post"] != "CFR" {
		t.Errorf("post-response scripts ran %q, want CFR", out.Vars["post"])
	}
	// Scripts' writes and the environment come back; inherited and vault
	// values never leak into what the app saves to the environment.
	for _, name := range []string{"base", "only", "token", "vault:token"} {
		if _, ok := out.Vars[name]; ok {
			t.Errorf("Execution.Vars carries inherited %q", name)
		}
	}
	sources := map[string]string{}
	for _, v := range out.Variables {
		sources[v.Name] = v.Source
		if v.Name == "vault:token" && v.Value != "" {
			t.Error("a vault value was copied into the resolved variables")
		}
	}
	for name, want := range map[string]string{"base": "Collection: Shop/Orders", "only": "Global", "vault:token": "Vault", "order": "Pre-request script", "env": "Run input"} {
		if sources[name] != want {
			t.Errorf("source of %s = %q, want %q", name, sources[name], want)
		}
	}
}

func TestVaultSecretNeverReachesAnotherHost(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer upstream.Close()
	secrets := vault.Secrets{{Name: "token", Value: "s", Domains: []string{"api.example.com"}}}
	r := Request{Method: "GET", URL: upstream.URL, Headers: "Authorization: Bearer {{vault:token}}"}
	out := r.Execute(t.Context(), testProxy(t), Context{Vault: secrets.Lookup}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "not allowed") || hits.Load() != 0 {
		t.Fatalf("err = %v, upstream hits = %d; want a refusal before sending", out.Err, hits.Load())
	}
	out = r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || hits.Load() != 0 {
		t.Fatalf("without a vault: err = %v, hits = %d", out.Err, hits.Load())
	}
}

func TestChainAndInheritAuth(t *testing.T) {
	folders := []Folder{{Path: "Shop/Orders/Refunds"}, {Path: "Shop", Auth: Auth{Type: "Basic"}}, {Path: "Shop/Orders", Auth: Auth{Type: AuthInherit}}, {Path: "Shopping", Auth: Auth{Type: "Bearer"}}}
	var paths []string
	for _, f := range Chain(folders, "Shop/Orders/Refunds") {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "Shop,Shop/Orders,Shop/Orders/Refunds" {
		t.Fatalf("Chain = %q", paths)
	}
	in := Context{Folders: Chain(folders, "Shop/Orders/Refunds")}
	if auth, from := in.InheritAuth(Auth{Type: AuthInherit}); auth.Type != "Basic" || from != "Shop" {
		t.Errorf("inherited %q from %q, want Basic from Shop", auth.Type, from)
	}
	if auth, _ := in.InheritAuth(Auth{Type: "None"}); auth.Type != "None" {
		t.Error("an explicit auth must not inherit")
	}
	if auth, from := (Context{}).InheritAuth(Auth{Type: AuthInherit}); auth.Type != "None" || from != "" {
		t.Error("nothing to inherit must send no auth")
	}
}
