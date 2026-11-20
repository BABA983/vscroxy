package request

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/baba983/vscroxy/internal/capture"
)

// ResponseRef is one {{response "Request" "/token"}} reference in an authored
// text. Name is how the user wrote the request; resolution settles it to the
// request's stable ID before looking up a response, so renaming the request
// cannot change which response is read.
type ResponseRef struct {
	Name    string
	Pointer string
	Start   int
	End     int
}

// ResponseRefs lists the response references in s, in order, with byte
// offsets of the whole {{...}} expression.
func ResponseRefs(s string) []ResponseRef {
	var refs []ResponseRef
	for at := 0; at < len(s); {
		start := strings.Index(s[at:], "{{")
		if start < 0 {
			break
		}
		start += at
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		if ref, ok := ParseResponseRef(s[start : end+2]); ok {
			ref.Start, ref.End = start, end+2
			refs = append(refs, ref)
		}
		at = end + 2
	}
	return refs
}

// ParseResponseRef reads the two quoted arguments of a response reference.
// The second is an RFC 6901 JSON pointer, or "header:<name>" for a response
// header. Both spellings are chosen explicitly so a bare word cannot be
// mistaken for a variable.
func ParseResponseRef(expr string) (ResponseRef, bool) {
	inside := strings.TrimSpace(expr)
	if !strings.HasPrefix(inside, "{{") || !strings.HasSuffix(inside, "}}") {
		return ResponseRef{}, false
	}
	inside = strings.TrimSpace(inside[2 : len(inside)-2])
	name, rest, ok := strings.Cut(inside, "response")
	if !ok || strings.TrimSpace(name) != "" {
		return ResponseRef{}, false
	}
	rest = strings.TrimSpace(rest)
	rawName, rest, ok := cutQuoted(rest)
	if !ok {
		return ResponseRef{}, false
	}
	rest = strings.TrimSpace(rest)
	rawPointer, rest, ok := cutQuoted(rest)
	if !ok || strings.TrimSpace(rest) != "" || strings.TrimSpace(rawName) == "" {
		return ResponseRef{}, false
	}
	if !strings.HasPrefix(rawPointer, "/") {
		if header, ok := strings.CutPrefix(rawPointer, "header:"); !ok || strings.TrimSpace(header) == "" {
			return ResponseRef{}, false
		}
	}
	return ResponseRef{Name: rawName, Pointer: rawPointer}, true
}

// cutQuoted reads a Go-quoted string, which permits escaped quotes in a
// request name and escaped slashes or tildes in a pointer.
func cutQuoted(s string) (string, string, bool) {
	if len(s) < 2 || s[0] != '"' {
		return "", "", false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			value, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", "", false
			}
			return value, s[i+1:], true
		}
	}
	return "", "", false
}

// ReplaceResponseRefs substitutes every response reference in s with what fn
// returns for it, leaving all other text, including other {{variables}},
// untouched.
func ReplaceResponseRefs(s string, fn func(ResponseRef) string) string {
	refs := ResponseRefs(s)
	if len(refs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, ref := range refs {
		b.WriteString(s[last:ref.Start])
		b.WriteString(fn(ref))
		last = ref.End
	}
	b.WriteString(s[last:])
	return b.String()
}

// rewriteResponseRefs applies fn to every authored text of a request the
// expansion, send and export paths read. The request is a copy.
func rewriteResponseRefs(r *Request, fn func(string) string) {
	fields := append([]*string{}, r.expandableFields()...)
	for _, field := range fields {
		*field = fn(*field)
	}
	for i := range r.PathVariables {
		r.PathVariables[i].Name = fn(r.PathVariables[i].Name)
		r.PathVariables[i].Value = fn(r.PathVariables[i].Value)
	}
	for i := range r.HeaderList {
		r.HeaderList[i].Name = fn(r.HeaderList[i].Name)
		r.HeaderList[i].Value = fn(r.HeaderList[i].Value)
	}
}

// RewriteResponseRefs replaces every response reference in the request, so
// the Postman exporter can write an equivalent pre-request script.
func (r Request) RewriteResponseRefs(fn func(string) string) Request {
	rewriteResponseRefs(&r, fn)
	return r
}

// ResponseSnapshot is a completed response kept for this session only. It
// holds the body in memory so a spilled capture file can disappear without
// taking the value with it.
type ResponseSnapshot struct {
	Status int
	Header http.Header
	Body   string
}

// Responses is the session's latest response for each saved request, keyed by
// its stable ID. A send records its own response before callers can discard
// the capture's spill files.
type Responses struct {
	mu       sync.Mutex
	latest   map[string]ResponseSnapshot
	tooLarge map[string]bool
}

// Add puts flow in the store under id, when it has a response. A body too
// large for a reference is remembered so the missing value can be reported
// without reading it again.
func (s *Responses) Add(id string, flow *capture.Flow) {
	if s == nil || id == "" || flow == nil {
		return
	}
	body, ok := responseRefBody(flow)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		s.latest, s.tooLarge = map[string]ResponseSnapshot{}, map[string]bool{}
	}
	s.latest[id] = ResponseSnapshot{Status: flow.Status, Header: flow.ResponseHeader.Clone(), Body: body}
	s.tooLarge[id] = !ok
}

// Lookup returns the latest response of id.
func (s *Responses) Lookup(id string) (ResponseSnapshot, bool, bool) {
	if s == nil {
		return ResponseSnapshot{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, ok := s.latest[id]
	return got, ok, s.tooLarge[id]
}

// maxResponseRefBytes bounds what one response can contribute to a request.
const maxResponseRefBytes = 4 << 20

func responseRefBody(flow *capture.Flow) (string, bool) {
	if flow.ResponseBodyError != "" {
		return "", false
	}
	if flow.ResponseBodyFile == "" {
		if len(flow.ResponseBody) > maxResponseRefBytes {
			return "", false
		}
		return flow.ResponseBody, true
	}
	info, err := os.Stat(flow.ResponseBodyFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxResponseRefBytes {
		return "", false
	}
	data, err := os.ReadFile(flow.ResponseBodyFile)
	if err != nil || len(data) > maxResponseRefBytes {
		return "", false
	}
	return string(data), true
}

// Value reads one reference's value from a snapshot. It never includes the
// value in an error: errors name the request and pointer only.
func Value(snapshot ResponseSnapshot, ref ResponseRef) (string, error) {
	if header, ok := strings.CutPrefix(ref.Pointer, "header:"); ok {
		if value := snapshot.Header.Get(strings.TrimSpace(header)); value != "" {
			return value, nil
		}
		return "", fmt.Errorf("response header %q is not in the latest response", strings.TrimSpace(header))
	}
	if len(snapshot.Body) == 0 {
		return "", errors.New("the latest response has no JSON body")
	}
	dec := jsontext.NewDecoder(strings.NewReader(snapshot.Body))
	doc, err := dec.ReadValue()
	if err != nil {
		return "", errors.New("the latest response is not valid JSON")
	}
	// ReadValue can lend bytes from the decoder's buffer; clone before asking
	// the decoder for the trailing token.
	doc = doc.Clone()
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return "", errors.New("the latest response is not valid JSON")
	}
	segments, err := pointerSegments(ref.Pointer)
	if err != nil {
		return "", err
	}
	at := doc
	for _, segment := range segments {
		at, err = jsonValueAt(at, segment)
		if err != nil {
			return "", err
		}
	}
	switch at.Kind() {
	case '"':
		var value string
		if err := json.Unmarshal(at, &value); err != nil {
			return "", errors.New("the response pointer is not a string")
		}
		return value, nil
	case 'n':
		return "", errors.New("response pointer is null")
	default:
		// Numbers, booleans, objects and arrays keep the JSON text exactly
		// as the response wrote it: float64 would lose large integer IDs.
		return strings.TrimSpace(string(at)), nil
	}
}

// jsonValueAt walks one RFC 6901 segment while keeping every child's raw
// JSON text, so no numeric value passes through a floating-point conversion.
func jsonValueAt(raw jsontext.Value, segment string) (jsontext.Value, error) {
	missing := errors.New("response pointer is not in the latest response")
	switch raw.Kind() {
	case '{':
		var object map[string]jsontext.Value
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, missing
		}
		value, ok := object[segment]
		if !ok {
			return nil, missing
		}
		return value, nil
	case '[':
		var array []jsontext.Value
		if err := json.Unmarshal(raw, &array); err != nil {
			return nil, missing
		}
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(array) || segment != strconv.Itoa(index) {
			return nil, missing
		}
		return array[index], nil
	default:
		return nil, missing
	}
}

// pointerSegments decodes RFC 6901 escaping. An invalid escape is an error,
// rather than a path that silently points at the wrong field.
func pointerSegments(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("a response JSON pointer must start with /")
	}
	raw := strings.Split(pointer[1:], "/")
	segments := make([]string, len(raw))
	for i, part := range raw {
		var b strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				b.WriteByte(part[j])
				continue
			}
			j++
			if j >= len(part) {
				return nil, errors.New("invalid response JSON pointer escape")
			}
			switch part[j] {
			case '0':
				b.WriteByte('~')
			case '1':
				b.WriteByte('/')
			default:
				return nil, errors.New("invalid response JSON pointer escape")
			}
		}
		segments[i] = b.String()
	}
	return segments, nil
}

// responseRefName is the pseudo-variable used once per distinct reference in
// a send. $ keeps it in the dynamic-variable namespace, which no authored
// environment can define, and the index keeps identical references together.
func responseRefName(index int) string { return "$vscroxy_response_" + strconv.Itoa(index) }

// resolveResponseRefs sends a referenced request when this session has no
// response for it, and rewrites the references to variables that ExpandVars
// already understands. Cycle state lives on the Context copy of each nested
// send, so a source request that reads its own response fails immediately.
func resolveResponseRefs(r Request, ctx context.Context, srv *capture.Server, in Context, vars map[string]string, jar http.CookieJar) (Request, map[string]string, error) {
	values := map[string]string{}
	byRef := map[string]string{}
	var exprs []string
	// The same exhaustive field list as withDynamic covers URL, headers,
	// body, auth and protocol documents, so expansion behaves the same
	// everywhere variables do.
	fields := append([]*string{}, r.expandableFields()...)
	for i := range r.PathVariables {
		fields = append(fields, &r.PathVariables[i].Name, &r.PathVariables[i].Value)
	}
	for i := range r.HeaderList {
		fields = append(fields, &r.HeaderList[i].Name, &r.HeaderList[i].Value)
	}
	for _, field := range fields {
		for _, ref := range ResponseRefs(*field) {
			exprs = append(exprs, (*field)[ref.Start:ref.End])
		}
	}
	if len(exprs) == 0 {
		return r, values, nil
	}
	if in.Resolve == nil || in.Responses == nil {
		return r, nil, errors.New("response references need a collection resolver and a session response store")
	}
	for _, expr := range exprs {
		if _, ok := byRef[responseRefKey(expr)]; ok {
			continue
		}
		ref, _ := ParseResponseRef(expr)
		value, err := responseRefValue(ref, r.Collection, ctx, srv, in, vars, jar)
		if err != nil {
			return r, nil, fmt.Errorf("response reference %s: %w", queriedResponseName(ref), err)
		}
		name := responseRefName(len(byRef))
		byRef[responseRefKey(expr)] = name
		values[name] = value
	}
	rewritten := r
	rewriteResponseRefs(&rewritten, func(s string) string {
		return ReplaceResponseRefs(s, func(ref ResponseRef) string {
			name, ok := byRef[ResponseRefKey(ref)]
			if !ok {
				return s // unreachable: all references were resolved above
			}
			return "{{" + name + "}}"
		})
	})
	return rewritten, values, nil
}

// ResponseRefKey is the map key for one reference, without its offsets.
func ResponseRefKey(ref ResponseRef) string { return ref.Name + "\x00" + ref.Pointer }

func responseRefKey(expr string) string {
	ref, _ := ParseResponseRef(expr)
	return ResponseRefKey(ref)
}

func queriedResponseName(ref ResponseRef) string {
	return fmt.Sprintf("%s %s", strconv.Quote(ref.Name), strconv.Quote(ref.Pointer))
}

func responseRefValue(ref ResponseRef, collection string, ctx context.Context, srv *capture.Server, in Context, vars map[string]string, jar http.CookieJar) (string, error) {
	callee, err := in.Resolve(ref.Name)
	if err != nil {
		return "", fmt.Errorf("resolve source request: %w", err)
	}
	if rootOf(callee.Request.Collection) != rootOf(collection) {
		return "", errors.New("source request is not in the same collection")
	}
	id := callee.Request.ID
	if id == "" {
		return "", errors.New("source request has no stable ID")
	}
	if slices.Contains(in.ResponseStack, id) {
		return "", fmt.Errorf("response reference cycle through %q", callee.Request.Name)
	}
	snapshot, found, tooLarge := in.Responses.Lookup(id)
	if tooLarge {
		return "", errors.New("the source response is too large to read")
	}
	if !found {
		child := in
		child.Folders = callee.Folders
		child.ResponseStack = append(slices.Clone(in.ResponseStack), id)
		out := callee.Request.Execute(ctx, srv, child, vars, jar)
		if out.Err != nil {
			return "", fmt.Errorf("send source request %q: %w", callee.Request.Name, out.Err)
		}
		in.Responses.Add(id, out.Response)
		snapshot, found, tooLarge = in.Responses.Lookup(id)
		if !found || tooLarge {
			return "", errors.New("the source request returned no readable response")
		}
	}
	return Value(snapshot, ref)
}

func rootOf(collection string) string {
	name, _, _ := strings.Cut(strings.Trim(collection, "/"), "/")
	return name
}
