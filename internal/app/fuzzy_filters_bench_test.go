package app

import (
	"fmt"
	"strings"
	"testing"
)

func benchmarkMQTTEvents(count int) []mqttEvent {
	events := make([]mqttEvent, count)
	for i := range events {
		events[i] = benchmarkMQTTEvent(i + 1)
	}
	return events
}

func benchmarkMQTTEvent(seq int) mqttEvent {
	return mqttEvent{
		seq: seq, direction: "In",
		name:    fmt.Sprintf("sensors/zone%03d/kitchen/temperature", seq%997),
		note:    "Content type: application/json",
		payload: []byte(fmt.Sprintf(`{"celsius":%d,"site":"kitchen"}`, seq)),
	}
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
	u := &mqttDocument{}
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
				_, _ = filterMQTTEvents(a, u, events, query)
			}
		})
		b.Run("after-fuzzy-warm/"+query, func(b *testing.B) {
			_, _ = filterMQTTEvents(a, u, events, query)
			b.ResetTimer()
			for b.Loop() {
				_, _ = filterMQTTEvents(a, u, events, query)
			}
		})
	}
}

// BenchmarkMQTTFilterAppending5000 is the live case from the review: a filter
// stays active while one event arrives per frame. Only the new tail should be
// scored after the first frame.
func BenchmarkMQTTFilterAppending5000(b *testing.B) {
	const window = 5000
	for _, query := range []string{"before-strings-contains", "after-fuzzy"} {
		b.Run(query, func(b *testing.B) {
			a := workspaceApp()
			u := &mqttDocument{}
			backing := benchmarkMQTTEvents(window + 20000)
			events := backing[:window]
			if query == "after-fuzzy" {
				_, _ = filterMQTTEvents(a, u, events, "kitchen")
			}
			frame := 0
			b.ResetTimer()
			for b.Loop() {
				backing[window+frame] = benchmarkMQTTEvent(window + frame + 1)
				events = backing[frame+1 : frame+1+window]
				frame++
				if window+frame >= len(backing) {
					frame = 0
					events = backing[:window]
					a.fuzzyLists = nil
				}
				if query == "after-fuzzy" {
					_, _ = filterMQTTEvents(a, u, events, "kitchen")
				} else {
					_ = oldMQTTEventFilter(events, "kitchen")
				}
			}
		})
	}
}
