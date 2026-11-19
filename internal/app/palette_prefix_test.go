package app

import (
	"reflect"
	"slices"

	"testing"
)

func paletteFindEntry(entries []paletteEntry, label string) (paletteEntry, bool) {
	for _, e := range entries {
		if e.label == label {
			return e, true
		}
	}
	return paletteEntry{}, false
}

// TestQuickAccessPrefixesLimitScope keeps the mixed results of a bare query
// while each prefix shows only its own provider's rows. VS Code picks a
// provider by its leading prefix and scores only the text after it.
func TestQuickAccessPrefixesLimitScope(t *testing.T) {
	a := paletteTestApp()
	actions := a.actions(nil)
	if got := paletteLabels(a.paletteSearchEntries("order")); !slices.Equal(got, []string{"Orders", "Create order"}) {
		t.Fatalf("unprefixed order = %v", got)
	}
	if got := paletteLabels(a.paletteRankedResults("order", actions).entries); !slices.Contains(got, "Create order") {
		t.Fatalf("unprefixed palette lost its request result: %v", got)
	}

	t.Run("commands", func(t *testing.T) {
		result := a.paletteRankedResults(">layout", actions)
		if len(result.entries) == 0 {
			t.Fatal(">layout found no commands")
		}
		for _, e := range result.entries {
			if !e.action {
				t.Fatalf(">layout included a library row: %+v", e)
			}
		}
		if _, ok := paletteFindEntry(result.entries, "Layout: Side by side"); !ok {
			t.Fatalf(">layout missed its command: %v", paletteLabels(result.entries))
		}
	})
	t.Run("bare commands", func(t *testing.T) {
		result := a.paletteRankedResults(">", actions)
		if len(result.entries) == 0 {
			t.Fatal("a bare > listed no commands")
		}
		for _, e := range result.entries {
			if !e.action {
				t.Fatalf("bare > included a library row: %+v", e)
			}
		}
	})
	t.Run("requests", func(t *testing.T) {
		result := a.paletteRankedResults("#order", actions)
		if got := paletteLabels(result.entries); !slices.Equal(got, []string{"Create order"}) {
			t.Fatalf("#order = %v", got)
		}
		if result.entries[0].action {
			t.Fatal("a request prefix included a command")
		}
		if got := paletteLabels(a.paletteSearchEntries("#order")); !slices.Equal(got, []string{"Create order"}) {
			t.Fatalf("paletteSearch #order = %v", got)
		}
	})
	t.Run("environments and variables", func(t *testing.T) {
		result := a.paletteRankedResults("$base", actions)
		got := paletteLabels(result.entries)
		if !slices.Contains(got, "base_url") {
			t.Fatalf("$base = %v, missing the variable", got)
		}
		for _, e := range result.entries {
			if e.action {
				t.Fatalf("$base included a command: %+v", e)
			}
		}
		local := paletteLabels(a.paletteSearchEntries("$local"))
		if !slices.Contains(local, "Local") || slices.Contains(local, "Create order") || slices.Contains(local, "Orders") {
			t.Fatalf("$local = %v, want environments and variables only", local)
		}
	})
}

// TestQuickAccessHelp lists every prefix and makes choosing one fill the
// field with it, as VS Code's '?' provider does.
func TestQuickAccessHelp(t *testing.T) {
	a := paletteTestApp()
	help := a.paletteRankedResults("?", a.actions(nil))
	if len(help.entries) != len(paletteHelpItems()) {
		t.Fatalf("help = %v, want all prefixes", paletteLabels(help.entries))
	}
	for i, item := range paletteHelpItems() {
		if help.entries[i].label != item.label {
			t.Fatalf("help order = %v, want prefixes registered first", paletteLabels(help.entries))
		}
	}
	for _, item := range paletteHelpItems() {
		if _, ok := paletteFindEntry(help.entries, item.label); !ok {
			t.Fatalf("help is missing %q: %v", item.label, paletteLabels(help.entries))
		}
	}
	if got := paletteLabels(a.paletteRankedResults("?comm", a.actions(nil)).entries); len(got) != 1 || !slices.Contains(got, paletteHelpItems()[0].label) {
		t.Fatalf("filtered help = %v", got)
	}
	entry, ok := paletteFindEntry(help.entries, ">  Commands")
	if !ok {
		t.Fatal("missing the command prefix row")
	}
	a.workbench.command = 3
	entry.run()
	if a.workbench.query != ">" || a.workbench.command != 0 {
		t.Fatalf("choosing a prefix left query=%q row=%d", a.workbench.query, a.workbench.command)
	}
}

// TestQuickAccessPrefixKeepsFuzzyContract checks that the prefix is not part
// of the scored text: the row's matches and detail matches are byte-identical
// to the same search without the prefix.
func TestQuickAccessPrefixKeepsFuzzyContract(t *testing.T) {
	a := paletteTestApp()
	actions := a.actions(nil)
	mixed := a.paletteRankedResults("create order", actions)
	scoped := a.paletteRankedResults("#create order", actions)
	plain, plainOK := paletteFindEntry(mixed.entries, "Create order")
	prefixed, prefixOK := paletteFindEntry(scoped.entries, "Create order")
	if !plainOK || !prefixOK {
		t.Fatalf("Create order missing: mixed=%v scoped=%v", paletteLabels(mixed.entries), paletteLabels(scoped.entries))
	}
	if !reflect.DeepEqual(plain.matches, prefixed.matches) || !reflect.DeepEqual(plain.detailMatches, prefixed.detailMatches) {
		t.Fatalf("prefix changed highlights: %+v vs %+v", plain, prefixed)
	}
	if len(prefixed.matches) == 0 {
		t.Fatal("the prefixed fuzzy query produced no highlighted runs")
	}
	// Recent history keeps ordering scoped rows too, including a bare prefix
	// where every row has the same standing score.
	a.workbench.rememberPalette("request:" + a.requests[2].key())
	if got := paletteLabels(a.paletteRankedResults("#", actions).entries); len(got) == 0 || got[0] != "Health" {
		t.Fatalf("recent request did not lead the # scope: %v", got)
	}
	a.workbench.rememberPalette("command:view.layout-right")
	if got := paletteLabels(a.paletteRankedResults(">", actions).entries); len(got) == 0 || got[0] != "Layout: Side by side" {
		t.Fatalf("recent command did not lead the > scope: %v", got)
	}
}
