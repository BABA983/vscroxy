package app

import (
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/version"
)

func TestAboutLoadsVersionLicenseAndNotices(t *testing.T) {
	a := workspaceApp()
	a.openAbout()
	if !a.aboutOpen || a.aboutTab != 0 {
		t.Fatalf("about = %v, tab %d", a.aboutOpen, a.aboutTab)
	}
	if !strings.Contains(a.aboutLicense, "MIT License") {
		t.Fatalf("license = %q", a.aboutLicense)
	}
	if !strings.Contains(a.aboutNotices, "Third-party notices") || !strings.Contains(a.aboutNotices, "github.com/egoist/mygo") {
		t.Fatalf("notices do not list the linked modules")
	}
	if version.Number == "" || !strings.Contains(version.String(), version.Number) {
		t.Fatalf("version = %q", version.String())
	}
}
