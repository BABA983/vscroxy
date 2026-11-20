package mockserver

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/grpctest"
	"github.com/baba983/vscroxy/internal/request"
)

// blockingWriter lets a test hold one answer at WriteHeader while the server
// reloads, without either sleeps or scheduling hints.
type blockingWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	code    int
	body    strings.Builder
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{header: http.Header{}, entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (w *blockingWriter) Header() http.Header { return w.header }

func (w *blockingWriter) WriteHeader(code int) {
	w.code = code
	w.entered <- struct{}{}
	<-w.release
}

func (w *blockingWriter) Write(p []byte) (int, error) { return w.body.Write(p) }

func example(name, body string) request.Example {
	return request.Example{Name: name, Status: 200, Body: body,
		Request: request.ExampleRequest{Method: "GET", URL: "http://api.test/items"}}
}

// A request that already picked its answer keeps it; the next request sees
// the added, edited and deleted examples, all without a new server.
func TestReloadKeepsTheInflightAnswerAndSwapsTheNextOne(t *testing.T) {
	s := New([]request.Example{example("old", "old {{v}}")}, map[string]string{"v": "first"})
	writer := newBlockingWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/items", nil))
	}()
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first request never reached the example")
	}

	added := example("added", "added")
	edited := example("old", "edited {{v}}")
	s.Reload([]request.Example{added, edited}, map[string]string{"v": "second"})

	next := httptest.NewRecorder()
	s.ServeHTTP(next, httptest.NewRequest(http.MethodGet, "/items", nil))
	if next.Code != 200 || next.Body.String() != "added" {
		t.Fatalf("next answer = %d %q, want the added example", next.Code, next.Body.String())
	}
	named := httptest.NewRecorder()
	byName := httptest.NewRequest(http.MethodGet, "/items", nil)
	byName.Header.Set(SelectHeader, "old")
	s.ServeHTTP(named, byName)
	if named.Body.String() != "edited second" {
		t.Fatalf("named answer = %q, want the edited example with new vars", named.Body.String())
	}

	close(writer.release)
	<-done
	if writer.code != 200 || writer.body.String() != "old first" {
		t.Fatalf("in-flight answer = %d %q, want the old example and vars", writer.code, writer.body.String())
	}

	s.Reload(nil, nil)
	deleted := httptest.NewRecorder()
	s.ServeHTTP(deleted, httptest.NewRequest(http.MethodGet, "/items", nil))
	if deleted.Code != http.StatusNotFound {
		t.Fatalf("after deleting every example: %d %q, want 404", deleted.Code, deleted.Body.String())
	}
}

// A malformed gRPC example is refused before either set changes, so a broken
// collection keeps serving the last good HTTP and gRPC examples.
func TestReloadBothKeepsTheLastGoodSetOnError(t *testing.T) {
	_, schema := grpctest.Echo(t)
	good := GRPCExample{Method: "/grpctest.Echo/Unary", Schema: schema, Body: `"mocked"`}
	grpcServer, err := NewGRPCReloadable([]GRPCExample{good})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := New([]request.Example{example("ok", "http-good")}, nil)
	bad := good
	bad.Body = `not json`
	if err := ReloadBoth(httpServer, grpcServer, []request.Example{example("ok", "http-bad")}, []GRPCExample{bad}, nil); err == nil {
		t.Fatal("a malformed gRPC example was accepted")
	}
	rec := httptest.NewRecorder()
	httpServer.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	if rec.Body.String() != "http-good" {
		t.Fatalf("HTTP answer after a failed reload = %q, want the last good one", rec.Body.String())
	}

	// The gRPC set is also untouched: calling the saved method still returns
	// the original body after a refused reload.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = grpcServer.Serve(ln)
	}()
	t.Cleanup(func() { grpcServer.Stop(); <-served })
	got := request.Request{Protocol: "gRPC", URL: "http://" + ln.Addr().String(), Body: `"hello"`,
		GRPC: request.GRPCConfig{Schema: schema, Method: "/grpctest.Echo/Unary"},
	}.Execute(t.Context(), nil, request.Context{}, nil, nil)
	if got.Err != nil || got.Response == nil || !strings.Contains(got.Response.ResponseBody, "mocked") {
		t.Fatalf("gRPC answer after a failed reload: %+v err=%v", got.Response, got.Err)
	}
}
