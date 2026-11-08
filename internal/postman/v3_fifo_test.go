//go:build unix

package postman_test

import (
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/postman"
)

func TestV3FIFOExampleDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Fifo\n",
		"One.request.yaml":           "$kind: http-request\nname: One\nurl: https://example.com/one\nexamples: ./pipe\n",
	})
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	readWithoutBlocking(t, dir, 1)
}

func TestV3FIFORequestDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Fifo\n",
	})
	if err := syscall.Mkfifo(filepath.Join(dir, "Pipe.request.yaml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	readWithoutBlocking(t, dir, 0)
}

func readWithoutBlocking(t *testing.T, dir string, requests int) {
	t.Helper()
	type result struct {
		col postman.Collection
		err error
	}
	done := make(chan result, 1)
	go func() {
		col, err := postman.ReadCollectionDir(dir)
		done <- result{col, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.col.Requests) != requests {
			t.Errorf("requests = %+v", got.col.Requests)
		}
		if !slices.Contains(got.col.Warnings, "Skipped 1 symbolic link, non-regular file or path outside the collection") {
			t.Errorf("warnings = %q", got.col.Warnings)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("import blocked on a FIFO")
	}
}
