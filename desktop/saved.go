package main

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
)

// savedRequest is a request kept by name, the first half of a collection.
type savedRequest struct {
	Name       string `json:"name"`
	Collection string `json:"collection,omitempty"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Headers    string `json:"headers,omitempty"`
	Body       string `json:"body,omitempty"`
}

// loadRequests reads the saved requests, an empty list when there is no file.
func loadRequests(path string) ([]savedRequest, error) {
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
	var list []savedRequest
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// saveRequests writes the list, sorted by collection and name.
func saveRequests(path string, list []savedRequest) error {
	slices.SortFunc(list, func(x, y savedRequest) int {
		return cmp.Or(cmp.Compare(x.Collection, y.Collection), cmp.Compare(x.Name, y.Name))
	})
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// upsertRequest adds r, replacing the request of the same name in the same
// collection.
func upsertRequest(list []savedRequest, r savedRequest) []savedRequest {
	for i, existing := range list {
		if existing.Name == r.Name && existing.Collection == r.Collection {
			list[i] = r
			return list
		}
	}
	return append(list, r)
}

// groupRequests collects the requests by collection, the names sorted, the
// requests outside a collection under the empty name.
func groupRequests(list []savedRequest) map[string][]savedRequest {
	groups := make(map[string][]savedRequest, len(list))
	for _, r := range list {
		groups[r.Collection] = append(groups[r.Collection], r)
	}
	return groups
}

// uniqueRequestName returns name, or name with a count in brackets when the
// list already holds it.
func uniqueRequestName(list []savedRequest, name string) string {
	taken := func(candidate string) bool {
		for _, r := range list {
			if r.Name == candidate {
				return true
			}
		}
		return false
	}
	if !taken(name) {
		return name
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)", name, n)
		if !taken(candidate) {
			return candidate
		}
	}
}
