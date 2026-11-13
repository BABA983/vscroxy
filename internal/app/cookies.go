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
	"github.com/baba983/vscroxy/locales"
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

const cookieSaveDelay = 500 * time.Millisecond

// cookieStore keeps metadata for inspection/removal; Go's jar remains the
// authority for domain/path/Secure matching and expiry, including redirects.
// It is shared by Requests, Runner, scripts and the interactive clients, and
// never used by raw Replay. Persistent cookies may be written to path, which
// holds nothing else and is private (0600).
//
// Writes are debounced: SetCookies only marks the persistent records dirty,
// and a timer writes them after cookieSaveDelay. Readers never wait for the
// disk, and a response that sets only session cookies never writes a file.
type cookieStore struct {
	mu        sync.Mutex
	jar       *cookiejar.Jar
	items     map[string]storedCookie
	path      string
	persist   bool
	delay     time.Duration
	dirty     bool
	saveTimer *time.Timer
	lastSaved string
	writes    int // successful file writes; tests use it to prove debouncing
	writeMu   sync.Mutex
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
	if s.delay == 0 {
		s.delay = cookieSaveDelay
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
	err := s.changedLocked()
	s.mu.Unlock()
	if err != nil {
		s.report(err)
	}
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
	err := s.changedLocked()
	s.mu.Unlock()
	if err != nil {
		s.report(err)
		return
	}
	s.report(s.flushNow())
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
	err := s.changedLocked()
	s.mu.Unlock()
	if err != nil {
		s.report(err)
		return
	}
	s.report(s.flushNow())
}

func (s *cookieStore) clear() {
	s.mu.Lock()
	s.jar, s.items = newCookieJar(), make(map[string]storedCookie)
	err := s.changedLocked()
	s.mu.Unlock()
	if err != nil {
		s.report(err)
		return
	}
	s.report(s.flushNow())
}

func (s *cookieStore) rebuildJarLocked() {
	s.jar = newCookieJar()
	for _, item := range s.items {
		s.jar.SetCookies(&item.origin, []*http.Cookie{&item.cookie})
	}
}

// persistentStateLocked is the file's next contents: persistent cookies only,
// in a stable order, or nil when nothing should remain on disk.
func (s *cookieStore) persistentStateLocked() ([]byte, error) {
	if !s.persist || s.path == "" {
		return nil, nil
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
		return nil, nil
	}
	slices.SortFunc(records, func(a, b cookieRecord) int {
		return strings.Compare(a.Origin+"\x00"+a.Domain+"\x00"+a.Path+"\x00"+a.Name,
			b.Origin+"\x00"+b.Domain+"\x00"+b.Path+"\x00"+b.Name)
	})
	data, err := json.Marshal(cookieFile{Cookies: records})
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// changedLocked compares the persistent records with what is on disk and
// schedules one debounced write when they differ. Session-only changes never
// mark the store dirty.
func (s *cookieStore) changedLocked() error {
	if !s.persist || s.path == "" {
		return nil
	}
	data, err := s.persistentStateLocked()
	if err != nil {
		return err
	}
	if string(data) == s.lastSaved {
		return nil
	}
	s.dirty = true
	if s.saveTimer == nil {
		s.saveTimer = time.AfterFunc(s.delay, func() { s.report(s.flush()) })
	}
	return nil
}

// flush writes the latest persistent records outside the reader lock. The
// write mutex keeps a snapshot and its write in order, so two flushes cannot
// replace a newer file with an older snapshot.
func (s *cookieStore) flush() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	if !s.dirty || !s.persist || s.path == "" {
		s.mu.Unlock()
		return nil
	}
	data, err := s.persistentStateLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.dirty = false
	path := s.path
	s.mu.Unlock()

	err = writeCookieData(path, data)
	s.mu.Lock()
	if err == nil {
		s.lastSaved = string(data)
		if len(data) > 0 {
			s.writes++
		}
	} else {
		s.dirty = true
	}
	s.mu.Unlock()
	return err
}

// flushNow stops the timer and writes synchronously, for an explicit clear or
// app exit. Waiting on writeMu makes a concurrent debounced write finish
// first, then this writes whatever changed after its snapshot.
func (s *cookieStore) flushNow() error {
	// A response may set a cookie while the first write runs. Stop the timer
	// and write twice at most, so exit catches that change without waiting on
	// a stream that keeps changing.
	for range 2 {
		s.mu.Lock()
		if s.saveTimer != nil {
			s.saveTimer.Stop()
			s.saveTimer = nil
		}
		s.mu.Unlock()
		if err := s.flush(); err != nil {
			return err
		}
		s.mu.Lock()
		dirty := s.dirty
		s.mu.Unlock()
		if !dirty {
			return nil
		}
	}
	return nil
}

func writeCookieData(path string, data []byte) error {
	if len(data) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return store.WriteFile(path, data)
}

func (s *cookieStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
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
	s.init()
	s.persist = on
	if on {
		err := s.changedLocked()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		return s.flushNow()
	}
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.dirty, s.lastSaved = false, ""
	path := s.path
	s.mu.Unlock()
	if path == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// settle records the file's loaded state as saved, so a later session-only
// change does not rewrite it.
func (s *cookieStore) settle() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.dirty = false
	data, err := s.persistentStateLocked()
	if err != nil {
		return err
	}
	s.lastSaved = string(data)
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
		a.cookies.onProblem = func(err error) { a.fail(locales.Cookies2(), err) }
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
		s.writeMu.Lock()
		err := os.Remove(a.cookiesPath)
		s.writeMu.Unlock()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	if err := s.settle(); err != nil {
		s.report(err)
	}
}

// flushCookies writes a pending debounced jar change before the app exits.
func (a *app) flushCookies() {
	if a.cookies == nil {
		return
	}
	if err := a.cookies.flushNow(); err != nil {
		a.fail(locales.Cookies2(), err)
	}
}

// setCookiePersistence saves the choice and the jar together, so the file and
// the setting never disagree for long.
func (a *app) setCookiePersistence(on bool) {
	if on == a.cookiePersistence {
		return
	}
	a.cookiePersistence = on
	if err := a.requestCookies().setPersist(on); err != nil {
		a.fail(locales.Cookies2(), err)
	}
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil {
		return
	}
	if on {
		a.done(locales.CookiesKeptIn(a.cookiesPath))
	} else {
		a.done(locales.CookiesWillBeForgottenWhen())
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
			uikit.DialogTitle(c, locales.Cookies2())
			uikit.Meta(c, locales.SharedByRequestsRunnerScripts())
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
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
				items := a.requestCookies().snapshot()
				if len(items) == 0 {
					uikit.Hint(c, locales.NoCookiesYetSendA())
				}
				groups := map[string][]storedCookie{}
				for _, item := range items {
					domain := cookieDomain(item)
					groups[domain] = append(groups[domain], item)
				}
				for _, domain := range slices.Sorted(maps.Keys(groups)) {
					ui.Row(c.Key("domain:" + domain)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						uikit.Tip(c, uikit.Label(c, domain).SingleLine().Grow(1), "Cookies for "+domain)
						if uikit.CompactButton(c, locales.Clear()).Label("Clear cookies for " + domain).Clicked() {
							a.requestCookies().clearDomain(domain)
							clear(e.reveal)
						}
					})
					for _, item := range groups[domain] {
						ui.Row(c.Key(item.key)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).Gap(t.Space(1)).Children(func() {
								value := "••••••••"
								if e.reveal[item.key] {
									value = item.cookie.Value
								}
								uikit.Mono(c, item.cookie.Name+" = "+value).SingleLine()
								expires := "Session cookie"
								if !item.cookie.Expires.IsZero() {
									expires = "Expires " + item.cookie.Expires.Local().Format("2006-01-02 15:04")
								}
								attrs := []string{item.cookie.Path, expires}
								if item.cookie.Secure {
									attrs = append(attrs, "Secure")
								}
								if item.cookie.HttpOnly {
									attrs = append(attrs, "HttpOnly")
								}
								uikit.Meta(c, strings.Join(attrs, " · "))
							})
							label := fmt.Sprintf("Reveal cookie %s %s", item.cookie.Name, item.cookie.Path)
							if e.reveal[item.key] {
								label = fmt.Sprintf("Hide cookie %s %s", item.cookie.Name, item.cookie.Path)
							}
							if uikit.IconButton(c, label, locales.CookieValuesAreMaskedBy(), eyeIcon).Clicked() {
								e.reveal[item.key] = !e.reveal[item.key]
							}
							if uikit.SecondaryButton(c, locales.Delete()).Label("Delete cookie " + item.cookie.Name + " " + item.cookie.Path).Clicked() {
								a.requestCookies().remove(item.key)
							}
						})
					}
				}
				ui.Divider(c)
				uikit.Meta(c, locales.AddReplacesTheSameDomain())
				ui.Form(c, func() {
					ui.Field(c, locales.CookieUrl(), func() {
						uikit.Input(c, &e.url).Label(locales.CookieUrl()).Placeholder("https://api.example.com/")
					})
					ui.Field(c, locales.CookieName(), func() {
						uikit.Input(c, &e.name).Label(locales.CookieName()).Placeholder(locales.Name())
					})
					ui.Field(c, locales.CookieValue(), func() {
						uikit.Input(c, &e.value).Label(locales.CookieValue()).Placeholder(locales.Value()).Password()
					})
					ui.Field(c, locales.CookieDomain(), func() {
						uikit.Input(c, &e.domain).Label(locales.CookieDomain()).Placeholder(locales.DomainOptional())
					})
					ui.Field(c, locales.CookiePath(), func() {
						ui.Row(c).Gap(t.Space(3)).Children(func() {
							uikit.Input(c, &e.path).Width(100).Label(locales.CookiePath()).Placeholder("/")
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
				if uikit.SecondaryButton(c, locales.ClearAllCookies()).Clicked() {
					a.requestCookies().clear()
					clear(e.reveal)
				}
				ui.Spacer(c)
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					e.open = false
				}
				if uikit.PrimaryButton(c, locales.AddCookie()).Clicked() {
					a.saveCookie()
				}
			})
		})
	})
}
