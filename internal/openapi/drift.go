package openapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// Exchange is one request and its response as they crossed the wire. Status
// zero checks the request only, for a send that got no response.
type Exchange struct {
	Method         string
	URL            string
	Header         http.Header
	Body           []byte
	Status         int
	ResponseHeader http.Header
	ResponseBody   []byte
}

// Drift is what one exchange did against one contract. Endpoint is the
// contract's operation, such as "GET /pets/{id}". MatchedBase and Servers say
// which server base path matched, so a caller can explain that the contract
// server's scheme and host were ignored.
type Drift struct {
	Endpoint    string
	MatchedBase string
	Servers     []string
	Problems    []string
}

// Checker matches exchanges against one parsed contract and validates them
// with kin-openapi's request and response filters.
type Checker struct {
	router  routers.Router
	bases   []string // server base paths, "/" when the document has none
	servers []string // original server URLs, for the note
}

// NewChecker builds a checker for doc. Routing is by method and path under a
// server's base path: the contract's scheme and host are not required to match,
// so a local or staging base URL can be checked against a production spec.
func NewChecker(doc *openapi3.T) (*Checker, error) {
	c := &Checker{bases: []string{"/"}}
	if len(doc.Servers) > 0 {
		c.bases, c.servers = nil, nil
		for _, server := range doc.Servers {
			if server == nil {
				continue
			}
			base, err := server.BasePath()
			if err != nil || strings.TrimSpace(base) == "" {
				base = "/"
			}
			base = strings.TrimSuffix(base, "/")
			if base == "" {
				base = "/"
			}
			if !slices.Contains(c.bases, base) {
				c.bases = append(c.bases, base)
			}
			c.servers = append(c.servers, server.URL)
		}
		if len(c.bases) == 0 {
			c.bases = []string{"/"}
		}
	}
	// A router with one neutral server matches the operation path alone; the
	// request is rewritten to that server after its own server base is
	// stripped. The original document's servers are only used for the note.
	routing := *doc
	routing.Servers = openapi3.Servers{{URL: "https://contract.invalid"}}
	router, err := legacy.NewRouter(&routing)
	if err != nil {
		return nil, err
	}
	c.router = router
	return c, nil
}

// LoadChecker reads path, its local $ref files included, and builds a checker.
func LoadChecker(path string) (*Checker, error) {
	spec, err := Load(path)
	if err != nil {
		return nil, err
	}
	return NewChecker(spec.Doc)
}

// Check validates ex against the contract. matched is false when no operation
// in the contract serves the request's method and path under any server base.
func (c *Checker) Check(ex Exchange) (drift Drift, matched bool) {
	parsed, err := url.Parse(ex.URL)
	if err != nil {
		return Drift{Problems: []string{err.Error()}}, false
	}
	var route *routers.Route
	var pathParams map[string]string
	baseUsed := "/"
	for _, base := range c.bases {
		relative, ok := relativeToBase(parsed.Path, base)
		if !ok {
			continue
		}
		candidate := &url.URL{Scheme: "https", Host: "contract.invalid", Path: relative, RawQuery: parsed.RawQuery}
		req, err := http.NewRequest(ex.Method, candidate.String(), bytes.NewReader(ex.Body))
		if err != nil {
			return Drift{Problems: []string{err.Error()}}, false
		}
		if ex.Header != nil {
			req.Header = ex.Header.Clone()
		}
		found, params, err := c.router.FindRoute(req)
		if err != nil {
			continue
		}
		route, pathParams, baseUsed = found, params, base
		break
	}
	if route == nil {
		return Drift{}, false
	}
	drift.Endpoint = route.Method + " " + route.Path
	drift.MatchedBase = baseUsed
	drift.Servers = slices.Clone(c.servers)
	options := &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		MultiError:            true,
		IncludeResponseStatus: true,
	}
	input := &openapi3filter.RequestValidationInput{
		Request: requestFor(route, ex, pathParams), PathParams: pathParams, Route: route, Options: options,
	}
	if err := openapi3filter.ValidateRequest(context.Background(), input); err != nil {
		drift.Problems = append(drift.Problems, validationProblems(err)...)
	}
	if ex.Status == 0 {
		return drift, true
	}
	response := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input, Status: ex.Status, Header: ex.ResponseHeader, Options: options,
	}
	response.SetBodyBytes(ex.ResponseBody)
	if err := openapi3filter.ValidateResponse(context.Background(), response); err != nil {
		drift.Problems = append(drift.Problems, validationProblems(err)...)
	}
	return drift, true
}

// requestFor rebuilds the validation request on the neutral server so the
// route's operation and the request's parameters agree.
func requestFor(route *routers.Route, ex Exchange, pathParams map[string]string) *http.Request {
	relative := route.Path
	for name, value := range pathParams {
		relative = strings.ReplaceAll(relative, "{"+name+"}", value)
	}
	u := &url.URL{Scheme: "https", Host: "contract.invalid", Path: relative}
	if parsed, err := url.Parse(ex.URL); err == nil {
		u.RawQuery = parsed.RawQuery
	}
	req, _ := http.NewRequest(ex.Method, u.String(), bytes.NewReader(ex.Body))
	if ex.Header != nil {
		req.Header = ex.Header.Clone()
	}
	return req
}

// relativeToBase strips a server's base path from a request path. A path
// outside the base does not match that server.
func relativeToBase(path, base string) (string, bool) {
	if path == "" {
		path = "/"
	}
	if base == "" || base == "/" {
		return path, true
	}
	base = strings.TrimSuffix(base, "/")
	if path == base {
		return "/", true
	}
	if strings.HasPrefix(path, base+"/") {
		return path[len(base):], true
	}
	return "", false
}

// validationProblems flattens a multi-error and drops repeated lines. A body
// parser's error can repeat the same reason once per schema branch.
func validationProblems(err error) []string {
	var list []error
	var multi openapi3.MultiError
	if errors.As(err, &multi) {
		list = multi
	} else {
		list = []error{err}
	}
	var out []string
	for _, item := range list {
		for _, line := range strings.Split(item.Error(), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !slices.Contains(out, line) {
				out = append(out, line)
			}
		}
	}
	if len(out) == 0 {
		out = []string{fmt.Sprint(err)}
	}
	return out
}
