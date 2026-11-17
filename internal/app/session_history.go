package app

import (
	"fmt"
	"github.com/baba983/vscroxy/locales"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// sessionSummary is what one interactive protocol session contributes to
// History: its final state, counts and a bounded timeline preview. It is a
// summary, not a replayable recording; payloads keep their preview limit.
type sessionSummary struct {
	Kind     string
	State    string
	Problem  string
	Sent     int
	Received int
	Lines    []string
}

// sessionHistoryLines bounds one session summary's timeline.
const sessionHistoryLines = 40

// sessionPreview is one timeline line's payload, valid UTF-8 and short.
func sessionPreview(data []byte, detail string) string {
	if detail != "" {
		return detail
	}
	text := string(data[:min(len(data), 512)])
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func sessionLine(direction string, size int, text string) string {
	text = strings.ReplaceAll(text, "\n", "\\n")
	if len(text) > 512 {
		text = text[:512] + "…"
	}
	if direction == "" {
		direction = "·"
	}
	return fmt.Sprintf("%s %d B: %s", direction, size, text)
}

// boundedLines keeps the most recent lines of a long session timeline.
func boundedLines(lines []string) []string {
	if len(lines) <= sessionHistoryLines {
		return lines
	}
	return lines[len(lines)-sessionHistoryLines:]
}

// recordSession retains a session summary in History. It runs on the UI
// thread, so it may update the log and request a persist; a read-only log
// keeps the session out rather than failing it.
func (a *app) recordSession(d *requestDocument, summary sessionSummary) {
	if d == nil || a.experiments.readOnly || a.experiments.closed {
		return
	}
	draft := d.value()
	// The schema and examples are request assets, not this session's, and a
	// large schema must not exhaust the history size by itself.
	draft.Request.Examples = nil
	draft.Request.GraphQL.Schema, draft.Request.GraphQL.SchemaSource = "", ""
	var body strings.Builder
	fmt.Fprintf(&body, "%s session · %s\n", summary.Kind, summary.State)
	if summary.Problem != "" {
		body.WriteString(summary.Problem + "\n")
	}
	if summary.Kind == "MCP" {
		fmt.Fprintf(&body, "%d events\n", summary.Sent+summary.Received)
	} else {
		fmt.Fprintf(&body, "%d sent · %d received\n", summary.Sent, summary.Received)
	}
	for _, line := range boundedLines(summary.Lines) {
		body.WriteString(line + "\n")
	}
	text := body.String()
	if len(text) > experimentBodyLimit {
		text = text[:experimentBodyLimit]
	}
	record := experimentRecord{
		At: time.Now(), Draft: draft, DraftKey: d.debugKey, Environment: a.activeEnv,
		Summary: summary.Kind + " · " + summary.State, Outcome: summary.State,
		ResponseBody: []byte(text),
	}
	if err := a.experiments.log.retain(record, a.experimentLimits()); err != nil {
		a.experiments.problem = locales.SessionNotRetainedWith(err.Error())
		return
	}
	a.experiments.persist()
}

// drainSessionHistory records each interactive session once, when it finishes.
// Sessions that are replaced by a reconnect are recorded separately, because
// the recorded pointer identifies the session that produced the summary.
func (a *app) drainSessionHistory() {
	if s := &a.socket; s.session != nil && s.document != nil && s.recorded != s.session {
		state, events, _ := s.session.snapshot()
		if state == "Disconnected" {
			s.recorded = s.session
			sent, received := 0, 0
			lines := make([]string, 0, len(events))
			for _, event := range events {
				switch event.Direction {
				case "Out":
					sent++
				case "In":
					received++
				}
				lines = append(lines, sessionLine(event.Direction, event.Size, sessionPreview(event.Data, event.Detail)))
			}
			a.recordSession(s.document, sessionSummary{Kind: "WebSocket", State: state, Sent: sent, Received: received, Lines: lines})
		}
	}
	for _, d := range a.documents {
		if u := d.socketIOUI; u != nil && u.session != nil && u.recorded != u.session {
			snap := u.session.snapshot()
			if snap.finished {
				u.recorded = u.session
				sent, received := 0, 0
				lines := make([]string, 0, len(snap.events))
				for _, event := range snap.events {
					switch event.direction {
					case "Out":
						sent++
					case "In":
						received++
					}
					lines = append(lines, sessionLine(event.direction, event.size, event.name+" "+event.text))
				}
				a.recordSession(d, sessionSummary{Kind: "Socket.IO", State: snap.state, Problem: snap.problem, Sent: sent, Received: received, Lines: lines})
			}
		}
		if g := d.grpcUI; g != nil && g.session != nil && g.recorded != g.session {
			snap := g.session.snapshot()
			if snap.finished {
				g.recorded = g.session
				sent, received := 0, 0
				lines := make([]string, 0, len(snap.events))
				for _, event := range snap.events {
					switch event.direction {
					case "Out":
						sent++
					case "In":
						received++
					}
					lines = append(lines, sessionLine(event.direction, event.size, event.text))
				}
				a.recordSession(d, sessionSummary{Kind: "gRPC", State: snap.state, Problem: snap.problem, Sent: sent, Received: received, Lines: lines})
			}
		}
		if u := d.mqttUI; u != nil && u.session != nil && u.recorded != u.session {
			snap := u.session.snapshot()
			if snap.finished {
				u.recorded = u.session
				sent, received := 0, 0
				lines := make([]string, 0, len(snap.events))
				for _, event := range snap.events {
					switch event.direction {
					case "Out":
						sent++
					case "In":
						received++
					}
					text := event.payload
					if len(text) > 0 {
						text = slices.Clone(text)
					}
					lines = append(lines, sessionLine(event.direction, event.size, sessionPreview(text, event.name+" "+event.note)))
				}
				a.recordSession(d, sessionSummary{Kind: "MQTT", State: snap.state, Problem: snap.problem, Sent: sent, Received: received, Lines: lines})
			}
		}
		if u := d.mcpUI; u != nil && u.session != nil && u.recorded != u.session {
			snap := u.session.snapshot()
			if snap.finished {
				u.recorded = u.session
				lines := make([]string, 0, len(snap.events))
				for _, event := range snap.events {
					lines = append(lines, sessionLine(event.kind, event.size, event.name+" "+event.text))
				}
				a.recordSession(d, sessionSummary{Kind: "MCP", State: snap.state, Problem: snap.problem, Sent: len(snap.events), Lines: lines})
			}
		}
	}
}
