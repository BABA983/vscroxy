package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/fuzzy"
)

func matchedLabelRuns(text string, matches []fuzzy.Match) []string {
	out := make([]string, 0, len(matches))
	runes := []rune(text)
	for _, match := range matches {
		if match.Start < 0 || match.End > len(runes) || match.End <= match.Start {
			out = append(out, "<invalid>")
			continue
		}
		out = append(out, string(runes[match.Start:match.End]))
	}
	return out
}

func TestFuzzyListFindsWhatSubstringCannot(t *testing.T) {
	a := workspaceApp()
	events := []mqttEvent{
		{seq: 1, direction: "In", name: "sensors/kitchen/temperature", payload: []byte(`{"c":21}`)},
		{seq: 2, direction: "In", name: "sensors/hall/temperature", payload: []byte(`{"c":19}`)},
		{seq: 3, direction: "Out", name: "commands/device-7", payload: []byte(`{"a":"calibrate"}`)},
	}
	const query = "ktchn"
	if strings.Contains(strings.ToLower(events[0].name), query) {
		t.Fatalf("test query is a substring; it cannot prove fuzzy matching")
	}
	shown, matches := filterMQTTEvents(a, &mqttDocument{}, events, query)
	if len(shown) != 1 || shown[0].seq != 1 {
		t.Fatalf("fuzzy event list = %+v, want only seq 1", shown)
	}
	label := "In · " + events[0].name
	if got := matchedLabelRuns(label, matches[1]); !slices.Equal(got, []string{"k", "tch", "n"}) {
		t.Fatalf("highlighted runs = %v, want k/tch/n in %q", got, label)
	}
}

func TestFuzzyListKeepsArrivalOrderAndLiteralPayloadHits(t *testing.T) {
	a := workspaceApp()
	events := []mqttEvent{
		{seq: 1, direction: "In", name: "alpha gamma service", payload: []byte("prefix")},
		{seq: 2, direction: "In", name: "beta", payload: []byte("mid-dle-of-a-very-long-payload-that-still-has-the-needle-inside")},
		{seq: 3, direction: "In", name: "delta", payload: []byte("other")},
	}
	shown, _ := filterMQTTEvents(a, &mqttDocument{}, events, "alp gam")
	if ids := eventSeqs(shown); !slices.Equal(ids, []int{1}) {
		t.Fatalf("multi-piece fuzzy list = %v, want [1]", ids)
	}
	shown, _ = filterMQTTEvents(a, &mqttDocument{}, events, "")
	if ids := eventSeqs(shown); !slices.Equal(ids, []int{1, 2, 3}) {
		t.Fatalf("empty query lost arrival order: %v", ids)
	}
	// Beyond the scored head the exact substring must still work. The row has
	// no label highlight because the hit is in the payload, not the name.
	shown, matches := filterMQTTEvents(a, &mqttDocument{}, events, "needle-inside")
	if ids := eventSeqs(shown); !slices.Equal(ids, []int{2}) {
		t.Fatalf("literal deep payload search = %v, want [2]", ids)
	}
	if len(matches[2]) != 0 {
		t.Fatalf("payload hit highlighted the label: %+v", matches[2])
	}
}

func eventSeqs(events []mqttEvent) []int {
	out := make([]int, len(events))
	for i, e := range events {
		out[i] = e.seq
	}
	return out
}

func TestFuzzyListCacheHasOwnerAndReusesAppend(t *testing.T) {
	a := workspaceApp()
	doc := &mqttDocument{}
	events := []mqttEvent{{seq: 1, direction: "In", name: "alpha"}}
	first, _ := filterMQTTEvents(a, doc, events, "alp")
	second, _ := filterMQTTEvents(a, doc, events, "alp")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("repeated query lost the row: %d / %d", len(first), len(second))
	}
	if a.fuzzyLists.Size() != 1 {
		t.Fatalf("repeated frames cached %d queries, want 1", a.fuzzyLists.Size())
	}
	events = append(events, mqttEvent{seq: 2, direction: "In", name: "alphabet"})
	third, _ := filterMQTTEvents(a, doc, events, "alp")
	if len(third) != 2 {
		t.Fatalf("appended event was not scored: %+v", third)
	}
	cached, ok := a.fuzzyLists.Get(fuzzyListKey{scope: "mqtt-timeline", owner: fuzzyListOwner(doc, doc.fuzzyGen), query: "alp"})
	if !ok || cached.window.last != 2 || cached.window.count != 2 {
		t.Fatalf("cached window after append = %+v, want last/count 2", cached.window)
	}

	// A second document with identical seqs but different content must not
	// inherit the first one's cached indices.
	other := &mqttDocument{}
	reversed := []mqttEvent{
		{seq: 1, direction: "In", name: "garage/door"},
		{seq: 2, direction: "In", name: "kitchen/temp"},
	}
	original := []mqttEvent{
		{seq: 1, direction: "In", name: "kitchen/temp"},
		{seq: 2, direction: "In", name: "garage/door"},
	}
	fromOriginal, _ := filterMQTTEvents(a, doc, original, "kitchen")
	fromOther, _ := filterMQTTEvents(a, other, reversed, "kitchen")
	if len(fromOriginal) != 1 || fromOriginal[0].name != "kitchen/temp" {
		t.Fatalf("first document = %+v", fromOriginal)
	}
	if len(fromOther) != 1 || fromOther[0].name != "kitchen/temp" {
		t.Fatalf("cache collided across documents: %+v", fromOther)
	}

	// A reconnect restarting at seq 1 is a different generation, even on
	// the same document: the old window must not be reused.
	doc.fuzzyGen++
	reconnected, _ := filterMQTTEvents(a, doc, reversed, "kitchen")
	if len(reconnected) != 1 || reconnected[0].name != "kitchen/temp" {
		t.Fatalf("reconnect reused the previous generation: %+v", reconnected)
	}
}

func TestFuzzyTreeFindsRequestSubstringCannot(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{
		{ID: "one", Name: "List items 1", Collection: "Workspace", Method: "GET"},
		{ID: "three", Name: "List items 3", Collection: "Workspace", Method: "GET"},
	}
	a.requestGen++
	hits := a.fuzzyList("lsttm3", "collection-tree-request", "1", len(a.requests), func(i int) fuzzyListItem {
		r := a.requests[i]
		return fuzzyListItem{label: r.Name, detail: r.Collection + " " + r.URL}
	}, false)
	if len(hits) != 1 || hits[0].index != 1 {
		t.Fatalf("tree find = %+v, want only the third item", hits)
	}
	if got := matchedLabelRuns(a.requests[1].Name, hits[0].matches); !slices.Equal(got, []string{"L", "st", "t", "m", "3"}) {
		t.Fatalf("tree highlights = %v", got)
	}
}

func TestHighlightsFitClampedStreamLabels(t *testing.T) {
	long := "#1 · " + strings.Repeat("event-name-", 8) + "needle"
	if len([]rune(long)) <= 48 {
		t.Fatalf("label is not longer than the drawn 48 runes")
	}
	full := []fuzzy.Match{{Start: 55, End: 60}, {Start: 2, End: 7}}
	drawn := clamp(long, 48)
	clipped := fuzzyListLabelMatches(full, drawn)
	if len(clipped) != 1 || clipped[0] != (fuzzy.Match{Start: 2, End: 7}) {
		t.Fatalf("clamped WS label highlights = %v, want only the run inside 48", clipped)
	}
	// SSE shifts after its "#n · " prefix, then clips to the drawn text.
	prefix := "#1 · "
	shifted := fuzzyListLabelMatches(fuzzyShiftMatches(full, len([]rune(prefix))), prefix+drawn)
	if !slices.Equal(shifted, []fuzzy.Match{{Start: 7, End: 12}}) {
		t.Fatalf("SSE clamped highlights = %v, want only the run inside the drawn text", shifted)
	}
}

func TestInlineScorerMatchesPortedVSCodeScorer(t *testing.T) {
	for _, target := range []string{
		"In · sensors/kitchen/temperature", "List items 3", "GET /api/getUserProfile",
		"中文 mixed 标签", "commands/device-7/reply",
	} {
		for _, query := range []string{"ktchn", "lsttm3", "gtusr", "dev7", "中文", "zzzz"} {
			want := fuzzy.ScoreFuzzy(target, query, query, true)
			got := fuzzyScoreInline(target, query)
			if got.score != want.Score || !slices.Equal(got.positions, want.Matches) {
				t.Errorf("score(%q, %q) = %d/%v, want %d/%v", target, query,
					got.score, got.positions, want.Score, want.Matches)
			}
		}
	}
}

func TestFuzzySessionExchangesAndProtoMessages(t *testing.T) {
	a := workspaceApp()
	exchange := sessionFlow{Method: "GET", URL: "https://api.example.com/getUserProfile", Status: 200}
	if strings.Contains(strings.ToLower(exchange.URL), "gtusr") {
		t.Fatalf("test query is a substring; it cannot prove fuzzy matching")
	}
	if !exchange.matches("", "gtusr") {
		t.Fatal("saved session exchange did not match a fuzzy URL search")
	}
	if exchange.matches("", "zzzzz") {
		t.Fatal("saved session exchange matched an absent query")
	}
	if exchange.matches("", "get2") {
		t.Fatal("fuzzy hit straddled Method and Status instead of scoring one field")
	}
	// A long URL must not push a later field out of the scored head.
	long := sessionFlow{Method: "GET", URL: "https://example.com/" + strings.Repeat("long/segment/", 16), Note: "cache miss"}
	if !long.matches("", "cachmss") {
		t.Fatal("fuzzy hit in Note was lost behind a long URL")
	}
	messages := []protoMessage{{text: "packet GetUserProfile acknowledged"}, {text: "other"}}
	if visible := filterProtoMessages(a, messages, "gtusr", "inspect-key"); !slices.Equal(visible, []int{0}) {
		t.Fatalf("protobuf messages = %v, want [0]", visible)
	}
}

func TestFuzzySuggestionsRankForIntelliSense(t *testing.T) {
	a := workspaceApp()
	a.domains = []domain{{name: "alphabet.example.com", n: 3}, {name: "api.example.com", n: 5}}
	got := a.querySuggestions("host:ap", false)
	if len(got) == 0 || got[0].text != "host:api.example.com" {
		t.Fatalf("suggestions = %+v, want host:api.example.com first", got)
	}
	if fuzzy := a.querySuggestions("host:exmpl", false); len(fuzzy) == 0 {
		t.Fatalf("fuzzy value suggestion did not match: %+v", fuzzy)
	}
}

// The filters below are semantics, not search. They must keep matching only
// literal substrings even though the name lists now score fuzzily.
func TestSemanticFiltersStayLiteral(t *testing.T) {
	t.Run("body condition", func(t *testing.T) {
		a := workspaceApp()
		a.filterBody = "needle"
		if matched, _ := a.matchesContent(flow{reqBody: "some conNeedle text"}); !matched {
			t.Fatal("case-insensitive literal body substring stopped matching")
		}
		a.filterBody = "ndl"
		if matched, _ := a.matchesContent(flow{reqBody: "some conNeedle text"}); matched {
			t.Fatal("fuzzy query matched a literal body condition")
		}
	})

	t.Run("text condition", func(t *testing.T) {
		e, problem := parseTextExpr("~cache", '~')
		if problem != "" || !e.match("HTTP cache control") {
			t.Fatalf("contains condition broke: problem=%q", problem)
		}
		if e.match("HTTP cash control") {
			t.Fatal("contains condition matched fuzzily")
		}
	})

	t.Run("capture search", func(t *testing.T) {
		a := workspaceApp()
		a.query = "n schost"
		if match := a.captureSearch(); match("api.example.com/no such host") {
			t.Fatal("free text capture search matched fuzzily")
		}
		a.query = "no such host"
		if match := a.captureSearch(); !match("api.example.com/no such host/x") {
			t.Fatal("literal capture substring stopped matching")
		}
	})

	t.Run("JSON paths and hex rows", func(t *testing.T) {
		node := &jsonNode{path: "/userName", key: "userName", value: "Ada"}
		if rows := matchingJSON([]*jsonNode{node}, "usrn"); len(rows) != 0 {
			t.Fatalf("fuzzy JSON path matched: %d rows", len(rows))
		}
		if rows := matchingJSON([]*jsonNode{node}, "NAME"); len(rows) != 1 {
			t.Fatalf("literal JSON path did not match: %d rows", len(rows))
		}
		if got := hexRows("0123456789abcdef", "345"); !slices.Equal(got, []int{0}) {
			t.Fatalf("hex rows = %v, want [0]", got)
		}
		if got := hexRows("0123456789abcdef", "345a"); len(got) != 0 {
			t.Fatalf("fuzzy hex search matched: %v", got)
		}
	})
}
