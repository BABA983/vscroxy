package app

import (
	"cmp"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type storedCookie struct {
	key    string
	origin url.URL
	cookie http.Cookie
}

// cookieStore keeps metadata for inspection/removal; Go's jar remains the
// authority for domain/path/Secure matching and expiry, including redirects.
// It is shared by Requests and Runner, never persisted or used by raw Replay.
type cookieStore struct {
	mu    sync.Mutex
	jar   *cookiejar.Jar
	items map[string]storedCookie
}

func newCookieJar() *cookiejar.Jar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return jar
}

func (s *cookieStore) init() {
	if s.jar == nil {
		s.jar, s.items = newCookieJar(), make(map[string]storedCookie)
	}
}

func (s *cookieStore) Cookies(u *url.URL) []*http.Cookie {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	return s.jar.Cookies(u)
}

func (s *cookieStore) SetCookies(u *url.URL, cookies []*http.Cookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.jar.SetCookies(u, cookies)
	now := time.Now()
	for _, c := range cookies {
		if c.Valid() != nil {
			continue
		}
		copy := *c
		if copy.Path == "" || copy.Path[0] != '/' {
			path, _, ok := strings.CutLast(u.Path, "/")
			copy.Path = "/"
			if ok && path != "" {
				copy.Path = path
			}
		}
		domain := strings.TrimPrefix(strings.ToLower(cmp.Or(copy.Domain, u.Hostname())), ".")
		if ip, err := netip.ParseAddr(domain); err == nil {
			domain = ip.String()
		} else {
			var err error
			domain, err = idna.Lookup.ToASCII(domain)
			if err != nil {
				continue
			}
		}
		key := domain + "\x00" + copy.Path + "\x00" + copy.Name
		// Probe a single cookie at its own path over HTTPS. Rejected domains
		// and public suffixes cannot add OR delete another origin's metadata.
		probe := u.Clone()
		probe.Scheme, probe.Path = "https", copy.Path
		test := newCookieJar()
		probeCookie := copy
		probeCookie.MaxAge, probeCookie.Expires = 0, time.Time{}
		test.SetCookies(u, []*http.Cookie{&probeCookie})
		if len(test.Cookies(probe)) == 0 {
			continue
		}
		if copy.MaxAge < 0 || !copy.Expires.IsZero() && !copy.Expires.After(now) && copy.MaxAge <= 0 {
			delete(s.items, key)
			continue
		}
		if copy.MaxAge > 0 {
			copy.Expires = now.Add(time.Duration(min(copy.MaxAge, int((1<<63-1)/int64(time.Second)))) * time.Second)
			copy.MaxAge = 0
		}
		s.items[key] = storedCookie{key: key, origin: *u.Clone(), cookie: copy}
	}
}

func (s *cookieStore) snapshot() []storedCookie {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	var out []storedCookie
	for key, c := range s.items {
		if !c.cookie.Expires.IsZero() && !c.cookie.Expires.After(time.Now()) {
			delete(s.items, key)
			continue
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b storedCookie) int { return strings.Compare(a.key, b.key) })
	return out
}

func (s *cookieStore) remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	delete(s.items, key)
	s.jar = newCookieJar()
	for _, item := range s.items {
		s.jar.SetCookies(&item.origin, []*http.Cookie{&item.cookie})
	}
}

func (s *cookieStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jar, s.items = newCookieJar(), make(map[string]storedCookie)
}

func (a *app) requestCookies() *cookieStore {
	if a.cookies == nil {
		a.cookies = &cookieStore{}
	}
	return a.cookies
}

type cookieEditor struct {
	open                                    bool
	url, name, value, path, domain, problem string
	secure, httpOnly                        bool
}

func (a *app) openCookies() {
	e := &a.cookieUI
	e.open = true
	if a.requestDocument != nil {
		e.url = expandVars(a.composeURL, a.activeVars())
	}
	if e.path == "" {
		e.path = "/"
	}
}

func (a *app) saveCookie() {
	e := &a.cookieUI
	u, err := url.Parse(e.url)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		e.problem = "Enter an absolute HTTP(S) URL"
		return
	}
	if !strings.HasPrefix(e.path, "/") {
		e.problem = "Cookie path must start with /"
		return
	}
	c := &http.Cookie{Name: e.name, Value: e.value, Path: e.path, Domain: e.domain, Secure: e.secure, HttpOnly: e.httpOnly}
	if err := c.Valid(); err != nil {
		e.problem = err.Error()
		return
	}
	probe := &cookieStore{}
	probe.SetCookies(u, []*http.Cookie{c})
	if len(probe.snapshot()) == 0 {
		e.problem = "Cookie domain was rejected for this URL"
		return
	}
	a.requestCookies().SetCookies(u, []*http.Cookie{c})
	e.problem = ""
	e.name, e.value = "", ""
}

func (a *app) cookiesModal(c *ui.Context, t *ui.Theme) {
	e := &a.cookieUI
	if !e.open {
		return
	}
	appModal(c, &e.open, func() {
		ui.Column(c).Width(760).Height(480).Gap(12).Children(func() {
			dialogTitle(c, "Session Cookies")
			ui.Text(c, "Shared by Requests and Runner, including Set-Cookie on redirects. Memory only: cleared when the app exits. Capture replay does not use this jar.").FontSize(11).TextColor(t.TextMuted)
			ui.Scroll(c).Grow(1).Gap(8).Children(func() {
				items := a.requestCookies().snapshot()
				if len(items) == 0 {
					ui.Text(c, "No cookies yet. Send a request or add one below.").TextColor(t.TextMuted)
				}
				for _, item := range items {
					ui.Row(c).Key(item.key).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(3).Children(func() {
							ui.Text(c, item.cookie.Name+" = "+item.cookie.Value).FontSize(12).Font("monospace").SingleLine()
							domain := cmp.Or(item.cookie.Domain, item.origin.Hostname())
							ui.Text(c, fmt.Sprintf("%s%s · Secure %t · HttpOnly %t", domain, item.cookie.Path, item.cookie.Secure, item.cookie.HttpOnly)).FontSize(11).TextColor(t.TextMuted)
						})
						if ui.Button(c, "Delete").Label("Delete cookie " + item.cookie.Name + " " + item.cookie.Path).Clicked() {
							a.cookies.remove(item.key)
						}
					})
				}
			})
			ui.Divider(c)
			ui.Text(c, "Add replaces the same domain / path / name. Leave Domain empty for a host-only cookie.").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &e.url).Label("Cookie URL").Placeholder("https://api.example.com/")
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &e.name).Grow(1).Label("Cookie name").Placeholder("Name")
				ui.TextInput(c, &e.value).Grow(2).Label("Cookie value").Placeholder("Value").Password()
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.TextInput(c, &e.domain).Grow(1).Label("Cookie domain").Placeholder("Domain (optional)")
				ui.TextInput(c, &e.path).Width(100).Label("Cookie path").Placeholder("/")
				ui.Checkbox(c, &e.secure, "Secure")
				ui.Checkbox(c, &e.httpOnly, "HttpOnly")
			})
			if e.problem != "" {
				ui.Text(c, e.problem).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Clear all cookies").Clicked() {
					a.requestCookies().clear()
				}
				ui.Spacer(c)
				if ui.Button(c, "Close").Clicked() {
					e.open = false
				}
				if ui.PrimaryButton(c, "Add cookie").Clicked() {
					a.saveCookie()
				}
			})
		})
	})
}
