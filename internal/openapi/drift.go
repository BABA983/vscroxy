package openapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
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
// contract's operation, such as "GET /pets/{id}".
type Drift struct {
	Endpoint string
	Problems []string
}

// Checker matches exchanges against one parsed contract and validates them
// with kin-openapi's request and response filters.
type Checker struct {
	router routers.Router
}

// NewChecker builds a checker for doc.
func NewChecker(doc *openapi3.T) (*Checker, error) {
	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	return &Checker{router: router}, nil
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
// in the contract serves the request, which is a drift finding only for a
// caller that expects full coverage.
func (c *Checker) Check(ex Exchange) (drift Drift, matched bool) {
	req, err := http.NewRequest(ex.Method, ex.URL, bytes.NewReader(ex.Body))
	if err != nil {
		return Drift{Problems: []string{err.Error()}}, false
	}
	if ex.Header != nil {
		req.Header = ex.Header.Clone()
	}
	route, pathParams, err := c.router.FindRoute(req)
	if err != nil {
		return Drift{}, false
	}
	drift.Endpoint = route.Method + " " + route.Path
	options := &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		MultiError:            true,
		IncludeResponseStatus: true,
	}
	input := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: pathParams, Route: route, Options: options,
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
