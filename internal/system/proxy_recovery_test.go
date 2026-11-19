package system

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The guard writes errors as JSON replies; the app must still know which KDE
// setting an administrator locked so it can show the localized notice.
func TestProxyReplyKeepsTheLockedKDEKeyAcrossTheGuardPipe(t *testing.T) {
	for _, tc := range []struct{ name, group string }{
		{name: "immutable key"},
		{name: "locked group", group: "Proxy Settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &KDELockedError{Key: "NoProxyFor", Group: tc.group}
			var buf bytes.Buffer
			encoder := jsontext.NewEncoder(&buf)
			if err := json.MarshalEncode(encoder, proxyReply{
				Error: original.Error(), Owned: true, LockedKey: original.Key, LockedGroup: original.Group,
			}); err != nil {
				t.Fatal(err)
			}
			var reply proxyReply
			if err := json.UnmarshalDecode(jsontext.NewDecoder(&buf), &reply); err != nil {
				t.Fatal(err)
			}
			locked, ok := errors.AsType[*KDELockedError](reply.err())
			if !ok || locked.Key != original.Key || locked.Group != original.Group ||
				locked.Error() != original.Error() || !errors.Is(reply.err(), ErrKDELocked) {
				t.Fatalf("reply = %+v, error = %v; want the locked setting and its detail", reply, reply.err())
			}
		})
	}
}

func TestRecoverProxyRejectsAnOlderJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.json")
	if err := os.WriteFile(path, []byte(`{"Version":1,"Target":"127.0.0.1:7890"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := recoverProxy(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy recovery journal") {
		t.Fatalf("recoverProxy = %v, want an unsupported-version error", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("a rejected journal was removed: %v", statErr)
	}
}
