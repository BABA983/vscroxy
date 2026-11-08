package yamlguard_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/yamlguard"
	"github.com/oasdiff/yaml"
)

func aliasBomb() []byte {
	return []byte("base: &b {k: " + strings.Repeat("x", 4000) + "}\nitems:\n" +
		strings.Repeat("  - *b\n", 100_000))
}

// escapedAliasBomb repeats a '<' scalar. Its expanded JSON is about six times
// larger than the scalar, which is the case the byte budget must catch.
func escapedAliasBomb() []byte {
	return []byte("base: &b \"" + strings.Repeat("<", 71_000) + "\"\nitems:\n" +
		strings.Repeat("  - *b\n", 150))
}

func TestRejectAliases(t *testing.T) {
	_, err := yamlguard.ToJSON([]byte("base: &b {k: v}\ncopy: *b\n"), yamlguard.Options{RejectAliases: true})
	if !errors.Is(err, yamlguard.ErrAlias) {
		t.Fatalf("err = %v, want ErrAlias", err)
	}
}

func TestExpansionLimitStopsAliasBomb(t *testing.T) {
	start := time.Now()
	_, err := yamlguard.ToJSON(aliasBomb(), yamlguard.Options{MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond") {
		t.Fatalf("err = %v, want an expansion-limit error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("alias bomb took %s", elapsed)
	}
}

func TestEscapedAliasBombRejected(t *testing.T) {
	start := time.Now()
	_, err := yamlguard.ToJSON(escapedAliasBomb(), yamlguard.Options{})
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond") {
		t.Fatalf("err = %v, want an expansion-limit error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("escaped alias bomb took %s", elapsed)
	}
}

func TestBlockScalarsMatchYAMLToJSON(t *testing.T) {
	documents := []string{
		"s: |\n\n  abc\n",
		"s: >\n  a\n    b\n  c\n",
	}
	for _, document := range documents {
		want, err := yaml.YAMLToJSON([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		got, err := yamlguard.ToJSON([]byte(document), yamlguard.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("ToJSON(%q) = %s, want %s", document, got, want)
		}
	}
}

func TestAliasDoesNotChargeOriginalDocument(t *testing.T) {
	var document strings.Builder
	document.WriteString("base: &b {x: 1}\nuse: *b\nitems:\n")
	line := "  - " + strings.Repeat("a", 200) + "\n"
	for document.Len() < 4<<20 {
		document.WriteString(line)
	}
	data := []byte(document.String())
	got, err := yamlguard.ToJSON(data, yamlguard.Options{})
	if err != nil {
		t.Fatalf("a small alias rejected the original document: %v", err)
	}
	want, err := yaml.YAMLToJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ToJSON output differs from YAMLToJSON for a 4 MiB document with one alias")
	}
}

func TestNoAliasLargeListParses(t *testing.T) {
	// 1.3 million items is both a >1M-node document and a >16 MiB input.
	document := strings.Repeat("- 1234567890\n", 1_300_000)
	start := time.Now()
	out, err := yamlguard.ToJSON([]byte(document), yamlguard.Options{MaxNodes: 2, MaxBytes: 1})
	if err != nil {
		t.Fatalf("a plain list was limited: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty JSON")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("plain list took %s", elapsed)
	}
}

func TestNoAliasDeepNestingParses(t *testing.T) {
	var document strings.Builder
	for depth := range 99 {
		document.WriteString(strings.Repeat("  ", depth))
		document.WriteString("level:\n")
	}
	document.WriteString(strings.Repeat("  ", 99))
	document.WriteString("value: 1\n")
	if _, err := yamlguard.ToJSON([]byte(document.String()), yamlguard.Options{MaxNodes: 1, MaxBytes: 1}); err != nil {
		t.Fatalf("a plain nested document was limited: %v", err)
	}
}

func TestNoAliasLargeScalarParses(t *testing.T) {
	document := "s: |\n  " + strings.Repeat("x", 16<<20) + "\n"
	out, err := yamlguard.ToJSON([]byte(document), yamlguard.Options{MaxNodes: 1, MaxBytes: 1})
	if err != nil {
		t.Fatalf("a plain large scalar was limited: %v", err)
	}
	if len(out) < 16<<20 {
		t.Fatalf("JSON is %d bytes, want at least 16 MiB", len(out))
	}
}

func TestSmallAliasAccepted(t *testing.T) {
	out, err := yamlguard.ToJSON([]byte("base: &b {k: v}\ncopy: *b\n"), yamlguard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Base map[string]string `json:"base"`
		Copy map[string]string `json:"copy"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Base["k"] != "v" || doc.Copy["k"] != "v" {
		t.Errorf("doc = %+v", doc)
	}
}

func TestRecursiveAliasRejected(t *testing.T) {
	_, err := yamlguard.ToJSON([]byte("a: &a [*a]\n"), yamlguard.Options{})
	if err == nil || !strings.Contains(err.Error(), "recursive alias") {
		t.Fatalf("err = %v, want a recursive-alias error", err)
	}
}

func TestNodeLimit(t *testing.T) {
	_, err := yamlguard.ToJSON([]byte("base: &b [a, b, c]\ncopy: *b\n"), yamlguard.Options{MaxNodes: 2})
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond 2 nodes") {
		t.Fatalf("err = %v, want a node-limit error", err)
	}
}
