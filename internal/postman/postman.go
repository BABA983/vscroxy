// Package postman converts Postman Collection v2.0/v2.1 and Postman
// environment files to and from the app's requests, folders and variables.
//
// Postman can express more than a request here holds. What does not fit is
// dropped and counted in human-readable warnings, so an import never fails
// over a feature the app lacks.
package postman

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// Kind is what a Postman file holds: "collection", "environment", "globals",
// or "" when the data is not a Postman file this package reads.
type Kind string

const (
	KindCollection  Kind = "collection"
	KindEnvironment Kind = "environment"
	KindGlobals     Kind = "globals"
)

// Detect says what kind of Postman file data is. A collection is an object
// whose info.schema names a collection, or one with info and an item array.
// An environment or globals file is an object with a values array, scoped by
// _postman_variable_scope (environment when absent).
func Detect(data []byte) Kind {
	var top struct {
		Info   jsontext.Value `json:"info"`
		Item   jsontext.Value `json:"item"`
		Values jsontext.Value `json:"values"`
		Scope  jsontext.Value `json:"_postman_variable_scope"`
	}
	if json.Unmarshal(data, &top) != nil {
		return ""
	}
	if top.Info.Kind() == '{' {
		var info struct {
			Schema jsontext.Value `json:"schema"`
		}
		if (json.Unmarshal(top.Info, &info) == nil && strings.Contains(text(info.Schema), "collection")) || top.Item.Kind() == '[' {
			return KindCollection
		}
	}
	if top.Values.Kind() == '[' {
		if text(top.Scope) == "globals" {
			return KindGlobals
		}
		return KindEnvironment
	}
	return ""
}

// text is a JSON value as a variable holds it: a string's contents, ""
// for null or nothing, and the JSON text of anything else.
func text(v jsontext.Value) string {
	switch v.Kind() {
	case 0, 'n':
		return ""
	case '"':
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	v = v.Clone()
	if v.Compact() != nil {
		return strings.TrimSpace(string(v))
	}
	return string(v)
}

// segment is a Postman folder or collection name as one path segment:
// a "/" in the name would otherwise start a new folder.
func segment(name string) string { return strings.ReplaceAll(name, "/", "∕") }

// unsegment is the Postman name of a path segment.
func unsegment(seg string) string { return strings.ReplaceAll(seg, "∕", "/") }

// headerRows reads a "Key: Value" block into rows, keeping order and repeats.
func headerRows(block string) []request.Field {
	var rows []request.Field
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		rows = append(rows, request.Field{Name: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
	}
	return rows
}

// headerBlock is the "Key: Value" block of the enabled, named rows in order.
func headerBlock(rows []request.Field) string {
	var b strings.Builder
	for _, row := range rows {
		if !row.Disabled && row.Name != "" {
			b.WriteString(row.Name + ": " + row.Value + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// warning is one kind of loss, counted across a file.
type warning struct{ what, detail string }

// tally counts what an import or export dropped.
type tally map[warning]int

func (w tally) add(what, detail string, n int) {
	if n > 0 {
		w[warning{what, detail}] += n
	}
}

// list renders the counts as sentences, sorted so the order is stable.
func (w tally) list() []string {
	var out []string
	for k, n := range w {
		out = append(out, k.text(n))
	}
	slices.Sort(out)
	return out
}

func (k warning) text(n int) string {
	switch k.what {
	case "example":
		return "Dropped " + count(n, "saved example that is unreadable or over 5 MiB", "saved examples that are unreadable or over 5 MiB")
	case "description":
		return "Dropped " + count(n, "request description", "request descriptions")
	case "settings":
		return "Dropped protocol profile, certificate or proxy settings on " + count(n, "item", "items")
	case "request variable":
		return "Dropped " + count(n, "request variable", "request variables")
	case "disabled query":
		return "Dropped " + count(n, "disabled query parameter", "disabled query parameters")
	case "disabled script":
		return "Skipped " + count(n, "disabled script", "disabled scripts")
	case "disabled variable":
		return "Skipped " + count(n, "disabled variable", "disabled variables")
	case "form files":
		return count(n, "form file field lists", "form file fields list") + " several files; imported the first"
	case "body":
		return count(n, "request uses", "requests use") + " a " + k.detail + " body; imported without a body"
	// An auth warning's detail is what was dropped: "hawk auth" or "the
	// OAuth 2.0 implicit grant".
	case "request auth":
		return count(n, "request uses", "requests use") + " " + k.detail + "; imported as no auth"
	case "folder auth":
		return count(n, "folder uses", "folders use") + " " + k.detail + "; imported as no auth"
	case "collection auth":
		return "The collection uses " + k.detail + "; imported as no auth"
	case "oauth2 callback":
		return count(n, "OAuth 2.0 callback URL is not a loopback address", "OAuth 2.0 callback URLs are not loopback addresses") +
			"; vscroxy signs in through http://127.0.0.1 instead"
	case "oauth2 query":
		return count(n, "OAuth 2.0 configuration adds", "OAuth 2.0 configurations add") +
			" the token to the query; vscroxy sends it in the Authorization header"
	case "digest retry":
		return count(n, "Digest auth configuration does", "Digest auth configurations do") +
			" not retry after the server's challenge; vscroxy always answers the challenge"
	case "aws query":
		return count(n, "AWS Signature configuration adds", "AWS Signature configurations add") +
			" the signature to the query; vscroxy sends it in headers"
	case "checks":
		return "Declarative checks on " + count(n, "request are", "requests are") + " not exported"
	}
	return fmt.Sprintf("%s %s: %d", k.what, k.detail, n)
}

// count is "1 thing" or "n things".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
