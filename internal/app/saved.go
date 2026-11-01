package app

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
)

// savedRequest is a library asset: the portable request plus the capture it
// came from, which only this app can interpret.
type savedRequest struct {
	request.Request
	Source *debugSource `json:"source,omitempty"`
}

// assignIDs gives requests without an ID a deterministic one, so reading
// the same hand-written library twice keeps the same identities. Duplicates
// are an error.
func assignIDs(list []savedRequest) error {
	seen := map[string]bool{}
	for i := range list {
		if list[i].ID == "" {
			list[i].ID = fmt.Sprintf("legacy-%x", sha256.Sum256([]byte(list[i].Collection+"\x00"+list[i].Name)))
		}
		if seen[list[i].ID] {
			return fmt.Errorf("duplicate request identity %q", list[i].ID)
		}
		seen[list[i].ID] = true
	}
	return nil
}

func (r savedRequest) key() string {
	if r.ID != "" {
		return "saved:" + r.ID
	}
	return "saved:" + r.Collection + "\x00" + r.Name // Unsaved, in-memory requests.
}

// persistRequests is the app's guarded library write.
func (a *app) persistRequests(list []savedRequest) error {
	return a.writeLibrary(list, a.folders)
}

// Explicit identities update only that asset. A request without an ID
// updates by name, adopting an existing identity rather than replacing it.
func upsertRequest(list []savedRequest, r savedRequest) []savedRequest {
	for i, existing := range list {
		if r.ID != "" && existing.ID == r.ID || r.ID == "" && existing.Name == r.Name && existing.Collection == r.Collection {
			if r.ID == "" {
				r.ID = existing.ID
			}
			if r.ID == "" {
				r.ID = uuid.New().String()
			}
			list[i] = r
			return list
		}
	}
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return append(list, r)
}

// Imports update matching IDs. An unrelated same-name asset is kept under a
// distinct label; repeated imports still update the same imported identity.
func mergeRequests(list, incoming []savedRequest) []savedRequest {
	list = slices.Clone(list)
	for _, r := range incoming {
		if r.Name == "" {
			continue
		}
		if slices.ContainsFunc(list, func(old savedRequest) bool {
			return old.ID != r.ID && old.Collection == r.Collection && old.Name == r.Name
		}) {
			peers := slices.DeleteFunc(slices.Clone(list), func(old savedRequest) bool { return old.ID == r.ID })
			r.Name = uniqueRequestName(peers, r.Name)
		}
		list = upsertRequest(list, r)
	}
	return list
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
