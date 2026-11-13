package app

import (
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// vaultKeyName is the vault key's entry in the system's credential store.
const vaultKeyName = "vault-key"

// credentials is where the vault key lives: the system's credential store,
// replaced in tests so they never touch the user's keychain.
var credentials interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
} = mygo.Secrets

// vaultState is the vault, opened on first use: reading the key may ask the
// user to unlock their keychain, so starting the app never does.
type vaultState struct {
	path, keyPath string
	loaded        bool
	key           []byte
	secrets       vault.Secrets
	// keyInFile says the key is in keyPath: this system has no credential
	// store, so the file's permissions are its only protection.
	keyInFile bool
	problem   string

	// scripts lets request scripts use pm.vault; scriptsDraft is the
	// dialog's unsaved choice.
	scripts, scriptsDraft bool

	open        bool
	rows        []vaultRow
	editProblem string
	resetting   bool
}

type vaultRow struct {
	name, value, domains string
	revealed             bool
}

// ensureVault opens the vault, making a new key and an empty vault when
// there is neither. A vault file whose key is gone is reported, never
// replaced, so its secrets are not silently lost.
func (a *app) ensureVault() error {
	v := &a.vault
	if v.loaded {
		return nil
	}
	data, err := a.file(v.path).Read()
	exists := data != nil
	if err != nil {
		return a.vaultFailed(err)
	}
	key, inFile, err := a.vaultKey(!exists)
	if err != nil {
		return a.vaultFailed(err)
	}
	var secrets vault.Secrets
	if exists {
		if secrets, err = vault.Open(key, data); err != nil {
			return a.vaultFailed(err)
		}
	}
	v.loaded, v.key, v.keyInFile, v.secrets, v.problem = true, key, inFile, secrets, ""
	return nil
}

func (a *app) vaultFailed(err error) error {
	a.vault.problem = err.Error()
	return err
}

// vaultKey reads the key from the credential store, or the key file when the
// system has none; create makes and keeps a new one when neither holds it.
func (a *app) vaultKey(create bool) (key []byte, inFile bool, err error) {
	v := &a.vault
	key, err = credentials.Get(vaultKeyName)
	unsupported := errors.Is(err, errors.ErrUnsupported)
	switch {
	case err == nil:
		return key, false, nil
	case !errors.Is(err, mygo.ErrSecretNotFound) && !unsupported:
		return nil, false, fmt.Errorf("read the vault key: %w", err)
	}
	if key, err := os.ReadFile(v.keyPath); err == nil {
		return key, true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	if !create {
		return nil, false, errors.New("the vault key is missing from this system's credential store, so the vault cannot be opened")
	}
	key = vault.NewKey()
	if err := credentials.Set(vaultKeyName, key); err == nil {
		return key, false, nil
	} else if !errors.Is(err, errors.ErrUnsupported) {
		return nil, false, fmt.Errorf("keep the vault key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(v.keyPath), 0o700); err != nil {
		return nil, false, err
	}
	return key, true, store.WriteFile(v.keyPath, key)
}

// sendContext is inheritance plus the vault, opened when the request, or
// the auth it inherits, reads a {{vault:name}}. Lookups use a snapshot, so a
// send in flight is unaffected by later edits.
func (a *app) sendContext(r request.Request) request.Context {
	in := a.inheritance(r.Collection)
	in.Environment, in.ScriptVault, in.Tokens = a.activeEnv, a.vault.scripts, &a.tokens
	in.Certificates = a.certificates.Clone()
	in.EnvironmentInitial = maps.Clone(a.activeEnvironment().Vars)
	r.Auth, _ = in.InheritAuth(r.Auth)
	names := r.Variables()
	if r.Auth.Type == request.AuthOAuth2 {
		names = append(names, r.Auth.OAuth2.Variables()...)
	}
	reads := slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, "vault:") })
	if reads || in.ScriptVault {
		if err := a.ensureVault(); err != nil {
			in.Vault = func(string, string) (string, error) { return "", fmt.Errorf("the vault is locked: %w", err) }
			return in
		}
	}
	if a.vault.loaded {
		in.Vault = slices.Clone(a.vault.secrets).Lookup
	}
	return in
}

func (a *app) openVault() {
	v := &a.vault
	v.open, v.editProblem, v.resetting, v.rows, v.scriptsDraft = true, "", false, nil, v.scripts
	if a.ensureVault() != nil {
		return
	}
	for _, s := range v.secrets {
		v.rows = append(v.rows, vaultRow{name: s.Name, value: s.Value, domains: strings.Join(s.Domains, ", ")})
	}
}

func (a *app) saveVault() {
	v := &a.vault
	var secrets vault.Secrets
	seen := map[string]bool{}
	for _, row := range v.rows {
		name := strings.TrimSpace(row.name)
		if name == "" && row.value == "" && row.domains == "" {
			continue
		}
		if name == "" || seen[name] || strings.ContainsAny(name, "{} ") {
			v.editProblem = "Secret names must be nonempty, unique and contain no spaces or braces."
			return
		}
		seen[name] = true
		var domains []string
		for d := range strings.SplitSeq(row.domains, ",") {
			if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
				domains = append(domains, d)
			}
		}
		secrets = append(secrets, vault.Secret{Name: name, Value: row.value, Domains: domains})
	}
	if err := a.writeVault(secrets); err != nil {
		v.editProblem = "Save failed: " + err.Error()
		return
	}
	v.open, v.scripts = false, v.scriptsDraft
	a.persistSettings()
	a.done(locales.VaultSaved(len(secrets)))
}

// writeVault seals and saves the secrets and makes them the ones in use.
func (a *app) writeVault(secrets vault.Secrets) error {
	v := &a.vault
	data, err := vault.Seal(v.key, secrets)
	if err == nil {
		err = a.file(v.path).Write(data)
	}
	if err == nil {
		v.secrets = secrets
	}
	return err
}

// resetVault deletes a vault nobody can open, so a new one can start.
func (a *app) resetVault() {
	v := &a.vault
	if err := os.Remove(v.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		v.problem = err.Error()
		return
	}
	a.file(v.path).Read() // the vault is gone, so a new one may be written
	v.loaded, v.problem, v.resetting = false, "", false
	a.openVault()
}

func (a *app) vaultModal(c *ui.Context, t *ui.Theme) {
	v := &a.vault
	if !v.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &v.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(520, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.Vault2())
			uikit.Meta(c, locales.UseVaultNameInA())
			if v.problem != "" {
				ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
					ui.Text(c, v.problem).FontSize(uikit.TextBody).TextColor(t.Danger).Selectable()
				})
				uikit.DialogFooter(c).Children(func() {
					if uikit.SecondaryButton(c, locales.Close()).Clicked() {
						v.open = false
					}
					if !v.resetting {
						if uikit.SecondaryButton(c, locales.ResetVault()).Clicked() {
							v.resetting = true
						}
					} else if uikit.PrimaryButton(c, locales.DeleteAllSecretsAndStart()).Clicked() {
						a.resetVault()
					}
				})
				return
			}
			where := "Encrypted with a key kept in this system's credential store."
			if v.keyInFile {
				where = "Encrypted with a key kept in " + v.keyPath + ": this system has no credential store, so anyone who can read that file can open the vault."
			}
			uikit.Meta(c, where)
			// The rows' scroll leaves the same side room for input halos.
			ui.Row(c).Gap(t.Space(2)).Padding(0, t.Space(2), 0, t.Space(1)).Children(func() {
				for i, label := range []string{"NAME", "VALUE", "ALLOWED DOMAINS"} {
					grow := float32(2)
					if i == 0 {
						grow = 1
					}
					ui.Box(c).Basis(0).Grow(grow).MinWidth(0).Children(func() {
						uikit.Label(c, label).FillWidth().Padding(0, t.Space(2))
					})
				}
				ui.Box(c).Width(76) // the two row buttons and the gap between them
			})
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(2)).Padding(0, t.Space(2), 0, t.Space(1)).Children(func() {
				remove := -1
				for i := range v.rows {
					r := &v.rows[i]
					ui.Row(c.Key(i)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Basis(0).Grow(1).MinWidth(0).Children(func() {
							uikit.Input(c, &r.name).Label(locales.SecretName(i + 1)).Placeholder("api_token").FillWidth().MinWidth(0)
						})
						ui.Box(c).Basis(0).Grow(2).MinWidth(0).Children(func() {
							value := uikit.Input(c, &r.value).Label(locales.SecretValue(i + 1)).Placeholder(locales.Value()).FillWidth().MinWidth(0)
							if !r.revealed {
								value.Password()
							}
						})
						ui.Box(c).Basis(0).Grow(2).MinWidth(0).Children(func() {
							uikit.Input(c, &r.domains).Label(locales.AllowedDomains(i + 1)).Placeholder(locales.AnyHostOrApiExample()).FillWidth().MinWidth(0)
						})
						label := "Reveal"
						if r.revealed {
							label = "Hide"
						}
						if uikit.Tip(c, uikit.SecondaryButton(c, "◉").Label(locales.SecretRow(label, i+1)), locales.SecretRow(label, i+1)).Width(34).Padding(0).Clicked() {
							r.revealed = !r.revealed
						}
						if uikit.Tip(c, uikit.SecondaryButton(c, "×").Label(locales.RemoveSecret(i+1)), locales.RemoveSecret(i+1)).Width(34).Padding(0).Clicked() {
							remove = i
						}
					})
				}
				if remove >= 0 {
					v.rows = slices.Delete(v.rows, remove, remove+1)
				}
				if uikit.SecondaryButton(c, locales.AddSecret()).Clicked() {
					v.rows = append(v.rows, vaultRow{})
				}
			})
			ui.Checkbox(c, &v.scriptsDraft, "Let scripts use pm.vault").
				Tooltip(locales.ScriptsCanThenReadAny())
			if v.editProblem != "" {
				ui.Text(c, v.editProblem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					v.open = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Label(locales.SaveVault()).Clicked() {
					a.saveVault()
				}
			})
		})
	})
}
