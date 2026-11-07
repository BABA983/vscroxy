package graphql

import (
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// Variables builds a JSON object skeleton for an operation's variable
// definitions: required and optional fields are present, enum values use their
// first member, and nested input objects stop at depth three. operationName is
// optional for a single-operation document. It never overwrites an authored
// value; the app only offers the skeleton for an empty editor.
func (s *Schema) Variables(source, operationName string) (string, error) {
	if s == nil || s.ast == nil {
		return "", errors.New("no schema is loaded")
	}
	remaining := maxTokens
	doc, err := parse(source, &remaining)
	if err != nil {
		return "", errors.New("the GraphQL document is invalid")
	}
	var op *ast.OperationDefinition
	if len(doc.Operations) == 1 {
		op = doc.Operations[0]
	} else {
		for _, candidate := range doc.Operations {
			if candidate.Name == operationName {
				op = candidate
				break
			}
		}
	}
	if op == nil {
		return "", errors.New("choose a GraphQL operation first")
	}
	if len(op.VariableDefinitions) == 0 {
		return "{}", nil
	}
	var b strings.Builder
	b.WriteString("{\n")
	for i, def := range op.VariableDefinitions {
		if i > 0 {
			b.WriteString(",\n")
		}
		name, err := json.Marshal(def.Variable)
		if err != nil {
			return "", err
		}
		b.WriteString("  " + string(name) + ": " + s.skeleton(def.Type, 0))
	}
	b.WriteString("\n}")
	return b.String(), nil
}

func (s *Schema) skeleton(t *ast.Type, depth int) string {
	if t == nil {
		return "null"
	}
	if t.NonNull {
		copy := *t
		copy.NonNull = false
		return s.skeleton(&copy, depth)
	}
	if t.Elem != nil {
		return "[]"
	}
	def := s.ast.Types[t.Name()]
	if def == nil {
		return "null"
	}
	switch def.Kind {
	case ast.Enum:
		if len(def.EnumValues) > 0 {
			quoted, _ := json.Marshal(def.EnumValues[0].Name)
			return string(quoted)
		}
		return "null"
	case ast.InputObject:
		if depth >= 3 {
			return "{}"
		}
		var fields []string
		for _, field := range def.Fields {
			if len(fields) == 20 {
				break
			}
			quoted, _ := json.Marshal(field.Name)
			fields = append(fields, string(quoted)+": "+s.skeleton(field.Type, depth+1))
		}
		if len(fields) == 0 {
			return "{}"
		}
		return "{\n" + strings.Repeat("  ", depth+2) + strings.Join(fields, ",\n"+strings.Repeat("  ", depth+2)) + "\n" + strings.Repeat("  ", depth+1) + "}"
	case ast.Scalar:
		switch def.Name {
		case "Int", "Float":
			return "0"
		case "Boolean":
			return "false"
		case "String", "ID":
			return `""`
		}
		return "null"
	}
	return `""`
}
