package graphql_test

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/graphql"
)

// testSchema is written as SDL, not generated from the implementation, so
// expected fields, arguments and types come from the GraphQL specification.
const testSchema = `
"""The root query."""
type Query {
  """The signed-in user."""
  viewer: User!
  """Search projects."""
  search(query: String!, limit: Int = 10, filter: SearchFilter): [SearchResult!]!
  node(id: ID!): Node
}

"""Anything with a stable identity."""
interface Node {
  id: ID!
}

type User implements Node {
  id: ID!
  name: String!
  role: Role!
  friends: [User!]!
}

type Project implements Node {
  id: ID!
  title: String!
}

union SearchResult = User | Project

enum Role {
  ADMIN
  USER
}

input SearchFilter {
  term: String!
  role: Role
  tags: [String!]
}

type Mutation {
  saveItem(input: SaveInput!): User!
}

input SaveInput {
  name: String!
  role: Role
}
`

func loadTestSchema(t *testing.T) *graphql.Schema {
	t.Helper()
	schema, err := graphql.ParseSDL([]byte(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func labels(items []graphql.Completion) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Label
	}
	return out
}

func hasLabel(items []graphql.Completion, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}

func completionAt(t *testing.T, schema *graphql.Schema, source, marker string) graphql.CompletionResult {
	t.Helper()
	at := strings.LastIndex(source, marker)
	if at < 0 {
		t.Fatalf("marker %q not in %q", marker, source)
	}
	return schema.Complete(source, at+len(marker))
}

func TestSchemaFromSDL(t *testing.T) {
	schema := loadTestSchema(t)
	if got, want := schema.TypeCount(), 9; got != want {
		t.Fatalf("TypeCount = %d, want %d", got, want)
	}
	if schema.QueryType() != "Query" {
		t.Fatalf("QueryType = %q", schema.QueryType())
	}
	if !strings.Contains(schema.SDL(), "union SearchResult") {
		t.Fatalf("SDL lost the union: %s", schema.SDL())
	}
}

func TestCompletionFields(t *testing.T) {
	schema := loadTestSchema(t)
	cases := []struct {
		name, source, marker string
		want                 []string
		not                  []string
	}{
		{"root", "query Q { vi", "vi", []string{"viewer"}, []string{"search"}},
		{"object", "query Q { viewer { ", "{ ", []string{"id", "name", "role", "friends", "__typename"}, []string{"search", "title"}},
		{"alias", "query Q { me: viewer { na", "na", []string{"name"}, []string{"title"}},
		{"nested", "query Q { viewer { friends { na", "na", []string{"name"}, []string{"title"}},
		{"interface", "fragment F on Node { ", "{ ", []string{"id"}, []string{"name", "title"}},
		{"interface fragment use", "query Q { viewer { ...F } } fragment F on User { ", "{ ", []string{"id", "name", "friends"}, []string{"search"}},
		{"union spread", "fragment F on SearchResult { ... on ", "on ", []string{"User", "Project"}, []string{"Role"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := completionAt(t, schema, tt.source, tt.marker)
			for _, label := range tt.want {
				if !hasLabel(got.Items, label) {
					t.Errorf("missing %q in %v", label, labels(got.Items))
				}
			}
			for _, label := range tt.not {
				if hasLabel(got.Items, label) {
					t.Errorf("unexpected %q in %v", label, labels(got.Items))
				}
			}
		})
	}
}

func TestCompletionArgumentsAndValues(t *testing.T) {
	schema := loadTestSchema(t)
	cases := []struct {
		name, source, marker string
		want                 []string
		not                  []string
	}{
		{"arguments", "query Q { search(", "(", []string{"query", "limit", "filter"}, []string{"viewer"}},
		{"argument prefix", "query Q { search(li", "li", []string{"limit"}, []string{"query"}},
		{"input fields", "query Q { search(filter: { ", "{ ", []string{"term", "role", "tags"}, []string{"name"}},
		{"enum value", "query Q { search(filter: { role: ", ": ", []string{"ADMIN", "USER"}, []string{"String"}},
		{"input object enum", "mutation { saveItem(input: { role: ", ": ", []string{"ADMIN", "USER"}, nil},
		{"variable type", "query Q($f: ", ": ", []string{"SearchFilter", "Role", "String", "Int"}, []string{"User"}},
		{"fragment type", "query Q { search(query: \"x\") { ... on ", "on ", []string{"User", "Project"}, []string{"Role", "Query"}},
		{"object fragment type", "query Q { viewer { ... on ", "on ", []string{"User", "Node"}, []string{"Project", "Query"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := completionAt(t, schema, tt.source, tt.marker)
			for _, label := range tt.want {
				if !hasLabel(got.Items, label) {
					t.Errorf("missing %q in %v", label, labels(got.Items))
				}
			}
			for _, label := range tt.not {
				if hasLabel(got.Items, label) {
					t.Errorf("unexpected %q in %v", label, labels(got.Items))
				}
			}
		})
	}
}

func TestCompletionVariablesAndFragments(t *testing.T) {
	schema := loadTestSchema(t)
	source := "query Q($q: String!) { viewer { ...F } } fragment F on User { id }"
	got := schema.Complete(source, strings.Index(source, "...F")+3)
	if !hasLabel(got.Items, "F") || !hasLabel(got.Items, "on") {
		t.Fatalf("fragment completion = %v", labels(got.Items))
	}
	source = "query Q($q: String!) { search(query: $"
	got = completionAt(t, schema, source, "query: $")
	if !hasLabel(got.Items, "q") {
		t.Fatalf("variable completion = %v", labels(got.Items))
	}
	spread := `query Q { viewer { ... } } fragment P on Project { id }`
	got = schema.Complete(spread, strings.Index(spread, "...")+3)
	if hasLabel(got.Items, "P") {
		t.Fatalf("an unrelated fragment was offered: %v", labels(got.Items))
	}
	spread = `query Q { search(query: "x") { ... } } fragment P on Project { id }`
	got = schema.Complete(spread, strings.Index(spread, "...")+3)
	if !hasLabel(got.Items, "P") {
		t.Fatalf("an overlapping fragment was not offered: %v", labels(got.Items))
	}
}

func TestDiagnostics(t *testing.T) {
	schema := loadTestSchema(t)
	valid := []string{
		"query Q { viewer { id name } }",
		"query Q { me: viewer { id } }",
		"fragment F on Node { id } query Q { node(id: \"1\") { ...F } }",
		"fragment F on User { id name } query Q { node(id: \"1\") { ...F } }",
		"fragment F on SearchResult { ... on User { id } ... on Project { title } } query Q { search(query: \"x\") { ...F } }",
	}
	for _, source := range valid {
		if got := schema.Diagnostics(source); len(got) != 0 {
			t.Errorf("valid document %q has diagnostics: %+v", source, got)
		}
	}
	bad := []struct{ source, want string }{
		{"query Q { viewer { nope } }", "nope"},
		{"query Q { search(q: \"x\") { id } }", "q"},
		{"fragment F on Role { id } query Q { viewer { ...F } }", "Role"},
	}
	for _, tt := range bad {
		got := schema.Diagnostics(tt.source)
		if len(got) == 0 {
			t.Errorf("%q has no diagnostics", tt.source)
			continue
		}
		found := false
		for _, diagnostic := range got {
			if strings.Contains(diagnostic.Message, tt.want) && diagnostic.End > diagnostic.Start {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: no diagnostic mentions %q: %+v", tt.source, tt.want, got)
		}
	}
}

func TestDiagnosticRangeAfterUnicode(t *testing.T) {
	schema := loadTestSchema(t)
	source := "query Q { viewer { id } }\n# 中文 🦊\nquery R { viewer { nope } }"
	got := schema.Diagnostics(source)
	if len(got) != 1 {
		t.Fatalf("diagnostics = %+v", got)
	}
	if source[got[0].Start:got[0].End] != "nope" {
		t.Fatalf("range %d:%d = %q", got[0].Start, got[0].End, source[got[0].Start:got[0].End])
	}
}

func TestHover(t *testing.T) {
	schema := loadTestSchema(t)
	source := "query Q($q: String!) { viewer { name role } }"
	for _, tt := range []struct{ at, want string }{
		{"viewer", "viewer: User!"},
		{"name", "name: String!"},
		{"role", "role: Role!"},
	} {
		offset := strings.Index(source, tt.at)
		got, ok := schema.Hover(source, offset+1)
		if !ok || !strings.Contains(got.Text, tt.want) {
			t.Errorf("hover %q = %q, %v; want %q", tt.at, got.Text, ok, tt.want)
		}
		if tt.at == "viewer" && !strings.Contains(got.Text, "The signed-in user.") {
			t.Errorf("hover %q lost its description: %q", tt.at, got.Text)
		}
	}
	enumSource := "query Q { search(filter: { role: ADMIN }) { id } }"
	if got, ok := schema.Hover(enumSource, strings.Index(enumSource, "ADMIN")+1); !ok || !strings.Contains(got.Text, "Role") {
		t.Errorf("enum hover = %q, %v", got.Text, ok)
	}
	if got, ok := schema.Hover(source, strings.Index(source, "$q")+1); !ok || !strings.Contains(got.Text, "$q: String!") {
		t.Errorf("variable hover = %q, %v", got.Text, ok)
	}
	refSource := `query Q($q: String!) { search(query: $q) { __typename } }`
	if got, ok := schema.Hover(refSource, strings.LastIndex(refSource, "$q")+1); !ok || !strings.Contains(got.Text, "$q: String!") {
		t.Errorf("variable reference hover = %q, %v", got.Text, ok)
	}
	argSource := `query Q { search(query: "x") { __typename } }`
	if got, ok := schema.Hover(argSource, strings.Index(argSource, "query:")+1); !ok || !strings.Contains(got.Text, "query: String!") {
		t.Errorf("argument hover = %q, %v", got.Text, ok)
	}
	fragSource := `query Q { node(id: "1") { ... on User { id } } }`
	if got, ok := schema.Hover(fragSource, strings.Index(fragSource, "User")+1); !ok || !strings.Contains(got.Text, "type User") {
		t.Errorf("fragment type hover = %q, %v", got.Text, ok)
	}
}

func TestVariablesSkeleton(t *testing.T) {
	schema := loadTestSchema(t)
	source := "query Q($q: String!, $limit: Int, $filter: SearchFilter, $role: Role) { viewer { id } }"
	got, err := schema.Variables(source, "Q")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("skeleton is not JSON: %v\n%s", err, got)
	}
	if decoded["q"] != "" || decoded["limit"] != float64(0) || decoded["role"] != "ADMIN" {
		t.Fatalf("skeleton values = %#v", decoded)
	}
	filter, ok := decoded["filter"].(map[string]any)
	if !ok || filter["term"] != "" || filter["role"] != "ADMIN" {
		t.Fatalf("filter = %#v", decoded["filter"])
	}
}

// testIntrospection is a handwritten standard introspection response for the
// same small API. Its expected completions are the SDL fields above.
const testIntrospection = `{
  "data": {
    "__schema": {
      "queryType": {"name": "Query"},
      "mutationType": null,
      "subscriptionType": null,
      "types": [
        {"kind": "OBJECT", "name": "Query", "description": "The root query.", "fields": [
          {"name": "viewer", "description": "The signed-in user.", "args": [], "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "OBJECT", "name": "User", "ofType": null}}, "isDeprecated": false, "deprecationReason": null},
          {"name": "search", "description": "Search projects.", "args": [
            {"name": "query", "description": null, "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}}, "defaultValue": null},
            {"name": "limit", "description": null, "type": {"kind": "SCALAR", "name": "Int", "ofType": null}, "defaultValue": "10"}
          ], "type": {"kind": "LIST", "name": null, "ofType": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "UNION", "name": "SearchResult", "ofType": null}}}, "isDeprecated": false, "deprecationReason": null}
        ], "inputFields": null, "interfaces": [], "enumValues": null, "possibleTypes": null},
        {"kind": "OBJECT", "name": "User", "description": "A person.", "fields": [
          {"name": "id", "description": null, "args": [], "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}}, "isDeprecated": false, "deprecationReason": null},
          {"name": "name", "description": "Display name.", "args": [], "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}}, "isDeprecated": false, "deprecationReason": null},
          {"name": "role", "description": null, "args": [], "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "ENUM", "name": "Role", "ofType": null}}, "isDeprecated": false, "deprecationReason": null}
        ], "inputFields": null, "interfaces": [{"kind": "INTERFACE", "name": "Node", "ofType": null}], "enumValues": null, "possibleTypes": null},
        {"kind": "INTERFACE", "name": "Node", "description": null, "fields": [
          {"name": "id", "description": null, "args": [], "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}}, "isDeprecated": false, "deprecationReason": null}
        ], "inputFields": null, "interfaces": [], "enumValues": null, "possibleTypes": [{"kind": "OBJECT", "name": "User", "ofType": null}]},
        {"kind": "UNION", "name": "SearchResult", "description": null, "fields": null, "inputFields": null, "interfaces": [], "enumValues": null, "possibleTypes": [{"kind": "OBJECT", "name": "User", "ofType": null}]},
        {"kind": "ENUM", "name": "Role", "description": null, "fields": null, "inputFields": null, "interfaces": [], "enumValues": [
          {"name": "ADMIN", "description": null, "isDeprecated": false, "deprecationReason": null},
          {"name": "USER", "description": null, "isDeprecated": false, "deprecationReason": null}
        ], "possibleTypes": null}
      ],
      "directives": [
        {"name": "skip", "description": null, "locations": ["FIELD", "FRAGMENT_SPREAD", "INLINE_FRAGMENT"], "args": []}
      ]
    }
  }
}`

func TestSchemaFromIntrospection(t *testing.T) {
	schema, err := graphql.ParseIntrospection([]byte(testIntrospection))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := schema.TypeCount(), 5; got != want {
		t.Fatalf("TypeCount = %d, want %d\n%s", got, want, schema.SDL())
	}
	got := completionAt(t, schema, "query Q { viewer { na", "na")
	if !hasLabel(got.Items, "name") {
		t.Fatalf("introspection completion = %v", labels(got.Items))
	}
	if diagnostics := schema.Diagnostics("query Q { viewer { nope } }"); len(diagnostics) == 0 {
		t.Fatal("introspection schema did not diagnose an unknown field")
	}
	if !strings.Contains(schema.SDL(), "type User implements Node") || !strings.Contains(schema.SDL(), `viewer: User!`) {
		t.Fatalf("introspection SDL:\n%s", schema.SDL())
	}
}

func TestIntrospectionDisabled(t *testing.T) {
	_, err := graphql.ParseIntrospection([]byte(`{"errors":[{"message":"GraphQL introspection is not allowed"}]}`))
	if err == nil || !strings.Contains(err.Error(), "introspection is disabled") {
		t.Fatalf("disabled introspection error = %v", err)
	}
	if problem := graphql.IntrospectionProblem([]byte(`{"errors":[{"message":"Introspection is disabled by policy"}]}`)); !strings.Contains(problem, "disabled") {
		t.Fatalf("problem = %q", problem)
	}
}

func TestIntrospectionCustomRootType(t *testing.T) {
	data := `{"data":{"__schema":{"queryType":{"name":"Root"},"mutationType":null,"subscriptionType":null,"types":[
		{"kind":"OBJECT","name":"Root","description":null,"fields":[{"name":"viewer","description":null,"args":[],"type":{"kind":"OBJECT","name":"User","ofType":null},"isDeprecated":false,"deprecationReason":null}],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null},
		{"kind":"OBJECT","name":"User","description":null,"fields":[{"name":"id","description":null,"args":[],"type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"ID","ofType":null}},"isDeprecated":false,"deprecationReason":null}],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null}
	],"directives":[]}}}`
	schema, err := graphql.ParseIntrospection([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema.SDL(), "schema {\n  query: Root\n}") {
		t.Fatalf("custom root lost:\n%s", schema.SDL())
	}
	if got := completionAt(t, schema, "query Q { vi", "vi"); !hasLabel(got.Items, "viewer") {
		t.Fatalf("completion = %v", labels(got.Items))
	}
}
