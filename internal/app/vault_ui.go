package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
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
	data, err := os.ReadFile(v.path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	return key, true, writePrivateFile(v.keyPath, key)
}

// sendContext is inheritance plus the vault, opened when the request, or
// the auth it inherits, reads a {{vault:name}}. Lookups use a snapshot, so a
// send in flight is unaffected by later edits.
func (a *app) sendContext(r request.Request) request.Context {
	in := a.inheritance(r.Collection)
	r.Auth, _ = in.InheritAuth(r.Auth)
	reads := slices.ContainsFunc(r.Variables(), func(name string) bool { return strings.HasPrefix(name, "vault:") })
	if reads {
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
	v.open, v.editProblem, v.resetting, v.rows = true, "", false, nil
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
	data, err := vault.Seal(v.key, secrets)
	if err == nil {
		err = writePrivateFile(v.path, data)
	}
	if err != nil {
		v.editProblem = "Save failed: " + err.Error()
		return
	}
	v.secrets, v.open = secrets, false
	a.setMessage(fmt.Sprintf("Vault saved · %d %s", len(secrets), plural(len(secrets), "secret", "secrets")))
}

// resetVault deletes a vault nobody can open, so a new one can start.
func (a *app) resetVault() {
	v := &a.vault
	if err := os.Remove(v.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		v.problem = err.Error()
		return
	}
	v.loaded, v.problem, v.resetting = false, "", false
	a.openVault()
}

func (a *app) vaultModal(c *ui.Context, t *ui.Theme) {
	v := &a.vault
	if !v.open {
		return
	}
	width, height := c.Size()
	appModal(c, &v.open, func() {
		ui.Column(c).Width(min(760, width-64)).Height(min(520, height-80)).Gap(10).Children(func() {
			dialogTitle(c, "Vault")
			ui.Text(c, "Use {{vault:name}} in a URL, header, body or auth field. A secret with domains is sent only to those hosts.").FontSize(11).TextColor(t.TextMuted)
			if v.problem != "" {
				ui.Text(c, v.problem).FontSize(12).TextColor(t.Danger).Selectable()
				ui.Spacer(c)
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if ui.Button(c, "Close").Clicked() {
						v.open = false
					}
					if !v.resetting {
						if ui.Button(c, "Reset vault…").Clicked() {
							v.resetting = true
						}
					} else if ui.PrimaryButton(c, "Delete all secrets and start over").Clicked() {
						a.resetVault()
					}
				})
				return
			}
			where := "Encrypted with a key kept in this system's credential store."
			if v.keyInFile {
				where = "Encrypted with a key kept in " + v.keyPath + ": this system has no credential store, so anyone who can read that file can open the vault."
			}
			ui.Text(c, where).FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(6).Padding(0, 8, 2, 0).Children(func() {
				ui.Text(c, "NAME").Grow(1).FontSize(10).Bold().TextColor(t.TextMuted)
				ui.Text(c, "VALUE").Grow(2).FontSize(10).Bold().TextColor(t.TextMuted)
				ui.Text(c, "ALLOWED DOMAINS").Grow(2).FontSize(10).Bold().TextColor(t.TextMuted)
				ui.Box(c).Width(74) // the two row buttons and the gap between them
			})
			ui.Scroll(c).Grow(1).FillWidth().Gap(6).Padding(0, 8, 0, 0).Children(func() {
				remove := -1
				for i := range v.rows {
					r := &v.rows[i]
					ui.Row(c).Key(i).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.TextInput(c, &r.name).Label(fmt.Sprintf("Secret name %d", i+1)).Placeholder("api_token").Grow(1)
						value := ui.TextInput(c, &r.value).Label(fmt.Sprintf("Secret value %d", i+1)).Placeholder("Value").Grow(2)
						if !r.revealed {
							value.Password()
						}
						ui.TextInput(c, &r.domains).Label(fmt.Sprintf("Allowed domains %d", i+1)).Placeholder("Any host, or api.example.com, *.example.com").Grow(2)
						label := "Reveal"
						if r.revealed {
							label = "Hide"
						}
						if ui.Button(c, "◉").Label(fmt.Sprintf("%s secret %d", label, i+1)).Width(34).Clicked() {
							r.revealed = !r.revealed
						}
						if ui.Button(c, "×").Label(fmt.Sprintf("Remove secret %d", i+1)).Width(34).Clicked() {
							remove = i
						}
					})
				}
				if remove >= 0 {
					v.rows = slices.Delete(v.rows, remove, remove+1)
				}
				if ui.Button(c, "Add secret").Clicked() {
					v.rows = append(v.rows, vaultRow{})
				}
			})
			if v.editProblem != "" {
				ui.Text(c, v.editProblem).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					v.open = false
				}
				if ui.PrimaryButton(c, "Save").Label("Save vault").Clicked() {
					a.saveVault()
				}
			})
		})
	})
}
