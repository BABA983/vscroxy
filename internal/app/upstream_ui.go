package app

// Upstream settings: a default proxy, first-match host rules, a bypass list,
// a PAC script and per-endpoint credentials. Passwords are sealed beside the
// settings file and never written into settings.json, a configuration export
// or the log.

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/egoist/mygo/ui"
)

// upstreamEditor is the Settings draft. It is read from the active
// configuration on the first frame after opening, and kept while the user
// edits.
type upstreamEditor struct {
	loaded  bool
	dirty   bool
	saving  bool
	problem string

	defaultAddr string
	rules       string
	bypass      string
	pac         string
	credentials []upstreamCredentialRow
}

type upstreamCredentialRow struct {
	Proxy    string
	Username string
	Password string
	// Saved says a sealed password exists for this endpoint; Saved with an
	// empty Password shows "Saved password in use".
	Saved bool
	// Clear asks Save to remove the sealed password.
	Clear bool
}

func (u *upstreamEditor) load(a *app) {
	if u.loaded {
		return
	}
	u.loaded = true
	u.defaultAddr = a.upstreamCfg.Default
	u.rules = formatUpstreamRules(a.upstreamCfg.Rules)
	u.bypass = strings.Join(a.upstreamCfg.Bypass, "\n")
	u.pac = a.upstreamCfg.PAC
	u.credentials = nil
	for _, c := range a.upstreamCfg.Credentials {
		key, err := upstream.CanonicalAddress(c.Proxy)
		if err != nil {
			key = c.Proxy
		}
		saved := a.upstreamSecrets[key]
		u.credentials = append(u.credentials, upstreamCredentialRow{Proxy: key, Username: c.Username, Saved: saved != ""})
	}
	u.dirty = false
}

func formatUpstreamRules(rules []upstream.Rule) string {
	lines := make([]string, len(rules))
	for i, r := range rules {
		lines[i] = r.Host + " " + r.Proxy
	}
	return strings.Join(lines, "\n")
}

// settings validates the draft. It returns the persisted settings (without
// passwords) and the sealed passwords by endpoint.
func (u *upstreamEditor) settings(a *app) (upstream.Settings, map[string]string, error) {
	out := upstream.Settings{
		Default: strings.TrimSpace(u.defaultAddr),
		PAC:     strings.TrimSpace(u.pac),
	}
	if strings.EqualFold(out.Default, "direct") {
		return out, nil, fmt.Errorf("default: leave the address empty to connect directly")
	}
	if err := upstream.ValidateAddress(out.Default); err != nil {
		return out, nil, fmt.Errorf("default: %w", err)
	}
	rules, err := parseUpstreamRules(u.rules)
	if err != nil {
		return out, nil, err
	}
	out.Rules = rules
	bypass, err := parseUpstreamBypass(u.bypass)
	if err != nil {
		return out, nil, err
	}
	out.Bypass = bypass

	secrets := make(map[string]string)
	for i, row := range u.credentials {
		proxy := strings.TrimSpace(row.Proxy)
		if proxy == "" {
			continue
		}
		key, err := upstream.CanonicalAddress(proxy)
		if err != nil {
			return out, nil, fmt.Errorf("credential %d: %w", i+1, err)
		}
		switch {
		case row.Clear:
			if row.Username != "" {
				secrets[key] = ""
			} else {
				continue
			}
		case row.Password != "":
			secrets[key] = row.Password
		case row.Saved:
			if saved, ok := a.upstreamSecrets[key]; ok {
				secrets[key] = saved
			}
		case row.Username != "":
			secrets[key] = ""
		default:
			continue // an empty row is not a credential
		}
		out.Credentials = append(out.Credentials, upstream.Credential{Proxy: key, Username: strings.TrimSpace(row.Username)})
	}
	if err := out.Validate(); err != nil {
		return out, nil, err
	}
	return out, secrets, nil
}

func parseUpstreamRules(text string) ([]upstream.Rule, error) {
	var rules []upstream.Rule
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("rule %d: write a host pattern and an upstream, such as *.example.com socks5://127.0.0.1:1080", i+1)
		}
		if err := upstream.ValidateHostPattern(fields[0]); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		if err := upstream.ValidateAddress(fields[1]); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		rules = append(rules, upstream.Rule{Host: fields[0], Proxy: fields[1]})
	}
	return rules, nil
}

func parseUpstreamBypass(text string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, part := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' }) {
			part = strings.TrimSpace(part)
			if part == "" || strings.HasPrefix(part, "#") {
				continue
			}
			if err := upstream.ValidateBypass(part); err != nil {
				return nil, fmt.Errorf("bypass %q: %w", part, err)
			}
			out = append(out, part)
		}
	}
	return out, nil
}

func (a *app) saveUpstream() {
	u := &a.upstreamUI
	u.problem = ""
	settings, secrets, err := u.settings(a)
	if err != nil {
		u.problem = err.Error()
		return
	}
	a.applyUpstreamSettings(settings, secrets, "Upstream settings saved")
}

// applyUpstreamSettings seals the passwords, installs the configuration and
// persists both. A PAC URL may need a fetch, so the work runs off the UI
// thread when a window exists.
func (a *app) applyUpstreamSettings(settings upstream.Settings, secrets map[string]string, message string) {
	u := &a.upstreamUI
	if a.srv == nil {
		u.problem = "Capture is not available"
		return
	}
	apply := func() {
		if err := upstream.SecretStoreFor(a.settingsPath).Save(secrets); err != nil {
			u.problem = err.Error()
			u.saving = false
			a.fail("Upstream credentials", err)
			a.invalidate()
			return
		}
		if err := a.srv.SetUpstreamConfig(settings, secrets); err != nil {
			u.problem = err.Error()
			u.saving = false
			a.invalidate()
			return
		}
		a.upstreamCfg, a.upstreamSecrets = settings.Clone(), maps.Clone(secrets)
		a.upstream = settings.Summary()
		a.upstreamProblem = ""
		u.loaded, u.dirty, u.saving, u.problem = false, false, false, ""
		a.lastSave = time.Time{}
		if err := a.persistSettings(); err != nil {
			a.fail("Save settings", err)
		} else {
			a.done(message)
		}
		a.invalidate()
	}
	if a.win == nil {
		apply()
		return
	}
	u.saving = true
	a.invalidate()
	go func() { a.win.Update(apply) }()
}

// clearUpstream turns off the default, host rules and PAC, keeping the bypass
// list and credentials for a later configuration.
func (a *app) clearUpstream() {
	cfg := a.upstreamCfg.Clone()
	cfg.Default, cfg.Rules, cfg.PAC = "", nil, ""
	a.upstreamUI.loaded = false
	a.applyUpstreamSettings(cfg, a.upstreamSecrets, "Upstream: off")
}

// upstreamSettingsGroup is the Settings → Proxy → Upstream section.
func (a *app) upstreamSettingsGroup(c *ui.Context, t *ui.Theme) {
	u := &a.upstreamUI
	u.load(a)
	uikit.SettingsGroup(c, "Upstream", func() {
		if a.upstreamProblem != "" {
			ui.Text(c, a.upstreamProblem).FontSize(uikit.TextBody).TextColor(t.Danger).Selectable()
		}
		if u.problem != "" {
			ui.Text(c, u.problem).FontSize(uikit.TextBody).TextColor(t.Danger).Selectable()
		}
		ui.Field(c, "Default", func() {
			if uikit.Input(c, &u.defaultAddr).Label("Default upstream").Placeholder("Direct connection").Grow(1).MinWidth(160).Changed() {
				u.dirty = true
			}
		}).Description("Empty connects directly. A host rule or PAC can override it.")
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				ui.Field(c, "Host rules", func() {
					if ui.TextArea(c, &u.rules).Label("Host rules").Placeholder("*.internal.test socks5://127.0.0.1:1080\napi.example.com:443 direct").Height(70).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Changed() {
						u.dirty = true
					}
				}).Description("One per line: host pattern then upstream. *.example.com, :port and direct; first match wins.")
			})
			ui.Column(c).Grow(1).Children(func() {
				ui.Field(c, "Bypass", func() {
					if ui.TextArea(c, &u.bypass).Label("Bypass list").Placeholder("localhost\n*.internal.test\n10.0.0.0/8\n<local>").Height(70).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Changed() {
						u.dirty = true
					}
				}).Description("Hosts, *.example.com, CIDR, localhost or <local>. Direct before rules and PAC.")
			})
		})
		ui.Field(c, "PAC", func() {
			if uikit.Input(c, &u.pac).Label("PAC file or URL").Placeholder("https://example.com/proxy.pac").Grow(1).MinWidth(200).Changed() {
				u.dirty = true
			}
		}).Description("A local .pac file or http(s) URL. FindProxyForURL runs with a timeout and a memory limit.")
		ui.Field(c, "Credentials", func() {
			ui.Column(c).Gap(t.Space(1)).Children(func() {
				remove := -1
				for i := range u.credentials {
					row := &u.credentials[i]
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Wrap().Children(func() {
						if uikit.Input(c, &row.Proxy).Label("Proxy address").Placeholder("http://proxy.example:8080").Width(200).MinWidth(160).Changed() {
							u.dirty = true
						}
						if uikit.Input(c, &row.Username).Label("Username").Placeholder("Username").Width(110).Changed() {
							u.dirty = true
						}
						if uikit.Input(c, &row.Password).Password().Label("Password").Placeholder("Password").Width(110).Changed() {
							row.Clear, u.dirty = false, true
						}
						if uikit.SecondaryButton(c, "Remove").Clicked() {
							remove = i
						}
					})
					if row.Clear || row.Saved {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							switch {
							case row.Clear:
								uikit.Hint(c, "Saved password will be removed")
								if uikit.SecondaryButton(c, "Keep").Clicked() {
									row.Clear, u.dirty = false, true
								}
							case row.Saved && row.Password == "":
								uikit.Hint(c, "Saved password in use")
								if uikit.SecondaryButton(c, "Clear").Clicked() {
									row.Clear, u.dirty = true, true
								}
							}
						})
					}
					ui.Divider(c)
				}
				if remove >= 0 {
					u.credentials = append(u.credentials[:remove], u.credentials[remove+1:]...)
					u.dirty = true
				}
				if uikit.SecondaryButton(c, "Add credentials").Clicked() {
					u.credentials = append(u.credentials, upstreamCredentialRow{})
					u.dirty = true
				}
			})
		}).Description("Username and password for one upstream address. Passwords are sealed locally and never exported.")
		ui.Field(c, "", func() {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				save := uikit.PrimaryButton(c, "Save upstream").Disabled(u.saving)
				if save.Clicked() {
					a.saveUpstream()
				}
				if u.dirty && uikit.SecondaryButton(c, "Discard changes").Disabled(u.saving).Clicked() {
					u.loaded, u.dirty, u.problem = false, false, ""
				}
				if u.saving {
					ui.Spinner(c).Label("Saving upstream")
				}
				uikit.Meta(c, "A change applies to new connections; existing connections keep theirs.")
			})
		})
	})
}
