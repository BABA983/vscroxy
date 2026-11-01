package app

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"time"
)

// historyEntry is the read-only legacy recent-request format. New sends are
// retained only in experiments.json; this file stays readable for one version.
type historyEntry struct {
	Method  string        `json:"method"`
	URL     string        `json:"url"`
	Headers string        `json:"headers,omitempty"`
	Body    string        `json:"body,omitempty"`
	At      time.Time     `json:"at"`
	Draft   *savedRequest `json:"draft,omitempty"`
}

// loadHistory reads the sent requests, newest first.
func loadHistory(path string) ([]historyEntry, error) {
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
	var list []historyEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// label names a sent request in the sidebar: the method and the host and path,
// without the scheme.
func (e historyEntry) label() string {
	label := e.URL
	if u, err := url.Parse(e.URL); err == nil && u.Host != "" {
		label = u.Host + u.Path
		if u.RawQuery != "" {
			label += "?" + u.RawQuery
		}
	}
	return strings.TrimSpace(e.Method + " " + label)
}
