package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Version is the format version every file the app writes today. A file
// without a version was written before 0.1.0 and is version 1.
const Version = 1

// ErrNewer is returned for a file written by a newer version of the app. The
// file is left untouched: reading it as an older format could lose what the
// newer version saved.
var ErrNewer = errors.New("written by a newer version of vscroxy; upgrade vscroxy to read it")

// Step upgrades a file one format version: Steps[i] turns a version i+1 file
// into version i+2. A step is pure: it reads and returns bytes, never files.
type Step func(data []byte) ([]byte, error)

// Formatter is how a file carries its format version. *Format is JSON,
// *ListFormat is a line-oriented text file.
type Formatter interface {
	Read(data []byte) ([]byte, error)
	Write(data []byte) ([]byte, error)
	// Upgraded reports whether Read runs a migration step on data, rather than
	// only adding the current version marker. A file that only needs the
	// marker is written when it next changes, not at startup.
	Upgraded(data []byte) bool
}

// Format is a JSON file with a top-level "version" member. A file rooted at
// an array or a map cannot hold that member, so Member names the member its
// root is wrapped under; an unversioned bare root is the version 1 encoding.
type Format struct {
	Name   string
	Member string
	// Current is the version this app writes; zero is Version. A format may
	// move ahead of others, so its version numbers are its own.
	Current int
	// Steps upgrade an older file to Current; see Step.
	Steps []Step
}

func (f *Format) current() int {
	if f.Current == 0 {
		return Version
	}
	return f.Current
}

// Read returns data as the current format version: an unversioned file is
// version 1, and older files go through each step in turn. A newer file is
// ErrNewer and is not changed.
func (f *Format) Read(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return data, nil
	}
	if f.Member != "" {
		if trimmed[0] == '{' {
			if _, found, err := versionOf(trimmed); err == nil && found {
				return f.upgrade(trimmed)
			}
		}
		return f.upgrade(wrapRoot(trimmed, f.Member, 1))
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("%s: expected a JSON object", f.Name)
	}
	return f.upgrade(trimmed)
}

// Write returns data stamped with Version, wrapping a Member format's root.
// It refuses data that already carries another version: the caller built the
// wrong file.
func (f *Format) Write(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	trailing := data[len(bytes.TrimRight(data, " \t\r\n")):]
	if len(trimmed) == 0 {
		return data, nil
	}
	if f.Member != "" {
		if trimmed[0] == '{' {
			version, found, err := versionOf(trimmed)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			if found {
				if version != f.current() {
					return nil, fmt.Errorf("%s: writing version %d, this app writes %d", f.Name, version, f.current())
				}
				return data, nil
			}
		}
		return append(wrapRoot(trimmed, f.Member, f.current()), trailing...), nil
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("%s: expected a JSON object", f.Name)
	}
	version, found, err := versionOf(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	switch {
	case !found:
		return append(stampVersion(trimmed, f.current()), trailing...), nil
	case version != f.current():
		return nil, fmt.Errorf("%s: writing version %d, this app writes %d", f.Name, version, f.current())
	}
	return data, nil
}

func (f *Format) upgrade(data []byte) ([]byte, error) {
	version, found, err := versionOf(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	if !found {
		version = 1
		data = stampVersion(data, 1)
	}
	return upgrade(f.Name, data, version, f.current(), f.Steps)
}

// Upgraded reports whether data is older than the format's current version.
func (f *Format) Upgraded(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	version := 1
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if v, found, err := versionOf(trimmed); err == nil && found {
			version = v
		} else if f.Member != "" {
			return f.current() > 1
		}
	}
	return version < f.current()
}

// ListFormat is a line-oriented text file whose first line is
// "# vscroxy format N". Lines starting with # are comments.
type ListFormat struct {
	Name    string
	Current int
	Steps   []Step
}

func (f *ListFormat) current() int {
	if f.Current == 0 {
		return Version
	}
	return f.Current
}

// Read returns data at Version. A text file without the header is version 1.
func (f *ListFormat) Read(data []byte) ([]byte, error) {
	version, found := listVersion(data)
	if !found {
		return data, nil
	}
	return upgrade(f.Name, data, version, f.current(), f.Steps)
}

// Write returns data with the current version header.
func (f *ListFormat) Write(data []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return data, nil
	}
	version, found := listVersion(data)
	switch {
	case !found:
		header := "# vscroxy format " + strconv.Itoa(f.current()) + "\n"
		return append([]byte(header), data...), nil
	case version != f.current():
		return nil, fmt.Errorf("%s: writing version %d, this app writes %d", f.Name, version, f.current())
	}
	return data, nil
}

func upgrade(name string, data []byte, version, current int, steps []Step) ([]byte, error) {
	if version > current {
		return nil, fmt.Errorf("%w: %s is version %d, this app reads up to %d", ErrNewer, name, version, current)
	}
	for v := version; v < current; v++ {
		if v-1 >= len(steps) || steps[v-1] == nil {
			return nil, fmt.Errorf("%s: no migration from version %d", name, v)
		}
		var err error
		data, err = steps[v-1](data)
		if err != nil {
			return nil, fmt.Errorf("%s: migrating version %d: %w", name, v, err)
		}
		if next, found, err := versionOf(data); err != nil || !found || next != v+1 {
			if err == nil {
				err = fmt.Errorf("step left version %d, want %d", next, v+1)
			}
			return nil, fmt.Errorf("%s: migrating version %d: %w", name, v, err)
		}
	}
	return data, nil
}

// Upgraded reports whether data is older than the list format's current version.
func (f *ListFormat) Upgraded(data []byte) bool {
	version, found := listVersion(data)
	if !found {
		return false
	}
	return version < f.current()
}

// versionOf reads a JSON object's top-level "version" member.
func versionOf(data []byte) (version int, found bool, err error) {
	var probe struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return 0, false, err
	}
	if probe.Version == nil {
		return 0, false, nil
	}
	return *probe.Version, true, nil
}

// stampVersion adds "version" right after the object's opening brace, so the
// rest of the file keeps the formatting its writer chose.
func stampVersion(data []byte, version int) []byte {
	open := bytes.IndexByte(data, '{')
	if open < 0 {
		return data
	}
	member := `"version":` + strconv.Itoa(version)
	rest := data[open+1:]
	if i := len(rest) - len(bytes.TrimLeft(rest, " \t\r\n")); i < len(rest) && rest[i] == '}' {
		return slices.Concat(data[:open+1], []byte(member), rest)
	}
	return slices.Concat(data[:open+1], []byte(member+","), rest)
}

func wrapRoot(data []byte, member string, version int) []byte {
	return slices.Concat([]byte(`{"version":`+strconv.Itoa(version)+`,"`+member+`":`), data, []byte("}"))
}

// listVersion reads the "# vscroxy format N" header of a text file.
func listVersion(data []byte) (version int, found bool) {
	line, _, _ := strings.Cut(string(data), "\n")
	rest, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "# vscroxy format ")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return n, true
}

// ReadFile reads path and returns it at the current format version. A missing
// file is nil, nil. Nothing is written; the next save stores the upgrade.
func ReadFile(path string, f Formatter) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		return data, nil
	}
	return f.Read(data)
}

// WriteFileVersioned replaces path with data stamped for f. A file on disk
// from a newer version is refused and left untouched. Before an existing file
// is replaced, a copy goes to backupDir, which may be empty for files not
// worth keeping (a regenerable cache).
func WriteFileVersioned(path string, f Formatter, data []byte, backupDir string) error {
	if f != nil {
		var err error
		if data, err = f.Write(data); err != nil {
			return err
		}
	}
	disk, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if exists && f != nil {
		if _, err := f.Read(disk); err != nil {
			return err
		}
	}
	if exists && bytes.Equal(disk, data) {
		return nil
	}
	if exists && backupDir != "" {
		if err := Backup(backupDir, path); err != nil {
			return err
		}
	}
	return WriteFile(path, data)
}

// backupsKept is how many copies of one file Backup keeps.
const backupsKept = 10

// BackupDirFor is the backup directory beside a file's own directory:
// <dir>/backups. Files under ~/.vscroxy therefore share ~/.vscroxy/backups.
func BackupDirFor(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), "backups")
}

// Backup copies path into dir under a name that sorts by time, then removes
// the oldest copies so each file keeps backupsKept. A missing file is nothing
// to back up. The copy is private (0600), like the files it keeps.
func Backup(dir, path string) error {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	prefix := backupPrefix(path)
	name := prefix + filepath.Base(path) + "." + time.Now().UTC().Format(backupTimeLayout)
	if err := WriteFile(filepath.Join(dir, name), data); err != nil {
		return err
	}
	return pruneBackups(dir, prefix)
}

const backupTimeLayout = "20060102T150405.000000000"

func backupPrefix(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:4]) + "-"
}

// BackupEntry is one copy of one path, newest first in the result of
// ListBackups. Path points at the private copy; At is when it was made.
type BackupEntry struct {
	Path string
	At   time.Time
}

// ListBackups finds the copies Backup made of path, newest first. Copies of
// other paths in the same directory, and names that are not timestamps, are
// ignored: a shared backup directory holds files from the whole library.
func ListBackups(dir, path string) ([]BackupEntry, error) {
	if dir == "" || path == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	prefix := backupPrefix(path) + filepath.Base(path) + "."
	var found []BackupEntry
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) {
			continue
		}
		at, err := time.Parse(backupTimeLayout, strings.TrimPrefix(name, prefix))
		if err != nil {
			continue
		}
		found = append(found, BackupEntry{Path: filepath.Join(dir, name), At: at})
	}
	slices.SortFunc(found, func(x, y BackupEntry) int {
		if c := y.At.Compare(x.At); c != 0 {
			return c
		}
		return strings.Compare(y.Path, x.Path)
	})
	return found, nil
}

func pruneBackups(dir, prefix string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			names = append(names, entry.Name())
		}
	}
	if len(names) <= backupsKept {
		return nil
	}
	slices.Sort(names)
	for _, name := range names[:len(names)-backupsKept] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
