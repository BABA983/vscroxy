package app

// Report issue opens a prefilled GitHub issue in the browser. The body is a
// short list of facts about the build and the setup; the allowlist is the same
// one the diagnostics bundle uses, and no capture, host, path or secret is
// part of it. System info is fixed English for maintainers, whatever language
// the reporter uses. Building the text and the URL is pure so privacy and
// length are easy to test without opening a browser.

import (
	"cmp"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/version"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	issueURLBase = "https://github.com/BABA983/vscroxy/issues/new"
	// issueBodyBytes leaves room for percent encoding: each UTF-8 byte can
	// take three characters in a URL, well under GitHub's ~8 KB URL limit.
	issueBodyBytes  = 2048
	issueValueBytes = 96

	issueHint          = "<!-- Describe the problem and the steps to reproduce. -->"
	issueSystemHeading = "### System info"
	issueAppLabel      = "App"
	issueOSLabel       = "OS"
	issueArchLabel     = "Architecture"
	issueMygoLabel     = "MyGo"
	issueLocaleLabel   = "Locale"
	issueThemeLabel    = "Theme"
	issueFeaturesLabel = "Enabled features"
	issueNoneLabel     = "None"
	issueSystemValue   = "system"
	issueTruncated     = "Details truncated to keep this URL below GitHub's limit."

	issueFeatureSocks        = "SOCKS5 entry"
	issueFeatureSocksAuth    = "SOCKS5 authentication"
	issueFeatureVaultScripts = "Vault in scripts"
	issueFeatureCookies      = "Persistent cookies"
	issueFeatureLocalControl = "Local automation"
	issueFeatureAllowList    = "Host and app allow list"
)

// issueInfo is all the report may contain. Values arrive from the diagnostics
// allowlist or fixed English constants, never from traffic or the vault.
type issueInfo struct {
	version, commit  string
	os, architecture string
	mygo             string
	locale, theme    string
	features         []string
}

// issueInfo builds the report from a settings snapshot. Reads of app state
// stay on the UI thread; the OS lookup called by issueInfo and reportIssue is
// deliberately left to the caller.
func issueInfoFrom(s diagnosticSettings, osVersion string) issueInfo {
	osName, arch, _ := strings.Cut(version.Platform(), "/")
	var features []string
	if s.SOCKSOn {
		features = append(features, issueFeatureSocks)
	}
	if s.SOCKSAuth {
		features = append(features, issueFeatureSocksAuth)
	}
	if s.VaultScripts {
		features = append(features, issueFeatureVaultScripts)
	}
	if s.CookiePersistence {
		features = append(features, issueFeatureCookies)
	}
	if s.LocalControl {
		features = append(features, issueFeatureLocalControl)
	}
	if s.AllowList {
		features = append(features, issueFeatureAllowList)
	}
	return issueInfo{
		version: version.Number, commit: cmp.Or(version.CommitString(), "?"),
		os: osName + " " + cmp.Or(osVersion, "?"), architecture: arch,
		mygo:   mygo.Version,
		locale: cmp.Or(s.Language, issueSystemValue), theme: cmp.Or(s.Appearance, issueSystemValue),
		features: features,
	}
}

// issueInfo collects a report for tests and diagnostics. Commands use the
// off-thread path in reportIssue instead.
func (a *app) issueInfo() issueInfo {
	return issueInfoFrom(a.diagnosticSettings(), version.OSVersion())
}

// reportIssue runs the OS lookup and the browser call off the UI thread. It
// snapshots only settings first, on this thread, so the worker never reads
// widgets or app state. The caller captures the window's services for this
// build; a command has no Context of its own.
func (a *app) reportIssue(services ui.Services) {
	settings := a.diagnosticSettings()
	go guarded(func() {
		services.OpenURL(issueURL(issueInfoFrom(settings, version.OSVersion())))
	})
}

// issueURL is the browser destination. Only the body is prefilled: GitHub
// creates the issue when the person submits the form themselves.
func issueURL(info issueInfo) string {
	query := make(url.Values, 1)
	query.Set("body", issueBody(info))
	return issueURLBase + "?" + query.Encode()
}

// issueBody lays out the facts in a form a person can edit before posting.
func issueBody(info issueInfo) string {
	lines := []string{
		issueHint,
		"",
		"",
		issueSystemHeading,
		"",
		issueAppLabel + ": " + issueValue(info.version) + " (" + issueValue(info.commit) + ")",
		issueOSLabel + ": " + issueValue(info.os),
		issueArchLabel + ": " + issueValue(info.architecture),
		issueMygoLabel + ": " + issueValue(info.mygo),
		issueLocaleLabel + ": " + issueValue(info.locale),
		issueThemeLabel + ": " + issueValue(info.theme),
		"",
		issueFeaturesLabel + ":",
	}
	features := info.features
	if len(features) == 0 {
		features = []string{issueNoneLabel}
	}
	for _, feature := range features {
		lines = append(lines, "- "+issueValue(feature))
	}
	body := strings.Join(lines, "\n")
	if len(body) <= issueBodyBytes {
		return body
	}
	// Cut at a line boundary, and never in the middle of a UTF-8 rune.
	limit := issueBodyBytes - len(issueTruncated) - 4
	if cut := strings.LastIndexByte(body[:limit], '\n'); cut > 0 {
		limit = cut
	}
	return strings.TrimRight(issueValidPrefix(body, limit), "\n") +
		"\n\n… " + issueTruncated + "\n"
}

// issueValue keeps one field to a single short line. Newlines in a value
// could otherwise forge more fields in the body.
func issueValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "?"
	}
	return issueValidPrefix(value, issueValueBytes)
}

func issueValidPrefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
