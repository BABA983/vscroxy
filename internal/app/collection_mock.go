package app

import (
	"errors"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

// collectionMock is a collection's mock server, listening on loopback ports
// until it is stopped or the app quits. Both protocols listen from the start,
// so examples added to either can be reloaded without a restart.
type collectionMock struct {
	server     *http.Server
	handler    *mockserver.Server
	grpcServer *mockserver.GRPCServer
	httpDone   chan struct{}
	grpcDone   chan struct{}
	url        string
	grpcURL    string
	options    mockserver.Options

	mu     sync.Mutex
	closed bool
}

// collectionExamples are the HTTP examples of the requests under path, in
// order. A gRPC example is served by the gRPC mock instead.
func (a *app) collectionExamples(path string) []request.Example {
	var examples []request.Example
	for _, r := range a.requests {
		if (r.Collection == path || strings.HasPrefix(r.Collection, path+"/")) && r.Protocol == "" {
			examples = append(examples, r.Examples...)
		}
	}
	return examples
}

// collectionGRPCExamples are the unary gRPC examples under path.
func (a *app) collectionGRPCExamples(path string) []mockserver.GRPCExample {
	var examples []mockserver.GRPCExample
	for _, r := range a.requests {
		if (r.Collection != path && !strings.HasPrefix(r.Collection, path+"/")) || r.Protocol != "gRPC" {
			continue
		}
		for _, e := range r.Examples {
			examples = append(examples, mockserver.GRPCExample{
				Method: r.GRPC.Method, Schema: r.GRPC.Schema, Body: e.Body,
				Headers: e.Headers, Status: e.Status,
				Delay: time.Duration(e.DelayMS) * time.Millisecond,
			})
		}
	}
	return examples
}

// startCollectionMock serves path's examples on free loopback ports with the
// default behaviors. Saved edits are reloaded while it runs.
func (a *app) startCollectionMock(path string) {
	if _, err := a.startCollectionMockWithOptions(path, mockserver.Options{}); err != nil {
		a.fail(locales.MockNamed(path), err)
	}
}

// startCollectionMockWithOptions starts the HTTP and gRPC mock servers and
// returns the running one. A second start for the same collection returns the
// first, unchanged.
func (a *app) startCollectionMockWithOptions(path string, opts mockserver.Options) (*collectionMock, error) {
	if m := a.mocks[path]; m != nil {
		return m, nil
	}
	examples := a.collectionExamples(path)
	grpcExamples := a.collectionGRPCExamples(path)
	if len(examples) == 0 && len(grpcExamples) == 0 {
		return nil, errors.New("no saved examples under this collection")
	}
	m := &collectionMock{options: opts, httpDone: make(chan struct{}), grpcDone: make(chan struct{})}
	vars := a.inheritance(path).Values(a.activeVars())
	// Both Serve loops are joined by close, which the stop and Quit paths
	// call. They own no app cleanup, so the app's goroutine guard is not used.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	m.handler = mockserver.NewWithOptions(examples, vars, opts)
	m.server = &http.Server{Handler: m.handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		defer close(m.httpDone)
		_ = m.server.Serve(ln)
	}()
	m.url = "http://" + ln.Addr().String()

	grpcServer, err := mockserver.NewGRPCReloadable(grpcExamples)
	if err != nil {
		m.close()
		return nil, err
	}
	grpcLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.close()
		return nil, err
	}
	m.grpcServer = grpcServer
	go func() {
		defer close(m.grpcDone)
		_ = m.grpcServer.Serve(grpcLn)
	}()
	m.grpcURL = "http://" + grpcLn.Addr().String()
	if a.mocks == nil {
		a.mocks = map[string]*collectionMock{}
	}
	a.mocks[path] = m
	if m.url != "" {
		a.done(locales.MockServerRunning(path, m.url))
	}
	if m.grpcURL != "" {
		a.done(locales.MockGRPCServerRunning(path, m.grpcURL))
	}
	return m, nil
}

func (a *app) stopCollectionMock(path string) {
	m := a.mocks[path]
	if m == nil {
		return
	}
	delete(a.mocks, path)
	m.close()
	a.inform(locales.MockServerStopped(path))
}

// stopCollectionMocks is an App Quit hook; it closes quietly.
func (a *app) stopCollectionMocks() {
	for path, m := range a.mocks {
		m.close()
		delete(a.mocks, path)
	}
}

// close stops and joins both listeners. Its flag is guarded so a Quit hook
// and an explicit stop cannot race into a second close of the same channels.
func (m *collectionMock) close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	if m.server != nil {
		_ = m.server.Close()
		<-m.httpDone
	}
	if m.grpcServer != nil {
		m.grpcServer.Stop()
		<-m.grpcDone
	}
}

// refresh reloads the examples of one collection from the app's current
// library and variables. A malformed set leaves the last good one answering.
func (m *collectionMock) refresh(examples []request.Example, grpcExamples []mockserver.GRPCExample, vars map[string]string) error {
	return mockserver.ReloadBoth(m.handler, m.grpcServer, examples, grpcExamples, vars)
}

// refreshCollectionMocks updates every running mock after a library write or
// a successful read from disk. Called on the UI thread; each mock copies what
// it needs before the next request is answered.
func (a *app) refreshCollectionMocks() {
	if a.requestsProblem != "" || len(a.mocks) == 0 {
		return
	}
	for path, m := range a.mocks {
		vars := a.inheritance(path).Values(a.activeVars())
		if err := m.refresh(a.collectionExamples(path), a.collectionGRPCExamples(path), vars); err != nil {
			a.fail(locales.MockReloadFailed(path, err.Error()), err)
		}
	}
}

// mockSummary is what the desktop control reports for a running mock.
type mockSummary struct {
	Collection  string  `json:"collection"`
	URL         string  `json:"url,omitempty"`
	GRPCURL     string  `json:"grpcUrl,omitempty"`
	RateLimit   int     `json:"rateLimit,omitempty"`
	ErrorRate   float64 `json:"errorRate,omitempty"`
	ErrorStatus int     `json:"errorStatus,omitempty"`
}

func (m *collectionMock) summary(path string) mockSummary {
	return mockSummary{Collection: path, URL: m.url, GRPCURL: m.grpcURL,
		RateLimit: m.options.RatePerSecond, ErrorRate: m.options.ErrorRate, ErrorStatus: m.options.ErrorStatus}
}

// controlMocks serves the desktop control's mock actions. Starting a listener
// changes shared state, so it requires confirm:true.
func (a *app) controlMocks(command control.Command) (any, error) {
	switch command.Action {
	case "mock.get":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
		summaries := make([]mockSummary, 0, len(a.mocks))
		for _, path := range slices.Sorted(maps.Keys(a.mocks)) {
			summaries = append(summaries, a.mocks[path].summary(path))
		}
		return map[string]any{"mocks": summaries}, nil
	case "mock.start":
		p, err := controlParams[struct {
			Collection  string  `json:"collection"`
			RateLimit   int     `json:"rateLimit"`
			ErrorRate   float64 `json:"errorRate"`
			ErrorStatus int     `json:"errorStatus"`
			ErrorBody   string  `json:"errorBody"`
			Confirm     bool    `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm || strings.TrimSpace(p.Collection) == "" {
			return nil, errors.New("collection and confirm:true are required")
		}
		if p.RateLimit < 0 || p.RateLimit > 100000 || p.ErrorRate < 0 || p.ErrorRate > 1 || p.ErrorStatus != 0 && (p.ErrorStatus < 400 || p.ErrorStatus > 599) {
			return nil, errors.New("rateLimit must be 0–100000, errorRate 0–1, and errorStatus 400–599 or 0")
		}
		m, err := a.startCollectionMockWithOptions(p.Collection, mockserver.Options{
			RatePerSecond: p.RateLimit, ErrorRate: p.ErrorRate, ErrorStatus: p.ErrorStatus, ErrorBody: p.ErrorBody,
		})
		if err != nil {
			return nil, err
		}
		return m.summary(p.Collection), nil
	case "mock.stop":
		p, err := controlParams[struct {
			Collection string `json:"collection"`
			Confirm    bool   `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm || a.mocks[p.Collection] == nil {
			return nil, errors.New("running collection and confirm:true are required")
		}
		a.stopCollectionMock(p.Collection)
		return map[string]bool{"stopped": true}, nil
	}
	return nil, errors.New("unknown mock action")
}
