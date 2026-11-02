// Package hosts is Host profiles: named sets of host mappings, as SwitchHosts
// keeps them, which the proxy resolves itself instead of the system's hosts
// file. A request keeps its name, URL and Host header; only the address the
// proxy dials changes, so it works for any app or device using the proxy and
// needs no administrator rights.
//
// A profile is a hosts file. Each line is the standard "address host…", or
// "host = address", either optionally followed by "@ app, app" to map the
// names for those apps only; # starts a comment. Hosts are patterns as
// rules.Host reads them, so a name covers its subdomains.
package hosts

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/rules"
)

// Rule maps a host to an address: Name is a host pattern and Apps app
// patterns, as rules.Host and rules.App read them; no apps means every app.
// Profile and Line say where it was written, Line counting from 1.
type Rule struct {
	Name    string
	IP      net.IP
	Apps    []string
	Profile string
	Line    int
}

// Problem is a line Parse could not read, which maps nothing.
type Problem struct {
	Line    int
	Message string
}

// Parse reads a hosts file. A line it cannot read is reported and skipped,
// so one typo does not turn a whole profile off.
func Parse(text string) ([]Rule, []Problem) {
	var out []Rule
	var problems []Problem
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		spec, scope, scoped := strings.Cut(line, "@")
		var apps []string
		for app := range strings.SplitSeq(scope, ",") {
			if app = strings.TrimSpace(app); app != "" {
				apps = append(apps, app)
			}
		}
		if scoped && len(apps) == 0 {
			problems = append(problems, Problem{n, "no app after @"})
			continue
		}
		var address string
		var names []string
		if name, addr, ok := strings.Cut(spec, "="); ok {
			address, names = strings.TrimSpace(addr), strings.Fields(name)
			if len(names) > 1 {
				problems = append(problems, Problem{n, "one host before ="})
				continue
			}
		} else {
			fields := strings.Fields(spec)
			if len(fields) == 0 {
				problems = append(problems, Problem{n, "no address before @"})
				continue
			}
			address, names = fields[0], fields[1:]
		}
		ip := net.ParseIP(address)
		if ip == nil {
			problems = append(problems, Problem{n, fmt.Sprintf("%q is not an address", address)})
			continue
		}
		if len(names) == 0 {
			problems = append(problems, Problem{n, "no host for " + address})
			continue
		}
		for _, name := range names {
			out = append(out, Rule{Name: strings.ToLower(name), IP: ip, Apps: apps, Line: n})
		}
	}
	return out, problems
}

// Lookup returns the rule that maps a host for an app: the first that covers
// both, as in a hosts file.
func Lookup(list []Rule, host, app string) (Rule, bool) {
	for _, r := range list {
		if (rules.Matcher{Hosts: []string{r.Name}, Apps: r.Apps}).Match(rules.Request{Host: host, App: app}) {
			return r, true
		}
	}
	return Rule{}, false
}

// Match returns the first rule that maps a host for every app: a DNS server
// answers names, not apps, so it answers these, and app-scoped rules apply to
// the requests their app sends.
func Match(list []Rule, host string) (Rule, bool) {
	for _, r := range list {
		if len(r.Apps) == 0 && rules.Host(host, []string{r.Name}) {
			return r, true
		}
	}
	return Rule{}, false
}

// Profile is one named hosts file. A subscribed profile has the URL it is
// fetched from and when that last worked; its text is the last copy fetched.
type Profile struct {
	Name    string    `json:"name"`
	On      bool      `json:"on,omitzero"`
	URL     string    `json:"url,omitempty"`
	Updated time.Time `json:"updated,omitzero"`
	Text    string    `json:"-"`
}

// Set is every profile, highest priority first, and whether only one may be
// on at a time. Several on at once combine: where two map the same host, the
// higher one wins.
type Set struct {
	Exclusive bool      `json:"exclusive,omitzero"`
	Profiles  []Profile `json:"profiles"`
}

// Rules is what the profiles that are on map, highest priority first, each
// rule saying where it was written.
func (s Set) Rules() []Rule {
	var out []Rule
	for _, p := range s.Profiles {
		if !p.On {
			continue
		}
		parsed, _ := Parse(p.Text)
		for _, r := range parsed {
			r.Profile = p.Name
			out = append(out, r)
		}
	}
	return out
}

// On lists the names of the profiles that are on, in priority order.
func (s Set) On() []string {
	var names []string
	for _, p := range s.Profiles {
		if p.On {
			names = append(names, p.Name)
		}
	}
	return names
}

// Switch turns profile i on or off. One at a time, turning one on turns the
// others off.
func (s *Set) Switch(i int, on bool) {
	if on && s.Exclusive {
		for j := range s.Profiles {
			s.Profiles[j].On = false
		}
	}
	s.Profiles[i].On = on
}

// SetExclusive changes the mode. Going to one at a time keeps only the
// highest profile that is on.
func (s *Set) SetExclusive(exclusive bool) {
	s.Exclusive = exclusive
	if !exclusive {
		return
	}
	seen := false
	for i := range s.Profiles {
		if s.Profiles[i].On && seen {
			s.Profiles[i].On = false
		}
		seen = seen || s.Profiles[i].On
	}
}

// Index finds a profile by name, ignoring case, or -1.
func (s Set) Index(name string) int {
	return slices.IndexFunc(s.Profiles, func(p Profile) bool { return strings.EqualFold(p.Name, name) })
}

// Conflict is a host two lines map to different addresses for the same apps,
// in profiles that are on: Winner is the one that applies, Loser the one it
// hides. Within one profile the earlier line wins.
type Conflict struct {
	Winner, Loser Rule
}

// Conflicts lists the rules hidden by others for the same host and apps.
func (s Set) Conflicts() []Conflict {
	var out []Conflict
	first := map[string]Rule{}
	for _, r := range s.Rules() {
		key := r.Name + "@" + strings.ToLower(strings.Join(r.Apps, ","))
		w, ok := first[key]
		switch {
		case !ok:
			first[key] = r
		case !w.IP.Equal(r.IP):
			out = append(out, Conflict{Winner: w, Loser: r})
		}
	}
	return out
}

// CheckName says what is wrong with a name for profile i (-1 for a new one):
// it names a file, so it is plain text, and no other profile has it.
func (s Set) CheckName(name string, i int) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("enter a name")
	case name != strings.TrimSpace(name) || strings.HasPrefix(name, "."):
		return fmt.Errorf("names cannot start or end with a space or start with a dot")
	case utf8.RuneCountInString(name) > 64:
		return fmt.Errorf("names are at most 64 characters")
	case strings.ContainsAny(name, `/\:*?"<>|`) || strings.ContainsFunc(name, unicode.IsControl):
		return fmt.Errorf(`names cannot contain / \ : * ? " < > |`)
	}
	if j := s.Index(name); j >= 0 && j != i {
		return fmt.Errorf("a profile named %q exists", s.Profiles[j].Name)
	}
	return nil
}

// FileName is the file a profile's hosts are kept in.
func FileName(name string) string { return name + ".hosts" }

// IndexFile is the file that keeps the profiles' order, switches and mode.
const IndexFile = "profiles.json"

// MarshalIndex encodes the order, switches, subscriptions and mode; the hosts
// themselves are in each profile's file.
func (s Set) MarshalIndex() ([]byte, error) {
	data, err := json.Marshal(s, jsontext.WithIndent("  "))
	return append(data, '\n'), err
}

// UnmarshalIndex reads what MarshalIndex wrote, without the profiles' text.
func UnmarshalIndex(data []byte) (Set, error) {
	var s Set
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Set{}, err
	}
	for i, p := range s.Profiles {
		if err := s.CheckName(p.Name, i); err != nil {
			return Set{}, fmt.Errorf("profile %d: %w", i+1, err)
		}
	}
	return s, nil
}
