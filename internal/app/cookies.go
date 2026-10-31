package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/request"
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
		e.url = request.ExpandVars(a.composeURL, a.activeVars())
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
	_, height := c.Size()
	uikit.Modal(c, &e.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(480, height-80)).Children(func() {
			uikit.DialogTitle(c, "Session Cookies")
			uikit.Meta(c, "Shared by Requests and Runner. Kept in memory until the app quits.")
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Children(func() {
				items := a.requestCookies().snapshot()
				if len(items) == 0 {
					uikit.Hint(c, "No cookies yet. Send a request or add one below.")
				}
				for _, item := range items {
					ui.Row(c).Key(item.key).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(t.Space(1)).Children(func() {
							uikit.Mono(c, item.cookie.Name+" = "+item.cookie.Value).SingleLine()
							domain := cmp.Or(item.cookie.Domain, item.origin.Hostname())
							uikit.Meta(c, fmt.Sprintf("%s%s · Secure %t · HttpOnly %t", domain, item.cookie.Path, item.cookie.Secure, item.cookie.HttpOnly))
						})
						if uikit.SecondaryButton(c, "Delete").Label("Delete cookie " + item.cookie.Name + " " + item.cookie.Path).Clicked() {
							a.cookies.remove(item.key)
						}
					})
				}
				ui.Divider(c)
				uikit.Meta(c, "Add replaces the same domain / path / name. Leave Domain empty for a host-only cookie.")
				ui.Form(c, func() {
					ui.Field(c, "Cookie URL", func() {
						uikit.Input(c, &e.url).Label("Cookie URL").Placeholder("https://api.example.com/")
					})
					ui.Field(c, "Cookie name", func() {
						uikit.Input(c, &e.name).Label("Cookie name").Placeholder("Name")
					})
					ui.Field(c, "Cookie value", func() {
						uikit.Input(c, &e.value).Label("Cookie value").Placeholder("Value").Password()
					})
					ui.Field(c, "Cookie domain", func() {
						uikit.Input(c, &e.domain).Label("Cookie domain").Placeholder("Domain (optional)")
					})
					ui.Field(c, "Cookie path", func() {
						ui.Row(c).Gap(t.Space(3)).Children(func() {
							uikit.Input(c, &e.path).Width(100).Label("Cookie path").Placeholder("/")
							ui.Checkbox(c, &e.secure, "Secure")
							ui.Checkbox(c, &e.httpOnly, "HttpOnly")
						})
					})
				})
			})
			if e.problem != "" {
				ui.Text(c, e.problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Clear all cookies").Clicked() {
					a.requestCookies().clear()
				}
				ui.Spacer(c)
				if uikit.SecondaryButton(c, "Close").Clicked() {
					e.open = false
				}
				if uikit.PrimaryButton(c, "Add cookie").Clicked() {
					a.saveCookie()
				}
			})
		})
	})
}
