package app

import (
	"encoding/json/v2"
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
	f := &store.File{Path: s.path, Format: store.OAuthTokens, BackupDir: store.BackupDirFor(s.path)}
	data, err := f.Read()
	if err != nil {
		s.problem = err.Error()
		return err
	}
	if data == nil {
		s.loaded, s.problem = true, ""
		return nil
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
			f := &store.File{Path: s.path, Format: store.OAuthTokens, BackupDir: store.BackupDirFor(s.path)}
			if _, err = f.Read(); err == nil {
				err = f.Write(sealed)
			}
		}
	}
	if err != nil {
		s.problem = err.Error()
		return
	}
	s.loaded, s.problem = true, ""
}
