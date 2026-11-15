package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/update"
	"github.com/baba983/vscroxy/internal/version"
)

func TestOfferUpdateOnlyForANewerRelease(t *testing.T) {
	a := workspaceApp()
	a.offerUpdate(update.Release{Tag: "v" + version.Number, URL: "https://example.com/old"})
	if len(a.notices) != 0 {
		t.Fatalf("the running version was offered: %+v", a.notices)
	}
	a.offerUpdate(update.Release{Tag: "v0.2.0", URL: "https://example.com/new"})
	if len(a.notices) != 1 {
		t.Fatalf("notices = %+v", a.notices)
	}
	n := a.notices[0]
	if !n.sticky || !strings.Contains(n.text, "0.2.0") || n.action != "" {
		t.Fatalf("notice = %+v", n)
	}
}

func TestUpdateCheckSettingPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeSettings(path, settings{DisableUpdateCheck: true}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path)
	if err != nil || !got.DisableUpdateCheck {
		t.Fatalf("settings = %+v, %v", got, err)
	}
}
