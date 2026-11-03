package capture

import (
	"bytes"
	"context"
	"encoding/base64"
	"maps"
	"unicode/utf8"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Scripts run in rule order before a message breakpoint. Only the script
// chain takes the connection-local lock: pausing one direction never holds
// the other direction's script state hostage to a UI decision.
func (s *Server) interceptMessage(ctx context.Context, f *proxy.Flow, m *proxy.WebSocketMessage) {
	direction := "in"
	if m.FromClient {
		direction = "out"
	}
	s.runMessageScripts(ctx, f, m, direction)
	if m.Drop || !s.Breaking() || ctx.Err() != nil {
		return
	}
	req := s.ruleRequest(f)
	s.rulesMu.RLock()
	var matched *Rule
	for _, r := range s.rules {
		if r.Type == RuleBreakMessage && (r.Direction == "" || r.Direction == direction) && r.matches(req) {
			matched = &r
			break
		}
	}
	s.rulesMu.RUnlock()
	if matched == nil {
		return
	}
	s.hit(*matched)
	bp := Breakpoint{Phase: PhaseMessage, Method: f.Request.Method, URL: req.URL,
		Direction: direction, MessageType: wsFrameKind(m.Type), BodyReadOnly: len(m.Content) > maxScriptBody}
	if !bp.BodyReadOnly {
		if m.Type == 2 {
			bp.Body = base64.StdEncoding.EncodeToString(m.Content)
		} else if utf8.Valid(m.Content) {
			bp.Body = string(m.Content)
		} else {
			bp.BodyReadOnly = true
		}
	}
	d := s.waitBreakpoint(ctx, bp)
	if d == nil {
		return
	}
	if d.Abort {
		m.Drop = true
		return
	}
	if d.KeepBody || bp.BodyReadOnly {
		return
	}
	data := []byte(d.Body)
	if m.Type == 2 {
		var err error
		data, err = base64.StdEncoding.Strict().DecodeString(d.Body)
		if err != nil || base64.StdEncoding.EncodeToString(data) != d.Body {
			return
		}
	}
	if len(data) <= maxScriptBody && (m.Type == 2 || utf8.Valid(data)) {
		m.Content = data
	}
}

func (s *Server) runMessageScripts(ctx context.Context, f *proxy.Flow, m *proxy.WebSocketMessage, direction string) {
	runner, d := s.scriptRunner(), debugOf(f)
	if runner == nil || d == nil || len(m.Content) > maxScriptBody {
		return
	}
	d.messageMu.Lock()
	defer d.messageMu.Unlock()
	s.rulesMu.RLock()
	list := s.rules
	s.rulesMu.RUnlock()
	req := s.ruleRequest(f)
	for _, r := range list {
		if ctx.Err() != nil || m.Drop {
			return
		}
		if r.Type != RuleScript || !r.matches(req) {
			continue
		}
		_, _, onMessage := runner.Hooks(r.Target)
		if !onMessage {
			continue
		}
		s.hit(r)
		locals := maps.Clone(d.locals)
		if locals == nil {
			locals = map[string]string{}
		}
		x := &ScriptExchange{Phase: "message", Rule: scriptRuleName(r), App: req.App,
			Method: f.Request.Method, URL: req.URL, Header: f.Request.Header.Clone(), Locals: locals,
			Message: &ScriptMessage{Type: wsFrameKind(m.Type), Direction: direction, Data: bytes.Clone(m.Content)}}
		if err := runner.Run(ctx, r.Target, x); err != nil {
			s.scriptErrors.Add(1)
			continue // this rule's message edits and locals are not published
		}
		msg := x.Message
		if msg == nil || len(msg.Data) > maxScriptBody || msg.Type != "text" && msg.Type != "binary" || msg.Type == "text" && !utf8.Valid(msg.Data) {
			s.scriptErrors.Add(1)
			continue
		}
		d.locals = x.Locals
		m.Content, m.Drop = msg.Data, msg.Drop
		m.Type = 1
		if msg.Type == "binary" {
			m.Type = 2
		}
	}
}
