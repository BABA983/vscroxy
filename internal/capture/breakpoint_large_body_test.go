package capture

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// These tests go through the proxy and Resume, the same paths the dialog
// uses: pause a matching exchange, decide, and read what the other side got.

func pauseTestBody(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i%61)
	}
	return b
}

func waitForTestBreakpoint(t *testing.T, s *Server) Breakpoint {
	t.Helper()
	select {
	case bp := <-s.Breakpoints():
		return bp
	case <-t.Context().Done():
		t.Fatal("no breakpoint")
		return Breakpoint{}
	}
}

func TestBreakpointEditsABodyAboveOneMiB(t *testing.T) {
	original := pauseTestBody(2<<20, 33)
	edited := bytes.Clone(original)
	edited[0], edited[len(edited)-1] = '{', 'Z'
	seen := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		switch {
		case err != nil:
			seen <- err
		case !bytes.Equal(body, edited):
			seen <- fmt.Errorf("upstream body = %d bytes, want the edited %d", len(body), len(edited))
		case r.Header.Get("Content-Length") != strconv.Itoa(len(edited)):
			seen <- fmt.Errorf("upstream Content-Length = %q, want %d", r.Header.Get("Content-Length"), len(edited))
		default:
			seen <- nil
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)

	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL, bytes.NewReader(original))
		if err != nil {
			done <- err
			return
		}
		resp, err := clientFor(t, s, nil).Do(req)
		if err != nil {
			done <- err
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			done <- fmt.Errorf("status = %d", resp.StatusCode)
			return
		}
		done <- nil
	}()

	bp := waitForTestBreakpoint(t, s)
	if bp.BodyReadOnly || bp.BodySize != int64(len(original)) || bp.Body != string(original) {
		t.Fatalf("pause: editable=%t size=%d body=%d bytes, want %d editable bytes",
			!bp.BodyReadOnly, bp.BodySize, len(bp.Body), len(original))
	}
	s.Resume(bp.ID, Decision{Header: bp.Header.Clone(), Body: string(edited)})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-seen; err != nil {
		t.Fatal(err)
	}
}

func TestBreakpointReplacesAnOversizedBodyFromAFile(t *testing.T) {
	original := pauseTestBody(6<<20, 40)
	replacement := append(pauseTestBody(5<<20+17, 200), 0, 0xff, '\n')
	dir := t.TempDir()
	file := filepath.Join(dir, "replacement.bin")
	if err := os.WriteFile(file, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	seen := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		switch {
		case err != nil:
			seen <- err
		case !bytes.Equal(body, replacement):
			sum, want := sha256.Sum256(body), sha256.Sum256(replacement)
			seen <- fmt.Errorf("upstream body %x, want %x", sum, want)
		case r.Header.Get("Content-Length") != strconv.Itoa(len(replacement)):
			seen <- fmt.Errorf("upstream Content-Length = %q, want %d", r.Header.Get("Content-Length"), len(replacement))
		case r.Header.Get("Content-Encoding") != "":
			seen <- fmt.Errorf("upstream Content-Encoding = %q", r.Header.Get("Content-Encoding"))
		default:
			seen <- nil
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)

	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL, bytes.NewReader(original))
		if err != nil {
			done <- err
			return
		}
		resp, err := clientFor(t, s, nil).Do(req)
		if err != nil {
			done <- err
			return
		}
		_ = resp.Body.Close()
		done <- nil
	}()

	bp := waitForTestBreakpoint(t, s)
	if !bp.BodyReadOnly || bp.Body != "" || bp.BodySize != int64(len(original)) {
		t.Fatalf("oversized pause: readOnly=%t body=%d bytes, size=%d", bp.BodyReadOnly, len(bp.Body), bp.BodySize)
	}
	saved := filepath.Join(dir, "saved.bin")
	n, err := s.SaveBreakpointBody(&bp, saved)
	if err != nil || n != int64(len(original)) {
		t.Fatalf("save body: %d bytes, %v", n, err)
	}
	savedBytes, err := os.ReadFile(saved)
	if err != nil || !bytes.Equal(savedBytes, original) {
		t.Fatalf("saved body = %d bytes, want the exact original: %v", len(savedBytes), err)
	}
	s.Resume(bp.ID, Decision{Header: bp.Header.Clone(), BodyFile: file})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-seen; err != nil {
		t.Fatal(err)
	}
}

func TestBreakpointStreamsAReplacementFileAsAResponse(t *testing.T) {
	original := pauseTestBody(5<<20, 60)
	replacement := append(pauseTestBody(9<<20+29, 170), 0, 0xff)
	file := filepath.Join(t.TempDir(), "response.bin")
	if err := os.WriteFile(file, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(original)
	}))
	defer upstream.Close()
	s, _ := startServerWithRules(t, Rule{Type: RuleBreakResponse, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	done := make(chan error, 1)
	go func() {
		resp, err := client.Get(upstream.URL)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		switch {
		case err != nil:
			done <- err
		case !bytes.Equal(body, replacement):
			done <- fmt.Errorf("client body = %d bytes, want %d", len(body), len(replacement))
		case resp.Header.Get("Content-Length") != strconv.Itoa(len(replacement)):
			done <- fmt.Errorf("Content-Length = %q, want %d", resp.Header.Get("Content-Length"), len(replacement))
		case resp.ContentLength != int64(len(replacement)):
			done <- fmt.Errorf("parsed Content-Length = %d, want %d", resp.ContentLength, len(replacement))
		default:
			done <- nil
		}
	}()

	bp := waitForTestBreakpoint(t, s)
	if !bp.BodyReadOnly || bp.BodySize != int64(len(original)) {
		t.Fatalf("oversized response: readOnly=%t size=%d", bp.BodyReadOnly, bp.BodySize)
	}
	s.Resume(bp.ID, Decision{Header: bp.Header.Clone(), BodyFile: file})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBreakpointGzipResponseEditDropsEncoding(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	plain := []byte(`{"order":"abc","status":"new"}`)
	if _, err := gz.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		_, _ = w.Write(compressed.Bytes())
	}))
	defer upstream.Close()
	s, _ := startServerWithRules(t, Rule{Type: RuleBreakResponse, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)
	client.Transport.(*http.Transport).DisableCompression = true

	edited := `{"order":"abc","status":"sent"}`
	done := make(chan error, 1)
	go func() {
		resp, err := client.Get(upstream.URL)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		switch {
		case err != nil:
			done <- err
		case string(body) != edited:
			done <- fmt.Errorf("client body = %q, want %q", body, edited)
		case resp.Header.Get("Content-Encoding") != "":
			done <- fmt.Errorf("Content-Encoding survived the edit: %q", resp.Header.Get("Content-Encoding"))
		case resp.Header.Get("Content-Length") != strconv.Itoa(len(edited)):
			done <- fmt.Errorf("Content-Length = %q, want %d", resp.Header.Get("Content-Length"), len(edited))
		default:
			done <- nil
		}
	}()

	bp := waitForTestBreakpoint(t, s)
	if bp.BodyReadOnly || bp.Body != string(plain) || bp.BodySize != int64(len(plain)) {
		t.Fatalf("gzip pause: editable=%t body=%q size=%d", !bp.BodyReadOnly, bp.Body, bp.BodySize)
	}
	if bp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("pause lost the encoding header: %v", bp.Header)
	}
	saved := filepath.Join(t.TempDir(), "decoded.json")
	n, err := s.SaveBreakpointBody(&bp, saved)
	if err != nil || n != int64(len(plain)) {
		t.Fatalf("save decoded body: %d bytes, %v", n, err)
	}
	decoded, err := os.ReadFile(saved)
	if err != nil || string(decoded) != string(plain) {
		t.Fatalf("saved decoded body = %q: %v", decoded, err)
	}
	s.Resume(bp.ID, Decision{Header: bp.Header.Clone(), Body: edited})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBreakpointUneditedNonUTF8BodyKeepsItsBytes(t *testing.T) {
	original := append(pauseTestBody(64<<10, 1), 0xff, 0x00, 0xfe)
	seen := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		switch {
		case err != nil:
			seen <- err
		case !bytes.Equal(body, original):
			seen <- fmt.Errorf("binary upstream body changed: %d bytes, want %d", len(body), len(original))
		case r.Header.Get("Content-Length") != strconv.Itoa(len(original)):
			seen <- fmt.Errorf("Content-Length = %q, want %d", r.Header.Get("Content-Length"), len(original))
		default:
			seen <- nil
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)

	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL, bytes.NewReader(original))
		if err != nil {
			done <- err
			return
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := clientFor(t, s, nil).Do(req)
		if err != nil {
			done <- err
			return
		}
		_ = resp.Body.Close()
		done <- nil
	}()

	bp := waitForTestBreakpoint(t, s)
	if !bp.BodyReadOnly || bp.Body != "" || bp.BodySize != int64(len(original)) {
		t.Fatalf("binary pause: readOnly=%t body=%q size=%d", bp.BodyReadOnly, bp.Body, bp.BodySize)
	}
	// A caller that sends text anyway must not turn binary bytes into text.
	s.Resume(bp.ID, Decision{Header: bp.Header.Clone(), Body: "edited as text"})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-seen; err != nil {
		t.Fatal(err)
	}
}
