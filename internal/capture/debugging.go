package capture

import (
	"context"
	"net/http"
	"slices"
	"sync"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Debug controls are session-only. Each exchange snapshots the cache switch,
// so toggling it while a response is in flight cannot change that response.
func (s *Server) SetNoCaching(on bool) { s.noCaching.Store(on) }
func (s *Server) NoCaching() bool      { return s.noCaching.Load() }

type debugKey struct{}
type exchangeDebug struct {
	messageMu sync.Mutex // serialize duplex scripts sharing connection locals
	noCache   bool
	actions   []string
	// hidden keeps the exchange out of the table; drop closes the client's
	// connection once it is published. Block rules set them.
	hidden, drop bool
	// scripts are the Script rules whose onResponse waits for the response;
	// locals are the exchange's pm.variables.
	scripts []Rule
	locals  map[string]string
}

func (s *Server) startDebug(f *proxy.Flow) {
	if raw := f.Request.Raw(); raw != nil {
		d := &exchangeDebug{noCache: s.NoCaching()}
		if d.noCache {
			d.actions = append(d.actions, "No Caching")
		}
		*raw = *raw.WithContext(context.WithValue(raw.Context(), debugKey{}, d))
	}
}

func debugOf(f *proxy.Flow) *exchangeDebug {
	if f.Request != nil && f.Request.Raw() != nil {
		d, _ := f.Request.Raw().Context().Value(debugKey{}).(*exchangeDebug)
		return d
	}
	return nil
}

func recordAction(f *proxy.Flow, action string) {
	if d := debugOf(f); d != nil {
		d.actions = append(d.actions, action)
	}
}

func debugActions(f *proxy.Flow) []string {
	if d := debugOf(f); d != nil {
		return slices.Clone(d.actions)
	}
	return nil
}

func noCacheRequest(f *proxy.Flow) {
	if d := debugOf(f); d != nil && d.noCache {
		h := f.Request.Header
		h.Del("If-Modified-Since")
		h.Del("If-None-Match")
		h.Set("Pragma", "no-cache")
		h.Set("Cache-Control", "no-cache")
	}
}

func noCacheResponse(f *proxy.Flow) {
	if d := debugOf(f); d != nil && d.noCache && f.Response != nil {
		if f.Response.Header == nil {
			f.Response.Header = make(http.Header)
		}
		h := f.Response.Header
		for _, name := range []string{"Expires", "ETag", "Last-Modified"} {
			h.Del(name)
		}
		h.Set("Expires", "0")
		h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}
}
