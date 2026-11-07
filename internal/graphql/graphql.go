// Package graphql extracts selected operations from HTTP requests and formats
// executable GraphQL documents without a schema or any UI dependencies.
package graphql

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/lexer"
	"github.com/vektah/gqlparser/v2/parser"
)

const (
	maxSource = 1 << 20
	maxOutput = 2 << 20
	maxDepth  = 64
	maxTokens = 16384
	maxBatch  = 32
)

// Operation is the operation selected for execution, not every operation in a
// document. Type is query, mutation, or subscription; Name is empty for an
// anonymous operation.
type Operation struct {
	Name string
	Type string
}

// RequestOperations returns selected operations in batch order. Ordinary HTTP
// requests and persisted-query-only requests return nil, nil. A batch containing
// a persisted query omits that entry: its operation cannot be verified locally.
// Any malformed entry fails the whole batch, with no partial result.
//
// GET uses URL parameters only; POST uses application/graphql or a JSON envelope
// (application/json or application/*+json). JSON without GraphQL envelope fields
// is ordinary HTTP. Malformed JSON in a JSON POST is an error. Unknown envelope
// members are ignored, but query/operationName/variables/extensions have strict,
// case-sensitive types and duplicate members are rejected. Empty operationName
// strings are invalid; omit it or use null to select a sole operation.
//
// Source bodies and URLs are limited to 1 MiB, delimiter nesting to 64, JSON and
// GraphQL token counts to 16384 each (GraphQL shared across a batch), and batches
// to 32 entries. Validation is syntactic plus operation-name uniqueness and lone
// anonymous operation checks, not schema or full executable-document validation.
func RequestOperations(method, rawURL, contentType, body string) ([]Operation, error) {
	remaining := maxTokens
	switch method {
	case http.MethodGet:
		if err := checkSource(rawURL); err != nil {
			return nil, err
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, errors.New("graphql: invalid request URL")
		}
		params, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return nil, errors.New("graphql: invalid URL parameters")
		}
		if !params.Has("query") && !params.Has("operationName") {
			return nil, nil
		}
		var envelope = make(map[string]jsontext.Value)
		for _, key := range []string{"query", "operationName", "variables", "extensions"} {
			values, ok := params[key]
			if !ok {
				continue
			}
			if len(values) != 1 {
				return nil, fmt.Errorf("graphql: repeated %s parameter", key)
			}
			if err := checkSource(values[0]); err != nil {
				return nil, err
			}
			if key == "variables" || key == "extensions" {
				if err := checkJSON(values[0]); err != nil {
					return nil, err
				}
				envelope[key] = jsontext.Value(values[0])
			} else {
				encoded, err := json.Marshal(values[0])
				if err != nil {
					return nil, err
				}
				envelope[key] = encoded
			}
		}
		ops, _, err := envelopeOperations(envelope, &remaining)
		return ops, err
	case http.MethodPost:
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return nil, nil
		}
		if mediaType == "application/graphql" {
			return selectOperation(body, "", &remaining)
		}
		if mediaType != "application/json" && !(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")) {
			return nil, nil
		}
		return jsonOperations(body, &remaining)
	default:
		return nil, nil
	}
}

func jsonOperations(body string, remaining *int) ([]Operation, error) {
	if err := checkSource(body); err != nil {
		return nil, err
	}
	if err := checkJSON(body); err != nil {
		return nil, err
	}
	raw := jsontext.Value(body)
	if raw.Kind() == '{' {
		var envelope map[string]jsontext.Value
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		ops, _, err := envelopeOperations(envelope, remaining)
		return ops, err
	}
	if raw.Kind() != '[' {
		return nil, nil
	}
	// Read one entry at a time, bounding the batch before allocating its slice.
	d := jsontext.NewDecoder(strings.NewReader(body))
	_, _ = d.ReadToken() // checkJSON already checked the complete input.
	var envelopes []map[string]jsontext.Value
	for d.PeekKind() != ']' {
		if len(envelopes) == maxBatch {
			return nil, errors.New("graphql: batch exceeds 32 entries")
		}
		entry, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		var envelope map[string]jsontext.Value
		if entry.Kind() == '{' {
			if err := json.Unmarshal(entry, &envelope); err != nil {
				return nil, err
			}
		}
		envelopes = append(envelopes, envelope)
	}
	var ops []Operation
	ordinary, recognized := false, false
	for i, envelope := range envelopes {
		selected, ok, err := envelopeOperations(envelope, remaining)
		if err != nil {
			return nil, fmt.Errorf("graphql: batch entry %d: %w", i+1, err)
		}
		recognized = recognized || ok
		ordinary = ordinary || !ok
		ops = append(ops, selected...)
	}
	if recognized && ordinary {
		return nil, errors.New("graphql: batch mixes GraphQL and ordinary entries")
	}
	return ops, nil
}

func envelopeOperations(envelope map[string]jsontext.Value, remaining *int) ([]Operation, bool, error) {
	query, hasQuery := envelope["query"]
	name, hasName := envelope["operationName"]
	var extensions map[string]jsontext.Value
	if raw := envelope["extensions"]; raw.Kind() == '{' {
		if err := json.Unmarshal(raw, &extensions); err != nil {
			return nil, true, err
		}
	}
	persisted, hasPersisted := extensions["persistedQuery"]
	if !hasQuery && !hasName && !hasPersisted {
		return nil, false, nil
	}
	for _, key := range []string{"variables", "extensions"} {
		if raw, ok := envelope[key]; ok && raw.Kind() != '{' && raw.Kind() != 'n' {
			return nil, true, fmt.Errorf("graphql: %s must be an object or null", key)
		}
	}
	if hasPersisted && persisted.Kind() != '{' {
		return nil, true, errors.New("graphql: persistedQuery must be an object")
	}
	var selector string
	if hasName && name.Kind() != 'n' {
		if name.Kind() != '"' {
			return nil, true, errors.New("graphql: operationName must be a string or null")
		}
		if err := json.Unmarshal(name, &selector); err != nil {
			return nil, true, err
		}
		if !validName(selector) {
			return nil, true, errors.New("graphql: invalid operationName")
		}
	}
	if !hasQuery || query.Kind() == 'n' {
		if hasPersisted {
			return nil, true, nil
		}
		return nil, true, errors.New("graphql: missing query")
	}
	if query.Kind() != '"' {
		return nil, true, errors.New("graphql: query must be a string")
	}
	var source string
	if err := json.Unmarshal(query, &source); err != nil {
		return nil, true, err
	}
	ops, err := selectOperation(source, selector, remaining)
	return ops, true, err
}

// Operations returns an executable document's operations in document order.
// It is bounded by the source size rather than the formatter's smaller token
// budget, so the operation selector still works for a large document.
func Operations(source string) ([]Operation, error) {
	remaining := maxSource
	doc, err := parse(source, &remaining)
	if err != nil {
		return nil, err
	}
	ops := make([]Operation, 0, len(doc.Operations))
	for _, op := range doc.Operations {
		ops = append(ops, Operation{Name: op.Name, Type: string(op.Operation)})
	}
	return ops, nil
}

// SelectOperation returns the operationName an HTTP envelope must carry.
//
// A malformed document is sent as authored, because API clients need to test
// the server's own error handling; a saved name is still included so the
// server can report that too. A parseable document with one operation selects
// it when no name is saved. A parseable document with several operations
// requires a choice before sending, as the GraphQL specification requires.
// A saved name is sent even when it no longer matches an operation: the
// server's error is more useful than silently replacing the user's choice.
// A parseable document with no operations is also sent for the server to
// explain; only several operations require a local choice.
func SelectOperation(source, requested string) (string, error) {
	// A send is bounded by the source size, not the editor's smaller token
	// budget: a large valid document must still be sendable.
	remaining := maxSource
	doc, err := parse(source, &remaining)
	if err != nil {
		return requested, nil
	}
	if requested != "" {
		return requested, nil
	}
	if len(doc.Operations) == 1 {
		return doc.Operations[0].Name, nil
	}
	if len(doc.Operations) > 1 {
		return "", fmt.Errorf("graphql: the document defines %d operations; choose one", len(doc.Operations))
	}
	// A parseable document with no operations is not executable, but it is
	// still the caller's document: send it and let the server explain.
	return "", nil
}

func validName(name string) bool {
	// The library lexer defines names; do not infer them from document text.
	if name == "" || len(name) > maxSource || !utf8.ValidString(name) {
		return false
	}
	l := lexer.New(&ast.Source{Input: name})
	t, err := l.ReadToken()
	if err != nil || t.Kind != lexer.Name || t.Value != name {
		return false
	}
	t, err = l.ReadToken()
	return err == nil && t.Kind == lexer.EOF
}

func selectOperation(source, selector string, remaining *int) ([]Operation, error) {
	doc, err := parse(source, remaining)
	if err != nil {
		return nil, err
	}
	if selector == "" && len(doc.Operations) != 1 {
		return nil, errors.New("graphql: multiple operations require operationName")
	}
	for _, op := range doc.Operations {
		if selector == "" || op.Name == selector {
			return []Operation{{Name: op.Name, Type: string(op.Operation)}}, nil
		}
	}
	return nil, errors.New("graphql: operationName does not select an operation")
}

func checkSource(source string) error {
	if len(source) > maxSource {
		return errors.New("graphql: source exceeds 1 MiB")
	}
	if !utf8.ValidString(source) {
		return errors.New("graphql: source is not valid UTF-8")
	}
	return nil
}

func checkJSON(source string) error {
	// Streaming tokens enforce bounds before recursive JSON unmarshalling. The
	// decoder also rejects duplicate members, malformed UTF-8 and surrogate escapes.
	d := jsontext.NewDecoder(strings.NewReader(source))
	for count := 0; ; count++ {
		_, err := d.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("graphql: invalid JSON envelope")
		}
		if count == maxTokens || d.StackDepth() > maxDepth {
			return errors.New("graphql: JSON envelope exceeds token or depth limit")
		}
	}
	// A token stream can contain several top-level values (or none).
	if !jsontext.Value(source).IsValid() {
		return errors.New("graphql: expected one JSON value")
	}
	return nil
}

func parse(source string, remaining *int) (*ast.QueryDocument, error) {
	if err := checkSource(source); err != nil {
		return nil, err
	}
	src := &ast.Source{Name: "query", Input: source}
	l := lexer.New(src)
	// Token positions are rune offsets. Keep the bounded source available for
	// stricter string checks: the upstream lexer replaces lone surrogate escapes.
	runes := []rune(source)
	var stack [maxDepth]lexer.Type
	depth, count := 0, 0
	previous := lexer.Token{}
	for {
		t, err := l.ReadToken()
		if err != nil {
			return nil, errors.New("graphql: invalid GraphQL token")
		}
		if t.Kind == lexer.EOF {
			break
		}
		count++
		if count > *remaining {
			return nil, errors.New("graphql: document exceeds token limit")
		}
		if t.Kind == lexer.String && !validString(string(runes[t.Pos.Start:t.Pos.End])) {
			return nil, errors.New("graphql: invalid Unicode string escape")
		}
		if t.Kind == lexer.Name && previous.Pos.End == t.Pos.Start && (previous.Kind == lexer.Int || previous.Kind == lexer.Float) {
			return nil, errors.New("graphql: number cannot be followed by a name")
		}
		previous = t
		switch t.Kind {
		case lexer.ParenL, lexer.BracketL, lexer.BraceL:
			if depth == maxDepth {
				return nil, errors.New("graphql: document exceeds depth limit")
			}
			stack[depth] = t.Kind
			depth++
		case lexer.ParenR, lexer.BracketR, lexer.BraceR:
			if depth == 0 || stack[depth-1] != t.Kind-1 {
				return nil, errors.New("graphql: unbalanced delimiters")
			}
			depth--
		}
	}
	if depth != 0 {
		return nil, errors.New("graphql: unbalanced delimiters")
	}
	*remaining -= count
	// Include EOF in the library's budget. Its built-in depth cap is 1000, so
	// the lexer pass above is essential to enforce our smaller cap before recursion.
	doc, err := parser.ParseQueryWithTokenLimit(src, count+1)
	if err != nil {
		return nil, errors.New("graphql: invalid executable document")
	}
	if len(doc.Operations) == 0 {
		return nil, errors.New("graphql: document has no operations")
	}
	seen := make(map[string]bool)
	for _, op := range doc.Operations {
		if seen[op.Name] || (op.Name == "" && len(doc.Operations) != 1) {
			return nil, errors.New("graphql: duplicate or non-lone anonymous operation")
		}
		seen[op.Name] = true
	}
	return doc, nil
}

func validString(raw string) bool {
	if jsontext.Value(raw).IsValid() {
		return true
	}
	// GraphQL's ordinary strings share JSON's escapes, with two additions: raw
	// tabs and braced Unicode escapes. The lexer already validated braced values;
	// substitute a valid scalar only for JSON's surrogate-pair validation. Copy
	// other escapes intact, including escaped backslashes, without interpreting
	// GraphQL grammar ourselves.
	var normalized strings.Builder
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\t':
			normalized.WriteString(`\t`)
		case '\\':
			if strings.HasPrefix(raw[i:], `\u{`) {
				end := strings.IndexByte(raw[i:], '}')
				if end < 0 {
					return false
				}
				normalized.WriteString(`\uFFFD`)
				i += end
			} else if i+1 < len(raw) {
				normalized.WriteString(raw[i : i+2])
				i++
			} else {
				return false
			}
		default:
			normalized.WriteByte(raw[i])
		}
	}
	return jsontext.Value(normalized.String()).IsValid()
}

// Format formats a complete executable document, retaining comments supported by
// the parser. It does not expand request-template variables. Block strings may
// become quoted strings, preserving their decoded values. No schema is needed.
// A format that would alter the parsed AST is rejected rather than returned;
// notably gqlparser currently omits variable-definition directives. Output is
// bounded to 2 MiB and must also fit the 1 MiB source budget so it can be formatted
// again. Every successful result is semantically checked and idempotent.
func Format(query string) (string, error) {
	remaining := maxTokens
	doc, err := parse(query, &remaining)
	if err != nil {
		return "", err
	}
	formatted, err := printDocument(doc)
	if err != nil {
		return "", err
	}
	remaining = maxTokens
	reparsed, err := parse(formatted, &remaining)
	if err != nil {
		return "", errors.New("graphql: formatted document exceeds source limits or is invalid")
	}
	before, err := semanticJSON(doc)
	if err != nil {
		return "", err
	}
	after, err := semanticJSON(reparsed)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(before, after) {
		return "", errors.New("graphql: formatter cannot preserve this document's semantics")
	}
	again, err := printDocument(reparsed)
	if err != nil || formatted != again {
		return "", errors.New("graphql: formatter cannot produce a stable result")
	}
	return formatted, nil
}

func semanticJSON(v any) ([]byte, error) {
	// Positions and comments are presentation, and block/quoted strings with
	// equal decoded values are equivalent. Everything else must survive printing.
	type value ast.Value
	return json.Marshal(v, json.WithMarshalers(json.JoinMarshalers(
		json.MarshalFunc(func(*ast.CommentGroup) ([]byte, error) { return []byte("null"), nil }),
		json.MarshalFunc(func(v *ast.Value) ([]byte, error) {
			copy := value(*v)
			if copy.Kind == ast.BlockValue {
				copy.Kind = ast.StringValue
			}
			return semanticJSON(copy)
		}),
	)))
}

type boundedOutput struct {
	strings.Builder
	exceeded bool
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxOutput-w.Len() {
		w.exceeded = true
		return 0, errors.New("graphql: formatted output exceeds 2 MiB")
	}
	return w.Builder.Write(p)
}

func printDocument(doc *ast.QueryDocument) (string, error) {
	var out boundedOutput
	formatter.NewFormatter(&out, formatter.WithIndent("  "), formatter.WithComments()).FormatQueryDocument(doc)
	// gqlparser's formatter ignores Writer errors, so retain the failure here.
	if out.exceeded {
		return "", errors.New("graphql: formatted output exceeds 2 MiB")
	}
	return out.String(), nil
}
