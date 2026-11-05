package app

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type storedCookie struct {
	key    string
	origin url.URL
	cookie http.Cookie
}

// cookieRecord is one cookie in the local file. Session cookies have no
// Expires and are never written; the origin is kept so a host-only cookie
// loads back as one.
type cookieRecord struct {
	Origin   string    `json:"origin"`
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain,omitempty"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires"`
	Secure   bool      `json:"secure,omitempty"`
	HttpOnly bool      `json:"httpOnly,omitempty"`
}

type cookieFile struct {
	Cookies []cookieRecord `json:"cookies"`
}

// cookieStore keeps metadata for inspection/removal; Go's jar remains the
// authority for domain/path/Secure matching and expiry, including redirects.
// It is shared by Requests, Runner, scripts and the interactive clients, and
// never used by raw Replay. Persistent cookies may be written to path, which
// holds nothing else and is private (0600).
type cookieStore struct {
	mu        sync.Mutex
	jar       *cookiejar.Jar
	items     map[string]storedCookie
	path      string
	persist   bool
	problem   error
	onProblem func(error)
}

func newCookieJar() *cookiejar.Jar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return jar
}

func (s *cookieStore) init() {
	if s.jar == nil {
		s.jar = newCookieJar()
	}
	if s.items == nil {
		s.items = make(map[string]storedCookie)
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
	s.init()
	s.setCookiesLocked(u, cookies)
	err := s.saveLocked()
	s.mu.Unlock()
	s.report(err)
}

// setCookiesLocked records what the jar accepted. It never writes the file,
// so loading persisted cookies cannot overwrite them while they are read.
func (s *cookieStore) setCookiesLocked(u *url.URL, cookies []*http.Cookie) {
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
		// Only scheme and host are the cookie's origin. A request URL may
		// carry a path, query or userinfo that the cookie file must not keep.
		s.items[key] = storedCookie{key: key, origin: url.URL{Scheme: u.Scheme, Host: u.Host}, cookie: copy}
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
	s.init()
	delete(s.items, key)
	s.rebuildJarLocked()
	err := s.saveLocked()
	s.mu.Unlock()
	s.report(err)
}

// clearDomain removes every cookie whose effective domain is domain, exact
// and case-insensitive. Subdomains are separate domains.
func (s *cookieStore) clearDomain(domain string) {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	s.mu.Lock()
	s.init()
	for key, item := range s.items {
		if cookieDomain(item) == domain {
			delete(s.items, key)
		}
	}
	s.rebuildJarLocked()
	err := s.saveLocked()
	s.mu.Unlock()
	s.report(err)
}

func (s *cookieStore) clear() {
	s.mu.Lock()
	s.jar, s.items = newCookieJar(), make(map[string]storedCookie)
	err := s.saveLocked()
	s.mu.Unlock()
	s.report(err)
}

func (s *cookieStore) rebuildJarLocked() {
	s.jar = newCookieJar()
	for _, item := range s.items {
		s.jar.SetCookies(&item.origin, []*http.Cookie{&item.cookie})
	}
}

// saveLocked writes persistent cookies and never session or expired ones.
// With nothing to keep it removes the file, so a cleared jar leaves no values
// behind.
func (s *cookieStore) saveLocked() error {
	if !s.persist || s.path == "" {
		return nil
	}
	now := time.Now()
	var records []cookieRecord
	for _, item := range s.items {
		c := item.cookie
		if c.Expires.IsZero() || !c.Expires.After(now) {
			continue
		}
		records = append(records, cookieRecord{
			Origin: item.origin.String(), Name: c.Name, Value: c.Value, Domain: c.Domain,
			Path: c.Path, Expires: c.Expires, Secure: c.Secure, HttpOnly: c.HttpOnly,
		})
	}
	if len(records) == 0 {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	slices.SortFunc(records, func(a, b cookieRecord) int {
		return strings.Compare(a.Origin+"\x00"+a.Domain+"\x00"+a.Path+"\x00"+a.Name,
			b.Origin+"\x00"+b.Domain+"\x00"+b.Path+"\x00"+b.Name)
	})
	data, err := json.Marshal(cookieFile{Cookies: records})
	if err != nil {
		return err
	}
	return store.WriteFile(s.path, append(data, '\n'))
}

// load puts records read from disk into the jar. Invalid, expired and session
// records are ignored, so an edited file cannot keep a cookie alive.
func (s *cookieStore) load(records []cookieRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	now := time.Now()
	for _, r := range records {
		origin, err := url.Parse(r.Origin)
		if err != nil || origin.Hostname() == "" || origin.Scheme != "http" && origin.Scheme != "https" {
			continue
		}
		if r.Name == "" || !strings.HasPrefix(r.Path, "/") || r.Expires.IsZero() || !r.Expires.After(now) {
			continue
		}
		c := &http.Cookie{Name: r.Name, Value: r.Value, Domain: r.Domain, Path: r.Path,
			Expires: r.Expires, Secure: r.Secure, HttpOnly: r.HttpOnly}
		if c.Valid() != nil {
			continue
		}
		s.setCookiesLocked(origin, []*http.Cookie{c})
	}
}

// setPersist turns writing on, saving what the jar already holds, or off,
// removing the file so turning persistence off leaves no values behind.
func (s *cookieStore) setPersist(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.persist = on
	if on {
		return s.saveLocked()
	}
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *cookieStore) report(err error) {
	s.mu.Lock()
	if err == nil {
		s.problem = nil
		s.mu.Unlock()
		return
	}
	first := s.problem == nil
	s.problem = err
	callback := s.onProblem
	s.mu.Unlock()
	if first && callback != nil {
		callback(err)
	}
}

func (s *cookieStore) problemText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problem == nil {
		return ""
	}
	return s.problem.Error()
}

func cookieDomain(c storedCookie) string {
	return strings.TrimPrefix(strings.ToLower(cmp.Or(c.cookie.Domain, c.origin.Hostname())), ".")
}

// readCookieFile reads the local jar; a missing file is an empty jar.
func readCookieFile(path string) ([]cookieRecord, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f cookieFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Cookies, nil
}

func (a *app) requestCookies() *cookieStore {
	if a.cookies == nil {
		a.cookies = &cookieStore{path: a.cookiesPath, persist: a.cookiePersistence}
		a.cookies.onProblem = func(err error) { a.fail("Cookies", err) }
	}
	return a.cookies
}

// loadCookies reads the local jar at startup. With persistence off, it removes
// a file left by an earlier choice and keeps only the in-memory jar.
func (a *app) loadCookies() {
	if a.cookiesPath == "" {
		return
	}
	s := a.requestCookies()
	s.mu.Lock()
	s.path, s.persist = a.cookiesPath, a.cookiePersistence
	s.mu.Unlock()
	if !a.cookiePersistence {
		if err := os.Remove(a.cookiesPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.report(err)
		}
		return
	}
	records, err := readCookieFile(a.cookiesPath)
	if err != nil {
		s.report(err)
		return
	}
	s.load(records)
}

// setCookiePersistence saves the choice and the jar together, so the file and
// the setting never disagree for long.
func (a *app) setCookiePersistence(on bool) {
	if on == a.cookiePersistence {
		return
	}
	a.cookiePersistence = on
	if err := a.requestCookies().setPersist(on); err != nil {
		a.fail("Cookies", err)
	}
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil {
		return
	}
	if on {
		a.done("Cookies will be kept after quitting in " + a.cookiesPath)
	} else {
		a.done("Cookies will be forgotten when the app quits")
	}
}

type cookieEditor struct {
	open                                    bool
	url, name, value, path, domain, problem string
	secure, httpOnly                        bool
	reveal                                  map[string]bool
}

func (a *app) openCookies() {
	e := &a.cookieUI
	e.open = true
	if e.reveal == nil {
		e.reveal = make(map[string]bool)
	}
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
	if e.reveal == nil {
		e.reveal = make(map[string]bool)
	}
	_, height := c.Size()
	uikit.Modal(c, &e.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(640, height-80)).Children(func() {
			uikit.DialogTitle(c, "Cookies")
			uikit.Meta(c, "Shared by Requests, Runner, scripts and interactive clients.")
			keep := a.cookiePersistence
			if ui.Checkbox(c, &keep, "Keep cookies after quitting").Changed() {
				a.setCookiePersistence(keep)
			}
			hint := "Cookies stay in memory until the app quits. Session cookies are never saved."
			if a.cookiePersistence {
				hint = "Persistent cookies are saved to ~/.vscroxy/cookies.json (0600), outside collections and exports. Session cookies are never saved."
			}
			uikit.Hint(c, hint)
			if problem := a.requestCookies().problemText(); problem != "" {
				ui.Text(c, "The local cookie file was not read or written: "+problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Children(func() {
				items := a.requestCookies().snapshot()
				if len(items) == 0 {
					uikit.Hint(c, "No cookies yet. Send a request or add one below.")
				}
				groups := map[string][]storedCookie{}
				for _, item := range items {
					domain := cookieDomain(item)
					groups[domain] = append(groups[domain], item)
				}
				for _, domain := range slices.Sorted(maps.Keys(groups)) {
					ui.Row(c).Key("domain:" + domain).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						uikit.Label(c, domain).SingleLine().Grow(1).Tooltip("Cookies for " + domain)
						if uikit.CompactButton(c, "Clear").Label("Clear cookies for " + domain).Clicked() {
							a.requestCookies().clearDomain(domain)
							clear(e.reveal)
						}
					})
					for _, item := range groups[domain] {
						ui.Row(c).Key(item.key).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).Gap(t.Space(1)).Children(func() {
								value := "••••••••  masked"
								if e.reveal[item.key] {
									value = item.cookie.Value
								}
								uikit.Mono(c, item.cookie.Name+" = "+value).SingleLine()
								expires := "Session cookie"
								if !item.cookie.Expires.IsZero() {
									expires = "Expires " + item.cookie.Expires.Local().Format("2006-01-02 15:04")
								}
								uikit.Meta(c, fmt.Sprintf("%s · %s · Secure %t · HttpOnly %t", item.cookie.Path, expires, item.cookie.Secure, item.cookie.HttpOnly))
							})
							label := fmt.Sprintf("Reveal cookie %s %s", item.cookie.Name, item.cookie.Path)
							if e.reveal[item.key] {
								label = fmt.Sprintf("Hide cookie %s %s", item.cookie.Name, item.cookie.Path)
							}
							if uikit.IconButton(c, label, "Cookie values are masked by default; reveal this row to read it", eyeIcon).Clicked() {
								e.reveal[item.key] = !e.reveal[item.key]
							}
							if uikit.SecondaryButton(c, "Delete").Label("Delete cookie " + item.cookie.Name + " " + item.cookie.Path).Clicked() {
								a.requestCookies().remove(item.key)
							}
						})
					}
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
					clear(e.reveal)
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
