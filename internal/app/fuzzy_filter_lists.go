package app

import (
	"fmt"
	"strings"

	"github.com/baba983/vscroxy/internal/fuzzy"
)

// These filters keep VS Code's list-find order: matching events and messages
// stay where they arrived, and only the label's matched runes are highlighted.
// The source generation is the list's edge sequence numbers, so a query cached
// on one frame is reused by the next until the list changes.

func filterMQTTEvents(a *app, events []mqttEvent, query string) ([]mqttEvent, map[int][]fuzzy.Match) {
	source := fuzzyEventSource(events, func(e mqttEvent) int { return e.seq })
	hits := a.fuzzyList(query, "mqtt-timeline", source, len(events), func(i int) fuzzyListItem {
		e := events[i]
		haystack := e.direction + " " + e.name + " " + e.note + " " + e.properties
		if e.direction != "System" {
			haystack += " " + string(e.payload)
		}
		return fuzzyListItem{label: e.direction + " · " + e.name, detail: haystack}
	}, false)
	out := make([]mqttEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

func filterSocketIOEvents(a *app, events []socketIOEvent, query string) ([]socketIOEvent, map[int][]fuzzy.Match) {
	source := fuzzyEventSource(events, func(e socketIOEvent) int { return e.seq })
	hits := a.fuzzyList(query, "socketio-timeline", source, len(events), func(i int) fuzzyListItem {
		e := events[i]
		return fuzzyListItem{label: e.direction + " · " + e.name, detail: e.text}
	}, false)
	out := make([]socketIOEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

func filterMCPEvents(a *app, events []mcpEvent, query string) ([]mcpEvent, map[int][]fuzzy.Match) {
	source := fuzzyEventSource(events, func(e mcpEvent) int { return e.seq })
	hits := a.fuzzyList(query, "mcp-timeline", source, len(events), func(i int) fuzzyListItem {
		e := events[i]
		return fuzzyListItem{label: e.kind + " · " + e.name, detail: e.text}
	}, false)
	out := make([]mcpEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

func filterWSSessionEvents(a *app, events []wsSessionEvent, query string) ([]wsSessionEvent, map[int][]fuzzy.Match) {
	source := fuzzyEventSource(events, func(e wsSessionEvent) int { return e.Sequence })
	hits := a.fuzzyList(query, "ws-session-timeline", source, len(events), func(i int) fuzzyListItem {
		e := events[i]
		name := e.Direction
		if name == "" {
			name = e.Detail
		}
		return fuzzyListItem{label: fmt.Sprintf("#%d · %s", e.Sequence, name), detail: string(e.Data) + " " + e.Detail}
	}, false)
	out := make([]wsSessionEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.Sequence] = hit.matches
	}
	return out, matches
}

func filterSSESessionEvents(a *app, events []sseSessionEvent, query string) ([]sseSessionEvent, map[int][]fuzzy.Match) {
	source := fuzzyEventSource(events, func(e sseSessionEvent) int { return e.seq })
	hits := a.fuzzyList(query, "sse-session-timeline", source, len(events), func(i int) fuzzyListItem {
		e := events[i]
		head := e.eventType
		if e.detail != "" {
			head, _, _ = strings.Cut(e.detail, "\n")
		}
		if head == "" {
			head = "Event"
		}
		return fuzzyListItem{label: head, detail: e.eventType + " " + e.id + " " + e.data + " " + e.detail}
	}, false)
	out := make([]sseSessionEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

// filterProtoMessages scores decoded protobuf messages in place. The visible
// row is its index, so there is no label to highlight; the search still sees
// the decoded text plus its index, as it did when it was a substring match.
func filterProtoMessages(a *app, messages []protoMessage, query, source string) []int {
	hits := a.fuzzyList(query, "protobuf-messages", source, len(messages), func(i int) fuzzyListItem {
		return fuzzyListItem{label: messages[i].text, detail: fmt.Sprintf("#%d", i+1)}
	}, false)
	out := make([]int, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.index)
	}
	return out
}

func fuzzyEventSource[T any](events []T, seq func(T) int) string {
	if len(events) == 0 {
		return "0"
	}
	return fuzzySource(len(events), seq(events[0]), seq(events[len(events)-1]))
}
