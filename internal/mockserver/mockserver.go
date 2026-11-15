// Package mockserver answers HTTP requests from a collection's examples, as
// Postman's mock servers do. It is an http.Handler: the caller owns the
// listener, and requests never go through the proxy, so a front end or a CI
// job only changes its base URL.
package mockserver

import (
	"cmp"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/request"
)

// Headers that pick an example of a route, as Postman's mock servers read them.
const (
	// SelectHeader names the example to answer with.
	SelectHeader = "X-Mock-Response-Name"
	// CodeHeader picks the first example with this status code.
	CodeHeader = "X-Mock-Response-Code"
	// MatchBodyHeader, set to true, answers only from examples whose request
	// body equals the request's body.
	MatchBodyHeader = "x-mock-match-request-body"
)

// maxMatchBody bounds how much of a request body is read to match examples.
const maxMatchBody = 1 << 20

// Options are a mock server's behaviors beyond answering from examples.
type Options struct {
	// RatePerSecond limits how many requests the server starts each second;
	// zero means no limit. Burst is the bucket size (default RatePerSecond).
	RatePerSecond int
	Burst         int
	// ErrorRate injects an error for this share of requests, from 0 to 1.
	// ErrorStatus and ErrorBody are what the injected response says.
	ErrorRate   float64
	ErrorStatus int
	ErrorBody   string
}

// Server answers from examples. Examples are grouped by method and path, in
// the order given. Within a route the example is chosen by its name, then by
// its status code, then the first one; MatchBodyHeader limits the choice to
// the examples whose request body matches first.
type Server struct {
	vars   map[string]string
	routes []route
	opts   Options

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

type route struct {
	method   string
	path     *regexp.Regexp
	examples []request.Example
}

// New serves each example at the method and URL it was saved from, with
// {{variables}} in the URL, the request body and the response filled from
// vars. A path segment written :name or {{name}} matches any one segment; the
// query is ignored. An example with no method is a GET.
func New(examples []request.Example, vars map[string]string) *Server {
	return NewWithOptions(examples, vars, Options{})
}

// NewWithOptions serves the examples with the given behaviors. An error rate
// outside 0–1 is clamped, and a missing error status/body gets a 503 JSON body.
func NewWithOptions(examples []request.Example, vars map[string]string, opts Options) *Server {
	opts.ErrorRate = min(max(opts.ErrorRate, 0), 1)
	if opts.ErrorStatus == 0 {
		opts.ErrorStatus = http.StatusServiceUnavailable
	}
	if opts.ErrorStatus < 400 || opts.ErrorStatus > 599 {
		opts.ErrorStatus = http.StatusServiceUnavailable
	}
	if strings.TrimSpace(opts.ErrorBody) == "" {
		opts.ErrorBody = `{"error":"injected mock error"}`
	}
	if opts.Burst <= 0 {
		opts.Burst = max(1, opts.RatePerSecond)
	}
	s := &Server{vars: vars, opts: opts}
	index := map[string]int{}
	for _, e := range examples {
		method := strings.ToUpper(cmp.Or(e.Request.Method, "GET"))
		p := routePath(e.Request.URL, vars)
		key := method + " " + p
		i, ok := index[key]
		if !ok {
			i = len(s.routes)
			index[key] = i
			s.routes = append(s.routes, route{method: method, path: pathPattern(p)})
		}
		s.routes[i].examples = append(s.routes[i].examples, e)
	}
	return s
}

// ServeHTTP answers with the matching route's example, or 404 when none
// matches the method and path. A rate limit answers 429 before any example;
// error injection answers the configured status instead of the example.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.allow() {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":"mock rate limit exceeded"}`)
		return
	}
	if s.opts.ErrorRate > 0 && rand.Float64() < s.opts.ErrorRate {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.opts.ErrorStatus)
		io.WriteString(w, s.opts.ErrorBody)
		return
	}
	for _, rt := range s.routes {
		if rt.method == r.Method && rt.path.MatchString(r.URL.Path) {
			s.answer(w, r, rt.examples)
			return
		}
	}
	http.Error(w, "no example for "+r.Method+" "+r.URL.Path, http.StatusNotFound)
}

// allow takes one token, refilling the bucket for the elapsed time.
func (s *Server) allow() bool {
	if s.opts.RatePerSecond <= 0 {
		return true
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.IsZero() {
		s.tokens, s.last = float64(s.opts.Burst), now
	}
	s.tokens = min(float64(s.opts.Burst), s.tokens+now.Sub(s.last).Seconds()*float64(s.opts.RatePerSecond))
	s.last = now
	if s.tokens < 1 {
		return false
	}
	s.tokens--
	return true
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request, examples []request.Example) {
	candidates := examples
	if strings.EqualFold(r.Header.Get(MatchBodyHeader), "true") {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxMatchBody))
		candidates = slices.DeleteFunc(slices.Clone(examples), func(e request.Example) bool {
			return strings.TrimSpace(request.ExpandVars(e.Request.Body, s.vars)) != strings.TrimSpace(string(body))
		})
		if len(candidates) == 0 {
			http.Error(w, "no example has this request body", http.StatusNotFound)
			return
		}
	}
	e := pick(r, candidates)
	if wait := min(time.Duration(e.DelayMS)*time.Millisecond, time.Minute); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			return
		}
	}
	for name, values := range request.ParseHeaderBlock(e.Headers) {
		for _, v := range values {
			w.Header().Add(name, request.ExpandVars(v, s.vars))
		}
	}
	w.WriteHeader(cmp.Or(e.Status, http.StatusOK))
	io.WriteString(w, request.ExpandVars(e.Body, s.vars))
}

// pick is the example a request asks for among candidates, which are not empty.
func pick(r *http.Request, candidates []request.Example) request.Example {
	if want := r.Header.Get(SelectHeader); want != "" {
		if i := slices.IndexFunc(candidates, func(x request.Example) bool { return x.Name == want }); i >= 0 {
			return candidates[i]
		}
	}
	if code, err := strconv.Atoi(r.Header.Get(CodeHeader)); err == nil {
		if i := slices.IndexFunc(candidates, func(x request.Example) bool { return x.Status == code }); i >= 0 {
			return candidates[i]
		}
	}
	return candidates[0]
}

// routePath is the path of an example's URL with its variables filled in. The
// scheme, host, query and fragment are not part of the route.
func routePath(raw string, vars map[string]string) string {
	s, _, _ := strings.Cut(request.ExpandVars(raw, vars), "?")
	s, _, _ = strings.Cut(s, "#")
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	if i := strings.Index(s, "/"); i >= 0 {
		return s[i:]
	}
	return "/"
}

// pathPattern matches a route path segment by segment: a segment written :name
// or still holding {{name}} matches any one segment, and the rest match exactly.
func pathPattern(path string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i, seg := range strings.Split(path, "/") {
		if i > 0 {
			b.WriteString("/")
		}
		if (strings.HasPrefix(seg, ":") && len(seg) > 1) || strings.Contains(seg, "{{") {
			b.WriteString("[^/]+")
		} else {
			b.WriteString(regexp.QuoteMeta(seg))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
