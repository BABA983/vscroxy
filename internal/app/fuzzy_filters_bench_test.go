package app

import (
	"fmt"
	"strings"
	"testing"
)

func benchmarkMQTTEvents(count int) []mqttEvent {
	events := make([]mqttEvent, count)
	for i := range events {
		events[i] = mqttEvent{
			seq: i + 1, direction: "In",
			name:    fmt.Sprintf("sensors/zone%03d/kitchen/temperature", i%997),
			note:    "Content type: application/json",
			payload: []byte(fmt.Sprintf(`{"celsius":%d,"site":"kitchen"}`, i)),
		}
	}
	return events
}

// oldMQTTEventFilter is the pre-change strings.Contains loop, kept here only
// so benchmark output compares one large list before and after with the same
// events and queries.
func oldMQTTEventFilter(events []mqttEvent, raw string) []mqttEvent {
	query := strings.ToLower(strings.TrimSpace(raw))
	var out []mqttEvent
	for _, e := range events {
		if query == "" {
			out = append(out, e)
			continue
		}
		haystack := e.direction + " " + e.name + " " + e.note + " " + " " + string(e.payload)
		if strings.Contains(strings.ToLower(haystack), query) {
			out = append(out, e)
		}
	}
	return out
}

// BenchmarkMQTTFilter5000BeforeAfter covers one large timeline with a fuzzy
// query, a literal query and a no-match query. Cold clears the cached result
// before every iteration; Warm repeats the same query frame after frame.
func BenchmarkMQTTFilter5000BeforeAfter(b *testing.B) {
	a := workspaceApp()
	events := benchmarkMQTTEvents(5000)
	for _, query := range []string{"ktchn", "kitchen", "zzzz"} {
		b.Run("before-strings-contains/"+query, func(b *testing.B) {
			for b.Loop() {
				_ = oldMQTTEventFilter(events, query)
			}
		})
		b.Run("after-fuzzy-cold/"+query, func(b *testing.B) {
			for b.Loop() {
				a.fuzzyLists = nil
				_, _ = filterMQTTEvents(a, events, query)
			}
		})
		b.Run("after-fuzzy-warm/"+query, func(b *testing.B) {
			_, _ = filterMQTTEvents(a, events, query)
			b.ResetTimer()
			for b.Loop() {
				_, _ = filterMQTTEvents(a, events, query)
			}
		})
	}
}
