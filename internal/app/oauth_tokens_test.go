package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
)

// The token file is sealed with the vault key: the access token must not
// appear in the bytes on disk, and another key must not open it.
func TestOAuthTokensAreSealedAndReloaded(t *testing.T) {
	dir := t.TempDir()
	key := vault.NewKey()
	a := &app{}
	a.vault.loaded, a.vault.key = true, key
	a.oauthTokens.path = filepath.Join(dir, "oauth-tokens.json")
	a.tokens.SetSave(a.saveOAuthTokens)
	a.tokens.Load([]request.Token{{
		Access: "access-secret", Refresh: "refresh-secret", Key: "k1", Label: "Client credentials · app · login.example.com",
		Config: request.OAuth2{Grant: request.GrantClientCredentials, TokenURL: "https://login.example.com/token", ClientID: "app"},
	}})
	a.saveOAuthTokens(a.tokens.All())
	data, err := os.ReadFile(a.oauthTokens.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("access-secret")) || bytes.Contains(data, []byte("refresh-secret")) {
		t.Fatalf("the token file is plaintext: %q", data)
	}

	b := &app{}
	b.vault.loaded, b.vault.key = true, key
	b.oauthTokens.path = a.oauthTokens.path
	if err := b.ensureOAuthTokens(); err != nil {
		t.Fatal(err)
	}
	list := b.tokens.All()
	if len(list) != 1 || list[0].Access != "access-secret" || list[0].Config.ClientID != "app" || list[0].Label == "" {
		t.Fatalf("reloaded tokens = %+v", list)
	}

	c := &app{}
	c.vault.loaded, c.vault.key = true, vault.NewKey()
	c.oauthTokens.path = a.oauthTokens.path
	if err := c.ensureOAuthTokens(); err == nil {
		t.Fatal("another key opened the token file")
	}
}

func TestOAuthTokenDeletePersists(t *testing.T) {
	a := &app{}
	a.vault.loaded, a.vault.key = true, vault.NewKey()
	a.oauthTokens.path = filepath.Join(t.TempDir(), "oauth-tokens.json")
	a.tokens.SetSave(a.saveOAuthTokens)
	a.tokens.Load([]request.Token{{Access: "one", Key: "k1"}, {Access: "two", Key: "k2"}})
	if !a.tokens.Delete("k1") {
		t.Fatal("delete missed the token")
	}
	b := &app{}
	b.vault.loaded, b.vault.key = true, a.vault.key
	b.oauthTokens.path = a.oauthTokens.path
	if err := b.ensureOAuthTokens(); err != nil {
		t.Fatal(err)
	}
	if list := b.tokens.All(); len(list) != 1 || list[0].Key != "k2" {
		t.Fatalf("after delete = %+v", list)
	}
}
