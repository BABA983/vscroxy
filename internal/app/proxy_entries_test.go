package app

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSOCKS5SettingsSaveValidateAndRestartState(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.socksOnDraft, a.socksPortInput = true, "9001"
	a.socksAuthDraft, a.socksUserDraft, a.socksPassDraft = true, "alice", "secret"
	a.saveSOCKS()
	if a.socksProblem != "" {
		t.Fatalf("save problem: %s", a.socksProblem)
	}
	if !a.socksOn || a.socksPort != 9001 || !a.socksAuth || a.socksUsername != "alice" || a.socksPassword != "secret" {
		t.Fatalf("saved SOCKS5 = %+v", a.socksEntry())
	}
	if a.socksEntry() == a.launchSOCKS {
		t.Fatal("a saved change did not mark a restart pending")
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.SOCKSOn || saved.SOCKSPort != 9001 || !saved.SOCKSAuth || saved.SOCKSUsername != "alice" || saved.SOCKSPassword != "secret" {
		t.Fatalf("persisted SOCKS5 = %+v", saved)
	}

	// A port outside the fixed range is refused without changing what is
	// saved, and a username is required when authentication is on.
	a.socksPortInput = "80"
	a.saveSOCKS()
	if a.socksProblem == "" || a.socksPort != 9001 {
		t.Fatalf("invalid port: problem %q, port %d", a.socksProblem, a.socksPort)
	}
	a.socksPortInput, a.socksUserDraft = "9001", "  "
	a.saveSOCKS()
	if a.socksProblem == "" || a.socksUsername != "alice" {
		t.Fatalf("missing username: problem %q, user %q", a.socksProblem, a.socksUsername)
	}
	a.socksUserDraft, a.socksPassDraft = "alice", ""
	a.saveSOCKS()
	if !strings.Contains(a.socksProblem, "password") || a.socksPassword != "secret" {
		t.Fatalf("missing password: problem %q, password %q", a.socksProblem, a.socksPassword)
	}

	// The fixed SOCKS5 port cannot collide with the HTTP proxy or a reverse
	// proxy, either.
	a.socksUserDraft, a.proxyPort = "alice", 7890
	a.socksPortInput = "7890"
	a.saveSOCKS()
	if !strings.Contains(a.socksProblem, "HTTP proxy") || a.socksPort != 9001 {
		t.Fatalf("HTTP port conflict: problem %q, port %d", a.socksProblem, a.socksPort)
	}
	a.proxyPort = 0
	a.reverses = []reverseProxySetting{{ID: "r", Port: 9002, Target: "http://example.test", Enabled: true}}
	a.socksPortInput = "9002"
	a.saveSOCKS()
	if !strings.Contains(a.socksProblem, "reverse proxy") || a.socksPort != 9001 {
		t.Fatalf("reverse proxy port conflict: problem %q, port %d", a.socksProblem, a.socksPort)
	}
}

func TestReverseProxyAddEditToggleRemove(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")

	a.openReverseProxy("")
	a.reverseDraft.port, a.reverseDraft.target, a.reverseDraft.keepHost, a.reverseDraft.insecure = "9002", "https://api.example.com/v1", true, true
	a.saveReverseProxy()
	if a.reverseDraft.problem != "" || len(a.reverses) != 1 {
		t.Fatalf("add: problem %q, entries %+v", a.reverseDraft.problem, a.reverses)
	}
	id := a.reverses[0].ID
	if id == "" || a.reverses[0].Port != 9002 || !a.reverses[0].KeepHost || !a.reverses[0].Enabled || !a.reverses[0].Insecure {
		t.Fatalf("added %+v", a.reverses[0])
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ReverseProxies) != 1 || saved.ReverseProxies[0].Target != "https://api.example.com/v1" || !saved.ReverseProxies[0].Insecure {
		t.Fatalf("persisted %+v", saved.ReverseProxies)
	}

	// A second entry cannot reuse the port.
	a.openReverseProxy("")
	a.reverseDraft.port, a.reverseDraft.target = "9002", "http://other.test"
	a.saveReverseProxy()
	if a.reverseDraft.problem == "" || len(a.reverses) != 1 {
		t.Fatalf("duplicate port accepted: %q %+v", a.reverseDraft.problem, a.reverses)
	}
	// Nor can a malformed target be saved.
	a.reverseDraft.port, a.reverseDraft.target = "9003", "ftp://other.test"
	a.saveReverseProxy()
	if a.reverseDraft.problem == "" || len(a.reverses) != 1 {
		t.Fatalf("bad target accepted: %q %+v", a.reverseDraft.problem, a.reverses)
	}

	a.openReverseProxy(id)
	a.reverseDraft.target = "http://127.0.0.1:9/base"
	a.saveReverseProxy()
	if len(a.reverses) != 1 || a.reverses[0].ID != id || a.reverses[0].Target != "http://127.0.0.1:9/base" {
		t.Fatalf("edit %+v", a.reverses)
	}
	a.setReverseEnabled(id, false)
	if a.reverses[0].Enabled {
		t.Fatal("toggle off did not apply")
	}
	a.removeReverseProxy(id)
	if len(a.reverses) != 0 {
		t.Fatalf("remove left %+v", a.reverses)
	}
}

func TestReverseProxyRejectsTheRunningProxyPort(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.srv = requestProxy(t)
	_, portText, err := net.SplitHostPort(a.srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	a.openReverseProxy("")
	a.reverseDraft.port, a.reverseDraft.target = strconv.Itoa(port), "http://example.test"
	a.saveReverseProxy()
	if !strings.Contains(a.reverseDraft.problem, "HTTP proxy") || len(a.reverses) != 0 {
		t.Fatalf("running proxy port: %q %+v", a.reverseDraft.problem, a.reverses)
	}
}

func TestProxySettingsExposeSOCKS5AndReverseProxies(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.settingsOpen, a.settingsTab = true, 1
	tt := ui.NewTester(a.view, 1280, 900)
	for range 3 {
		tt.Scroll(640, 500, 0, 500)
	}
	if _, ok := tt.Find("Enable SOCKS5 proxy"); !ok {
		t.Fatal("Settings → Proxy does not show the SOCKS5 switch")
	}
	if err := tt.Click("Enable SOCKS5 proxy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for range 2 {
		tt.Scroll(640, 500, 0, 500)
	}
	if err := tt.Click("Save SOCKS5 settings"); err != nil {
		t.Fatal(err)
	}
	if !a.socksOn || a.socksProblem != "" {
		t.Fatalf("SOCKS5 save: on=%t problem=%q", a.socksOn, a.socksProblem)
	}
	for range 2 {
		tt.Scroll(640, 500, 0, 500)
	}
	if err := tt.Click("Add reverse proxy…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Local port"); err != nil {
		t.Fatal(err)
	}
	tt.Type("9005")
	if err := tt.Click("Remote URL"); err != nil {
		t.Fatal(err)
	}
	tt.Type("https://api.example.com")
	if _, ok := tt.Find("Allow self-signed certificates"); !ok {
		t.Fatal("the reverse proxy dialog does not show the certificate option")
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if len(a.reverses) != 1 || a.reverses[0].Port != 9005 || a.reverses[0].Target != "https://api.example.com" {
		t.Fatalf("reverse proxy from the UI: %+v (problem %q)", a.reverses, a.reverseDraft.problem)
	}
	if _, err := os.Stat(a.settingsPath); err != nil {
		t.Fatalf("settings were not saved: %v", err)
	}
}

func TestConfigImportKeepsTheLocalSOCKSPassword(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.socksOn, a.socksAuth, a.socksUsername, a.socksPassword = true, true, "alice", "local-secret"

	a.applySettings(settings{SOCKSOn: true, SOCKSAuth: true, SOCKSUsername: "bob"})
	if !a.socksAuth || a.socksUsername != "bob" || a.socksPassword != "local-secret" {
		t.Fatalf("imported SOCKS5 = %+v, want auth on with the local password", a.socksEntry())
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.SOCKSAuth || saved.SOCKSPassword != "local-secret" {
		t.Fatalf("persisted SOCKS5 = %+v", saved)
	}
}

func TestConfigImportDisablesAuthWithoutAnyPassword(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.socksOn, a.socksAuth, a.socksUsername, a.socksPassword = true, false, "", ""

	a.applySettings(settings{SOCKSOn: true, SOCKSAuth: true, SOCKSUsername: "bob"})
	if a.socksAuth || a.socksPassword != "" {
		t.Fatalf("imported SOCKS5 = %+v, want authentication off without a password", a.socksEntry())
	}
	a.mu.Lock()
	notices := slices.Clone(a.notices)
	a.mu.Unlock()
	found := false
	for _, n := range notices {
		if strings.Contains(n.text, "SOCKS5 authentication is off") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning for disabled SOCKS5 authentication: %+v", notices)
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.SOCKSAuth || saved.SOCKSPassword != "" {
		t.Fatalf("persisted SOCKS5 = %+v", saved)
	}

	// The configuration path itself refuses authentication without a
	// password, so a hand-edited settings file cannot start in that state.
	a.socksAuth, a.socksUsername, a.socksPassword = true, "bob", ""
	if _, err := a.socksConfig(); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("socksConfig error = %v", err)
	}
}
