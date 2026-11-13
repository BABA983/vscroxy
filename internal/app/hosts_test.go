package app

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/egoist/mygo/ui"
)

// getThrough asks the proxy for target and returns the status, 0 for a
// failed request.
func getThrough(t *testing.T, a *app, target string) int {
	t.Helper()
	proxy, _ := url.Parse("http://" + a.srv.Addr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	resp, err := client.Get(target)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestHostProfilesEditedInToolsAndSwitchedFromTheTitleBar(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	target := "http://mapped.test:" + port + "/"

	a := workspaceApp()
	a.srv = requestProxy(t)
	a.profiles.dir = t.TempDir()
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if got := getThrough(t, a, target); got != http.StatusBadGateway {
		t.Fatalf("before any profile, status = %d, want 502", got)
	}

	// No profile is on, so the footer says nothing about Hosts: the chip is
	// for the profiles in use, and Tools is where they are turned on.
	if tt.HasText("Host profiles") {
		t.Fatal("the footer offers Hosts while none is on")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyH)
	tt.Frame()
	if !a.toolsOpen || !tt.HasText("No Host profiles") {
		t.Fatal("the Host profiles shortcut did not open Tools on Host profiles")
	}
	click("New profile")
	st := a.profiles.editors["Untitled"]
	if st == nil {
		t.Fatalf("no editor for the new profile; profiles = %+v", a.profiles.set.Profiles)
	}
	st.Buffer.Set("# test\n127.0.0.1 mapped.test\n10.0.0.x broken.test\n")
	tt.Frame()
	if len(a.profiles.diag) != 1 || !strings.Contains(a.profiles.diag[0].Message, `"10.0.0.x" is not an address`) {
		t.Fatalf("diagnostics = %+v, want the bad address on line 3", a.profiles.diag)
	}
	click("Save")
	path := filepath.Join(a.profiles.dir, "Untitled.hosts")
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "127.0.0.1 mapped.test") {
		t.Fatalf("saved profile = %q, %v", data, err)
	}
	// Saved but off: nothing is mapped yet.
	if got := getThrough(t, a, target); got != http.StatusBadGateway {
		t.Fatalf("with the profile off, status = %d, want 502", got)
	}

	click("Profile name")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Staging")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if _, err := os.Stat(filepath.Join(a.profiles.dir, "Staging.hosts")); err != nil || a.profiles.set.Index("Staging") != 0 {
		t.Fatalf("rename: %v; profiles = %+v", err, a.profiles.set.Profiles)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the old file is still there: %v", err)
	}
	click("Done")

	tt.Key(ui.Cmd|ui.Shift, ui.KeyH)
	tt.Frame()
	click("Host profile: Staging")
	if got := getThrough(t, a, target); got != http.StatusOK {
		t.Fatalf("with Staging on, status = %d, want the upstream's 200", got)
	}
	index, err := os.ReadFile(filepath.Join(a.profiles.dir, hosts.IndexFile))
	if err != nil || !strings.Contains(string(index), `"on": true`) {
		t.Fatalf("the switch was not saved: %s, %v", index, err)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if _, ok := tt.Find("Host profiles"); !ok || !tt.HasText("Staging") {
		t.Fatalf("no Hosts chip while a profile is on: %q", tt.Texts())
	}
	click("Disable Staging")
	if len(a.profiles.set.On()) != 0 {
		t.Fatal("the chip's × left a profile on")
	}
	if got := getThrough(t, a, target); got != http.StatusBadGateway {
		t.Fatalf("after turning profiles off, status = %d, want 502", got)
	}
}

func TestMapThisHostAddsToAProfileAndDeleteCanBeUndone(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	dir := t.TempDir()
	a.profiles.dir = dir
	// A hosts file put there by hand joins the profiles, off.
	if err := os.WriteFile(filepath.Join(dir, "Lab.hosts"), []byte("10.0.0.7 lab.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadHosts()
	if a.profiles.set.Index("Lab") != 0 || a.profiles.set.Profiles[0].On {
		t.Fatalf("profiles = %+v, want Lab, off", a.profiles.set.Profiles)
	}
	tt := ui.NewTester(a.view, 1280, 800)
	a.openDNSRule("api.example.com", "Browser")
	a.dnsRuleAddr = "10.0.3.21"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	lab := a.profiles.set.Profiles[0]
	if !lab.On || !strings.HasSuffix(lab.Text, "10.0.0.7 lab.test\n10.0.3.21 api.example.com @ Browser\n") {
		t.Fatalf("Lab = %+v, want the mapping appended and Lab on", lab)
	}
	if r, ok := hosts.Lookup(a.profiles.set.Rules(), "api.example.com", "Browser"); !ok || r.IP.String() != "10.0.3.21" {
		t.Fatalf("lookup = %+v, %v", r, ok)
	}

	a.deleteProfile(0)
	if _, err := os.Stat(filepath.Join(dir, "Lab.hosts")); !os.IsNotExist(err) || len(a.profiles.set.Profiles) != 0 {
		t.Fatalf("delete left %+v, %v", a.profiles.set.Profiles, err)
	}
	tt.Frame()
	if err := tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if i := a.profiles.set.Index("Lab"); i != 0 || !a.profiles.set.Profiles[0].On {
		t.Fatalf("undo = %+v, want Lab back and on", a.profiles.set.Profiles)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "Lab.hosts")); !strings.Contains(string(data), "api.example.com") {
		t.Fatalf("undo restored %q", data)
	}
}

// A new profile takes the selection. A keyed list puts the selection back on
// the row whose key it remembers, so pointing at the new profile alone handed
// the selection to the one chosen before it.
func TestNewHostProfileTakesTheSelection(t *testing.T) {
	a := workspaceApp()
	a.profiles.dir = t.TempDir()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyH)
	tt.Frame()
	if err := tt.Click("New profile"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("New profile"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h := &a.profiles
	if len(h.set.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(h.set.Profiles))
	}
	if got := h.set.Profiles[h.choice].Name; got == h.set.Profiles[0].Name {
		t.Fatalf("the selection stayed on %q after a second profile", got)
	}
	if *h.list.Selected != h.choice {
		t.Fatalf("the list shows row %d, the app chose %d", *h.list.Selected, h.choice)
	}
}
