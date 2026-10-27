package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestOpenAPIGenerationOmitsValuesAndMergesEndpoints(t *testing.T) {
	requests := []savedRequest{
		{Name: "a secret-query", Collection: "Store", Method: "GET", URL: "https://user:secret-password@api.example.test/v1/items/{{id}}?token=secret-query&limit=7", Headers: "Authorization: Bearer secret-auth\nCookie: session=secret-cookie\nX-Tenant: secret-header", Post: "secret-script"},
		{Name: "b", Collection: "Store", Method: "GET", URL: "https://staging.example.test/v1/items/{{id}}?offset=19&limit=9"},
		{Name: "c", Collection: "Store", Method: "POST", URL: "{{api_host}}/v1/items", Headers: "Content-Type: multipart/form-data; boundary=secret-boundary", Body: "secret-body"},
		{Name: "d", Collection: "Store", Method: "GET", URL: "https://api.example.test/v1/items/42"},
		{Name: "e", Collection: "Other", Method: "DELETE", URL: "https://other.example.test/not-included"},
		{Name: "f", Collection: "Store", Method: "CONNECT", URL: "https://api.example.test/"},
	}
	spec, err := generateOpenAPI(requests, "Store")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-query", "secret-password", "secret-auth", "secret-cookie", "secret-header", "secret-script", "secret-boundary", "secret-body", "not-included"} {
		if strings.Contains(spec.Source+strings.Join(spec.Notes, "\n"), secret) {
			t.Fatalf("generated specification leaked %q", secret)
		}
	}
	doc, err := parseOpenAPI(spec.Source)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.0.3" || len(apiOperations(doc)) != 3 || doc.Paths.Value("/v1/items/42") == nil {
		t.Fatal("generation changed literal paths, included another collection or failed to merge duplicates")
	}
	op := doc.Paths.Value("/v1/items/{id}").Get
	if op.Servers == nil || len(*op.Servers) != 2 || (*op.Servers)[0].URL != "https://api.example.test" || (*op.Servers)[1].URL != "https://staging.example.test" {
		t.Fatal("merged endpoints lost a server or retained URL credentials")
	}
	for _, name := range []string{"limit", "offset", "token"} {
		p := op.Parameters.GetByInAndName("query", name)
		if p == nil || p.Required || !p.Schema.Value.Type.Is("string") || p.Example != nil {
			t.Fatalf("query parameter %s was lost or incorrectly inferred", name)
		}
	}
	if p := op.Parameters.GetByInAndName("path", "id"); p == nil || !p.Required {
		t.Fatal("template path parameter must be required")
	}
	if op.Parameters.GetByInAndName("header", "Authorization") != nil || op.Responses.Value("200") != nil || op.Responses.Default() == nil {
		t.Fatal("generation invented authentication or a successful response")
	}
	body := doc.Paths.Value("/v1/items").Post.RequestBody.Value.Content["multipart/form-data"]
	if body == nil || body.Example != nil || len(body.Schema.Value.Required) > 0 || len(body.Schema.Value.Properties) > 0 {
		t.Fatal("a captured body must not be mistaken for a complete payload schema")
	}
	if _, err := generateOpenAPI(requests, "Missing"); err == nil {
		t.Fatal("empty collection was reported as a generated API")
	}
}

const referencedOpenAPI = `# Keep this comment and extensions when saving.
openapi: 3.1.0
info: {title: Referenced API, version: 2.7.0}
x-owner: client-team
servers: [{url: 'https://root.example.test/wrong'}]
paths:
  /widgets/{id}:
    servers: [{url: 'https://path.example.test/also-wrong'}]
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
      - {name: ignored, in: query, required: true, schema: {type: string}}
    post:
      operationId: updateWidget
      servers:
        - url: 'https://{region}.example.test/v2'
          variables: {region: {default: eu}}
      parameters:
        - {name: ignored, in: query, required: false, schema: {type: string}}
        - {name: limit, in: query, required: true, schema: {type: integer}}
        - {name: X-Trace, in: header, required: true, schema: {type: string}}
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Payload'}
            examples: {sample: {$ref: '#/components/examples/Payload'}}
      responses: {'201': {description: Created}}
components:
  schemas:
    Payload:
      type: object
      properties: {count: {type: [integer, 'null']}}
  examples:
    Payload: {value: {count: 7}}
`

func TestOpenAPIRequestUsesEffectiveServerParametersAndReferencedExample(t *testing.T) {
	doc, err := parseOpenAPI(referencedOpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	r := requestFromOperation(doc, apiOperations(doc)[0])
	if r.Method != "POST" || r.URL != "https://eu.example.test/v2/widgets/{{id}}?limit={{limit}}" || r.Name != "updateWidget" || r.Collection != "Referenced API" {
		t.Fatalf("incorrect executable draft: %+v", r)
	}
	if h := request.ParseHeaderBlock(r.Headers); h.Get("Content-Type") != "application/json" || h.Get("X-Trace") != "{{X-Trace}}" {
		t.Fatalf("required headers missing: %v", h)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(r.Body), &payload); err != nil || payload.Count != 7 {
		t.Fatalf("referenced example was not preserved: %s (%v)", r.Body, err)
	}
	doc.Servers, doc.Paths.Value("/widgets/{id}").Servers, doc.Paths.Value("/widgets/{id}").Post.Servers = nil, nil, nil
	if r := requestFromOperation(doc, apiOperations(doc)[0]); !strings.HasPrefix(r.URL, "{{base_url}}/widgets/") {
		t.Fatalf("missing server should require an environment URL, not guess a host: %s", r.URL)
	}
}

func TestOpenAPIValidationRejectsExternalRefsAndInvalidContracts(t *testing.T) {
	var fetched atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fetched.Add(1) }))
	defer server.Close()
	for _, ref := range []string{server.URL + "/schema.json", "file:///tmp/private-schema.json"} {
		source := strings.Replace(referencedOpenAPI, "#/components/schemas/Payload", ref, 1)
		if _, err := parseOpenAPI(source); err == nil || !strings.Contains(err.Error(), "disallowed external reference") {
			t.Fatalf("external ref %s was not refused: %v", ref, err)
		}
	}
	if fetched.Load() != 0 {
		t.Fatal("validation made a network request")
	}
	for _, source := range []string{
		`{"swagger":"2.0","info":{"title":"Old","version":"1"},"paths":{}}`,
		`{"openapi":"3.2.0","info":{"title":"New","version":"1"},"paths":{}}`,
		`{"openapi":"3.0.3","info":{"title":"Broken","version":"1"},"paths":{"/a":{"get":{}}}}`,
	} {
		if _, err := parseOpenAPI(source); err == nil {
			t.Fatalf("unsupported or invalid document was accepted: %s", source)
		}
	}
}
