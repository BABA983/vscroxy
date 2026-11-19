package app

import (
	"fmt"
	"testing"
)

// The cap keeps the newest approvals. Equal LastUsed times still prefer the
// lexicographically smaller key, as they did when pruning a map by sorting.
func TestPruneMCPConsentsKeepsMostRecent(t *testing.T) {
	consents := make(map[string]mcpConsentApproval, mcpConsentLimit+1)
	for i := range mcpConsentLimit + 1 {
		consents[fmt.Sprintf("key-%04d", i)] = mcpConsentApproval{LastUsed: int64(i), Persist: true}
	}
	consents["key-0000"] = mcpConsentApproval{LastUsed: 1, Persist: true}

	kept := pruneMCPConsents(consents)
	if len(kept) != mcpConsentLimit {
		t.Fatalf("kept %d approvals, want %d", len(kept), mcpConsentLimit)
	}
	for _, key := range []string{"key-0000", fmt.Sprintf("key-%04d", mcpConsentLimit-1), fmt.Sprintf("key-%04d", mcpConsentLimit)} {
		if _, ok := kept[key]; !ok {
			t.Errorf("kept no %s", key)
		}
	}
	if _, ok := kept["key-0001"]; ok {
		t.Error("kept the larger key at a tied LastUsed time")
	}
}
