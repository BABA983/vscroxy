package openapi

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Issue is one lint finding. Code is stable for automation; Pointer names the
// document location; Severity is "error" or "warning".
type Issue struct {
	Code     string `json:"code"`
	Pointer  string `json:"pointer"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

var lintPathVariable = regexp.MustCompile(`\{([^{}]+)\}`)

// Lint checks conventions structural validation does not: operation IDs,
// summaries, path parameters, servers, tags and unused components.
func Lint(doc *openapi3.T) []Issue {
	var issues []Issue
	if doc == nil {
		return issues
	}
	add := func(severity, code, pointer, format string, args ...any) {
		issues = append(issues, Issue{Code: code, Pointer: pointer, Message: fmt.Sprintf(format, args...), Severity: severity})
	}
	declaredTags := map[string]bool{}
	for _, tag := range doc.Tags {
		declaredTags[tag.Name] = true
	}
	operationIDs := map[string]string{}
	for _, path := range doc.Paths.Keys() {
		item := doc.Paths.Value(path)
		if item == nil {
			continue
		}
		template := map[string]bool{}
		for _, match := range lintPathVariable.FindAllStringSubmatch(path, -1) {
			template[match[1]] = true
		}
		for _, method := range slices.Sorted(maps.Keys(item.Operations())) {
			op := item.GetOperation(method)
			pointer := strings.ToUpper(method) + " " + path
			if op.OperationID == "" {
				add("warning", "operation-id-missing", pointer, "add an operationId so generated clients and collections have stable names")
			} else if first, ok := operationIDs[op.OperationID]; ok {
				add("error", "operation-id-duplicate", pointer, "operationId %q is already used by %s", op.OperationID, first)
			} else {
				operationIDs[op.OperationID] = pointer
			}
			if strings.TrimSpace(op.Summary) == "" {
				add("warning", "operation-summary-missing", pointer, "add a summary; it becomes the saved request's name")
			}
			declaredPath := map[string]bool{}
			for _, refs := range []openapi3.Parameters{item.Parameters, op.Parameters} {
				for _, ref := range refs {
					if ref == nil || ref.Value == nil || ref.Value.In != "path" {
						continue
					}
					declaredPath[ref.Value.Name] = true
					if !template[ref.Value.Name] {
						add("warning", "path-parameter-unused", pointer, "path parameter %q is not in the path template", ref.Value.Name)
					}
				}
			}
			for name := range template {
				if !declaredPath[name] {
					add("error", "path-parameter-undeclared", pointer, "path parameter {%s} needs a parameter with in: path", name)
				}
			}
			for _, tag := range op.Tags {
				if !declaredTags[tag] {
					add("warning", "tag-undefined", pointer, "tag %q is not declared in the document's tags", tag)
				}
			}
			hasSuccess := false
			for code, response := range op.Responses.Map() {
				if code == "default" || strings.HasPrefix(code, "2") {
					hasSuccess = true
				}
				if strings.HasPrefix(code, "2") && code != "204" && code != "205" && response != nil && response.Value != nil && len(response.Value.Content) == 0 {
					add("warning", "response-schema-missing", pointer, "response %s has no content schema", code)
				}
			}
			if !hasSuccess {
				add("warning", "success-response-missing", pointer, "add the success status the operation actually returns")
			}
		}
	}
	for _, server := range doc.Servers {
		if server == nil {
			continue
		}
		if u, err := url.Parse(server.URL); err == nil && u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
			add("warning", "server-insecure", "servers", "server %q uses http; use https outside local development", server.URL)
		}
	}
	if doc.Components != nil {
		data, err := json.Marshal(doc.Components)
		if err == nil {
			text := string(data)
			for kind, names := range map[string][]string{
				"schemas":         slices.Sorted(maps.Keys(doc.Components.Schemas)),
				"parameters":      slices.Sorted(maps.Keys(doc.Components.Parameters)),
				"responses":       slices.Sorted(maps.Keys(doc.Components.Responses)),
				"requestBodies":   slices.Sorted(maps.Keys(doc.Components.RequestBodies)),
				"headers":         slices.Sorted(maps.Keys(doc.Components.Headers)),
				"securitySchemes": slices.Sorted(maps.Keys(doc.Components.SecuritySchemes)),
			} {
				for _, name := range names {
					if !strings.Contains(text, `"#/components/`+kind+`/`+name+`"`) {
						add("warning", "component-unused", "components."+kind+"."+name, "component is not referenced")
					}
				}
			}
		}
	}
	slices.SortFunc(issues, func(a, b Issue) int {
		return strings.Compare(a.Pointer+"\x00"+a.Code, b.Pointer+"\x00"+b.Code)
	})
	return issues
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return strings.HasPrefix(host, "127.")
}
