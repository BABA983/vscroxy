package app

import (
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

// inputHistoryLimit is VS Code's HistoryNavigator2 default: fifty unique
// entries, oldest first. The cursor sits past the last entry while the user
// edits their own text rather than a recalled one.
const inputHistoryLimit = 50

type inputHistory struct {
	entries []string
	cursor  int
	// draft is what the field held when navigation began. Down past the
	// newest entry puts it back.
	draft string
}

// add keeps value once, at the newest end. Re-adding an existing value moves
// it there; a full history drops its oldest entry. Empty text is not an entry.
func (h *inputHistory) add(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	value = strings.Clone(value)
	if at := slices.Index(h.entries, value); at >= 0 {
		h.entries = slices.Delete(h.entries, at, at+1)
	} else if len(h.entries) == inputHistoryLimit {
		h.entries = slices.Delete(h.entries, 0, 1)
	}
	h.entries = append(h.entries, value)
	h.reset()
	return true
}

// load takes the persisted entries through the same uniqueness and cap
// rules, so a hand-edited file cannot grow the history past its bound.
func (h *inputHistory) load(entries []string) {
	h.entries, h.cursor, h.draft = nil, 0, ""
	for _, value := range entries {
		h.add(value)
	}
}

func (h *inputHistory) reset() {
	h.cursor, h.draft = len(h.entries), ""
}

func (h *inputHistory) clear() {
	h.entries, h.cursor, h.draft = nil, 0, ""
}

// up steps toward the older entries, remembering the field's own text on the
// first step. At the oldest entry it stays there.
func (h *inputHistory) up(current string) (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if h.cursor == len(h.entries) {
		h.draft, h.cursor = current, h.cursor-1
	} else if h.cursor > 0 {
		h.cursor--
	}
	return h.entries[h.cursor], true
}

// down steps toward the newest entry, then back to the text the user was
// typing. Past the draft it leaves the field alone.
func (h *inputHistory) down() (string, bool) {
	if h.cursor >= len(h.entries) {
		return "", false
	}
	h.cursor++
	if h.cursor == len(h.entries) {
		return h.draft, true
	}
	return h.entries[h.cursor], true
}

// textKey claims Up and Down for a field unless a modifier, a multiline caret
// away from either end, or a completion that owns the key came first.
func (h *inputHistory) textKey(k ui.TextKey, caret [2]int) bool {
	if k.Mods != 0 {
		return false
	}
	text := k.Text()
	if strings.ContainsAny(text, "\r\n") {
		end := utf8.RuneCountInString(text)
		if caret[0] != caret[1] || caret[0] != 0 && caret[0] != end {
			return false
		}
	}
	var next string
	var ok bool
	switch k.Key {
	case ui.KeyUp:
		next, ok = h.up(text)
	case ui.KeyDown:
		next, ok = h.down()
	default:
		return false
	}
	if !ok {
		return false
	}
	k.SetText(next)
	return true
}

// rememberURLHistory stores the URL as authored, without expanding its
// variables or keeping a credential that was sent in it.
func (a *app) rememberURLHistory(authored string) {
	if a.urlHistory.add(redactHistoryURL(authored)) {
		a.saveInputHistory()
	}
}

// rememberFilterHistory stores a filter when it is applied. The same text
// applied twice is only one entry, whether Enter or blur did the applying.
func (a *app) rememberFilterHistory(text string) {
	text = strings.TrimSpace(text)
	if text == "" || text == a.filterLast {
		return
	}
	a.filterLast = text
	if a.filterHistory.add(text) {
		a.saveInputHistory()
	}
}

func (a *app) clearInputHistory() {
	a.urlHistory.clear()
	a.filterHistory.clear()
	a.filterLast = ""
	a.saveInputHistory()
}

// redactHistoryURL keeps an authored URL's bytes while removing values that
// would disclose a credential. It works on the string, not a parsed URL: a
// {{variable}} in the host or path is not a parseable URL, and re-encoding a
// parsed one escapes the references a recalled entry must keep verbatim.
// Query parameter names use the app's own secret-name rules; {{references}}
// stay unresolved, since a send expands them only in the traffic it produces.
func redactHistoryURL(raw string) string {
	base, fragment, hasFragment := strings.Cut(raw, "#")
	before, query, hasQuery := strings.Cut(base, "?")
	before = redactHistoryUserInfo(before)
	if hasQuery {
		parts := strings.Split(query, "&")
		for i, part := range parts {
			name, value, hasValue := strings.Cut(part, "=")
			decoded, err := url.QueryUnescape(name)
			// An undecodable name may hide a credential; drop its value
			// rather than trusting a guessed name.
			if err == nil && !request.IsSensitiveQuery(decoded) || strings.Contains(value, "{{") {
				continue
			}
			if hasValue {
				parts[i] = name + "="
			} else {
				parts[i] = name
			}
		}
		before += "?" + strings.Join(parts, "&")
	}
	if hasFragment {
		before += "#" + fragment
	}
	return before
}

// redactHistoryUserInfo blanks a literal password in scheme://user:pass@host.
// A {{reference}} is not a stored secret and stays as written.
func redactHistoryUserInfo(url string) string {
	scheme, rest, ok := strings.Cut(url, "://")
	if !ok {
		return url
	}
	authority, tail, slash := strings.Cut(rest, "/")
	userinfo, host, at := strings.CutLast(authority, "@")
	if !at {
		return url
	}
	user, password, hasPassword := strings.Cut(userinfo, ":")
	if !hasPassword || strings.Contains(password, "{{") {
		return url
	}
	redacted := scheme + "://" + user + ":@" + host
	if slash {
		redacted += "/" + tail
	}
	return redacted
}
