package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileNeverOverwritesChangesMadeOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf", "rules.json")
	f := File{Path: path}
	if data, err := f.Read(); err != nil || data != nil {
		t.Fatalf("missing file = %q, %v", data, err)
	}
	// Created by someone else after the app saw no file.
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("theirs"), 0o644)
	if err := f.Write([]byte("ours")); !errors.Is(err, ErrChanged) {
		t.Fatalf("write over a file created meanwhile: %v", err)
	}
	if _, err := f.Read(); err != nil {
		t.Fatal(err)
	}
	if err := f.Write([]byte("ours")); err != nil {
		t.Fatal("write after reload:", err)
	}
	// Edited by someone else after the app wrote it.
	os.WriteFile(path, []byte("edited by hand"), 0o644)
	if err := f.Write([]byte("ours again")); !errors.Is(err, ErrChanged) {
		t.Fatalf("write over a hand edit: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "edited by hand" {
		t.Fatalf("hand edit replaced: %q", data)
	}
	// Removed by someone else.
	f.Read()
	os.Remove(path)
	if err := f.Write([]byte("x")); !errors.Is(err, ErrChanged) {
		t.Fatalf("write over a removed file: %v", err)
	}
}

func TestUnreadableFileStaysUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.json")
	os.WriteFile(path, []byte("{damaged"), 0o600)
	f := File{Path: path}
	if _, err := f.Read(); err != nil {
		t.Fatal(err)
	}
	f.Fail(errors.New("invalid JSON"))
	if err := f.Write([]byte("{}")); err == nil || f.Problem() == nil {
		t.Fatal("a file that did not parse was replaced")
	}
	if data, _ := os.ReadFile(path); string(data) != "{damaged" {
		t.Fatalf("damaged file replaced: %q", data)
	}
	// A directory where the file should be cannot be read at all.
	blocked := File{Path: t.TempDir()}
	if _, err := blocked.Read(); err == nil || blocked.Write([]byte("x")) == nil {
		t.Fatal("an unreadable path was written")
	}
}

func TestWriteFileIsPrivateAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	path := filepath.Join(dir, "vault.json")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(path, []byte("old"), 0o644) // an older version wrote it world-readable
	if err := WriteFile(path, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	entries, _ := os.ReadDir(dir)
	if data, _ := os.ReadFile(path); string(data) != "secret" || info.Mode().Perm() != 0o600 || len(entries) != 1 {
		t.Fatalf("mode %v, %d entries", info.Mode().Perm(), len(entries))
	}
}
