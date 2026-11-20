package app

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
)

func sampleIssueInfo() issueInfo {
	return issueInfo{
		version: version.Number, commit: "c754ffe",
		os: runtime.GOOS + " 26.2", architecture: runtime.GOARCH,
		mygo: mygo.Version, locale: "zh-Hans", theme: "dark",
		features: []string{issueFeatureSocks, issueFeatureCookies},
	}
}

func TestIssueBodyContainsOnlyTheChosenDiagnostics(t *testing.T) {
	info := sampleIssueInfo()
	body := issueBody(info)
	if !strings.HasPrefix(body, issueHint+"\n\n\n"+issueSystemHeading+"\n\n") {
		t.Fatalf("body does not leave room before the system info:\n%s", body)
	}
	for _, want := range []string{
		issueAppLabel + ": " + version.Number + " (c754ffe)",
		issueOSLabel + ": " + runtime.GOOS + " 26.2",
		issueArchLabel + ": " + runtime.GOARCH,
		issueMygoLabel + ": " + mygo.Version,
		issueLocaleLabel + ": zh-Hans",
		issueThemeLabel + ": dark",
		issueFeaturesLabel + ":",
		"- " + issueFeatureSocks,
		"- " + issueFeatureCookies,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	if !strings.HasSuffix(issueURL(info), url.QueryEscape(body)) {
		t.Errorf("URL does not carry the whole body: %q", issueURL(info))
	}
}

func TestIssueReportLeavesOutTrafficPathsAndSecrets(t *testing.T) {
	home := t.TempDir()
	a := workspaceApp()
	a.settingsPath = filepath.Join(home, "settings.json")
	a.projectDir = filepath.Join(home, "private-project")
	a.socksUsername, a.socksPassword = "socks-user-seed", "socks-password-seed"
	a.appearance, a.language = "dark", "zh-Hans"
	a.socksOn, a.cookiePersistence = true, true
	a.vault.rows = []vaultRow{{name: "token", value: "vault-token-seed", domains: "api.example.com"}}
	a.flows[0].url = "https://api.example.com/projects?token=captured-query-secret"
	a.flows[0].path = "/projects?token=captured-query-secret"
	a.flows[0].reqBody = "captured-body-secret"
	a.requests[0].URL = a.flows[0].url

	info := a.issueInfo()
	body := issueBody(info)
	for _, want := range []string{
		version.Number, runtime.GOOS, runtime.GOARCH, mygo.Version, "zh-Hans",
		issueThemeLabel + ": dark", issueFeatureSocks, issueFeatureCookies,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{
		"vault-token-seed", "socks-user-seed", "socks-password-seed",
		"api.example.com", "captured-query-secret", "captured-body-secret",
		"/projects", home, "settings.json", "private-project",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body carries %q:\n%s", forbidden, body)
		}
	}
	u, err := url.Parse(issueURL(info))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "github.com" || u.Path != "/BABA983/vscroxy/issues/new" {
		t.Fatalf("issue destination = %q", u.String())
	}
	if u.Query().Get("body") != body {
		t.Fatalf("URL body = %q, want %q", u.Query().Get("body"), body)
	}
	for _, forbidden := range []string{"vault-token-seed", "api.example.com", home} {
		if strings.Contains(u.String(), forbidden) {
			t.Errorf("URL carries %q", forbidden)
		}
	}
}

func TestIssueBodyDoesNotFollowTheUILanguage(t *testing.T) {
	a := workspaceApp()
	a.language, a.appearance = "zh-Hans", "dark"
	original := i18n.Locale()
	t.Cleanup(func() { mygo.App.SetLocale(original) })
	mygo.App.SetLocale("en")
	en := issueBody(a.issueInfo())
	mygo.App.SetLocale("zh-Hans")
	zh := issueBody(a.issueInfo())
	if en != zh {
		t.Fatalf("body changed with the UI language:\nen:\n%s\nzh:\n%s", en, zh)
	}
}

func TestIssueURLTruncatesInsideGitHubsLimit(t *testing.T) {
	info := sampleIssueInfo()
	info.version = strings.Repeat("very-long-version ", 200)
	info.features = make([]string, 60)
	for i := range info.features {
		info.features[i] = "a very long feature name " + strings.Repeat("details ", 20)
	}
	body := issueBody(info)
	if len(body) > issueBodyBytes {
		t.Fatalf("body has %d bytes, limit %d", len(body), issueBodyBytes)
	}
	if !strings.Contains(body, issueTruncated) {
		t.Fatalf("body was not marked as truncated:\n%s", body)
	}
	if !strings.Contains(body, issueMygoLabel+": "+mygo.Version) {
		t.Fatalf("truncation lost the first fields:\n%s", body)
	}
	if link := issueURL(info); len(link) > 8192 {
		t.Fatalf("URL has %d bytes: %s", len(link), link)
	}
}

func TestReportIssueIsInPaletteAndHelpMenu(t *testing.T) {
	a := workspaceApp()
	actions := a.actions(nil)
	command := pick(actions, "help.report-issue")
	if command.id != "help.report-issue" || !command.enabled || command.label != locales.ReportIssue() ||
		command.why() != locales.ReportIssueHint() {
		t.Fatalf("command = %+v", command)
	}
	if _, ok := paletteFindEntry(a.paletteRankedResults("report issue", actions).entries, locales.ReportIssue()); !ok {
		t.Fatalf("command palette has no %q", locales.ReportIssue())
	}
	var found bool
	for _, top := range a.menu().Items() {
		if top.Label != locales.Help() {
			continue
		}
		for _, item := range top.Submenu {
			if item.ID == "help.report-issue" && item.Label == locales.ReportIssue() {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("Help menu has no Report issue command")
	}
}
