package app

import (
	"fmt"
	"strings"

	"github.com/baba983/vscroxy/internal/fuzzy"
)

// These filters keep VS Code's list-find order: matching events and messages
// stay where they arrived, and only the label's matched runes are highlighted.
// The cache key owns the document and session generation; sequence numbers
// alone are not an identity, since another open session can have the same
// ones. An appended tail reuses the earlier hits and scores only new events.

func fuzzyWindow[T any](events []T, seq func(T) int) (fuzzyListRange, func(int) int) {
	window := fuzzyListRange{count: len(events)}
	if len(events) > 0 {
		window.first, window.last = seq(events[0]), seq(events[len(events)-1])
	}
	return window, func(i int) int { return seq(events[i]) }
}

func filterMQTTEvents(a *app, u *mqttDocument, events []mqttEvent, query string) ([]mqttEvent, map[int][]fuzzy.Match) {
	window, seq := fuzzyWindow(events, func(e mqttEvent) int { return e.seq })
	hits := a.fuzzyListStream(query, "mqtt-timeline", fuzzyListOwner(u, u.fuzzyGen), window, seq,
		func(i int) fuzzyListItem {
			e := events[i]
			haystack := e.direction + " " + e.name + " " + e.note + " " + e.properties
			if e.direction != "System" {
				haystack += " " + string(e.payload)
			}
			return fuzzyListItem{label: e.direction + " · " + e.name, detail: haystack}
		})
	out := make([]mqttEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

func filterSocketIOEvents(a *app, u *socketIODocument, events []socketIOEvent, query string) ([]socketIOEvent, map[int][]fuzzy.Match) {
	window, seq := fuzzyWindow(events, func(e socketIOEvent) int { return e.seq })
	hits := a.fuzzyListStream(query, "socketio-timeline", fuzzyListOwner(u, u.fuzzyGen), window, seq,
		func(i int) fuzzyListItem {
			e := events[i]
			return fuzzyListItem{label: e.direction + " · " + e.name, detail: e.text}
		})
	out := make([]socketIOEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

func filterMCPEvents(a *app, u *mcpDocument, events []mcpEvent, query string) ([]mcpEvent, map[int][]fuzzy.Match) {
	window, seq := fuzzyWindow(events, func(e mcpEvent) int { return e.seq })
	hits := a.fuzzyListStream(query, "mcp-timeline", fuzzyListOwner(u, u.fuzzyGen), window, seq,
		func(i int) fuzzyListItem {
			e := events[i]
			return fuzzyListItem{label: e.kind + " · " + e.name, detail: e.text}
		})
	out := make([]mcpEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

func filterWSSessionEvents(a *app, owner *websocketWorkbench, events []wsSessionEvent, query string) ([]wsSessionEvent, map[int][]fuzzy.Match) {
	window, seq := fuzzyWindow(events, func(e wsSessionEvent) int { return e.Sequence })
	hits := a.fuzzyListStream(query, "ws-session-timeline", fuzzyListOwner(owner, owner.fuzzyGen), window, seq,
		func(i int) fuzzyListItem {
			e := events[i]
			name := e.Direction
			if name == "" {
				name = e.Detail
			}
			return fuzzyListItem{label: fmt.Sprintf("#%d · %s", e.Sequence, name), detail: string(e.Data) + " " + e.Detail}
		})
	out := make([]wsSessionEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.Sequence] = hit.matches
	}
	return out, matches
}

func filterSSESessionEvents(a *app, owner *sseWorkbench, events []sseSessionEvent, query string) ([]sseSessionEvent, map[int][]fuzzy.Match) {
	window, seq := fuzzyWindow(events, func(e sseSessionEvent) int { return e.seq })
	hits := a.fuzzyListStream(query, "sse-session-timeline", fuzzyListOwner(owner, owner.fuzzyGen), window, seq,
		func(i int) fuzzyListItem {
			e := events[i]
			head := e.eventType
			if e.detail != "" {
				head, _, _ = strings.Cut(e.detail, "\n")
			}
			if head == "" {
				head = "Event"
			}
			return fuzzyListItem{label: head, detail: e.eventType + " " + e.id + " " + e.data + " " + e.detail}
		})
	out := make([]sseSessionEvent, 0, len(hits))
	matches := make(map[int][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		e := events[hit.index]
		out = append(out, e)
		matches[e.seq] = hit.matches
	}
	return out, matches
}

// filterProtoMessages scores decoded protobuf messages in place. Rows show
// only an index, so there is no label to highlight; the search itself scores
// the decoded message text, as the old substring filter did.
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
