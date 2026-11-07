package graphql

import (
	"bytes"
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// MaxSchemaBytes bounds the SDL kept with a request. MaxIntrospectionBytes
// bounds the raw JSON it is read from: an introspection response is verbose,
// and a formatted GitHub schema is about 5 MB while its SDL is under 900 KB.
// The latter matches the 8 MiB response limit of ReplayWithPolicy.
const (
	MaxSchemaBytes        = 4 << 20
	MaxIntrospectionBytes = 8 << 20
)

// IntrospectionQuery is the standard schema query. It asks only for fields
// every GraphQL server has exposed since the June 2018 specification, so an
// older server can answer it without rejecting newer arguments.
const IntrospectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types {
      ...FullType
    }
    directives {
      name
      description
      locations
      args {
        ...InputValue
      }
    }
  }
}

fragment FullType on __Type {
  kind
  name
  description
  fields(includeDeprecated: true) {
    name
    description
    args {
      ...InputValue
    }
    type {
      ...TypeRef
    }
    isDeprecated
    deprecationReason
  }
  inputFields {
    ...InputValue
  }
  interfaces {
    ...TypeRef
  }
  enumValues(includeDeprecated: true) {
    name
    description
    isDeprecated
    deprecationReason
  }
  possibleTypes {
    ...TypeRef
  }
}

fragment InputValue on __InputValue {
  name
  description
  type { ...TypeRef }
  defaultValue
}

fragment TypeRef on __Type {
  kind
  name
  ofType {
    kind
    name
    ofType {
      kind
      name
      ofType {
        kind
        name
        ofType {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
              ofType {
                kind
                name
              }
            }
          }
        }
      }
    }
  }
}`

// Schema is a loaded GraphQL schema. It is immutable after parsing and is
// shared by completion, hover and diagnostics.
type Schema struct {
	ast *ast.Schema
	sdl string
}

// ParseSchema loads an SDL document or a standard introspection result. JSON
// may be wrapped in {"data":{"__schema":…}} or hold __schema at the top level.
func ParseSchema(data []byte) (*Schema, error) {
	if len(data) > MaxIntrospectionBytes {
		return nil, fmt.Errorf("schema exceeds %d KiB", MaxIntrospectionBytes>>10)
	}
	trimmed := bytes.TrimPrefix(bytes.TrimSpace(data), []byte{0xEF, 0xBB, 0xBF})
	if len(trimmed) == 0 {
		return nil, errors.New("the schema is empty")
	}
	if trimmed[0] == '{' {
		// SDL cannot start with a bare brace; a JSON file is an introspection
		// result. A wrong JSON file is named as such, not as a server failure.
		schema, err := ParseIntrospection(trimmed)
		if err != nil && strings.Contains(err.Error(), "no data.__schema") {
			return nil, errors.New("the JSON file has no data.__schema; choose an SDL document or a standard introspection result")
		}
		return schema, err
	}
	return ParseSDL(trimmed)
}

// ParseSDL loads a GraphQL schema definition document.
func ParseSDL(data []byte) (*Schema, error) {
	if len(data) > MaxSchemaBytes {
		return nil, fmt.Errorf("schema exceeds %d KiB", MaxSchemaBytes>>10)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, errors.New("the schema is empty")
	}
	loaded, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: text})
	if err != nil {
		return nil, fmt.Errorf("invalid GraphQL schema: %w", err)
	}
	if loaded.Query == nil {
		return nil, errors.New("the schema has no query type")
	}
	return &Schema{ast: loaded, sdl: text}, nil
}

// SDL is the schema text kept with a request. An introspection result is
// normalized to SDL first, so loading a saved request needs no JSON branch.
func (s *Schema) SDL() string { return s.sdl }

// TypeCount counts the application types, excluding built-in scalars and the
// introspection system's own __ types.
func (s *Schema) TypeCount() int {
	n := 0
	for name, def := range s.ast.Types {
		if def.BuiltIn || strings.HasPrefix(name, "__") {
			continue
		}
		n++
	}
	return n
}

// QueryType names the root query type, for a status line.
func (s *Schema) QueryType() string {
	if s.ast.Query == nil {
		return ""
	}
	return s.ast.Query.Name
}

// field finds a field of a composite type. __typename exists on every
// composite type; a union has no other fields.
func (s *Schema) field(typeName, name string) *ast.FieldDefinition {
	return fieldOf(s.ast, typeName, name)
}

func fieldOf(schema *ast.Schema, typeName, name string) *ast.FieldDefinition {
	if name == "__typename" {
		return &ast.FieldDefinition{Name: "__typename", Type: ast.NonNullNamedType("String", nil)}
	}
	def := schema.Types[typeName]
	if def == nil || def.Kind == ast.Union {
		return nil
	}
	return def.Fields.ForName(name)
}

func namedType(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return t.Name()
}

// typeLabel names a definition kind as a completion or hover says it.
func typeLabel(def *ast.Definition) string {
	switch def.Kind {
	case ast.Scalar:
		return "scalar"
	case ast.Object:
		return "type"
	case ast.Interface:
		return "interface"
	case ast.Union:
		return "union"
	case ast.Enum:
		return "enum"
	case ast.InputObject:
		return "input"
	}
	return strings.ToLower(string(def.Kind))
}

// introspectionSchema is the part of a standard __schema result this app
// keeps. Pointers distinguish an absent field from an empty one.
type introspectionSchema struct {
	QueryType        *introspectionTypeRef    `json:"queryType"`
	MutationType     *introspectionTypeRef    `json:"mutationType"`
	SubscriptionType *introspectionTypeRef    `json:"subscriptionType"`
	Types            []introspectionType      `json:"types"`
	Directives       []introspectionDirective `json:"directives"`
}

type introspectionType struct {
	Kind          string                    `json:"kind"`
	Name          string                    `json:"name"`
	Description   *string                   `json:"description"`
	Fields        []introspectionField      `json:"fields"`
	InputFields   []introspectionInputValue `json:"inputFields"`
	Interfaces    []introspectionTypeRef    `json:"interfaces"`
	EnumValues    []introspectionEnumValue  `json:"enumValues"`
	PossibleTypes []introspectionTypeRef    `json:"possibleTypes"`
}

type introspectionField struct {
	Name              string                    `json:"name"`
	Description       *string                   `json:"description"`
	Args              []introspectionInputValue `json:"args"`
	Type              *introspectionTypeRef     `json:"type"`
	IsDeprecated      bool                      `json:"isDeprecated"`
	DeprecationReason *string                   `json:"deprecationReason"`
}

type introspectionInputValue struct {
	Name         string                `json:"name"`
	Description  *string               `json:"description"`
	Type         *introspectionTypeRef `json:"type"`
	DefaultValue *string               `json:"defaultValue"`
}

type introspectionEnumValue struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

type introspectionDirective struct {
	Name        string                    `json:"name"`
	Description *string                   `json:"description"`
	Locations   []string                  `json:"locations"`
	Args        []introspectionInputValue `json:"args"`
}

type introspectionTypeRef struct {
	Kind   string                `json:"kind"`
	Name   *string               `json:"name"`
	OfType *introspectionTypeRef `json:"ofType"`
}

// introspectionProblem is the first GraphQL error in a response, if any.
type introspectionProblem struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// IntrospectionProblem returns the server's first GraphQL error message, one
// line and bounded, or "". It never includes the request.
func IntrospectionProblem(data []byte) string {
	if len(data) > MaxIntrospectionBytes {
		return ""
	}
	var probe introspectionProblem
	if err := json.Unmarshal(data, &probe); err != nil || len(probe.Errors) == 0 {
		return ""
	}
	message := strings.Join(strings.Fields(probe.Errors[0].Message), " ")
	if message == "" {
		return ""
	}
	if runes := []rune(message); len(runes) > 400 {
		message = string(runes[:400]) + "…"
	}
	return message
}

// ParseIntrospection loads a standard introspection response. A server that
// disabled introspection usually answers with errors and no data; the error
// then says so, because that is the reason a person can act on.
func ParseIntrospection(data []byte) (*Schema, error) {
	if len(data) > MaxIntrospectionBytes {
		return nil, fmt.Errorf("introspection response exceeds %d KiB", MaxIntrospectionBytes>>10)
	}
	var envelope struct {
		Data *struct {
			Schema *introspectionSchema `json:"__schema"`
		} `json:"data"`
		Schema *introspectionSchema `json:"__schema"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.New("the response is not a GraphQL introspection result")
	}
	schema := envelope.Schema
	if envelope.Data != nil && envelope.Data.Schema != nil {
		schema = envelope.Data.Schema
	}
	if schema == nil {
		// Some tools save the __schema object itself, without the data wrapper.
		var direct introspectionSchema
		if json.Unmarshal(data, &direct) == nil && len(direct.Types) > 0 {
			schema = &direct
		}
	}
	if schema == nil || len(schema.Types) == 0 {
		if len(envelope.Errors) > 0 {
			message := strings.Join(strings.Fields(envelope.Errors[0].Message), " ")
			if message == "" {
				message = "no reason given"
			}
			if strings.Contains(strings.ToLower(message), "introspection") {
				return nil, fmt.Errorf("introspection is disabled by the server: %s", message)
			}
			return nil, fmt.Errorf("the server rejected introspection: %s", message)
		}
		return nil, errors.New("the response has no data.__schema; introspection may be disabled")
	}
	sdl, err := sdlFromIntrospection(schema)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseSDL([]byte(sdl))
	if err != nil {
		return nil, fmt.Errorf("the server's introspection result cannot be loaded: %w", err)
	}
	return parsed, nil
}

// sdlFromIntrospection renders the schema as SDL. The result is deterministic
// so the same server answer keeps the same bytes with a saved request.
func sdlFromIntrospection(s *introspectionSchema) (string, error) {
	var b strings.Builder
	root := func(name string, ref *introspectionTypeRef) string {
		if ref == nil || ref.Name == nil {
			return ""
		}
		return name + ": " + *ref.Name
	}
	roots := []string{
		root("query", s.QueryType),
		root("mutation", s.MutationType),
		root("subscription", s.SubscriptionType),
	}
	if q := s.QueryType; q == nil || q.Name == nil {
		return "", errors.New("the introspection result has no query type")
	}
	// Omitting the schema definition means types named Query, Mutation and
	// Subscription are the roots. A type with one of those names that is not
	// the corresponding root must be excluded by writing the block, or
	// gqlparser would silently make it a root.
	names := make(map[string]bool, len(s.Types))
	for _, typ := range s.Types {
		names[typ.Name] = true
	}
	queryName := ""
	if s.QueryType != nil && s.QueryType.Name != nil {
		queryName = *s.QueryType.Name
	}
	mutationName, subscriptionName := "", ""
	if s.MutationType != nil && s.MutationType.Name != nil {
		mutationName = *s.MutationType.Name
	}
	if s.SubscriptionType != nil && s.SubscriptionType.Name != nil {
		subscriptionName = *s.SubscriptionType.Name
	}
	defaultRoots := queryName == "Query" &&
		(mutationName == "" || mutationName == "Mutation") &&
		(subscriptionName == "" || subscriptionName == "Subscription") &&
		!(mutationName != "Mutation" && names["Mutation"]) &&
		!(subscriptionName != "Subscription" && names["Subscription"])
	if !defaultRoots {
		b.WriteString("schema {\n")
		for _, line := range roots {
			if line != "" {
				b.WriteString("  " + line + "\n")
			}
		}
		b.WriteString("}\n\n")
	}
	types := slices.Clone(s.Types)
	slices.SortFunc(types, func(a, b introspectionType) int { return cmp.Compare(a.Name, b.Name) })
	for _, typ := range types {
		if strings.HasPrefix(typ.Name, "__") || builtinScalar(typ.Name) {
			continue
		}
		if err := writeIntrospectionType(&b, typ); err != nil {
			return "", err
		}
	}
	directives := slices.Clone(s.Directives)
	slices.SortFunc(directives, func(a, b introspectionDirective) int { return cmp.Compare(a.Name, b.Name) })
	for _, dir := range directives {
		if builtinDirective(dir.Name) {
			continue
		}
		writeDescription(&b, dir.Description, "")
		locations := slices.Clone(dir.Locations)
		slices.Sort(locations)
		b.WriteString("directive @" + dir.Name)
		if len(dir.Args) > 0 {
			b.WriteString("(")
			for i, arg := range dir.Args {
				if i > 0 {
					b.WriteString(", ")
				}
				if err := writeInputValue(&b, arg, ""); err != nil {
					return "", err
				}
			}
			b.WriteString(")")
		}
		if len(locations) == 0 {
			return "", fmt.Errorf("directive @%s has no locations", dir.Name)
		}
		b.WriteString(" on " + strings.Join(locations, " | ") + "\n\n")
	}
	return b.String(), nil
}

func writeIntrospectionType(b *strings.Builder, typ introspectionType) error {
	writeDescription(b, typ.Description, "")
	switch typ.Kind {
	case "SCALAR":
		b.WriteString("scalar " + typ.Name + "\n\n")
	case "ENUM":
		b.WriteString("enum " + typ.Name + " {\n")
		for _, value := range typ.EnumValues {
			writeDescription(b, value.Description, "  ")
			b.WriteString("  " + value.Name)
			if value.IsDeprecated {
				b.WriteString(deprecated(value.DeprecationReason))
			}
			b.WriteString("\n")
		}
		b.WriteString("}\n\n")
	case "UNION":
		var members []string
		for _, member := range typ.PossibleTypes {
			if member.Name != nil {
				members = append(members, *member.Name)
			}
		}
		if len(members) == 0 {
			return fmt.Errorf("union %s has no member types", typ.Name)
		}
		slices.Sort(members)
		b.WriteString("union " + typ.Name + " = " + strings.Join(members, " | ") + "\n\n")
	case "INTERFACE", "OBJECT":
		kind := "type"
		if typ.Kind == "INTERFACE" {
			kind = "interface"
		}
		b.WriteString(kind + " " + typ.Name)
		if len(typ.Interfaces) > 0 {
			var names []string
			for _, iface := range typ.Interfaces {
				if iface.Name != nil {
					names = append(names, *iface.Name)
				}
			}
			if len(names) > 0 {
				slices.Sort(names)
				b.WriteString(" implements " + strings.Join(names, " & "))
			}
		}
		b.WriteString(" {\n")
		for _, field := range typ.Fields {
			writeDescription(b, field.Description, "  ")
			b.WriteString("  " + field.Name)
			if len(field.Args) > 0 {
				b.WriteString("(")
				for i, arg := range field.Args {
					if i > 0 {
						b.WriteString(", ")
					}
					if err := writeInputValue(b, arg, ""); err != nil {
						return err
					}
				}
				b.WriteString(")")
			}
			fieldType, err := renderTypeRef(field.Type)
			if err != nil {
				return fmt.Errorf("field %s.%s: %w", typ.Name, field.Name, err)
			}
			b.WriteString(": " + fieldType)
			if field.IsDeprecated {
				b.WriteString(deprecated(field.DeprecationReason))
			}
			b.WriteString("\n")
		}
		b.WriteString("}\n\n")
	case "INPUT_OBJECT":
		b.WriteString("input " + typ.Name + " {\n")
		for _, field := range typ.InputFields {
			if err := writeInputValue(b, field, "  "); err != nil {
				return err
			}
			b.WriteString("\n")
		}
		b.WriteString("}\n\n")
	}
	return nil
}

// writeInputValue writes an argument or input field. An empty indent writes it
// inline in a field's argument list, where the SDL grammar still allows a
// quoted Description before the name.
func writeInputValue(b *strings.Builder, value introspectionInputValue, indent string) error {
	if indent != "" {
		writeDescription(b, value.Description, indent)
		b.WriteString(indent)
	} else if value.Description != nil && strings.TrimSpace(*value.Description) != "" {
		quoted, err := json.Marshal(*value.Description)
		if err == nil {
			b.WriteString(string(quoted) + " ")
		}
	}
	b.WriteString(value.Name + ": ")
	t, err := renderTypeRef(value.Type)
	if err != nil {
		return err
	}
	b.WriteString(t)
	if value.DefaultValue != nil {
		b.WriteString(" = " + *value.DefaultValue)
	}
	return nil
}

func writeDescription(b *strings.Builder, description *string, indent string) {
	if description == nil || strings.TrimSpace(*description) == "" {
		return
	}
	quoted, err := json.Marshal(*description)
	if err != nil {
		return
	}
	b.WriteString(indent + string(quoted) + "\n")
}

func deprecated(reason *string) string {
	if reason == nil || strings.TrimSpace(*reason) == "" {
		return " @deprecated"
	}
	quoted, err := json.Marshal(*reason)
	if err != nil {
		return " @deprecated"
	}
	return " @deprecated(reason: " + string(quoted) + ")"
}

func renderTypeRef(ref *introspectionTypeRef) (string, error) {
	if ref == nil {
		return "", errors.New("a type reference is missing")
	}
	switch ref.Kind {
	case "NON_NULL":
		inner, err := renderTypeRef(ref.OfType)
		if err != nil {
			return "", err
		}
		return inner + "!", nil
	case "LIST":
		inner, err := renderTypeRef(ref.OfType)
		if err != nil {
			return "", err
		}
		return "[" + inner + "]", nil
	default:
		if ref.Name == nil || *ref.Name == "" {
			return "", errors.New("a named type reference has no name")
		}
		return *ref.Name, nil
	}
}

func builtinScalar(name string) bool {
	switch name {
	case "Int", "Float", "String", "Boolean", "ID":
		return true
	}
	return false
}

func builtinDirective(name string) bool {
	switch name {
	case "skip", "include", "deprecated", "specifiedBy", "oneOf":
		return true
	}
	return false
}
