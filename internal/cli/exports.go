package cli

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/store"
)

// runExports are the state files a run may write when it finishes, as Newman's
// --export-environment, --export-globals and --export-cookie-jar do.
type runExports struct {
	environment, globals, cookieJar string
}

func (e runExports) paths() []string {
	var paths []string
	for _, path := range []string{e.environment, e.globals, e.cookieJar} {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// write saves the run's final environment and globals as Postman environment
// files, which --env reads back, and the jar as a cookie export. A missing
// destination is skipped. One failed write does not stop the others.
func (e runExports) write(environment string, vars, globals map[string]string, jar *recordingJar) error {
	var problems []error
	if e.environment != "" {
		data, err := postman.WriteEnvironment(cmp.Or(environment, "environment"), vars, false)
		if err == nil {
			err = store.WriteFile(e.environment, data)
		}
		problems = append(problems, err)
	}
	if e.globals != "" {
		data, err := postman.WriteEnvironment("globals", globals, true)
		if err == nil {
			err = store.WriteFile(e.globals, data)
		}
		problems = append(problems, err)
	}
	if e.cookieJar != "" {
		data, err := jar.export()
		if err == nil {
			err = store.WriteFile(e.cookieJar, data)
		}
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// recordingJar is the run's cookie jar with an export view: cookiejar.Jar
// answers lookups but cannot list what it holds, so each SetCookies is also
// kept, with deletions applied as they happen.
type recordingJar struct {
	jar *cookiejar.Jar
	mu  sync.Mutex
	// cookies are keyed by host-only flag, domain, path and name, the tuple
	// that identifies a cookie in RFC 6265.
	cookies map[cookieKey]exportedCookie
}

type cookieKey struct {
	hostOnly bool
	domain   string
	path     string
	name     string
}

// exportedCookie is the cookie export's shape, matching the browser and
// Postman cookie JSON: expirationDate is Unix seconds and absent for a
// session cookie.
type exportedCookie struct {
	Name           string   `json:"name"`
	Value          string   `json:"value"`
	Domain         string   `json:"domain"`
	Path           string   `json:"path"`
	HostOnly       bool     `json:"hostOnly"`
	Secure         bool     `json:"secure"`
	HTTPOnly       bool     `json:"httpOnly"`
	Session        bool     `json:"session"`
	ExpirationDate *float64 `json:"expirationDate,omitempty"`
	StoreID        string   `json:"storeId"`
}

type cookieJarFile struct {
	Cookies []exportedCookie `json:"cookies"`
}

func newRecordingJar() *recordingJar {
	jar, _ := cookiejar.New(nil)
	return &recordingJar{jar: jar, cookies: map[cookieKey]exportedCookie{}}
}

func (j *recordingJar) Cookies(u *url.URL) []*http.Cookie { return j.jar.Cookies(u) }

func (j *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.jar.SetCookies(u, cookies)
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range cookies {
		key, record := exportCookie(u, cookie)
		if cookie.MaxAge < 0 || !cookie.Expires.IsZero() && cookie.Expires.Before(time.Now()) {
			delete(j.cookies, key)
			continue
		}
		j.cookies[key] = record
	}
}

func exportCookie(u *url.URL, cookie *http.Cookie) (cookieKey, exportedCookie) {
	domain := cookie.Domain
	hostOnly := domain == ""
	if hostOnly {
		domain = u.Hostname()
	}
	path := cmp.Or(cookie.Path, "/")
	key := cookieKey{hostOnly: hostOnly, domain: domain, path: path, name: cookie.Name}
	record := exportedCookie{
		Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
		HostOnly: hostOnly, Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly, StoreID: "0",
	}
	if cookie.MaxAge > 0 {
		record.ExpirationDate = unixSeconds(time.Now().Add(time.Duration(cookie.MaxAge) * time.Second))
	} else if !cookie.Expires.IsZero() {
		record.ExpirationDate = unixSeconds(cookie.Expires)
	}
	record.Session = record.ExpirationDate == nil
	return key, record
}

func unixSeconds(t time.Time) *float64 {
	seconds := float64(t.UnixNano()) / float64(time.Second)
	return &seconds
}

// export encodes every cookie the run saw set, sorted for a stable diff.
func (j *recordingJar) export() ([]byte, error) {
	j.mu.Lock()
	cookies := slices.Collect(maps.Values(j.cookies))
	j.mu.Unlock()
	slices.SortFunc(cookies, func(a, b exportedCookie) int {
		return cmp.Or(cmp.Compare(a.Domain, b.Domain), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Name, b.Name))
	})
	if cookies == nil {
		cookies = []exportedCookie{}
	}
	data, err := json.Marshal(cookieJarFile{Cookies: cookies}, jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
