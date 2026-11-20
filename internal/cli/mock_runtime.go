package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
)

var errNoMockExamples = errors.New("no saved examples in the selected collection; save a response as an example first")

// mockSource is one collection's saved examples and the variables they use.
// It can be read again while a mock is running, so edits do not need a
// restart. The environment is read once when the source is created.
type mockSource struct {
	input  string
	folder string
	vars   map[string]string
}

type mockExamples struct {
	http    []request.Example
	grpc    []mockserver.GRPCExample
	count   int
	hasGRPC bool
}

func (s mockSource) load() (mockExamples, []string, error) {
	requests, _, warnings, err := loadCollection(s.input, s.folder)
	if err != nil {
		return mockExamples{}, nil, err
	}
	var set mockExamples
	for _, r := range requests {
		if r.Protocol == "gRPC" {
			set.hasGRPC = true
			for _, e := range r.Examples {
				set.grpc = append(set.grpc, mockserver.GRPCExample{
					Method: r.GRPC.Method, Schema: r.GRPC.Schema, Body: e.Body,
					Headers: e.Headers, Status: e.Status,
					Delay: time.Duration(e.DelayMS) * time.Millisecond,
				})
			}
			continue
		}
		if r.Protocol == "" {
			set.http = append(set.http, r.Examples...)
		}
	}
	set.count = len(set.http) + len(set.grpc)
	return set, warnings, nil
}

// mockRuntime owns a mock's listeners and its file watch. Close joins all of
// them, including on cancellation, and is safe to call more than once.
type mockRuntime struct {
	source   mockSource
	initial  mockExamples
	httpURL  string
	grpcURL  string
	http     *http.Server
	handler  *mockserver.Server
	grpc     *mockserver.GRPCServer
	httpDone chan struct{}
	grpcDone chan struct{}
	watch    *collectionWatch

	mu     sync.Mutex
	closed bool
}

// startMockRuntime serves source on loopback. Both protocols listen from the
// start so examples added later have a port to appear on. A collection with
// no examples at all is refused; a collection with only gRPC examples still
// gets an HTTP listener that answers 404 until HTTP examples are added.
func startMockRuntime(source mockSource, set mockExamples, opts mockserver.Options, listen, grpcAddr string, stdout, stderr io.Writer, watch bool) (*mockRuntime, error) {
	if set.count == 0 {
		return nil, errNoMockExamples
	}
	m := &mockRuntime{source: source, initial: set}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	m.handler = mockserver.NewWithOptions(set.http, source.vars, opts)
	m.http = &http.Server{Handler: m.handler, ReadHeaderTimeout: 10 * time.Second}
	m.httpDone = make(chan struct{})
	go func() {
		defer close(m.httpDone)
		_ = m.http.Serve(ln)
	}()
	m.httpURL = "http://" + ln.Addr().String()

	grpcServer, err := mockserver.NewGRPCReloadable(set.grpc)
	if err != nil {
		m.Close()
		return nil, err
	}
	grpcLn, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		m.Close()
		return nil, err
	}
	m.grpc, m.grpcDone = grpcServer, make(chan struct{})
	go func() {
		defer close(m.grpcDone)
		_ = m.grpc.Serve(grpcLn)
	}()
	m.grpcURL = grpcLn.Addr().String()

	if watch {
		m.watch = startCollectionWatch(source.input, m.reload, stdout, stderr)
	}
	return m, nil
}

// reload reads the collection again and swaps its examples only when the
// whole set is valid. A broken file leaves the last good set answering.
func (m *mockRuntime) reload() (int, error) {
	set, _, err := m.source.load()
	if err != nil {
		return 0, err
	}
	if err := mockserver.ReloadBoth(m.handler, m.grpc, set.http, set.grpc, m.source.vars); err != nil {
		return 0, err
	}
	return set.count, nil
}

// Close stops the file watch, then the servers, and waits for every goroutine
// it started to return. The flag is guarded because a caller can cancel a run
// while its deferred cleanup is already closing the same mock.
func (m *mockRuntime) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()

	if m.watch != nil {
		m.watch.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if m.http != nil {
		_ = m.http.Shutdown(ctx)
		_ = m.http.Close()
		<-m.httpDone
	}
	if m.grpc != nil {
		m.grpc.Stop()
		<-m.grpcDone
	}
}

// collectionWatch polls the input's files and calls reload when their names,
// sizes or modification times change. Polling needs no platform-specific
// watcher; a successful reload prints a line callers can wait for.
type collectionWatch struct {
	stop chan struct{}
	done chan struct{}

	mu     sync.Mutex
	closed bool
}

func startCollectionWatch(input string, reload func() (int, error), stdout, stderr io.Writer) *collectionWatch {
	w := &collectionWatch{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		ticks := time.NewTicker(50 * time.Millisecond)
		defer ticks.Stop()
		seen := collectionFingerprint(input)
		for {
			select {
			case <-w.stop:
				return
			case <-ticks.C:
			}
			fp := collectionFingerprint(input)
			if fp == seen {
				continue
			}
			seen = fp
			count, err := reload()
			if err != nil {
				fmt.Fprintf(stderr, "Could not reload the collection mock; keeping the last working examples: %v\n", err)
				continue
			}
			fmt.Fprintf(stdout, "Reloaded %d examples from the collection\n", count)
		}
	}()
	return w
}

func (w *collectionWatch) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	close(w.stop)
	w.mu.Unlock()
	<-w.done
}

func collectionFingerprint(input string) string {
	h := sha256.New()
	info, err := os.Stat(input)
	switch {
	case err == nil && info.IsDir():
		_ = filepath.WalkDir(input, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(h, "err %s %v\n", path, err)
				return nil
			}
			stat, statErr := d.Info()
			if statErr != nil {
				fmt.Fprintf(h, "err %s %v\n", path, statErr)
				return nil
			}
			fmt.Fprintf(h, "%s %v %d %d\n", path, d.IsDir(), stat.Size(), stat.ModTime().UnixNano())
			return nil
		})
	case err == nil:
		fmt.Fprintf(h, "%s %d %d\n", input, info.Size(), info.ModTime().UnixNano())
	default:
		fmt.Fprintf(h, "err %s %v\n", input, err)
	}
	return string(h.Sum(nil))
}
