package store

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testFormats pairs every format with the version 1 test data that goes with
// it. A new format adds its file here.
func testFormats(t *testing.T) map[string]struct {
	format Formatter
	file   string
	member string
} {
	t.Helper()
	return map[string]struct {
		format Formatter
		file   string
		member string
	}{
		"settings":           {Settings, "settings.json", ""},
		"rules":              {Rules, "rules.json", "rules"},
		"ssl":                {SSL, "ssl.json", ""},
		"env":                {Env, "env.json", ""},
		"project env":        {ProjectEnv, "environments.json", ""},
		"views":              {Views, "views.json", "filters"},
		"apis":               {APIs, "apis.json", "apis"},
		"breakpoints":        {Breakpoints, "breakpoint-templates.json", "templates"},
		"network presets":    {NetworkPresets, "network-presets.json", ""},
		"host profiles":      {HostsProfiles, "profiles.json", ""},
		"cookies":            {Cookies, "cookies.json", ""},
		"android proxy":      {AndroidProxy, "android-proxy.json", "records"},
		"collection request": {Collection, "collection.request.json", ""},
		"folder":             {Collection, "folder.json", ""},
	}
}

func TestUnversionedTestDataReadsAsVersionOne(t *testing.T) {
	for name, tc := range testFormats(t) {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			got, err := tc.format.Read(data)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(got, &doc); err != nil {
				t.Fatalf("upgraded file is not a JSON object: %v\n%s", err, got)
			}
			if fmt.Sprint(doc["version"]) != "1" {
				t.Fatalf("version = %v, want 1", doc["version"])
			}
			if tc.member != "" {
				if _, ok := doc[tc.member]; !ok {
					t.Fatalf("array format lost its %q member: %s", tc.member, got)
				}
			}
			// Reading the upgraded file again changes nothing.
			again, err := tc.format.Read(got)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("second read = %s, %v", again, err)
			}
		})
	}
}

func TestEveryFormatRefusesANewerFile(t *testing.T) {
	for name, tc := range testFormats(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file.json")
			newer := []byte(`{"version":2,"from":"the future"}`)
			if err := os.WriteFile(path, newer, 0o600); err != nil {
				t.Fatal(err)
			}
			f := &File{Path: path, Format: tc.format}
			if _, err := f.Read(); !errors.Is(err, ErrNewer) {
				t.Fatalf("read = %v, want ErrNewer", err)
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, newer) {
				t.Fatalf("a newer file was changed: %s", got)
			}
			if err := f.Write([]byte(`{"version":1}`)); !errors.Is(err, ErrNewer) {
				t.Fatalf("write = %v, want ErrNewer", err)
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, newer) {
				t.Fatalf("a newer file was overwritten: %s", got)
			}
		})
	}
}

// bumpStep is a pure migration step: it changes the document, then its version.
func bumpStep(to int, change func(map[string]any)) Step {
	return func(data []byte) ([]byte, error) {
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		change(doc)
		doc["version"] = to
		return json.Marshal(doc)
	}
}

func testMigrationFormat() *Format {
	return &Format{Name: "test", Current: 3, Steps: []Step{
		bumpStep(2, func(doc map[string]any) { doc["name"] = strings.ToUpper(doc["name"].(string)) }),
		bumpStep(3, func(doc map[string]any) { doc["extra"] = true }),
	}}
}

func TestMigrationsRunInOrderWithTestData(t *testing.T) {
	v1, err := os.ReadFile("testdata/migration/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/migration/v3.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := testMigrationFormat().Read(v1)
	if err != nil {
		t.Fatal(err)
	}
	var have, expected map[string]any
	if err := json.Unmarshal(got, &have); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &expected); err != nil {
		t.Fatal(err)
	}
	if have["name"] != expected["name"] || have["extra"] != expected["extra"] || have["version"] != expected["version"] {
		t.Fatalf("migrated = %s, want %s", got, want)
	}
	// The middle fixture is what a version 2 app wrote.
	v2, err := os.ReadFile("testdata/migration/v2.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err = testMigrationFormat().Read(v2)
	if err != nil || !bytes.Contains(got, []byte(`"version":3`)) {
		t.Fatalf("version 2 file = %s, %v", got, err)
	}
	// A step that leaves the wrong version is refused rather than written.
	broken := &Format{Name: "broken", Current: 2, Steps: []Step{func(data []byte) ([]byte, error) { return data, nil }}}
	if _, err := broken.Read(v1); err == nil {
		t.Fatal("a step that did not bump the version was accepted")
	}
}

func TestMigrationIsBackedUpAndKeepsTen(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "rules.json")
	data, err := os.ReadFile("testdata/migration/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	f := &File{Path: path, Format: testMigrationFormat(), BackupDir: backups}
	for range 12 {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		f = &File{Path: path, Format: testMigrationFormat(), BackupDir: backups}
		if _, err := f.Read(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != backupsKept {
		t.Fatalf("%d backups, want %d", len(entries), backupsKept)
	}
	// The upgraded file is what the app now reads.
	if got, _ := os.ReadFile(path); !bytes.Contains(got, []byte(`"version":3`)) {
		t.Fatalf("migration was not written back: %s", got)
	}
}

func TestWriteStampsVersionAndBacksUpOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"proxyPort":8899}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: path, Format: Settings, BackupDir: backups}
	if _, err := f.Read(); err != nil {
		t.Fatal(err)
	}
	if err := f.Write([]byte(`{"proxyPort":8900}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte(`"version":1`)) {
		t.Fatalf("write did not stamp the version: %s", got)
	}
	entries, _ := os.ReadDir(backups)
	if len(entries) != 1 {
		t.Fatalf("%d backups after one change, want 1", len(entries))
	}
	// Saving the same contents again writes nothing and backs up nothing.
	if err := f.Write([]byte(`{"proxyPort":8900}`)); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(backups)
	if len(entries) != 1 {
		t.Fatalf("%d backups after an unchanged save, want 1", len(entries))
	}
}

func TestWriteFileVersionedRefusesANewerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	newer := []byte(`{"version":2}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileVersioned(path, Settings, []byte(`{"version":1}`), ""); !errors.Is(err, ErrNewer) {
		t.Fatalf("write = %v, want ErrNewer", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, newer) {
		t.Fatalf("newer file overwritten: %s", got)
	}
}

func TestListFormatWritesAndChecksItsHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ignore.txt")
	if err := WriteFileVersioned(path, Lists, []byte("example.com\n"), ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.HasPrefix(got, []byte("# vscroxy format 1\n")) {
		t.Fatalf("missing header: %s", got)
	}
	// The list parser still sees only the hosts.
	if lines := strings.Split(strings.TrimSpace(string(got)), "\n"); len(lines) != 2 || lines[1] != "example.com" {
		t.Fatalf("header changed the list: %q", lines)
	}
	if _, err := Lists.Read([]byte("# vscroxy format 2\nfuture.example\n")); !errors.Is(err, ErrNewer) {
		t.Fatalf("newer list = %v, want ErrNewer", err)
	}
	// An empty list stays empty instead of becoming a lone header.
	if err := WriteFileVersioned(path, Lists, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); len(got) != 0 {
		t.Fatalf("empty list = %q", got)
	}
}

func TestStampVersionKeepsFormatting(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{}`, `{"version":1}`},
		{"{\n  \"a\": 1\n}\n", "{\"version\":1,\n  \"a\": 1\n}\n"},
		{`{ }`, `{"version":1 }`},
	} {
		if got := string(stampVersion([]byte(tc.in), 1)); got != tc.want {
			t.Errorf("stampVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
