package main

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

// maxHistory bounds the requests kept: the recent ones are the useful ones.
const maxHistory = 100

// historyEntry is a request Compose sent.
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

// saveHistory writes the list, newest first.
func saveHistory(path string, list []historyEntry) error {
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}

// pushHistory puts e first, dropping an earlier send of the same method and
// URL, and keeps the list to maxHistory.
func pushHistory(list []historyEntry, e historyEntry) []historyEntry {
	list = slices.DeleteFunc(list, func(old historyEntry) bool {
		return old.Method == e.Method && old.URL == e.URL
	})
	list = append([]historyEntry{e}, list...)
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	return list
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
