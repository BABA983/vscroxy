package app

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

// collectionMock is a collection's mock server, listening on a loopback port
// until it is stopped or the app quits.
type collectionMock struct {
	server *http.Server
	url    string
}

// collectionExamples are the examples of the requests under path, in order.
func (a *app) collectionExamples(path string) []request.Example {
	var examples []request.Example
	for _, r := range a.requests {
		if r.Collection == path || strings.HasPrefix(r.Collection, path+"/") {
			examples = append(examples, r.Examples...)
		}
	}
	return examples
}

// startCollectionMock serves path's examples on a free loopback port. The
// server answers from the examples and variables it started with, so an edit
// to an example needs a restart to show.
func (a *app) startCollectionMock(path string) {
	if a.mocks[path] != nil {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		a.fail(locales.MockNamed(path), err)
		return
	}
	handler := mockserver.New(a.collectionExamples(path), a.inheritance(path).Values(a.activeVars()))
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	if a.mocks == nil {
		a.mocks = map[string]*collectionMock{}
	}
	url := "http://" + ln.Addr().String()
	a.mocks[path] = &collectionMock{server: srv, url: url}
	a.done(locales.MockServerRunning(path, url))
}

func (a *app) stopCollectionMock(path string) {
	m := a.mocks[path]
	if m == nil {
		return
	}
	delete(a.mocks, path)
	m.server.Close()
	a.inform(locales.MockServerStopped(path))
}

// stopCollectionMocks is an App Quit hook; it closes quietly.
func (a *app) stopCollectionMocks() {
	for path, m := range a.mocks {
		m.server.Close()
		delete(a.mocks, path)
	}
}
