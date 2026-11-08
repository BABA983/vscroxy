package postman_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/postman"
)

func TestV3ExampleWithoutResponseBody(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: No body\n",
		"A.request.yaml":             "$kind: http-request\nname: A\nurl: https://example.com/a\nexamples: ./.resources/A.resources/examples\n",
		".resources/A.resources/examples/x.example.yaml": "$kind: http-example\nname: x\n" +
			"response:\n  statusCode: 204\n  statusText: No Content\n",
	})
	col := readV3(t, dir)
	a := v3Request(t, col, "No body", "A")
	if len(a.Examples) != 1 || a.Examples[0].Status != 204 || a.Examples[0].Name != "x" || a.Examples[0].Body != "" {
		t.Errorf("examples = %+v", a.Examples)
	}
}

func TestV3RejectsYAMLAliases(t *testing.T) {
	dir := t.TempDir()
	payload := "$kind: http-request\nname: Alias\nurl: https://example.com/a\n" +
		"base: &b {k: " + strings.Repeat("x", 4000) + "}\n" +
		"headers:\n" + strings.Repeat("  - *b\n", 100_000)
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Aliases\n",
		"A.request.yaml":             payload,
	})
	start := time.Now()
	col := readV3(t, dir)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("alias payload took %s", elapsed)
	}
	if len(col.Requests) != 0 {
		t.Fatalf("alias request was imported: %+v", col.Requests)
	}
	if !slices.Contains(col.Warnings, "Skipped 1 unreadable or malformed file (first: A.request.yaml)") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestV3ResourcesSymlinkIsNotFollowed(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		dir, outside := t.TempDir(), t.TempDir()
		writeV3(t, outside, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Evil\nvariables:\n  - key: stolen\n    value: yes\n",
		})
		writeV3(t, dir, map[string]string{
			"One.request.yaml": "$kind: http-request\nname: One\nurl: https://example.com/one\n",
		})
		if err := os.Symlink(filepath.Join(outside, ".resources"), filepath.Join(dir, ".resources")); err != nil {
			t.Fatal(err)
		}
		col := readV3(t, dir)
		if col.Name == "Evil" || len(col.Folders) != 0 {
			t.Errorf("followed the .resources symlink: name %q folders %+v", col.Name, col.Folders)
		}
		if !slices.Contains(col.Warnings, "Skipped 1 symbolic link, non-regular file or path outside the collection") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("folder", func(t *testing.T) {
		dir, outside := t.TempDir(), t.TempDir()
		writeV3(t, outside, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Evil\n",
		})
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Root\n",
			"Sub/One.request.yaml":       "$kind: http-request\nname: One\nurl: https://example.com/one\n",
		})
		if err := os.Symlink(filepath.Join(outside, ".resources"), filepath.Join(dir, "Sub", ".resources")); err != nil {
			t.Fatal(err)
		}
		col := readV3(t, dir)
		one := v3Request(t, col, "Root/Sub", "One")
		if one.URL != "https://example.com/one" {
			t.Errorf("request = %+v", one)
		}
		if !slices.Contains(col.Warnings, "Skipped 1 symbolic link, non-regular file or path outside the collection") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})
}

func TestV3GraphQLScripts(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: GQL\nscripts:\n" +
			"  - type: graphql:beforeQuery\n    code: console.log('root');\n",
		"Folder/.resources/definition.yaml": "$kind: collection\nname: Folder\nscripts:\n" +
			"  - type: graphql:beforeQuery\n    code: console.log('folder');\n",
		"Folder/Query.request.yaml": "$kind: graphql-request\nname: Query\nurl: https://example.com/graphql\n" +
			"query: '{ ping }'\nscripts:\n" +
			"  - type: beforeQuery\n    code: console.log('request');\n" +
			"  - type: afterResponse\n    code: console.log('after');\n",
		"HTTP.request.yaml": "$kind: http-request\nname: HTTP\nurl: https://example.com/http\nscripts:\n" +
			"  - type: beforeRequest\n    code: print('python')\n    language: text/python\n",
	})
	col := readV3(t, dir)
	query := v3Request(t, col, "GQL/Folder", "Query")
	if !strings.Contains(query.Pre, "console.log('request')") || !strings.Contains(query.Post, "console.log('after')") {
		t.Errorf("GraphQL request scripts = %q / %q", query.Pre, query.Post)
	}
	http := v3Request(t, col, "GQL", "HTTP")
	if strings.Contains(http.Pre, "root") || strings.Contains(http.Pre, "folder") || http.Pre != "" {
		t.Errorf("GraphQL collection scripts leaked into HTTP: %q", http.Pre)
	}
	if !slices.Contains(col.Warnings, "Skipped 2 graphql:beforeQuery scripts; vscroxy does not run them") {
		t.Errorf("warnings = %q", col.Warnings)
	}
	if !slices.Contains(col.Warnings, "Skipped 1 script written in text/python") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestV3DuplicateRequestIDs(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Dups\n",
		"A.request.yaml":             "$kind: http-request\nid: 5d1c\nname: A\nurl: https://example.com/a\n",
		"B.request.yaml":             "$kind: http-request\nid: 5d1c\nname: B\nurl: https://example.com/b\n",
	})
	col := readV3(t, dir)
	if len(col.Requests) != 2 {
		t.Fatalf("requests = %+v", col.Requests)
	}
	if col.Requests[0].ID == col.Requests[1].ID {
		t.Errorf("duplicate id survived: %q", col.Requests[0].ID)
	}
	if !slices.Contains(col.Warnings, "Duplicate request id 5d1c was replaced with a derived id") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestV3ObjectKeyValueForms(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Objects\n",
		"HTTP.request.yaml": "$kind: http-request\nname: HTTP\nurl: 'https://example.com/x?q=shoes'\n" +
			"headers:\n  Content-Type: application/json\nqueryParams:\n  q: shoes\n",
		"GRPC.request.yaml": "$kind: grpc-request\nname: GRPC\nurl: https://example.com\n" +
			"methodPath: demo.Echo/Say\nmetadata:\n  x-tag: one\n",
	})
	col := readV3(t, dir)
	http := v3Request(t, col, "Objects", "HTTP")
	if http.Headers != "Content-Type: application/json" || http.URL != "https://example.com/x?q=shoes" {
		t.Errorf("HTTP object form = %+v", http)
	}
	grpc := v3Request(t, col, "Objects", "GRPC")
	if grpc.Headers != "x-tag: one" {
		t.Errorf("gRPC object metadata = %q", grpc.Headers)
	}
}

func TestV3UnreadableNamesFirstFile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{".resources/definition.yaml": "$kind: collection\nname: Broken\n"}
	for i := range 8 {
		files[fmt.Sprintf("bad%d.request.yaml", i)] = "not: [valid\n"
	}
	writeV3(t, dir, files)
	col := readV3(t, dir)
	if !slices.Contains(col.Warnings, "Skipped 8 unreadable or malformed files (first: bad0.request.yaml)") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestV3MessageOrder(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Streams\n",
		"One.request.yaml": "$kind: websocket-request\nname: One\nurl: wss://example.com/stream\n" +
			"messages: ./.resources/One.resources/messages\n",
	}
	for i := range 6 {
		files[fmt.Sprintf(".resources/One.resources/messages/m%d.message.yaml", i)] =
			fmt.Sprintf("$kind: websocket-message\nname: m%d\ncontent: m%d\ncontentType: text\n", i, i)
	}
	writeV3(t, dir, files)
	col := readV3(t, dir)
	one := v3Request(t, col, "Streams", "One")
	if one.Body != "m0" {
		t.Errorf("first message = %q, want m0", one.Body)
	}
	if !slices.Contains(col.Warnings, "Dropped 5 extra saved stream messages; vscroxy sends one at a time") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestV3GraphQLExampleKeepsRequest(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: GQL\n",
		"One.request.yaml": "$kind: graphql-request\nname: One\nurl: https://example.com/graphql\n" +
			"query: '{ ping }'\nexamples: ./.resources/One.resources/examples\n",
		".resources/One.resources/examples/ok.example.yaml": "$kind: graphql-example\nname: ok\n" +
			"request:\n  url: https://example.com/graphql\n" +
			"  query: 'query Q($id: ID!) { order(id: $id) { id } }'\n" +
			"  variables: '{\"id\":\"1\"}'\n  operationName: Q\n" +
			"response:\n  events:\n    - id: e1\n      type: received-response\n" +
			"      content: '{\"data\":{\"order\":{\"id\":\"1\"}}}'\n",
	})
	col := readV3(t, dir)
	one := v3Request(t, col, "GQL", "One")
	if len(one.Examples) != 1 {
		t.Fatalf("examples = %+v", one.Examples)
	}
	body := one.Examples[0].Request.Body
	if !strings.Contains(body, `"query"`) || !strings.Contains(body, `"variables"`) ||
		!strings.Contains(body, `"operationName"`) || !strings.Contains(body, `"id":"1"`) {
		t.Errorf("GraphQL example request body = %q", body)
	}
}

func TestV3FolderDefinitionKindIsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml":     "$kind: collection\nname: Root\n",
		"Sub/.resources/definition.yaml": "$kind: environment\nname: Bad\n",
		"Sub/One.request.yaml":           "$kind: http-request\nname: One\nurl: https://example.com/one\n",
	})
	col := readV3(t, dir)
	if len(col.Requests) != 1 || col.Requests[0].Collection != "Root/Sub" {
		t.Errorf("requests = %+v", col.Requests)
	}
	if !slices.Contains(col.Warnings, "Skipped 1 definition whose $kind is not collection (first: Sub/.resources/definition.yaml)") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestV3DeepDetectionWithoutDefinition(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		"A/B/One.request.yaml": "$kind: http-request\nname: One\nurl: https://example.com/one\n",
	})
	if !postman.LooksLikeCollectionDir(dir) {
		t.Error("a collection with requests two levels down was not recognized")
	}
}
