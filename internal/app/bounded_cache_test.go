package app

import (
	"fmt"
	"testing"

	"github.com/baba983/vscroxy/internal/fuzzy"
)

// The markers below are written into the slice a cached value holds. A hit
// returns the same underlying slice; a miss rebuilds it without the marker.
// Capacities are spelled out instead of named by the new constants so these
// tests also compile against the old clear-all implementation.

func TestPaletteAssetsEvictOnlyLeastRecent(t *testing.T) {
	a := workspaceApp()
	a.requests[0].Name = "Cache hit request"
	prepared := fuzzy.PrepareQuery("Cache hit request")
	const firstMark, secondMark = "first-palette-mark", "second-palette-mark"

	for gen := range paletteAssetCacheLimit {
		a.requestGen = gen
		result := a.paletteAssetsFor(prepared, paletteRequestsOnly)
		if len(result.candidates) == 0 {
			t.Fatalf("generation %d: query matched no asset", gen)
		}
		if gen == 0 {
			result.candidates[0].label = firstMark
		}
		if gen == 1 {
			result.candidates[0].label = secondMark
		}
	}

	// Make the oldest key the newest, then insert one more query.
	a.requestGen = 0
	if got := a.paletteAssetsFor(prepared, paletteRequestsOnly); got.candidates[0].label != firstMark {
		t.Fatal("touching the first query did not return its cached value")
	}
	a.requestGen = paletteAssetCacheLimit + 1
	a.paletteAssetsFor(prepared, paletteRequestsOnly)

	a.requestGen = 0
	if got := a.paletteAssetsFor(prepared, paletteRequestsOnly); got.candidates[0].label != firstMark {
		t.Errorf("touched query was evicted: got %q, want %q", got.candidates[0].label, firstMark)
	}
	a.requestGen = 1
	if got := a.paletteAssetsFor(prepared, paletteRequestsOnly); got.candidates[0].label == secondMark {
		t.Error("least recently used query is still cached after trimming")
	}
}

func TestContractCacheEvictsOnlyLeastRecent(t *testing.T) {
	a := &app{}
	a.addAPI(apiSpec{ID: "cache-contract", Source: "openapi: 3.0.3\ninfo: [invalid\n"}, true)
	ex := a.flowExchange(flow{id: 1, method: "GET", url: "https://api.example.com/projects"})
	const firstMark, secondMark = "first-contract-mark", "second-contract-mark"

	for i := range 256 {
		result := a.contractCheck(fmt.Sprintf("exchange-%03d", i), ex)
		if len(result.Failures) == 0 {
			t.Fatalf("exchange-%03d: expected the invalid spec to report a failure", i)
		}
		if i == 0 {
			result.Failures[0] = firstMark
		}
		if i == 1 {
			result.Failures[0] = secondMark
		}
	}

	// Make the oldest key the newest, then insert one more exchange.
	a.contractCheck("exchange-000", ex)
	a.contractCheck("exchange-257", ex)

	if got := a.contractCheck("exchange-000", ex); got.Failures[0] != firstMark {
		t.Errorf("touched exchange was evicted: got %q, want %q", got.Failures[0], firstMark)
	}
	if got := a.contractCheck("exchange-001", ex); got.Failures[0] == secondMark {
		t.Error("least recently used exchange is still cached after trimming")
	}
}

func TestTextExprCacheEvictsOnlyLeastRecent(t *testing.T) {
	a := &app{}
	const firstMark, secondMark = "first-text-mark", "second-text-mark"

	for i := range 256 {
		expr := fmt.Sprintf("item%03d", i)
		result := a.textFor(expr, '=')
		if len(result.conds) == 0 {
			t.Fatalf("%q: expected one compiled condition", expr)
		}
		if i == 0 {
			result.conds[0].value = firstMark
		}
		if i == 1 {
			result.conds[0].value = secondMark
		}
	}

	// Make the oldest key the newest, then compile one more expression.
	a.textFor("item000", '=')
	a.textFor("item256", '=')

	if got := a.textFor("item000", '='); got.conds[0].value != firstMark {
		t.Errorf("touched expression was evicted: got %q, want %q", got.conds[0].value, firstMark)
	}
	if got := a.textFor("item001", '='); got.conds[0].value == secondMark {
		t.Error("least recently used expression is still cached after trimming")
	}
}
