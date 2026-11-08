package yamlguard_test

import (
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/yamlguard"
)

func aliasBomb() []byte {
	return []byte("base: &b {k: " + strings.Repeat("x", 4000) + "}\nitems:\n" +
		strings.Repeat("  - *b\n", 100_000))
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
	_, err := yamlguard.ToJSON([]byte("- a\n- b\n- c\n"), yamlguard.Options{MaxNodes: 2})
	if err == nil || !strings.Contains(err.Error(), "YAML expands beyond 2 nodes") {
		t.Fatalf("err = %v, want a node-limit error", err)
	}
}
