package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func cookieTestApp(t *testing.T, path string, persist bool) *app {
	t.Helper()
	a := workspaceApp()
	a.cookiesPath, a.cookiePersistence = path, persist
	a.loadCookies()
	return a
}

func cookieNames(cookies []*http.Cookie) []string {
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		names = append(names, c.Name)
	}
	slices.Sort(names)
	return names
}

func TestCookiePersistenceRoundTripPreservesScopeAndPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	a := cookieTestApp(t, path, true)
	expires := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	u, _ := url.Parse("https://api.example.com/app/login")
	a.requestCookies().SetCookies(u, []*http.Cookie{
		{Name: "session", Value: "session-value", Path: "/app"},
		{Name: "host", Value: "host-value", Path: "/app", Expires: expires, HttpOnly: true},
		{Name: "domain", Value: "domain-value", Domain: "example.com", Path: "/", Expires: expires, Secure: true, HttpOnly: true},
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cookie file mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session-value") {
		t.Fatal("a session cookie was written to disk")
	}
	if !strings.Contains(string(data), "host-value") || !strings.Contains(string(data), "domain-value") {
		t.Fatalf("persistent cookies missing from disk: %s", data)
	}

	// A second app, as after a restart, reads the same file.
	b := cookieTestApp(t, path, true)
	app, _ := url.Parse("https://api.example.com/app/page")
	names := cookieNames(b.requestCookies().Cookies(app))
	if !slices.Equal(names, []string{"domain", "host"}) {
		t.Fatalf("loaded cookies for /app/page = %v", names)
	}
	other, _ := url.Parse("https://api.example.com/other")
	if names := cookieNames(b.requestCookies().Cookies(other)); !slices.Equal(names, []string{"domain"}) {
		t.Fatalf("path scope after restart = %v", names)
	}
	plain, _ := url.Parse("http://api.example.com/app/page")
	if names := cookieNames(b.requestCookies().Cookies(plain)); !slices.Equal(names, []string{"host"}) {
		t.Fatalf("Secure scope after restart = %v", names)
	}
	for _, c := range b.requestCookies().snapshot() {
		if !c.cookie.HttpOnly {
			t.Fatalf("HttpOnly lost for %q: %+v", c.cookie.Name, c.cookie)
		}
	}
}

func TestCookiePersistenceKeepsOnlyTheCookieOrigin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	a := cookieTestApp(t, path, true)
	u, _ := url.Parse("https://private-user:private-pass@api.example.com/app/login?token=query-secret#fragment")
	a.requestCookies().SetCookies(u, []*http.Cookie{{Name: "keep", Value: "v", Path: "/", Expires: time.Now().Add(time.Hour)}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-user", "private-pass", "query-secret", "login"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("cookie file kept URL %q: %s", secret, data)
		}
	}
	if !strings.Contains(string(data), `"origin":"https://api.example.com"`) {
		t.Fatalf("cookie origin = %s", data)
	}
}

func TestCookiePersistenceIgnoresExpiredAndSessionRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	now := time.Now()
	data, err := json.Marshal(cookieFile{Cookies: []cookieRecord{
		{Origin: "https://api.example.com/", Name: "expired", Value: "old", Path: "/", Expires: now.Add(-time.Hour)},
		{Origin: "https://api.example.com/", Name: "session", Value: "kept-in-memory", Path: "/"},
		{Origin: "https://api.example.com/", Name: "alive", Value: "yes", Path: "/", Expires: now.Add(time.Hour)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	a := cookieTestApp(t, path, true)
	u, _ := url.Parse("https://api.example.com/")
	if names := cookieNames(a.requestCookies().Cookies(u)); !slices.Equal(names, []string{"alive"}) {
		t.Fatalf("loaded cookies = %v, want only alive", names)
	}
}

func TestCookiePersistenceClearDomainAndClearAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	a := cookieTestApp(t, path, true)
	expires := time.Now().Add(time.Hour)
	api, _ := url.Parse("https://api.example.com/")
	other, _ := url.Parse("https://other.test/")
	a.requestCookies().SetCookies(api, []*http.Cookie{
		{Name: "host", Value: "one", Path: "/", Expires: expires},
		{Name: "domain", Value: "two", Domain: "example.com", Path: "/", Expires: expires},
	})
	a.requestCookies().SetCookies(other, []*http.Cookie{
		{Name: "elsewhere", Value: "three", Path: "/", Expires: expires},
	})
	a.requestCookies().clearDomain("example.com")
	if names := cookieNames(a.requestCookies().Cookies(api)); !slices.Equal(names, []string{"host"}) {
		t.Fatalf("clearDomain removed the wrong cookies: %v", names)
	}
	if names := cookieNames(a.requestCookies().Cookies(other)); !slices.Equal(names, []string{"elsewhere"}) {
		t.Fatalf("clearDomain removed another domain: %v", names)
	}
	a.requestCookies().clearDomain("api.example.com")
	if names := cookieNames(a.requestCookies().Cookies(api)); len(names) != 0 {
		t.Fatalf("host-only clear left cookies: %v", names)
	}
	a.requestCookies().clear()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clear all left the cookie file: %v", err)
	}
}

func TestCookiesStayInMemoryByDefaultAndTurningOffRemovesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	a := cookieTestApp(t, path, false)
	u, _ := url.Parse("https://api.example.com/")
	a.requestCookies().SetCookies(u, []*http.Cookie{{Name: "keep", Value: "v", Path: "/", Expires: time.Now().Add(time.Hour)}})
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("default-off persistence wrote a file: %v", err)
	}

	a.setCookiePersistence(true)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("turning persistence on did not write the jar: %v", err)
	}
	a.setCookiePersistence(false)
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("turning persistence off left the cookie file: %v", err)
	}
	if names := cookieNames(a.requestCookies().Cookies(u)); !slices.Equal(names, []string{"keep"}) {
		t.Fatalf("turning persistence off lost the in-memory cookie: %v", names)
	}
}

func TestPersistedCookieJarFeedsSendAndPmCookies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("keep"); err != nil || c.Value != "v" {
			http.Error(w, "missing persisted cookie", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := cookieTestApp(t, filepath.Join(t.TempDir(), "cookies.json"), true)
	u, _ := url.Parse(upstream.URL)
	a.requestCookies().SetCookies(u, []*http.Cookie{{Name: "keep", Value: "v", Path: "/", Expires: time.Now().Add(time.Hour)}})
	out := savedRequest{Method: "GET", URL: upstream.URL, Pre: `pm.test("cookie jar", () => pm.expect(pm.cookies.get("keep")).to.equal("v"));`}.
		Execute(t.Context(), requestProxy(t), request.Context{}, nil, a.requestCookies())
	if out.Err != nil || out.Response == nil || out.Response.Status != 200 || len(out.Tests) != 1 || !out.Tests[0].Pass {
		t.Fatalf("send with the persisted jar: err=%v response=%+v tests=%+v", out.Err, out.Response, out.Tests)
	}
}

func TestCookieRetentionIsNotPartOfConfigurationExportOrImport(t *testing.T) {
	a := workspaceApp()
	a.cookiePersistence = true
	b, _ := a.exportConfig()
	if b.Settings == nil || b.Settings.CookiePersistence {
		t.Fatal("configuration export carried the cookie retention choice")
	}
	a.cookiePersistence = false
	a.applySettings(settings{CookiePersistence: true})
	if a.cookiePersistence {
		t.Fatal("configuration import turned cookie retention on")
	}
}

func TestCookieManagerMasksValuesAndClearsByDomain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	a := cookieTestApp(t, path, true)
	u, _ := url.Parse("https://api.example.com/")
	a.requestCookies().SetCookies(u, []*http.Cookie{{Name: "session", Value: "super-secret", Domain: "example.com", Path: "/", Expires: time.Now().Add(time.Hour)}})
	a.openCookies()
	tt := ui.NewTester(a.view, 1000, 760)
	if !tt.HasText("session = ••••••••  masked") {
		t.Fatalf("cookie value was not masked: %v", tt.Texts())
	}
	if tt.HasText("session = super-secret") {
		t.Fatal("cookie value was visible before reveal")
	}
	if err := tt.Click("Reveal cookie session /"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("session = super-secret") {
		t.Fatalf("revealing the cookie did not show its value: %v", tt.Texts())
	}
	if err := tt.Click("Clear cookies for example.com"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("No cookies yet. Send a request or add one below.") {
		t.Fatalf("clearing a domain left rows: %v", tt.Texts())
	}
}
