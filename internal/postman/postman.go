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
	case "oauth1 header encoding":
		return count(n, "OAuth 1.0 configuration disables", "OAuth 1.0 configurations disable") +
			" header encoding; vscroxy always encodes"
	case "checks":
		return "Invalid checks on " + count(n, "request are", "requests are") + " not exported"
	case "protocol request":
		return "Skipped " + count(n, k.detail+" request", k.detail+" requests") + "; Postman Collection v2.1 only supports HTTP requests"
	case "token source":
		return "Native pre-setup token invalidation on " + count(n, "request is", "requests is") + " not exported; script fallback remains but cannot cover inherited or compilation failures"
	case "timeout":
		return count(n, "request timeout is", "request timeouts are") + " not part of Postman collections"
	case "curl redirects":
		return count(n, "cURL redirect rule is", "cURL redirect rules are") + " not part of Postman collections"
	case "example metadata":
		return "Dropped descriptions, auth or settings on " + count(n, "saved example", "saved examples")
	case "auth description":
		return "Dropped " + count(n, "auth description", "auth descriptions")
	case "body metadata":
		return "Dropped JSON Schema annotations on " + count(n, "body", "bodies")
	case "stream message description":
		return "Dropped " + count(n, "description on a saved stream message", "descriptions on saved stream messages")
	case "collection uses":
		return "Dropped " + count(n, "collection resource dependency", "collection resource dependencies")
	case "v3 path":
		return "Skipped " + count(n, "symbolic link, non-regular file or path outside the collection", "symbolic links, non-regular files or paths outside the collection")
	case "v3 unreadable":
		text := "Skipped " + count(n, "unreadable or malformed file", "unreadable or malformed files")
		if k.detail != "" {
			text += " (first: " + k.detail + ")"
		}
		return text
	case "v3 definition kind":
		text := "Skipped " + count(n, "definition whose $kind is not collection", "definitions whose $kind is not collection")
		if k.detail != "" {
			text += " (first: " + k.detail + ")"
		}
		return text
	case "v3 auth":
		return "Skipped " + count(n, "malformed auth object", "malformed auth objects")
	case "v3 limit":
		switch k.detail {
		case "file size":
			return "Skipped example, message or request files larger than 8 MiB"
		case "document size":
			return "Skipped collection or folder definitions larger than 1 MiB"
		case "GraphQL schema size":
			return "Skipped GraphQL schema files larger than 1 MiB"
		case "YAML aliases":
			return "Skipped files that use YAML aliases"
		case "YAML expansion":
			return "Skipped files whose YAML expands beyond the limit"
		case "total size":
			return fmt.Sprintf("Stopped reading at the %d MiB total-size limit", v3MaxBytes>>20)
		case "files":
			return fmt.Sprintf("Stopped reading at the %d-entry limit", v3MaxFiles)
		case "depth":
			return "Stopped reading folders nested more than 32 levels deep"
		}
		return "Stopped reading the collection at its " + k.detail + " limit"
	case "v3 limit first":
		return "First skipped file: " + k.detail
	case "v3 request kind":
		return "Skipped " + count(n, "request of an unknown kind", "requests of unknown kinds") + " (" + k.detail + ")"
	case "v3 protocol":
		return "Skipped " + count(n, k.detail+" request", k.detail+" requests") + "; vscroxy does not support them"
	case "v3 folder name":
		return "Renamed " + count(n, "folder whose display name collides with another", "folders whose display names collide")
	case "v3 duplicate id":
		return "Duplicate request id " + k.detail + " was replaced with a derived id"
	case "multi auth":
		return "Imported the first auth set on " + count(n, k.detail+" item", k.detail+" items") + " where several are offered; vscroxy uses one"
	case "secret variable":
		if k.detail == "local" {
			return "Kept " + count(n, "secret variable value", "secret variable values") + " in this computer's Current values"
		}
		return "Skipped " + count(n, "secret variable that references a vault", "secret variables that reference a vault")
	case "variable description":
		return "Dropped " + count(n, "variable description", "variable descriptions")
	case "parameter metadata":
		return "Dropped descriptions or types on " + count(n, "parameter", "parameters")
	case "script event":
		return "Skipped " + count(n, "script", "scripts") + " with an event vscroxy does not run (" + k.detail + ")"
	case "script language":
		return "Skipped " + count(n, "script", "scripts") + " written in " + k.detail
	case "script package":
		return "Skipped " + count(n, "script package or request reference", "script package or request references")
	case "protocol script":
		return "Skipped " + count(n, k.detail+" script", k.detail+" scripts") + "; vscroxy does not run them"
	case "grpc schema":
		return "Dropped " + count(n, "gRPC schema or method descriptor", "gRPC schemas or method descriptors") + "; load the proto in the gRPC editor"
	case "graphql schema":
		return "Dropped " + count(n, "GraphQL schema source that is not a local file", "GraphQL schema sources that are not local files")
	case "graphql schema export":
		if n == 1 {
			return "Dropped 1 GraphQL schema; Postman collections cannot hold it"
		}
		return fmt.Sprintf("Dropped %d GraphQL schemas; Postman collections cannot hold them", n)
	case "stream example":
		return "Skipped " + count(n, k.detail+" example", k.detail+" examples") + "; vscroxy examples are HTTP responses"
	case "stream messages":
		if k.detail == "binary" {
			return "Skipped " + count(n, "binary stream message", "binary stream messages") + "; vscroxy sends text"
		}
		return "Dropped " + count(n, "extra saved stream message", "extra saved stream messages") + "; vscroxy sends one at a time"
	case "socketio events":
		return "Dropped " + count(n, "Socket.IO subscription event", "Socket.IO subscription events") + "; vscroxy shows every event the server sends"
	case "socketio settings":
		return "Dropped Socket.IO " + k.detail + " setting"
	case "socketio args":
		return "Imported " + count(n, "Socket.IO binary argument", "Socket.IO binary arguments") + " as text"
	case "mcp command":
		return "Could not split " + count(n, "MCP command line", "MCP command lines") + " (" + k.detail + "); imported it as the program name"
	case "mcp transport":
		return "Skipped " + count(n, "MCP request", "MCP requests") + " with transport " + k.detail + "; the schema supports sse and stdio"
	case "mcp version":
		return "Dropped MCP protocol version " + k.detail + "; use Auto, Legacy or 2026-07-28"
	case "mcp timeout":
		return "Clamped " + count(n, "MCP request timeout", "MCP request timeouts") + " to 1–3600 seconds"
	case "mcp session timeout":
		return "Dropped " + count(n, "MCP session timeout", "MCP session timeouts") + "; a session stays open until you disconnect"
	case "mcp message":
		return "Could not map " + count(n, "MCP JSON-RPC message", "MCP JSON-RPC messages") + " (" + k.detail + "); choose the operation in the MCP editor"
	case "mcp auth":
		return "Imported " + count(n, "MCP OAuth 2.0 auth set", "MCP OAuth 2.0 auth sets") + "; the MCP client uses a Bearer token, Basic or API key, so sign in and use a Bearer token"
	case "graphql example events":
		return "Kept the first event of " + count(n, "GraphQL subscription example", "GraphQL subscription examples")
	case "mqtt qos":
		return "Imported MQTT QoS " + k.detail + " as 0; only 0, 1 and 2 are valid"
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
