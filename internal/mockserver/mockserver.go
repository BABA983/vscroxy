// Package mockserver answers HTTP requests from a collection's examples, as
// Postman's mock servers do. It is an http.Handler: the caller owns the
// listener, and requests never go through the proxy, so a front end or a CI
// job only changes its base URL.
package mockserver

import (
	"cmp"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// SelectHeader names the example a request wants when it is not the route's
// first one, as the proxy's Map Local mocks read it.
const SelectHeader = "X-Mock-Response-Name"

// Server answers from examples. Examples are grouped by method and path, in
// the order given, and the first example of a route answers unless the request
// names another with SelectHeader.
type Server struct {
	vars   map[string]string
	routes []route
}

type route struct {
	method   string
	path     *regexp.Regexp
	examples []request.Example
}

// New serves each example at the method and URL it was saved from, with
// {{variables}} in the URL and body filled from vars. A path segment written
// :name or {{name}} matches any one segment; the query is ignored. An example
// with no method is a GET.
func New(examples []request.Example, vars map[string]string) *Server {
	s := &Server{vars: vars}
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
// matches the method and path.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, rt := range s.routes {
		if rt.method == r.Method && rt.path.MatchString(r.URL.Path) {
			s.answer(w, r, rt.examples)
			return
		}
	}
	http.Error(w, "no example for "+r.Method+" "+r.URL.Path, http.StatusNotFound)
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request, examples []request.Example) {
	e := examples[0]
	if want := r.Header.Get(SelectHeader); want != "" {
		if i := slices.IndexFunc(examples, func(x request.Example) bool { return x.Name == want }); i >= 0 {
			e = examples[i]
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
