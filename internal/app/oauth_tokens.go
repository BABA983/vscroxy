package app

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"sync"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/vault"
)

// oauthTokenStore keeps OAuth 2.0 tokens in a file sealed with the vault key.
// It is loaded on first use, so starting the app does not touch the keychain.
type oauthTokenStore struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	problem string
}

// ensureOAuthTokens opens the sealed token file once. A missing file is an
// empty store; a damaged file is reported and leaves the in-memory tokens
// usable, but is never replaced until a token changes.
func (a *app) ensureOAuthTokens() error {
	s := &a.oauthTokens
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return nil
	}
	if s.path == "" {
		// Headless tests have no store; an empty list is the honest view.
		s.loaded = true
		return nil
	}
	if err := a.ensureVault(); err != nil {
		s.problem = err.Error()
		return err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.loaded, s.problem = true, ""
		return nil
	}
	if err != nil {
		s.problem = err.Error()
		return err
	}
	plain, err := vault.OpenData(a.vault.key, data)
	if err == nil {
		var list []request.Token
		if err = json.Unmarshal(plain, &list); err == nil {
			a.tokens.Load(list)
			s.loaded, s.problem = true, ""
			return nil
		}
	}
	s.problem = err.Error()
	return err
}

// saveOAuthTokens seals a snapshot for the token store. It is the Tokens
// callback, so a refreshed token is written as soon as it arrives.
func (a *app) saveOAuthTokens(list []request.Token) {
	s := &a.oauthTokens
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return
	}
	if err := a.ensureVault(); err != nil {
		s.problem = err.Error()
		return
	}
	plain, err := json.Marshal(list)
	if err == nil {
		var sealed []byte
		if sealed, err = vault.SealData(a.vault.key, plain); err == nil {
			err = store.WriteFile(s.path, sealed)
		}
	}
	if err != nil {
		s.problem = err.Error()
		return
	}
	s.loaded, s.problem = true, ""
}
