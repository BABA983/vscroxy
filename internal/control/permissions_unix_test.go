//go:build unix

package control

import (
	"os"
	"strings"
	"testing"
)

func TestLoadRejectsDifferentOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("changing owner requires root")
	}
	path := writeDiscovery(t, discovery{Version: 1, Address: "127.0.0.1:12345", Token: strings.Repeat("ab", 32)})
	if err := os.Chown(path, 1, -1); err != nil {
		t.Skip("changing owner unavailable")
	}
	if _, err := Load(path); err == nil {
		t.Fatal("accepted discovery owned by a different user")
	}
}
