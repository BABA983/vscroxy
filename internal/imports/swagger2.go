package imports

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/yaml"
)

// ReadSwagger2 converts a Swagger 2.0 document to OpenAPI 3.0, the form the
// app's existing OpenAPI collection importer understands. The conversion is
// structural: schemas, parameters, servers and security schemes are carried
// over, but request examples are not invented.
func ReadSwagger2(data []byte) (*openapi3.T, []string, error) {
	if len(data) > 16<<20 {
		return nil, nil, errors.New("Swagger documents are limited to 16 MiB")
	}
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, nil, fmt.Errorf("read Swagger document: %w", err)
	}
	// YAML turns an unquoted 2.0 into a number; the OpenAPI 2 decoder expects
	// the specification's string version.
	var top map[string]any
	if json.Unmarshal(jsonData, &top) == nil {
		if version, ok := top["swagger"].(float64); ok && version == 2 {
			top["swagger"] = "2.0"
			if normalized, err := json.Marshal(top); err == nil {
				jsonData = normalized
			}
		}
	}
	var doc2 openapi2.T
	if err := json.Unmarshal(jsonData, &doc2); err != nil {
		return nil, nil, fmt.Errorf("read Swagger document: %w", err)
	}
	if doc2.Swagger != "2.0" {
		return nil, nil, fmt.Errorf("supported Swagger version is 2.0, not %q", doc2.Swagger)
	}
	if doc2.Info.Title == "" {
		return nil, nil, errors.New("Swagger document has no info.title")
	}
	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		return nil, nil, fmt.Errorf("convert Swagger 2.0 to OpenAPI 3.0: %w", err)
	}
	if err := doc3.Validate(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("Swagger document is invalid: %w", err)
	}
	return doc3, swaggerWarnings(&doc2), nil
}

// swaggerWarnings lists what the OpenAPI collection importer cannot carry
// over from Swagger 2.0. Counts are per operation, not per document.
func swaggerWarnings(doc *openapi2.T) []string {
	var bodies, forms, files, secured, operationConsumes int
	for _, item := range doc.Paths {
		if item == nil {
			continue
		}
		for _, parameter := range item.Parameters {
			if parameter != nil && parameter.Type != nil && parameter.Type.Is("file") {
				files++
			}
		}
		for _, operation := range []*openapi2.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete, item.Head, item.Options} {
			if operation == nil {
				continue
			}
			hasBody, hasForm := false, false
			for _, parameter := range operation.Parameters {
				if parameter == nil {
					continue
				}
				switch parameter.In {
				case "body":
					hasBody = true
				case "formData":
					hasForm = true
				}
				if parameter.Type != nil && parameter.Type.Is("file") {
					files++
				}
			}
			if hasBody || hasForm {
				bodies++
			}
			if hasForm {
				forms++
			}
			if operation.Security != nil && len(*operation.Security) > 0 || len(doc.Security) > 0 && operation.Security == nil {
				secured++
			}
			if len(operation.Consumes) > 0 && !sameStrings(operation.Consumes, doc.Consumes) {
				operationConsumes++
			}
		}
	}
	var warnings []string
	if bodies > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s with a request body were imported as schemas without values; review each body before sending", bodies, plural(bodies, "operation", "operations")))
	}
	if forms > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s use form data; the OpenAPI request draft carries the media type but not the form fields", forms, plural(forms, "operation", "operations")))
	}
	if files > 0 {
		warnings = append(warnings, fmt.Sprintf("%d file %s cannot be represented; choose a local file in the request", files, plural(files, "upload field", "upload fields")))
	}
	if secured > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s declare security; authentication is not applied to imported requests", secured, plural(secured, "operation", "operations")))
	}
	if operationConsumes > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s set their own consumes media type; only the document-level media type was used", operationConsumes, plural(operationConsumes, "operation", "operations")))
	}
	if len(doc.Schemes) > 1 {
		warnings = append(warnings, "the document lists several schemes; imported requests use the first server")
	}
	return warnings
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, value := range a {
		seen[value]++
	}
	for _, value := range b {
		seen[value]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
