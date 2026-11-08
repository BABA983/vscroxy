package app

import (
	"strings"
	"testing"
	"time"
)

func TestParseOpenAPIRejectsAliasBomb(t *testing.T) {
	source := "openapi: 3.0.0\ninfo: {title: Bomb, version: \"1\"}\n" +
		"base: &b {k: " + strings.Repeat("x", 4000) + "}\n" +
		"paths:\n  /ping:\n    get:\n      responses:\n        \"200\": {description: ok}\n" +
		"items:\n" + strings.Repeat("  - *b\n", 100_000)
	start := time.Now()
	_, err := parseOpenAPI(source)
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond") {
		t.Fatalf("err = %v, want an expansion-limit error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("alias bomb took %s", elapsed)
	}
}

func TestParseOpenAPIAcceptsSmallAlias(t *testing.T) {
	doc, err := parseOpenAPI(`openapi: 3.0.0
info: {title: Anchored, version: "1"}
paths:
  /ping:
    get:
      responses:
        "200": &ok
          description: ok
        "201": *ok
`)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Paths.Find("/ping") == nil {
		t.Errorf("paths = %+v", doc.Paths)
	}
}

func TestImportReportsYAMLAliasBomb(t *testing.T) {
	bomb := "base: &b {k: " + strings.Repeat("x", 4000) + "}\nitems:\n" + strings.Repeat("  - *b\n", 100_000)
	payloads := map[string]string{
		"Swagger":  "swagger: \"2.0\"\ninfo: {title: Bomb, version: \"1\"}\npaths: {}\n" + bomb,
		"OpenAPI":  "openapi: 3.0.0\ninfo: {title: Bomb, version: \"1\"}\npaths: {}\n" + bomb,
		"Insomnia": "type: collection.insomnia.rest/5.0\n" + bomb,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			a := libraryApp(t)
			start := time.Now()
			a.importData([]byte(payload), "bomb.yaml")
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("alias bomb took %s", elapsed)
			}
			if len(a.notices) == 0 {
				t.Fatal("no notice for an alias bomb")
			}
			last := a.notices[len(a.notices)-1]
			if last.level != noticeError || !strings.Contains(last.detail, "YAML expands beyond") {
				t.Errorf("notice = %+v, want an expansion error", last)
			}
		})
	}
}
