package app

// The settings registry is the Settings editor's model: one stable id, one
// section, one localized title and description, and one default per setting.
// Nothing in search, the modified indicator or Reset goes through a
// localized label; the label is only data for the index and the row.
//
// Defaults live here, next to the id, and are the same values the controls
// fall back to. A setting that already has a named default in the app uses
// that constant, so changing the default means changing one place.

import (
	"maps"
	"reflect"
	"slices"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/baba983/vscroxy/locales"
)

// settingsSection is a tab of Settings. The numbers are the ids the
// category list already uses, so revealing a result needs no label lookup.
type settingsSection int

const (
	settingsGeneral settingsSection = iota
	settingsProxy
	settingsHTTPS
	settingsAppearance
	settingsCapture
	settingsDevices
	settingsCertificates
)

func (s settingsSection) title() string {
	switch s {
	case settingsGeneral:
		return locales.General()
	case settingsProxy:
		return locales.Proxy()
	case settingsHTTPS:
		return locales.HttpsDecryption()
	case settingsAppearance:
		return locales.Appearance()
	case settingsCapture:
		return locales.Capture()
	case settingsDevices:
		return locales.Devices()
	case settingsCertificates:
		return locales.Certificates()
	}
	return ""
}

func (s settingsSection) id() string {
	switch s {
	case settingsGeneral:
		return "0"
	case settingsProxy:
		return "1"
	case settingsHTTPS:
		return "2"
	case settingsAppearance:
		return "3"
	case settingsCapture:
		return "4"
	case settingsDevices:
		return "5"
	case settingsCertificates:
		return "6"
	}
	return ""
}

// settingValue reads the live value, holds its one default and writes a
// reset value through the same state the controls edit. Only the title,
// description and id go into the index; rows marked secret carry a value
// here only so "modified" can be computed, never so it can be rendered.
type settingValue struct {
	get func(*app) any
	def any
	set func(*app, any)
}

type settingSpec struct {
	id          string
	section     settingsSection
	title       func() string
	description func() string
	value       settingValue
	secret      bool
}

func (s settingSpec) modified(a *app) bool {
	return !sameSettingValue(s.value.get(a), s.value.def)
}

// settingSpecs is the whole registry, in the order VS Code's tree shows
// settings: by section, then by the row in that section. It is a function of
// a's state only through the closures, which read values when asked.
func settingSpecs() []settingSpec { return settingsRegistry }

var settingsRegistry = []settingSpec{
	// General.
	{
		id: "general.language", section: settingsGeneral,
		title: locales.Language, description: locales.LanguageDescription,
		value: settingValue{get: func(a *app) any { return a.language }, def: "",
			set: func(a *app, v any) { a.setLanguage(v.(string)) }},
	},
	{
		id: "general.attentionSignals", section: settingsGeneral,
		title: locales.AttentionSignals, description: locales.AttentionSignalsHint,
		// attentionMode always normalizes the saved empty value to "auto".
		value: settingValue{get: func(a *app) any { return a.attentionMode() }, def: attentionAuto,
			set: func(a *app, v any) { a.setAttentionMode(v.(string)) }},
	},
	{
		id: "general.logLevel", section: settingsGeneral,
		title: locales.LogLevel, description: locales.LogLevelHint,
		// An omitted level means Info in settings.json too.
		value: settingValue{get: func(a *app) any {
			if a.logLevel == "" {
				return applog.LevelInfo
			}
			return a.logLevel
		}, def: applog.LevelInfo,
			set: func(a *app, v any) { a.setLogLevel(v.(string)) }},
	},
	{
		id: "general.projectFolder", section: settingsGeneral,
		title: locales.ProjectFolder, description: locales.ProjectFolderHint,
		value: settingValue{get: func(a *app) any { return a.projectDir }, def: "",
			set: func(a *app, v any) { a.setProjectDir(v.(string)) }},
	},
	{
		id: "general.trustedLinkHosts", section: settingsGeneral,
		title: locales.TrustedLinkHosts, description: locales.TrustedLinkHostsHint,
		value: settingValue{get: func(a *app) any { return a.trustedLinkHosts }, def: []string(nil),
			set: func(a *app, _ any) { a.trustedLinkHosts = nil }},
	},
	{
		id: "general.localControl", section: settingsGeneral,
		title: locales.EnableLocalAutomation, description: locales.LocalAutomationAbout,
		value: settingValue{get: func(a *app) any { return a.localControl }, def: false,
			set: func(a *app, v any) { a.setLocalControl(v.(bool)) }},
	},
	{
		id: "general.checkForUpdates", section: settingsGeneral,
		title: locales.CheckForUpdates, description: locales.CheckForUpdatesHint,
		value: settingValue{get: func(a *app) any { return a.updateCheck }, def: true,
			set: func(a *app, v any) { a.updateCheck = v.(bool) }},
	},
	{
		id: "general.dismissedNotices", section: settingsGeneral,
		title: locales.DismissedNotices, description: locales.DismissedNoticesHint,
		value: settingValue{get: func(a *app) any { return slices.Clone(a.dismissedNotices) }, def: []string(nil),
			set: func(a *app, _ any) { a.resetDismissedNotices() }},
	},
	{
		id: "general.setupGuide", section: settingsGeneral,
		title: locales.SetupGuide, description: locales.CapturingTrustingTheCertificateAnd,
		// A fresh settings file opens the setup guide.
		value: settingValue{get: func(a *app) any { return a.guide }, def: true,
			set: func(a *app, v any) { a.guide = v.(bool) }},
	},
	// Appearance.
	{
		id: "appearance.theme", section: settingsAppearance,
		title: locales.Appearance, description: locales.SystemFollowsThisComputerS,
		value: settingValue{get: func(a *app) any { return a.appearance }, def: "",
			set: func(a *app, v any) { a.appearance = v.(string); a.syncNativeAppearance() }},
	},
	// Proxy.
	{
		id: "proxy.port", section: settingsProxy,
		title: locales.ProxyPort, description: locales.BlankIsAutomaticPrefers7890,
		value: settingValue{get: func(a *app) any { return a.proxyPort }, def: 0,
			// saveProxyPort is the existing explicit save and keeps its
			// restart notice when the launch port was different.
			set: func(a *app, _ any) { a.proxyPortInput = ""; a.saveProxyPort() }},
	},
	{
		id: "proxy.systemBypassEnabled", section: settingsProxy,
		title: locales.ReplaceSystemBypassList, description: locales.SystemProxyBypassList,
		value: settingValue{get: func(a *app) any { return a.systemProxyBypassSet }, def: false,
			set: func(a *app, _ any) { a.systemProxyBypassSet = false; a.saveSystemProxyBypass() }},
	},
	{
		id: "proxy.systemBypass", section: settingsProxy,
		title: locales.SystemProxyBypassList, description: locales.SystemProxyBypassList,
		value: settingValue{get: func(a *app) any { return a.systemProxyBypass }, def: []string(nil),
			set: func(a *app, _ any) { a.systemProxyBypassText = ""; a.saveSystemProxyBypass() }},
	},
	{
		id: "proxy.upstream.default", section: settingsProxy,
		title: locales.DefaultText2, description: locales.EmptyConnectsDirectlyAHost,
		value: settingValue{get: func(a *app) any { return a.upstreamCfg.Default }, def: "",
			set: func(a *app, _ any) {
				a.resetUpstream(func(s *upstream.Settings) { s.Default = "" })
			}},
	},
	{
		id: "proxy.upstream.rules", section: settingsProxy,
		title: locales.HostRules, description: locales.OnePerLineHostPattern,
		value: settingValue{get: func(a *app) any { return a.upstreamCfg.Rules }, def: []upstream.Rule(nil),
			set: func(a *app, _ any) {
				a.resetUpstream(func(s *upstream.Settings) { s.Rules = nil })
			}},
	},
	{
		id: "proxy.upstream.bypass", section: settingsProxy,
		title: locales.Bypass, description: locales.HostsExampleComCidrLocalhost,
		value: settingValue{get: func(a *app) any { return a.upstreamCfg.Bypass }, def: []string(nil),
			set: func(a *app, _ any) {
				a.resetUpstream(func(s *upstream.Settings) { s.Bypass = nil })
			}},
	},
	{
		id: "proxy.upstream.pac", section: settingsProxy,
		title: locales.Pac, description: locales.ALocalPacFileOr,
		value: settingValue{get: func(a *app) any { return a.upstreamCfg.PAC }, def: "",
			set: func(a *app, _ any) {
				a.resetUpstream(func(s *upstream.Settings) { s.PAC = "" })
			}},
	},
	{
		id: "proxy.upstream.credentials", section: settingsProxy,
		title: locales.Credentials, description: locales.UsernameAndPasswordForOne, secret: true,
		value: settingValue{get: func(a *app) any { return a.upstreamCfg.Credentials }, def: []upstream.Credential(nil),
			set: func(a *app, _ any) {
				a.resetUpstream(func(s *upstream.Settings) { s.Credentials = nil })
			}},
	},
	{
		id: "proxy.socks.enabled", section: settingsProxy,
		title: locales.EnableSocks5Proxy, description: locales.OffByDefaultTheApp,
		value: settingValue{get: func(a *app) any { return a.socksOn }, def: false,
			set: func(a *app, _ any) { a.resetSOCKS(socksEntry{}) }},
	},
	{
		id: "proxy.socks.port", section: settingsProxy,
		title: locales.Socks5Port, description: locales.BlankIsAutomaticAnOs,
		value: settingValue{get: func(a *app) any { return a.socksPort }, def: 0,
			set: func(a *app, _ any) { a.resetSOCKS(socksEntry{}) }},
	},
	{
		id: "proxy.socks.auth", section: settingsProxy,
		title: locales.Authentication, description: locales.RequireUsernameAndPassword,
		value: settingValue{get: func(a *app) any { return a.socksAuth }, def: false,
			set: func(a *app, _ any) { a.resetSOCKS(socksEntry{}) }},
	},
	{
		id: "proxy.socks.username", section: settingsProxy,
		title: locales.Socks5Username, description: locales.Username,
		value: settingValue{get: func(a *app) any { return a.socksUsername }, def: "",
			set: func(a *app, _ any) { a.resetSOCKS(socksEntry{}) }},
	},
	{
		id: "proxy.socks.password", section: settingsProxy,
		title: locales.Socks5Password, description: locales.Password, secret: true,
		value: settingValue{get: func(a *app) any { return a.socksPassword }, def: "",
			set: func(a *app, _ any) { a.resetSOCKS(socksEntry{}) }},
	},
	{
		id: "proxy.reverseProxies", section: settingsProxy,
		title: locales.ReverseProxies, description: locales.NoReverseProxiesALocal, secret: true,
		value: settingValue{get: func(a *app) any { return a.reverses }, def: []reverseProxySetting(nil),
			set: func(a *app, _ any) { a.reverses = nil; a.applyReverseProxies() }},
	},
	// HTTPS decryption.
	{
		id: "https.allDomains", section: settingsHTTPS,
		title: locales.EveryDomain, description: locales.SettingEveryDomainHint,
		value: settingValue{get: func(a *app) any { return a.sslAll }, def: false,
			set: func(a *app, v any) {
				cfg := a.inspectionConfig()
				cfg.All = v.(bool)
				a.saveSSL(cfg)
			}},
	},
	{
		id: "https.domains", section: settingsHTTPS,
		title: locales.DomainsAndApps, description: locales.SettingDomainsHint,
		value: settingValue{get: func(a *app) any { return a.sslHosts }, def: "",
			set: func(a *app, _ any) {
				cfg := a.inspectionConfig()
				cfg.Hosts = nil
				a.saveSSL(cfg)
			}},
	},
	{
		id: "https.apps", section: settingsHTTPS,
		title: locales.Apps, description: locales.SettingAppsHint,
		value: settingValue{get: func(a *app) any { return a.sslApps }, def: []capture.SSLApp(nil),
			set: func(a *app, _ any) {
				cfg := a.inspectionConfig()
				cfg.Apps = nil
				a.saveSSL(cfg)
			}},
	},
	{
		id: "https.neverDecrypt", section: settingsHTTPS,
		title: locales.NeverDecrypt, description: locales.NeverDecryptHint,
		value: settingValue{get: func(a *app) any { return a.sslExclude }, def: "",
			set: func(a *app, _ any) {
				cfg := a.inspectionConfig()
				cfg.Exclude = nil
				a.saveSSL(cfg)
			}},
	},
	{
		id: "https.allowUntrusted", section: settingsHTTPS,
		title: locales.AllowUntrustedUpstream, description: locales.AllowUntrustedUpstreamHint,
		value: settingValue{get: func(a *app) any { return a.sslUntrusted }, def: "",
			set: func(a *app, _ any) {
				cfg := a.inspectionConfig()
				cfg.AllowUntrusted = nil
				a.saveSSL(cfg)
			}},
	},
	{
		id: "https.keyLog", section: settingsHTTPS,
		title: locales.TlsKeyLog, description: locales.SettingKeyLogHint,
		secret: true, // a log path can carry a user name or token
		// setKeyLog closes the writer the proxy holds, not only the setting.
		value: settingValue{get: func(a *app) any { return a.keyLogPath }, def: "",
			set: func(a *app, _ any) { a.setKeyLog("") }},
	},
	// Capture.
	{
		id: "capture.noCaching", section: settingsCapture,
		title: locales.Caching, description: locales.StripCachingHeadersBothWays,
		value: settingValue{get: func(a *app) any { return a.srv != nil && a.srv.NoCaching() }, def: false,
			set: func(a *app, v any) {
				if a.srv != nil {
					a.srv.SetNoCaching(v.(bool))
				}
			}},
	},
	{
		id: "capture.allowList", section: settingsCapture,
		title: locales.OnlyShowAllowedTraffic, description: locales.ShowOnlyTheHostsAnd,
		value: settingValue{get: func(a *app) any { return a.allowOn }, def: false,
			set: func(a *app, v any) { a.allowOn = v.(bool); a.applyAllow() }},
	},
	{
		id: "capture.flowLimit", section: settingsCapture,
		title: locales.SettingKeepUpTo, description: locales.SettingKeepUpToHint,
		value: settingValue{get: func(a *app) any { return a.flowLimit }, def: 0,
			set: func(a *app, _ any) { a.flowLimit = 0; a.trimFlows(); a.recount(); a.applyFilter() }},
	},
	{
		id: "capture.memoryLimit", section: settingsCapture,
		title: locales.SettingMemoryLimit, description: locales.SettingMemoryLimitHint,
		value: settingValue{get: func(a *app) any { return a.memoryLimit }, def: 0,
			set: func(a *app, _ any) { a.memoryLimit = 0; a.trimFlows(); a.recount(); a.applyFilter() }},
	},
	{
		id: "capture.diskLimit", section: settingsCapture,
		title: locales.SettingDiskLimit, description: locales.BodiesPast64KbAre,
		value: settingValue{get: func(a *app) any { return a.diskLimit }, def: 0,
			set: func(a *app, _ any) { a.diskLimit = 0; a.trimFlows(); a.recount(); a.applyFilter() }},
	},
	{
		id: "capture.historyLimit", section: settingsCapture,
		title: locales.HistoryEntries, description: locales.HistoryEntriesHelp,
		value: settingValue{get: func(a *app) any { return a.historyLimit }, def: 0,
			set: func(a *app, _ any) { a.historyLimit = 0; a.applyHistoryLimits() }},
	},
	{
		id: "capture.historySizeMB", section: settingsCapture,
		title: locales.HistorySize, description: locales.HistorySizeHelp,
		value: settingValue{get: func(a *app) any { return a.historySizeMB }, def: 0,
			set: func(a *app, _ any) { a.historySizeMB = 0; a.applyHistoryLimits() }},
	},
	// Certificates.
	{
		id: "certificates.trustSystemRoots", section: settingsCertificates,
		title: locales.TrustSystemRoots, description: locales.SettingTrustSystemRootsHint,
		value: settingValue{get: func(a *app) any { return a.certificates.TrustsSystem() }, def: true,
			set: func(a *app, _ any) { a.resetCertificates() }},
	},
	{
		id: "certificates.signingCA", section: settingsCertificates,
		title: locales.SigningCaCertificate, description: locales.SigningCaKeyHint, secret: true,
		value: settingValue{get: func(a *app) any { return a.certificates.SigningCA }, def: (*certs.SigningCA)(nil),
			set: func(a *app, _ any) { a.resetCertificates() }},
	},
	{
		id: "certificates.clients", section: settingsCertificates,
		title: locales.ClientCertificates, description: locales.PresentedToAMatchingHost, secret: true,
		value: settingValue{get: func(a *app) any { return a.certificates.Clients }, def: []certs.Client(nil),
			set: func(a *app, _ any) { a.resetCertificates() }},
	},
	{
		id: "certificates.servers", section: settingsCertificates,
		title: locales.ServerCertificates, description: locales.ServerCertificatesHint, secret: true,
		value: settingValue{get: func(a *app) any { return a.certificates.Servers }, def: []certs.Server(nil),
			set: func(a *app, _ any) { a.resetCertificates() }},
	},
	{
		id: "certificates.roots", section: settingsCertificates,
		title: locales.TrustedRoots, description: locales.SettingTrustedRootsHint,
		value: settingValue{get: func(a *app) any { return a.certificates.RootCAs }, def: []string(nil),
			set: func(a *app, _ any) { a.resetCertificates() }},
	},
}

// resetUpstream changes one field of the saved upstream and goes through
// applyUpstreamSettings, the same path Save uses: the proxy's effective
// configuration and the sealed password store change with the setting.
func (a *app) resetUpstream(change func(*upstream.Settings)) {
	cfg := a.upstreamCfg.Clone()
	change(&cfg)
	secrets := maps.Clone(a.upstreamSecrets)
	if len(cfg.Credentials) == 0 {
		secrets = nil
	}
	a.upstreamUI.loaded = false
	if a.srv == nil {
		a.upstreamCfg, a.upstreamSecrets = cfg, secrets
		return
	}
	a.applyUpstreamSettings(cfg, secrets, locales.UpstreamSettingsSaved())
}

// resetSOCKS fills the editor's drafts with the default entry, saves through
// saveSOCKS, then rebinds the live listener. Saving alone only changes the
// next launch, which would leave a listener and its old credentials running.
func (a *app) resetSOCKS(entry socksEntry) {
	a.socksOnDraft, a.socksPortInput = entry.on, ""
	a.socksAuthDraft, a.socksUserDraft, a.socksPassDraft = entry.auth, entry.username, entry.password
	if a.settingsPath != "" {
		a.saveSOCKS()
	} else {
		a.socksOn, a.socksPort, a.socksAuth = entry.on, 0, entry.auth
		a.socksUsername, a.socksPassword = entry.username, entry.password
		a.resetSOCKSDrafts()
	}
	cfg, err := a.socksConfig()
	if err != nil {
		a.fail(locales.Socks5(), err)
		return
	}
	if a.srv != nil {
		if err := a.srv.ApplySOCKS(cfg); err != nil {
			a.fail(locales.Socks5(), err)
		}
	}
}

// resetCertificates clears the certificate editor and saves it through
// saveCertificates, which reloads the proxy and restores the generated CA.
func (a *app) resetCertificates() {
	e := &a.certificateUI
	e.rows, e.roots = nil, nil
	e.trustSystem = true
	e.signingCert, e.signingKey, e.signingSubject = "", "", ""
	e.signingSaved, e.signingClear, e.dirty = false, true, true
	e.problem, a.certProblem = "", ""
	a.saveCertificates()
}

func settingByID(id string) (settingSpec, bool) {
	for _, spec := range settingSpecs() {
		if spec.id == id {
			return spec, true
		}
	}
	return settingSpec{}, false
}

// sameSettingValue compares a live value with its default. Empty and nil
// slices are the same value here: a settings file that omits a list and one
// that writes an empty list mean the same thing.
func sameSettingValue(current, def any) bool {
	if current == nil || def == nil {
		return current == nil && def == nil
	}
	x, y := reflect.ValueOf(current), reflect.ValueOf(def)
	if x.Kind() == reflect.Slice && y.Kind() == reflect.Slice {
		if x.Len() == 0 && y.Len() == 0 {
			return true
		}
	}
	return reflect.DeepEqual(current, def)
}

// resetSetting writes one setting's one default through the same save path
// the controls use. The id, never a label, chooses the default and setter.
func (a *app) resetSetting(id string) bool {
	spec, ok := settingByID(id)
	if !ok || spec.value.set == nil {
		return false
	}
	spec.value.set(a, spec.value.def)
	a.settingsGen++ // a reset can change a value without a persist write
	a.bypassSettingsSave()
	if err := a.persistSettings(); err != nil {
		a.fail(locales.Settings(), err)
		return false
	}
	a.done(locales.SettingsResetNotice(spec.title()))
	return true
}
