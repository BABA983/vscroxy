package imports

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

func ruleOf(t *testing.T, rules []capture.Rule, kind capture.RuleType) capture.Rule {
	t.Helper()
	for _, r := range rules {
		if r.Type == kind {
			return r
		}
	}
	t.Fatalf("no %s rule in %+v", kind, rules)
	return capture.Rule{}
}

func matches(t *testing.T, pattern, url string) bool {
	t.Helper()
	return regexp.MustCompile(pattern).MatchString(url)
}

func TestCharlesSettingsMapsPublishedLayouts(t *testing.T) {
	data, err := os.ReadFile("testdata/charles-settings.xml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadCharlesSettings(data, "charles-settings.xml")
	if err != nil {
		t.Fatal(err)
	}

	local := ruleOf(t, got.Rules, capture.RuleMapLocal)
	if local.Target != "/Users/me/app/dist" || local.Disabled || local.MatchMode != "regex" {
		t.Fatalf("map local = %+v", local)
	}
	// The path is a Charles wildcard, the case flag is off, and the default
	// port is optional: each side of every boundary is checked.
	for url, want := range map[string]bool{
		"https://static.example.test/app/main.js":      true,
		"https://STATIC.example.test:443/APP/main.js":  true,
		"https://static.example.test/app/main.js?v=2":  true,
		"https://static.example.test/app":              false,
		"https://static.example.test/other/main.js":    false,
		"https://static.example.test:8443/app/main.js": false,
		"http://static.example.test/app/main.js":       false,
		"https://static.example.testing/app/main.js":   false,
	} {
		if matches(t, local.Match, url) != want {
			t.Errorf("map local %q matches %s: want %v", local.Match, url, want)
		}
	}

	remote := ruleOf(t, got.Rules, capture.RuleMapRemote)
	if remote.Target != "http://localhost:8666" || remote.PathFrom != "/newtools/index" || remote.PathTo != "/activity/index.html" || !remote.KeepHost {
		t.Fatalf("map remote = %+v", remote)
	}
	for url, want := range map[string]bool{
		"http://old.example.test/newtools/index?id=1": true,
		"http://old.example.test:80/newtools/index":   true,
		"https://old.example.test/newtools/index":     false,
		"http://old.example.test/newtools/index2":     false,
	} {
		if matches(t, remote.Match, url) != want {
			t.Errorf("map remote %q matches %s: want %v", remote.Match, url, want)
		}
	}

	block := ruleOf(t, got.Rules, capture.RuleBlock)
	if block.Block != capture.BlockRefuse || !matches(t, block.Match, "https://ads.example.test/banner") {
		t.Errorf("block = %+v", block)
	}

	if !got.NoCaching {
		t.Error("global No Caching was not read")
	}
	if got.SSLAll || !slices.Equal(got.SSLHosts, []string{"api.example.test"}) {
		t.Errorf("SSL all=%v hosts=%v; want only the enabled host api.example.test", got.SSLAll, got.SSLHosts)
	}

	for _, r := range got.Rules {
		if r.Type == capture.RuleBreak || r.Type == capture.RuleBreakResponse {
			t.Errorf("breakpoint imported: %+v", r)
		}
	}
	report := strings.Join(got.Warnings, "\n")
	for _, want := range []string{
		"Rewrite: 1 set(s) not imported",
		"DNS Spoofing: switched on",
		"Breakpoints: not imported",
		"public export layouts",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestCharlesBlockActionZeroDropsTheConnection(t *testing.T) {
	data := `<?xml version="1.0"?><charles-export><toolConfiguration><configs><entry><string>Black List</string><blacklist>` +
		`<locations><locationPatterns><locationMatch><location><host>ads.example.test</host><port>*</port></location><enabled>true</enabled></locationMatch></locationPatterns></locations>` +
		`<toolEnabled>true</toolEnabled><useSelectedLocations>false</useSelectedLocations><action>0</action>` +
		`</blacklist></entry></configs></toolConfiguration></charles-export>`
	got, err := ReadCharlesSettings([]byte(data), "block.xml")
	if err != nil {
		t.Fatal(err)
	}
	block := ruleOf(t, got.Rules, capture.RuleBlock)
	if block.Block != capture.BlockDrop || block.Disabled {
		t.Fatalf("block = %+v; want an enabled drop rule for action 0", block)
	}
}

func TestCharlesSettingsRejectsDTDAndWrongRoot(t *testing.T) {
	for name, data := range map[string]string{
		"doctype": `<?xml version="1.0"?><!DOCTYPE charles-export [<!ENTITY x SYSTEM "file:///etc/passwd">]><charles-export/>`,
		"root":    `<?xml version="1.0"?><other-export/>`,
	} {
		if _, err := ReadCharlesSettings([]byte(data), name+".xml"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
