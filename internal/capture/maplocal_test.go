package capture

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mapLocalServer(t *testing.T, rules ...Rule) (*Server, *http.Client) {
	t.Helper()
	s, _ := startServer(t)
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	return s, clientFor(t, s, nil)
}

func TestMapLocalDelayWaitsBeforeAnswering(t *testing.T) {
	file := filepath.Join(t.TempDir(), "slow.json")
	if err := os.WriteFile(file, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, client := mapLocalServer(t, Rule{Type: RuleMapLocal, Match: "/slow", Target: file, DelayMS: 150})
	start := time.Now()
	resp, err := client.Get("http://example.test/slow")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Fatalf("body %q", body)
	}
	if elapsed := time.Since(start); elapsed < 140*time.Millisecond {
		t.Fatalf("Map Local answered after %v, want the 150 ms delay", elapsed)
	}
}

func TestMapLocalStreamsFilesAboveTheBufferedCap(t *testing.T) {
	const size = maxRuleBody + 1<<20
	file := filepath.Join(t.TempDir(), "large.bin")
	payload := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	if err := os.WriteFile(file, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	s, client := mapLocalServer(t, Rule{Type: RuleMapLocal, Match: "/large", Target: file})
	resp, err := client.Get("http://example.test/large")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || len(got) != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("streamed body: %d bytes, %v", len(got), err)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "9437184" {
		t.Fatalf("Content-Length %q", cl)
	}
	f := waitFlow(t, s)
	if f.ResponseBodySize != len(payload) {
		t.Fatalf("recorded size %d, want %d", f.ResponseBodySize, len(payload))
	}
	if len(f.ResponseBody) > localBodyPreview {
		t.Fatalf("the table kept %d bytes of a streamed file", len(f.ResponseBody))
	}
}

func TestMapLocalAnswerPausesAtAResponseBreakpoint(t *testing.T) {
	file := filepath.Join(t.TempDir(), "local.txt")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, client := mapLocalServer(t,
		Rule{Type: RuleMapLocal, Match: "/paused", Target: file},
		Rule{Type: RuleBreakResponse, Match: "/paused"},
	)
	s.SetBreak(true)
	type answer struct {
		status int
		body   string
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		resp, err := client.Get("http://example.test/paused")
		if err != nil {
			done <- answer{err: err}
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- answer{status: resp.StatusCode, body: string(body)}
	}()
	var bp Breakpoint
	select {
	case bp = <-s.Breakpoints():
	case <-time.After(10 * time.Second):
		t.Fatal("the Map Local answer did not pause")
	}
	if bp.Phase != PhaseResponse || bp.Status != http.StatusOK || bp.Body != "original" {
		t.Fatalf("breakpoint %+v", bp)
	}
	if !s.Resume(bp.ID, Decision{Status: http.StatusCreated, Body: "edited"}) {
		t.Fatal("resume failed")
	}
	select {
	case got := <-done:
		if got.err != nil || got.status != http.StatusCreated || got.body != "edited" {
			t.Fatalf("answer %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the edited answer did not arrive")
	}
	f := waitFlow(t, s)
	if f.Status != http.StatusCreated || f.ResponseBody != "edited" {
		t.Fatalf("row %d %q", f.Status, f.ResponseBody)
	}
	if !strings.Contains(strings.Join(f.Actions, "\n"), "Response breakpoint applied") {
		t.Fatalf("actions %v", f.Actions)
	}
}

func TestMapLocalDelayRejectsOutOfRange(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ms := range []int64{-1, 60_001} {
		if err := ValidateRules([]Rule{{Type: RuleMapLocal, Match: "/x", Target: file, DelayMS: ms}}); err == nil {
			t.Fatalf("accepted a %d ms delay", ms)
		}
	}
	if err := ValidateRules([]Rule{{Type: RuleBlock, Match: "/x", DelayMS: 1}}); err == nil {
		t.Fatal("a Block rule carried a delay")
	}
}
