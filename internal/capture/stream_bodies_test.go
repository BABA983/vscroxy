package capture

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bigBody returns n bytes of filler with marker at its end, so a test can tell
// the whole body from the head of it that a flow keeps inline.
func bigBody(n int, marker string) []byte {
	b := make([]byte, n)
	x := uint32(0x9e3779b9)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	copy(b[n-len(marker):], marker)
	return b
}

// bodyAt reads the body a flow kept: the file when it spilled, the head when it
// did not, and fails when the two disagree about how long the body is.
func bodyAt(t *testing.T, head, file string, size int) []byte {
	t.Helper()
	if file == "" {
		if len(head) != size {
			t.Fatalf("inline body = %d bytes, want %d", len(head), size)
		}
		return []byte(head)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read kept body: %v", err)
	}
	if len(data) != size {
		t.Fatalf("kept body = %d bytes, want %d", len(data), size)
	}
	return data
}

// waitNoFlow fails when the capture publishes a second flow for an exchange
// that was already reported.
func waitNoFlow(t *testing.T, s *Server) {
	t.Helper()
	select {
	case f := <-s.Flows():
		t.Fatalf("a second flow was published: %+v", f)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestStreamedRequestKeepsTheWholeBody(t *testing.T) {
	sent := bigBody(9<<20, "request-tail-marker")
	got := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		got <- body
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "stored")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Post(upstream.URL+"/upload", "application/octet-stream", bytes.NewReader(sent))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(body) != "stored" {
		t.Fatalf("client got %q, want %q", body, "stored")
	}
	if up := <-got; !bytes.Equal(up, sent) {
		t.Fatalf("upstream got %d bytes, want %d", len(up), len(sent))
	}

	f := waitFlow(t, s)
	if f.Method != "POST" || f.Status != 200 || f.Path != "/upload" {
		t.Fatalf("flow = %+v", f)
	}
	if f.RequestBodyError != "" {
		t.Fatalf("request body reported as incomplete: %s", f.RequestBodyError)
	}
	if f.RequestBodySize != len(sent) {
		t.Fatalf("request body size = %d, want %d", f.RequestBodySize, len(sent))
	}
	if len(f.RequestBody) != maxBody {
		t.Fatalf("inline request body = %d bytes, want the %d-byte head", len(f.RequestBody), maxBody)
	}
	kept := bodyAt(t, f.RequestBody, f.RequestBodyFile, f.RequestBodySize)
	if !bytes.Equal(kept, sent) {
		t.Fatal("the kept request body is not what the client sent")
	}
	if !bytes.HasSuffix(kept, []byte("request-tail-marker")) {
		t.Fatal("the kept request body lost its tail")
	}
	if f.ResponseBody != "stored" || f.ResponseBodySize != len("stored") || f.ResponseBodyError != "" {
		t.Fatalf("response body = %q, size %d, error %q", f.ResponseBody, f.ResponseBodySize, f.ResponseBodyError)
	}
	waitNoFlow(t, s)
}

// TestStreamedChunkedRequestKeepsTheWholeBody covers a request body of unknown
// length: the client sends it chunked, so no length says where it should end.
func TestStreamedChunkedRequestKeepsTheWholeBody(t *testing.T) {
	sent := bigBody(9<<20, "chunked-tail-marker")
	got := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		got <- body
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	// A reader the client cannot measure makes it chunk the body.
	unknown := struct{ io.Reader }{bytes.NewReader(sent)}
	resp, err := clientFor(t, s, nil).Post(upstream.URL+"/chunked", "application/octet-stream", unknown)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if up := <-got; !bytes.Equal(up, sent) {
		t.Fatalf("upstream got %d bytes, want %d", len(up), len(sent))
	}

	f := waitFlow(t, s)
	if f.Status != 200 || f.Path != "/chunked" {
		t.Fatalf("flow = %+v", f)
	}
	if f.RequestBodyError != "" || f.RequestBodySize != len(sent) {
		t.Fatalf("request body = %d bytes, error %q; want the %d sent", f.RequestBodySize, f.RequestBodyError, len(sent))
	}
	kept := bodyAt(t, f.RequestBody, f.RequestBodyFile, f.RequestBodySize)
	if !bytes.HasSuffix(kept, []byte("chunked-tail-marker")) {
		t.Fatal("the kept chunked request body lost its tail")
	}
	waitNoFlow(t, s)
}

func TestStreamedResponseKeepsTheTail(t *testing.T) {
	sent := bigBody(9<<20, "response-tail-marker")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(sent)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/download")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Equal(body, sent) {
		t.Fatalf("client got %d bytes, want %d, unchanged", len(body), len(sent))
	}

	f := waitFlow(t, s)
	if f.Status != 200 || f.Path != "/download" {
		t.Fatalf("flow = %+v", f)
	}
	if f.ResponseBodyError != "" {
		t.Fatalf("response body reported as incomplete: %s", f.ResponseBodyError)
	}
	if f.ResponseBodySize != len(sent) || f.Bytes != len(sent) {
		t.Fatalf("sizes = %d, %d; want %d", f.ResponseBodySize, f.Bytes, len(sent))
	}
	if len(f.ResponseBody) != maxBody || !bytes.Equal([]byte(f.ResponseBody), sent[:maxBody]) {
		t.Fatalf("inline response body = %d bytes, want the first %d", len(f.ResponseBody), maxBody)
	}
	kept := bodyAt(t, f.ResponseBody, f.ResponseBodyFile, f.ResponseBodySize)
	if !bytes.Equal(kept, sent) {
		t.Fatal("the kept response body is not what the server sent")
	}
	if !bytes.HasSuffix(kept, []byte("response-tail-marker")) {
		t.Fatal("the kept response body lost the marker past the buffering threshold")
	}
	if f.RequestBodySize != 0 || f.RequestBodyError != "" {
		t.Fatalf("request body = %d bytes, error %q; want none", f.RequestBodySize, f.RequestBodyError)
	}
	waitNoFlow(t, s)

	// The kept body goes away with the server, like every other spill.
	dir := filepath.Dir(f.ResponseBodyFile)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("spill directory survived close: %v", err)
	}
}

func TestStreamedGzipResponseIsDecoded(t *testing.T) {
	sent := bigBody(9<<20, "gzip-tail-marker")
	var wire bytes.Buffer
	zw := gzip.NewWriter(&wire)
	if _, err := zw.Write(sent); err != nil {
		t.Fatalf("gzip body: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if wire.Len() < 8<<20 {
		t.Fatalf("the compressed body is %d bytes, too small to stream", wire.Len())
	}
	encoded := wire.Bytes()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip")
		half := len(encoded) / 2
		_, _ = w.Write(encoded[:half])
		w.(http.Flusher).Flush() // no Content-Length: the response is chunked
		_, _ = w.Write(encoded[half:])
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	req, err := http.NewRequestWithContext(t.Context(), "GET", upstream.URL+"/gzip", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	// Asking for gzip keeps the client from decoding it for us, so the bytes
	// the client gets are the bytes the proxy forwarded.
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := clientFor(t, s, nil).Do(req)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Equal(body, encoded) {
		t.Fatalf("client got %d bytes, want the %d forwarded", len(body), len(encoded))
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("client body is not gzip: %v", err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decode client body: %v", err)
	}
	if !bytes.Equal(decoded, sent) {
		t.Fatalf("client decoded %d bytes, want %d", len(decoded), len(sent))
	}

	f := waitFlow(t, s)
	if f.Status != 200 || f.Path != "/gzip" {
		t.Fatalf("flow = %+v", f)
	}
	if got := f.ResponseHeader.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content-encoding = %q, want gzip", got)
	}
	if got := f.ResponseHeader.Get("Content-Length"); got != "" {
		t.Fatalf("content-length = %q, want a chunked response", got)
	}
	if f.ResponseBodyError != "" {
		t.Fatalf("response body reported as incomplete: %s", f.ResponseBodyError)
	}
	if f.ResponseBodySize != len(sent) {
		t.Fatalf("response body size = %d, want the decoded %d", f.ResponseBodySize, len(sent))
	}
	if len(f.ResponseBody) != maxBody || !bytes.Equal([]byte(f.ResponseBody), sent[:maxBody]) {
		t.Fatalf("inline response body = %d bytes, want the first %d decoded", len(f.ResponseBody), maxBody)
	}
	kept := bodyAt(t, f.ResponseBody, f.ResponseBodyFile, f.ResponseBodySize)
	if !bytes.Equal(kept, sent) {
		t.Fatal("the kept response body is not the decoded body the server sent")
	}
	waitNoFlow(t, s)
}

func TestStreamedResponseCutByClientAbort(t *testing.T) {
	stop := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		chunk := bytes.Repeat([]byte("y"), 32<<10)
		for range 400 {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()
	defer close(stop)

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/slow")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 64<<10)); err != nil {
		t.Fatalf("read the start of the body: %v", err)
	}
	// Hang up in the middle of the body: the capture must say the body it
	// kept is not the whole one, not publish a short body as complete.
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	if f.Status != 200 || f.Path != "/slow" {
		t.Fatalf("flow = %+v", f)
	}
	if f.ResponseBodyError == "" {
		t.Fatalf("a cut body was published as complete: %d bytes", f.ResponseBodySize)
	}
	if f.ResponseBodyFile != "" {
		t.Fatal("a cut body kept a file that reads as the whole body")
	}
	if f.ResponseBodySize <= len(f.ResponseBody) {
		t.Fatalf("cut body size %d, head %d: the flow does not say it is short", f.ResponseBodySize, len(f.ResponseBody))
	}
	waitNoFlow(t, s)
}

func TestStreamedResponseCutByUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		// Drop the connection in the middle of the chunked body.
		panic(http.ErrAbortHandler)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/events")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()
	// The client must see the body fail as the upstream's did, not end as if
	// it were whole: for a gRPC call that is the difference between a reset
	// stream and a call that ended without its status.
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("a body the upstream cut reached the client as complete: %q", body)
	}
	if string(body) != "data: first\n\n" {
		t.Fatalf("body before the cut = %q", body)
	}
}

// TestStreamedRequestToAnSSEEndpoint keeps the events an SSE flow publishes
// itself: a large request makes the engine stream the response too, and the
// capture must not publish the flow a second time.
func TestStreamedRequestToAnSSEEndpoint(t *testing.T) {
	sent := bigBody(9<<20, "prompt-tail-marker")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: tick\ndata: one\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: two\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Post(upstream.URL+"/events", "text/plain", bytes.NewReader(sent))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Contains(body, []byte("one")) || !bytes.Contains(body, []byte("two")) {
		t.Fatalf("client body = %q, want both events", body)
	}

	f := waitFlow(t, s)
	if f.Kind != "sse" || f.Status != 200 || f.Path != "/events" {
		t.Fatalf("flow = %+v", f)
	}
	if f.RequestBodyError != "" || f.RequestBodySize != len(sent) {
		t.Fatalf("request body = %d bytes, error %q; want the %d sent", f.RequestBodySize, f.RequestBodyError, len(sent))
	}
	kept := bodyAt(t, f.RequestBody, f.RequestBodyFile, f.RequestBodySize)
	if !bytes.Equal(kept, sent) {
		t.Fatal("the kept request body is not what the client sent")
	}
	if first := waitFrame(t, s); first.FlowID != f.ID || first.Frame.Data != "one" {
		t.Fatalf("first event = %+v, want the first the server sent", first)
	}
	if second := waitFrame(t, s); second.Frame.Data != "two" {
		t.Fatalf("second event = %+v", second)
	}
	waitNoFlow(t, s)
}

func TestStreamedRequestToADeadUpstream(t *testing.T) {
	sent := bigBody(9<<20, "never-delivered")
	s, _ := startServer(t)
	// Nothing listens on port 1, so the exchange fails after the request body
	// was streamed but before it was sent.
	resp, err := clientFor(t, s, nil).Post("http://127.0.0.1:1/upload", "text/plain", bytes.NewReader(sent))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	if f.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", f.Status, http.StatusBadGateway)
	}
	if f.RequestBodyError == "" {
		t.Fatalf("a request body that never went upstream was published as complete: %d bytes", f.RequestBodySize)
	}
	if f.RequestBodyFile != "" {
		t.Fatal("a body that never arrived kept a file")
	}
	waitNoFlow(t, s)
}
