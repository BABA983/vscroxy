package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// appGraphQLSchema is the schema the UI tests and snapshots load. It is SDL,
// so expected fields are the ones written here.
const appGraphQLSchema = `
type Query {
  viewer: User!
  search(query: String!, limit: Int = 10, filter: SearchFilter): [SearchResult!]!
  node(id: ID!): Node
}

interface Node { id: ID! }

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

enum Role { ADMIN USER }

input SearchFilter { term: String!, role: Role }

type Mutation { saveItem(input: SaveInput!): User! }

input SaveInput { name: String!, role: Role }
`

const appIntrospection = `{"data":{"__schema":{
  "queryType":{"name":"Query"},"mutationType":null,"subscriptionType":null,
  "types":[
    {"kind":"OBJECT","name":"Query","description":null,"fields":[
      {"name":"viewer","description":null,"args":[],"type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"OBJECT","name":"User","ofType":null}},"isDeprecated":false,"deprecationReason":null}
    ],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null},
    {"kind":"OBJECT","name":"User","description":null,"fields":[
      {"name":"id","description":null,"args":[],"type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"ID","ofType":null}},"isDeprecated":false,"deprecationReason":null}
    ],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null}
  ],
  "directives":[]
}}}`

func graphQLDraft(t *testing.T) (*app, *requestDocument) {
	t.Helper()
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeTab, d.bodyMode = composeTabBody, "GraphQL"
	d.composeURL = "https://api.example.com/graphql"
	return a, d
}

func TestGraphQLSchemaFetchUsesSendPath(t *testing.T) {
	var got struct {
		method, auth, contentType, query, operationName string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.method, got.auth, got.contentType = r.Method, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		var envelope struct {
			Query         string `json:"query"`
			OperationName string `json:"operationName"`
		}
		_ = json.Unmarshal(body, &envelope)
		got.query, got.operationName = envelope.Query, envelope.OperationName
		_, _ = io.WriteString(w, appIntrospection)
	}))
	defer upstream.Close()
	a, d := graphQLDraft(t)
	a.srv = requestProxy(t)
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"token": "schema-token"}}}
	a.activeEnv = "Local"
	d.composeURL, d.composeMethod = upstream.URL+"/graphql", "POST"
	d.auth = request.Auth{Type: "Bearer", Token: "{{token}}"}
	d.composeHeaders = "Content-Type: application/graphql"
	// A schema read must not run scripts or checks: this would replace the body
	// and fail the status check if it did.
	d.composePre, d.checks = "pm.request.body.update('not introspection');", request.Checks{Status: "599"}

	a.fetchGraphQLSchema(d)
	deadline := time.Now().Add(10 * time.Second)
	for d.graphqlState().loading != nil && time.Now().Before(deadline) {
		a.drainGraphQL()
		time.Sleep(5 * time.Millisecond)
	}
	g := d.graphqlState()
	if g.loading != nil {
		t.Fatal("schema fetch did not settle")
	}
	if g.problem != "" {
		t.Fatalf("schema fetch problem: %s", g.problem)
	}
	if !strings.Contains(d.graphql.Schema, "type Query") || !strings.Contains(d.graphql.Schema, "viewer: User!") {
		t.Fatalf("saved schema:\n%s", d.graphql.Schema)
	}
	if d.graphql.SchemaSource != d.composeURL {
		t.Fatalf("schema source = %q", d.graphql.SchemaSource)
	}
	if got.method != "POST" || got.auth != "Bearer schema-token" || got.contentType != "application/json" {
		t.Fatalf("introspection request = %+v", got)
	}
	if got.operationName != "IntrospectionQuery" || !strings.Contains(got.query, "__schema") {
		t.Fatalf("introspection envelope = %+v", got)
	}
}

func TestGraphQLSchemaFetchReportsDisabledIntrospection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"errors":[{"message":"GraphQL introspection is not allowed"}]}`)
	}))
	defer upstream.Close()
	a, d := graphQLDraft(t)
	a.srv = requestProxy(t)
	d.composeURL, d.composeMethod = upstream.URL+"/graphql", "POST"
	d.graphql.Schema = appGraphQLSchema

	a.fetchGraphQLSchema(d)
	deadline := time.Now().Add(10 * time.Second)
	for d.graphqlState().loading != nil && time.Now().Before(deadline) {
		a.drainGraphQL()
		time.Sleep(5 * time.Millisecond)
	}
	g := d.graphqlState()
	if !strings.Contains(g.problem, "introspection is disabled") || !strings.Contains(g.problem, "not allowed") {
		t.Fatalf("problem = %q", g.problem)
	}
	if d.graphql.Schema != appGraphQLSchema {
		t.Fatal("a failed refresh replaced the saved schema")
	}
}

func TestGraphQLOperationSelectionAndSend(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Query = `query Read { viewer { id } } mutation Write { saveItem(input: {name: "x"}) { id } }`
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("GraphQL operation"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Write"); err != nil {
		t.Fatal(err)
	}
	if d.graphql.OperationName != "Write" {
		t.Fatalf("operationName = %q", d.graphql.OperationName)
	}
	prepared, err := d.draft().Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.Body, `"operationName":"Write"`) {
		t.Fatalf("sent body = %s", prepared.Body)
	}
}

func TestGraphQLSchemaCompletionHoverAndDiagnostics(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Schema = appGraphQLSchema
	d.graphql.Query = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("GraphQL query"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"q", "u", "e", "r", "y", " ", "Q", " ", "{", " ", "v", "i"} {
		tt.Type(text)
		tt.Frame()
	}
	if !tt.HasText("viewer") {
		t.Fatalf("no schema completion: %v", tt.Texts())
	}
	if err := tt.Click("viewer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.graphql.Query, "viewer") {
		t.Fatalf("completion wrote %q", d.graphql.Query)
	}

	d.graphql.Query = "query Q {\n  viewer {\n    na\n  }\n}"
	tt.Frame()
	d.queryEditor.OnCompletion(editor.CompletionRequest{Offset: strings.Index(d.graphql.Query, "na") + 2})
	tt.Frame()
	if !tt.HasText("name") {
		t.Fatalf("no nested completion: %v", tt.Texts())
	}

	d.graphql.Query = "query Q { viewer { name } }"
	tt.Frame()
	d.queryEditor.OnHover(editor.HoverRequest{Offset: strings.Index(d.graphql.Query, "name") + 1})
	tt.Frame()
	if !tt.HasText("name: String!") {
		t.Fatalf("no schema hover: %v", tt.Texts())
	}

	d.graphql.Query = "query Q { viewer { nope } }"
	tt.Frame()
	diagnostics := d.queryEditor.Diagnostics()
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "nope") {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestGraphQLVariablesSkeleton(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Schema = appGraphQLSchema
	d.graphql.Query = `query Q($q: String!, $limit: Int, $filter: SearchFilter) { search(query: $q, limit: $limit, filter: $filter) { {{fields}} } }`
	d.graphqlTab = 1
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("Generate variables"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.Contains(d.graphql.Variables, `"q": ""`) || !strings.Contains(d.graphql.Variables, `"limit": 0`) || !strings.Contains(d.graphql.Variables, `"filter"`) {
		t.Fatalf("variables skeleton = %s", d.graphql.Variables)
	}
	if d.varsEditor == nil || d.varsEditor.Buffer.String() != d.graphql.Variables {
		t.Fatal("variables editor did not rebuild from the skeleton")
	}
}

func TestGraphQLSavedSchemaSurvivesReload(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Schema, d.graphql.SchemaSource = appGraphQLSchema, "https://api.example.com/graphql"
	d.graphql.OperationName = "Write"
	r := d.value()
	if r.GraphQL.Schema != appGraphQLSchema || r.GraphQL.SchemaSource != "https://api.example.com/graphql" || r.GraphQL.OperationName != "Write" {
		t.Fatalf("saved GraphQL body = %+v", r.GraphQL)
	}
	loaded := a.newDocument(r, "")
	if loaded.graphql.Schema != appGraphQLSchema || loaded.graphql.OperationName != "Write" {
		t.Fatalf("reloaded GraphQL body = %+v", loaded.graphql)
	}
	if schema := loaded.graphqlSchema(); schema == nil || schema.TypeCount() == 0 {
		t.Fatal("reloaded schema did not parse")
	}
}

func TestGraphQLOperationAutoSelectsSingle(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Query = "query Only { viewer { id } }"
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if d.graphql.OperationName != "" {
		t.Fatalf("a view pass must not dirty the request: %q", d.graphql.OperationName)
	}
	prepared, err := d.draft().Prepare(nil)
	if err != nil || !strings.Contains(prepared.Body, `"operationName":"Only"`) {
		t.Fatalf("single-operation body = %s, %v", prepared.Body, err)
	}
	d.graphql.Query = "{ viewer { id } }"
	tt.Frame()
	prepared, err = d.draft().Prepare(nil)
	if err != nil || strings.Contains(prepared.Body, "operationName") {
		t.Fatalf("anonymous body = %s, %v", prepared.Body, err)
	}
}

func TestGraphQLSchemaFetchHTTPErrorNamesIntrospection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errors":[{"message":"Introspection is not allowed"}]}`)
	}))
	defer upstream.Close()
	a, d := graphQLDraft(t)
	a.srv = requestProxy(t)
	d.composeURL, d.composeMethod = upstream.URL+"/graphql", "POST"

	a.fetchGraphQLSchema(d)
	deadline := time.Now().Add(10 * time.Second)
	for d.graphqlState().loading != nil && time.Now().Before(deadline) {
		a.drainGraphQL()
		time.Sleep(5 * time.Millisecond)
	}
	g := d.graphqlState()
	if !strings.Contains(g.problem, "HTTP 400") || !strings.Contains(g.problem, "introspection is disabled") {
		t.Fatalf("problem = %q", g.problem)
	}
}

func TestGraphQLSingleOperationDoesNotDirtySavedRequest(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{ID: "only", Name: "Only", Method: "POST", URL: "https://api.example.com/graphql",
		BodyMode: "GraphQL", GraphQL: request.GraphQLBody{Query: "query Only { viewer { id } }"}}, "saved:only")
	d := a.requestDocument
	d.composeTab = composeTabBody
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if d.dirty() {
		t.Fatal("opening a single-operation request marked it edited")
	}
	if !tt.HasText("Operation · Only") {
		t.Fatalf("single operation not shown: %v", tt.Texts())
	}
}

func TestGraphQLOperationSelectorShowsPlaceholder(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Query = "query Read { viewer { id } } mutation Write { saveItem(input: {name: \"x\"}) { id } }"
	d.graphql.OperationName = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText("Select operation") {
		t.Fatalf("multi-operation selector has no placeholder: %v", tt.Texts())
	}
}

func TestGraphQLOperationsIgnoreTemplates(t *testing.T) {
	_, d := graphQLDraft(t)
	d.graphql.Query = "query Read { {{field}} } mutation Write { {{field}} }"
	ops, err := d.graphqlOperations()
	if err != nil || len(ops) != 2 || ops[0].Name != "Read" || ops[1].Name != "Write" {
		t.Fatalf("operations = %+v, %v", ops, err)
	}
}

func TestGraphQLVariablesSkeletonKeepsAuthoredValues(t *testing.T) {
	a, d := graphQLDraft(t)
	d.graphql.Schema = appGraphQLSchema
	d.graphql.Query = `query Q($q: String!) { viewer { id } }`
	d.graphql.Variables = `{"q":"authored"}`
	a.generateGraphQLVariables(d, d.graphqlSchema(), []graphql.Operation{{Name: "Q", Type: "query"}})
	if d.graphql.Variables != `{"q":"authored"}` {
		t.Fatalf("authored variables were overwritten: %s", d.graphql.Variables)
	}
}
