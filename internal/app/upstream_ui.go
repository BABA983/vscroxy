package app

// Upstream settings: a default proxy, first-match host rules, a bypass list,
// a PAC script and per-endpoint credentials. Passwords are sealed beside the
// settings file and never written into settings.json, a configuration export
// or the log.

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// upstreamLoadResult is the startup PAC load's outcome. applied is false when
// a user save or import replaced the loading snapshot while it ran.
type upstreamLoadResult struct {
	epoch    uint64
	applied  bool
	err      error
	settings upstream.Settings
	secrets  map[string]string
}

// upstreamRetry holds the failed configuration and the next automatic retry.
// Only URL PACs retry automatically; the Retry button uses the same settings
// for a file PAC or a validation error.
type upstreamRetry struct {
	timer    *time.Timer
	attempt  int
	retrying bool
	epoch    uint64
	settings upstream.Settings
	secrets  map[string]string
}

var upstreamRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 60 * time.Second, 5 * time.Minute}

func upstreamRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(upstreamRetryDelays) {
		return upstreamRetryDelays[len(upstreamRetryDelays)-1]
	}
	return upstreamRetryDelays[attempt]
}

func isPACURL(source string) bool {
	u, err := url.Parse(strings.TrimSpace(source))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}

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
	a.stopUpstreamRetry()
	u.problem = ""
	settings, secrets, err := u.settings(a)
	if err != nil {
		u.problem = err.Error()
		return
	}
	a.applyUpstreamSettings(settings, secrets, "Upstream settings saved")
}

// drainUpstream delivers the startup PAC load result on the UI thread. A save
// or import that started after the load replaced its epoch, so its stale
// result is discarded even if the loading snapshot was already replaced.
func (a *app) drainUpstream() {
	if a.upstreamPending == nil {
		return
	}
	select {
	case result := <-a.upstreamPending:
		a.upstreamPending = nil
		if !result.applied || result.epoch != a.upstreamEpoch.Load() {
			a.upstreamLoading = false
			return
		}
		a.upstreamLoading = false
		if result.err != nil {
			a.upstreamProblem = result.err.Error()
			a.upstream = ""
			a.scheduleUpstreamRetry(result.settings, result.secrets, result.epoch)
		} else {
			a.upstreamProblem = ""
			a.upstream = result.settings.Summary()
		}
		a.invalidate()
	default:
	}
}

// scheduleUpstreamRetry remembers the failed configuration for the Retry
// button and, for a URL PAC, schedules the next automatic attempt. A retry
// keeps the backoff sequence; a new save starts it again.
func (a *app) scheduleUpstreamRetry(settings upstream.Settings, secrets map[string]string, epoch uint64) {
	if a.upstreamRetry.timer != nil {
		a.upstreamRetry.timer.Stop()
		a.upstreamRetry.timer = nil
	}
	attempt := 0
	if a.upstreamRetry.retrying {
		attempt = a.upstreamRetry.attempt
	}
	a.upstreamRetry.settings, a.upstreamRetry.secrets = settings.Clone(), maps.Clone(secrets)
	a.upstreamRetry.epoch, a.upstreamRetry.retrying = epoch, false
	if !isPACURL(settings.PAC) {
		return
	}
	a.upstreamRetry.attempt = attempt + 1
	win := a.win
	if win == nil {
		return // headless tests drive retryUpstream directly
	}
	a.upstreamRetry.timer = time.AfterFunc(upstreamRetryDelay(attempt), func() {
		win.Update(func() { a.retryUpstream() })
	})
}

func (a *app) stopUpstreamRetry() {
	if a.upstreamRetry.timer != nil {
		a.upstreamRetry.timer.Stop()
	}
	a.upstreamRetry = upstreamRetry{}
}

// retryUpstream runs the scheduled attempt. The epoch check drops a timer
// whose configuration was replaced by a save, import or Upstream off.
func (a *app) retryUpstream() {
	r := a.upstreamRetry
	if r.timer != nil {
		r.timer.Stop()
		a.upstreamRetry.timer = nil
	}
	if r.epoch != a.upstreamEpoch.Load() || !r.settings.Enabled() {
		a.stopUpstreamRetry()
		return
	}
	a.upstreamRetry.retrying = true
	a.applyUpstreamSettings(r.settings.Clone(), maps.Clone(r.secrets), "Upstream settings saved")
}

// retryUpstreamNow is the Settings button: retry immediately and start the
// backoff sequence over if it fails again.
func (a *app) retryUpstreamNow() {
	settings, secrets := a.upstreamCfg.Clone(), maps.Clone(a.upstreamSecrets)
	if a.upstreamRetry.settings.Enabled() {
		settings, secrets = a.upstreamRetry.settings.Clone(), maps.Clone(a.upstreamRetry.secrets)
	}
	a.stopUpstreamRetry()
	a.applyUpstreamSettings(settings, secrets, "Upstream settings saved")
}

// applyUpstreamSettings installs the configuration and seals its passwords.
// A PAC URL may need a fetch, so all I/O runs off the UI thread and only a
// successful result is committed to app state and disk.
func (a *app) applyUpstreamSettings(settings upstream.Settings, secrets map[string]string, message string) {
	u := &a.upstreamUI
	if a.srv == nil {
		u.problem = "Capture is not available"
		return
	}
	retrying := a.upstreamRetry.retrying
	if !retrying {
		a.stopUpstreamRetry()
	}
	wasUnavailable := a.upstreamLoading || a.srv.UpstreamError() != nil
	epoch := a.upstreamEpoch.Add(1)
	a.upstreamLoading = false
	a.upstreamProblem = ""
	previousSettings, previousSecrets := a.upstreamCfg.Clone(), maps.Clone(a.upstreamSecrets)
	work := func() (upstream.Settings, map[string]string, bool, bool, error) {
		appliedRuntime, err := a.srv.SetUpstreamConfigAt(epoch, settings, secrets)
		if err != nil {
			return upstream.Settings{}, nil, true, false, err
		}
		if !appliedRuntime || a.upstreamEpoch.Load() != epoch {
			return upstream.Settings{}, nil, false, true, nil
		}
		if err := upstream.SecretStoreFor(a.settingsPath).Save(secrets); err != nil {
			// The runtime configuration now uses passwords that could not be
			// sealed; put the previous one back so a restart is not worse.
			reverted, revertErr := a.srv.SetUpstreamConfigAt(epoch, previousSettings, previousSecrets)
			if revertErr != nil {
				return upstream.Settings{}, nil, true, false, errors.Join(err, revertErr)
			}
			if !reverted {
				return upstream.Settings{}, nil, false, true, nil
			}
			return upstream.Settings{}, nil, false, false, err
		}
		return settings, secrets, false, false, nil
	}
	finish := func(applied upstream.Settings, appliedSecrets map[string]string, failClosed, stale bool, err error) {
		if epoch != a.upstreamEpoch.Load() {
			return // a newer save or import owns the state now
		}
		u.saving = false
		if stale {
			return
		}
		if err != nil {
			u.problem = err.Error()
			a.upstreamLoading = false
			if wasUnavailable {
				a.upstreamProblem = err.Error()
				if failClosed {
					if !a.srv.SetUpstreamUnavailableAt(epoch, settings, err) {
						return
					}
					a.upstream = ""
				}
				a.scheduleUpstreamRetry(settings, secrets, epoch)
			}
			a.invalidate()
			return
		}
		a.stopUpstreamRetry()
		a.upstreamCfg, a.upstreamSecrets = applied.Clone(), maps.Clone(appliedSecrets)
		a.upstream = applied.Summary()
		a.upstreamProblem = ""
		a.upstreamLoading = false
		u.loaded, u.dirty, u.problem = false, false, ""
		a.lastSave = time.Time{}
		if err := a.persistSettings(); err != nil {
			a.fail("Save settings", err)
		} else {
			a.done(message)
		}
		a.invalidate()
	}
	if a.win == nil {
		applied, appliedSecrets, failClosed, stale, err := work()
		finish(applied, appliedSecrets, failClosed, stale, err)
		return
	}
	u.saving = true
	a.invalidate()
	go func() {
		applied, appliedSecrets, failClosed, stale, err := work()
		a.win.Update(func() { finish(applied, appliedSecrets, failClosed, stale, err) })
	}()
}

// clearUpstream turns off the default, host rules and PAC, keeping the bypass
// list and credentials for a later configuration.
func (a *app) clearUpstream() {
	a.stopUpstreamRetry()
	cfg := a.upstreamCfg.Clone()
	cfg.Default, cfg.Rules, cfg.PAC = "", nil, ""
	a.upstreamUI.loaded = false
	a.applyUpstreamSettings(cfg, a.upstreamSecrets, "Upstream: off")
}

// upstreamSettingsGroup is the Settings → Proxy → Upstream section.
func (a *app) upstreamSettingsGroup(c *ui.Context, t *ui.Theme) {
	u := &a.upstreamUI
	u.load(a)
	uikit.SettingsGroup(c, locales.Upstream2(), func() {
		if a.upstreamProblem != "" {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.upstreamProblem).FontSize(uikit.TextBody).TextColor(t.Danger).Selectable().Grow(1)
				if !a.upstreamLoading && uikit.SecondaryButton(c, locales.Retry()).Clicked() {
					a.retryUpstreamNow()
				}
			})
		}
		if u.problem != "" {
			ui.Text(c, u.problem).FontSize(uikit.TextBody).TextColor(t.Danger).Selectable()
		}
		ui.Field(c, locales.DefaultText2(), func() {
			if uikit.Input(c, &u.defaultAddr).Label(locales.DefaultUpstream()).Placeholder(locales.DirectConnection()).Grow(1).MinWidth(160).Changed() {
				u.dirty = true
			}
		}).Description(locales.EmptyConnectsDirectlyAHost())
		ui.Field(c, locales.HostRules(), func() {
			if ui.TextArea(c, &u.rules).Label(locales.HostRules()).Placeholder("*.internal.test socks5://127.0.0.1:1080\napi.example.com:443 direct").Height(60).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Changed() {
				u.dirty = true
			}
		}).Description(locales.OnePerLineHostPattern())
		ui.Field(c, locales.Bypass(), func() {
			if ui.TextArea(c, &u.bypass).Label(locales.BypassList()).Placeholder("localhost\n*.internal.test\n10.0.0.0/8").Height(50).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Changed() {
				u.dirty = true
			}
		}).Description(locales.HostsExampleComCidrLocalhost())
		ui.Field(c, locales.Pac(), func() {
			if uikit.Input(c, &u.pac).Label(locales.PacFileOrUrl()).Placeholder("https://example.com/proxy.pac").Grow(1).MinWidth(200).Changed() {
				u.dirty = true
			}
		}).Description(locales.ALocalPacFileOr())
		ui.Field(c, locales.Credentials(), func() {
			ui.Column(c).Gap(t.Space(1)).Children(func() {
				remove := -1
				for i := range u.credentials {
					row := &u.credentials[i]
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Wrap().Children(func() {
						if uikit.Input(c, &row.Proxy).Label(locales.ProxyAddress()).Placeholder("http://proxy.example:8080").Width(200).MinWidth(160).Changed() {
							u.dirty = true
						}
						if uikit.Input(c, &row.Username).Label(locales.Username()).Placeholder(locales.Username()).Width(110).Changed() {
							u.dirty = true
						}
						if uikit.Input(c, &row.Password).Password().Label(locales.Password()).Placeholder(locales.Password()).Width(110).Changed() {
							row.Clear, u.dirty = false, true
						}
						if uikit.SecondaryButton(c, locales.Remove()).Clicked() {
							remove = i
						}
					})
					if row.Clear || row.Saved {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							switch {
							case row.Clear:
								uikit.Hint(c, locales.SavedPasswordWillBeRemoved())
								if uikit.SecondaryButton(c, locales.Keep()).Clicked() {
									row.Clear, u.dirty = false, true
								}
							case row.Saved && row.Password == "":
								uikit.Hint(c, locales.SavedPasswordInUse())
								if uikit.SecondaryButton(c, locales.Clear()).Clicked() {
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
				if uikit.SecondaryButton(c, locales.AddCredentials()).Clicked() {
					u.credentials = append(u.credentials, upstreamCredentialRow{})
					u.dirty = true
				}
			})
		}).Description(locales.UsernameAndPasswordForOne())
		ui.Field(c, "", func() {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				save := uikit.PrimaryButton(c, locales.SaveUpstream()).Disabled(u.saving)
				if save.Clicked() {
					a.saveUpstream()
				}
				if u.dirty && uikit.SecondaryButton(c, locales.DiscardChanges()).Disabled(u.saving).Clicked() {
					u.loaded, u.dirty, u.problem = false, false, ""
				}
				if u.saving {
					ui.Spinner(c).Label(locales.SavingUpstream())
				}
				uikit.Meta(c, locales.AChangeAppliesToNew())
			})
		})
	})
}
