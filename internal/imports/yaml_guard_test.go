package imports

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"
)

func aliasBombYAML() []byte {
	return []byte("base: &b {k: " + strings.Repeat("x", 4000) + "}\nitems:\n" +
		strings.Repeat("  - *b\n", 100_000))
}

func swaggerAliasBomb() []byte {
	return []byte("swagger: \"2.0\"\ninfo: {title: Bomb, version: \"1\"}\n" +
		"base: &b {k: " + strings.Repeat("x", 4000) + "}\n" +
		"paths:\n  /ping:\n    get:\n      responses:\n        \"200\": {description: ok}\n" +
		"items:\n" + strings.Repeat("  - *b\n", 100_000))
}

func TestSwagger2RejectsAliasBomb(t *testing.T) {
	start := time.Now()
	_, _, err := ReadSwagger2(swaggerAliasBomb())
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond") {
		t.Fatalf("err = %v, want an expansion-limit error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("alias bomb took %s", elapsed)
	}
}

func TestSwagger2AcceptsSmallAlias(t *testing.T) {
	doc, _, err := ReadSwagger2([]byte(`swagger: "2.0"
info: {title: Anchored, version: "1"}
host: api.example.com
basePath: /v1
schemes: [https]
paths:
  /ping:
    get:
      responses: &ok
        "200": {description: ok}
      x-copy: *ok
definitions:
  Pet: &pet
    type: object
    properties: {id: {type: string}}
  PetCopy: *pet
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Paths.Find("/ping") == nil {
		t.Errorf("paths = %+v", doc.Paths)
	}
}

func TestInsomniaRejectsAliasBomb(t *testing.T) {
	start := time.Now()
	_, err := ReadInsomnia(aliasBombYAML())
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond") {
		t.Fatalf("err = %v, want an expansion-limit error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("alias bomb took %s", elapsed)
	}
}

func TestDetectRejectsAliasBomb(t *testing.T) {
	start := time.Now()
	if got := Detect(aliasBombYAML()); got != Unknown {
		t.Fatalf("Detect = %v, want Unknown", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("alias bomb took %s", elapsed)
	}
}

func TestSwagger2JSONInput(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"swagger": "2.0",
		"info": map[string]any{
			"title":       "JSON Swagger",
			"version":     "1",
			"description": strings.Repeat("x", 2<<20),
		},
		"paths": map[string]any{
			"/ping": map[string]any{
				"get": map[string]any{
					"responses": map[string]any{"200": map[string]any{"description": "ok"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := ReadSwagger2(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info == nil || doc.Info.Title != "JSON Swagger" || doc.Paths.Find("/ping") == nil {
		t.Errorf("doc = %+v", doc)
	}
}

func TestDetectWithErrorReportsAliasBomb(t *testing.T) {
	format, err := DetectWithError(aliasBombYAML())
	if format != Unknown || err == nil || !strings.Contains(err.Error(), "YAML expands beyond") {
		t.Fatalf("DetectWithError = %v, %v; want Unknown and an expansion error", format, err)
	}
}
