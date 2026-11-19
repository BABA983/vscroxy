package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/datadir"
)

func TestDesktopFilesComeFromTheSharedDataRoot(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(datadir.Env, root)
	t.Cleanup(func() { datadir.SetFlag("") })

	// A decoy in the home directory must lose to the configured root.
	settings := `{"certificates":{"trustSystemRoots":false}}`
	for _, dir := range []string{filepath.Join(home, ".vscroxy"), root} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		value := settings
		if dir == root {
			value = `{"certificates":{"trustSystemRoots":true}}`
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := loadCertificates()
	if err != nil {
		t.Fatal(err)
	}
	if got.TrustSystemRoots == nil || !*got.TrustSystemRoots {
		t.Fatalf("did not read VSCROXY_HOME settings: %+v", got)
	}

	var out, errOut bytes.Buffer
	if code := Run(t.Context(), []string{"--user-data-dir", root, "--help"}, &out, &errOut); code != 0 {
		t.Fatalf("CLI with --user-data-dir exited %d: %s", code, errOut.String())
	}
	if found, err := datadir.Root(); err != nil || found != root {
		t.Fatalf("--user-data-dir root = %q, %v; want %q", found, err, root)
	}
}
