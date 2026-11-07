// Package imports converts third-party API client exports into the app's
// requests, folders and environments, and reads other proxies' capture
// sessions as captured exchanges. It knows nothing about the window or MyGo,
// and it never sends a request or reads a referenced file.
//
// Every reader reports what it could not bring over in Warnings: an import
// may lose a feature, but it must not lose it silently.
package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// Format is a third-party export this package reads.
type Format int

const (
	Unknown Format = iota
	InsomniaV4
	InsomniaV5
	HoppscotchCollection
	HoppscotchEnvironment
	Swagger2
)

// Collection is one imported collection: its requests, the settings of the
// collection and its folders, and what could not be brought over.
type Collection struct {
	Name     string
	Requests []request.Request
	Folders  []request.Folder
	Warnings []string
}

// Environment is one imported environment. Values are the Initial values the
// export held. Current holds local-only values such as secrets and a source's
// current values, so they are never exported. Credentials marks the Current
// names that came from a secret, so Persist current leaves them local.
// Warnings say what was kept locally or could not be brought over.
type Environment struct {
	Name        string
	Values      map[string]string
	Current     map[string]string
	Credentials map[string]bool
	Warnings    []string
}

// Result is everything one export holds. An export may contain several
// collections (an Insomnia workspace export) and several environments.
type Result struct {
	Collections  []Collection
	Environments []Environment
	// Warnings are losses that do not belong to one collection.
	Warnings []string
}

// Segment makes a name one level of a collection path: a slash in it would
// otherwise start a new folder, as the Postman importer does.
func Segment(name string) string {
	return strings.TrimSpace(strings.ReplaceAll(name, "/", "∕"))
}

// HeaderBlock writes the enabled, named rows as the request's Key: Value
// block, in order.
func HeaderBlock(rows []request.Field) string {
	var b strings.Builder
	for _, row := range rows {
		if !row.Disabled && row.Name != "" {
			b.WriteString(row.Name + ": " + row.Value + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// HeaderRows reads a Key: Value block, keeping order and repeats.
func HeaderRows(block string) []request.Field {
	var rows []request.Field
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		rows = append(rows, request.Field{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	return rows
}

// AddHeader appends a header unless an enabled row with that name exists.
func AddHeader(r *request.Request, name, value string) {
	for _, row := range r.HeaderList {
		if !row.Disabled && strings.EqualFold(strings.TrimSpace(row.Name), name) {
			return
		}
	}
	r.HeaderList = append(r.HeaderList, request.Field{Name: name, Value: value})
}

// SetHeader replaces every enabled row of name with one value, or appends it.
func SetHeader(r *request.Request, name, value string) {
	r.HeaderList = slices.DeleteFunc(slices.Clone(r.HeaderList), func(row request.Field) bool {
		return strings.EqualFold(strings.TrimSpace(row.Name), name)
	})
	r.HeaderList = append(r.HeaderList, request.Field{Name: name, Value: value})
}

// FinishHeaders rebuilds the Key: Value block from the rows after a reader
// changed them.
func FinishHeaders(r *request.Request) {
	r.Headers = HeaderBlock(r.HeaderList)
}

// SetRawBody stores a raw body and the media type the source would send.
func SetRawBody(r *request.Request, contentType, body string) {
	r.BodyMode, r.Body = "Raw", body
	if contentType != "" {
		AddHeader(r, "Content-Type", contentType)
	}
}

// SetFormBody stores a urlencoded or multipart body.
func SetFormBody(r *request.Request, multipart bool, fields []request.Field) {
	if multipart {
		r.BodyMode = "Multipart"
	} else {
		r.BodyMode = "URL encoded"
	}
	r.Form = fields
}

// SetGraphQLBody stores a GraphQL query, its variables as JSON text, and the
// operation a document with several defines selects.
func SetGraphQLBody(r *request.Request, query, variables, operationName string) {
	r.BodyMode = "GraphQL"
	r.GraphQL.Query = query
	r.GraphQL.Variables = variables
	r.GraphQL.OperationName = operationName
}

// credentialMap marks every name of a confidential values map.
func credentialMap(values map[string]string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	names := make(map[string]bool, len(values))
	for name := range values {
		names[name] = true
	}
	return names
}

// VariableFields turns a scope's variables into the fields VariableScript
// takes, using the value a send would read.
func VariableFields(vars []request.Variable) []request.Field {
	var fields []request.Field
	for _, v := range vars {
		fields = append(fields, request.Field{Name: v.Name, Value: request.Prefer(v.Initial, v.Current), Disabled: v.Disabled})
	}
	return fields
}

// VariableScript is a pre-request script that sets fields as local variables,
// for a source whose request-level variables have no field of their own here.
func VariableScript(fields []request.Field) string {
	var b strings.Builder
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" || field.Disabled {
			continue
		}
		fmt.Fprintf(&b, "pm.variables.set(%s, %s);\n", strconv.Quote(name), strconv.Quote(field.Value))
	}
	return b.String()
}

// JoinScript puts a generated prelude before an existing script.
func JoinScript(prelude, script string) string {
	switch {
	case prelude == "":
		return script
	case script == "":
		return prelude
	default:
		return strings.TrimRight(prelude, "\n") + "\n" + script
	}
}

// StableID makes a request identity that survives importing the same export
// again. parts should identify the source and the request within it.
func StableID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "-" + hex.EncodeToString(sum[:16])
}

// Tally counts what an import left out, by reason, in first-seen order.
type Tally struct {
	order  []string
	counts map[string]int
}

// Add counts one occurrence of reason.
func (t *Tally) Add(reason string) {
	if t.counts == nil {
		t.counts = map[string]int{}
	}
	if t.counts[reason] == 0 {
		t.order = append(t.order, reason)
	}
	t.counts[reason]++
}

// Lines renders the counts as sentences.
func (t *Tally) Lines() []string {
	var out []string
	for _, reason := range t.order {
		n := t.counts[reason]
		out = append(out, fmt.Sprintf("%d %s %s", n, plural(n, "request", "requests"), reason))
	}
	return out
}

// LinesOf renders counts with the given noun, for a reason that is not
// about requests.
func (t *Tally) LinesOf(one, many string) []string {
	var out []string
	for _, reason := range t.order {
		n := t.counts[reason]
		out = append(out, fmt.Sprintf("%d %s %s", n, plural(n, one, many), reason))
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// HeadersOf turns an http.Header into rows, sorted for a stable import.
func HeadersOf(header http.Header) []request.Field {
	var rows []request.Field
	for _, name := range slices.Sorted(maps.Keys(header)) {
		for _, value := range header.Values(name) {
			rows = append(rows, request.Field{Name: name, Value: value})
		}
	}
	return rows
}

// AuthNone is the auth a source's "no auth" choice becomes.
func AuthNone() request.Auth { return request.Auth{Type: "None"} }
