package app

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
)

// mqttTimelineLimit is how many connection and message events the session
// keeps. Older events are evicted and counted, never silently dropped.
const mqttTimelineLimit = 200

// mqttReconnectLimit bounds a flapping broker: a session gives up after this
// many successful reconnects rather than reconnecting forever.
const mqttReconnectLimit = 20

// mqttRetainedPayload is how much of one payload the timeline keeps. The
// engine already caps the queue; this keeps 200 events small.
const mqttRetainedPayload = 64 << 10

type mqttEvent struct {
	seq        int
	at         time.Time
	direction  string // System, In or Out
	name       string // the event, or the topic of a message
	note       string
	properties string
	payload    []byte
	size       int
	qos        byte
	retain     bool
}

// mqttSession is the only state workers mutate. Documents and editors stay on
// the UI thread, and a reconnect replaces only the connection, not the
// timeline or the subscriptions the user chose.
type mqttSession struct {
	mu       sync.Mutex
	conn     *request.MQTTClient
	cancel   context.CancelFunc
	ctx      context.Context
	done     chan struct{}
	notify   func()
	state    string
	problem  string
	finished bool
	busy     bool
	events   []mqttEvent
	seq      int
	evicted  int
	dropped  int
	// connDropped is the current connection's counter; dropped accumulates
	// across reconnects instead of resetting with each new client.
	connDropped int64

	// active subscriptions are re-applied after a reconnect. They include the
	// document's topics and any the user added in this session.
	subscriptions map[string]request.MQTTSubscription

	// Inputs are immutable snapshots. Later messages use the original broker
	// URL, credentials and variable scopes, not another environment selected
	// mid-session.
	input          request.Request
	scope          request.Context
	vars           map[string]string
	cfg            request.MQTTConfig
	clientID       string
	sessionPresent bool
}

type mqttSnapshot struct {
	state, problem        string
	finished, ready, busy bool
	events                []mqttEvent
	evicted, dropped      int
	subscriptions         []string
	clientID              string
	sessionPresent        bool
}

func (s *mqttSession) snapshot() mqttSnapshot {
	if s == nil {
		return mqttSnapshot{state: "Not connected", finished: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := s.dropped
	if s.conn != nil {
		if total := s.conn.Dropped(); total > s.connDropped {
			dropped += int(total - s.connDropped)
		}
	}
	return mqttSnapshot{state: s.state, problem: s.problem, finished: s.finished, busy: s.busy,
		ready:  s.conn != nil && !s.finished && s.state == "Connected",
		events: slices.Clone(s.events), evicted: s.evicted, dropped: dropped,
		subscriptions: slices.Sorted(func(yield func(string) bool) {
			for topic := range s.subscriptions {
				if !yield(topic) {
					return
				}
			}
		}), clientID: s.clientID, sessionPresent: s.sessionPresent}
}

func (s *mqttSession) changed() {
	if s.notify != nil {
		s.notify()
	}
}

func (s *mqttSession) stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

// stopAndWait cancels the session and gives the connection loop a moment to
// send DISCONNECT. Quitting uses it so the broker does not publish the will
// for an app exit.
func (s *mqttSession) stopAndWait() {
	s.stop()
	s.await(time.Second)
}

// waitMQTTSessions gives all canceled sessions one shared window to send
// DISCONNECT, instead of spending the timeout once per document.
func waitMQTTSessions(sessions []*mqttSession, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for _, s := range sessions {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		s.await(remaining)
	}
}

// await waits for the connection loop to send DISCONNECT and close.
func (s *mqttSession) await(delay time.Duration) {
	if s == nil || s.done == nil {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
	}
}

// addDroppedLocked folds one connection's dropped count into the session
// total. A reconnect starts a new client at zero, so only the delta counts.
func (s *mqttSession) addDroppedLocked(total int64) {
	if total > s.connDropped {
		s.dropped += int(total - s.connDropped)
	}
	s.connDropped = total
}

func (s *mqttSession) accountDroppedLocked() {
	if s.conn != nil {
		s.addDroppedLocked(s.conn.Dropped())
	}
}

func (s *mqttSession) recordLocked(e mqttEvent) {
	s.seq++
	e.seq = s.seq
	if e.at.IsZero() {
		e.at = time.Now()
	}
	if len(s.events) == mqttTimelineLimit {
		s.events = slices.Delete(s.events, 0, 1)
		s.evicted++
	}
	s.events = append(s.events, e)
}

func (s *mqttSession) systemLocked(name, note string) {
	s.recordLocked(mqttEvent{direction: "System", name: name, note: ssePreview(note)})
}

func (s *mqttSession) system(name, note string) {
	s.mu.Lock()
	s.systemLocked(name, note)
	s.mu.Unlock()
	s.changed()
}

func (s *mqttSession) setState(state string) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	s.changed()
}

func (s *mqttSession) finish(state, problem string) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished, s.busy, s.state, s.problem = true, false, state, problem
	s.accountDroppedLocked()
	s.conn = nil
	s.systemLocked(state, problem)
	s.mu.Unlock()
	s.changed()
}

// openMQTTSession starts the connection loop. The caller keeps the returned
// session and cancels it to disconnect. proxy and tlsConfig are evaluated for
// every dial, so a reconnect follows the capture proxy's current state.
func openMQTTSession(url string, input request.Request, scope request.Context, vars map[string]string,
	cfg request.MQTTConfig, proxy func() (string, []byte), verifyUpstream bool, tlsConfig func([]byte) (*tls.Config, error), notify func()) *mqttSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &mqttSession{state: "Connecting", cancel: cancel, ctx: ctx, done: make(chan struct{}), notify: notify,
		subscriptions: map[string]request.MQTTSubscription{}, input: input, scope: scope, vars: maps.Clone(vars), cfg: cfg}
	go s.run(url, cfg, proxy, verifyUpstream, tlsConfig)
	return s
}

func (s *mqttSession) run(url string, cfg request.MQTTConfig, proxy func() (string, []byte), verifyUpstream bool, tlsConfig func([]byte) (*tls.Config, error)) {
	defer close(s.done)
	defer s.cancel()
	s.system("Connecting", request.MQTTVersionLabel(cfg.Version)+" · "+mqttEndpointLabel(url))
	reconnects, failures := 0, 0
	firstConnect := true
	for {
		var proxyAddress string
		var proxyCA []byte
		if proxy != nil {
			proxyAddress, proxyCA = proxy()
		}
		var config *tls.Config
		var err error
		if tlsConfig != nil {
			config, err = tlsConfig(proxyCA)
		}
		var conn *request.MQTTClient
		if err == nil {
			conn, err = request.DialMQTT(s.ctx, url, cfg, config, proxyAddress, verifyUpstream)
		}
		if err != nil {
			if s.ctx.Err() != nil {
				s.finish("Disconnected", "")
				return
			}
			if reconnects == 0 {
				s.finish("Failed", err.Error())
				return
			}
			failures++
			s.system("Reconnect failed", err.Error())
			if failures >= 5 {
				s.finish("Failed", fmt.Sprintf("connection lost; gave up after %d failed reconnects: %v", failures, err))
				return
			}
			if !s.wait(reconnectDelay(failures)) {
				s.finish("Disconnected", "")
				return
			}
			continue
		}
		failures = 0
		// MQTT 5 may assign an ID to an empty client ID. Reuse it for a
		// reconnect, or a clean-start-false session cannot be resumed.
		if cfg.ClientID == "" {
			cfg.ClientID = conn.ClientID()
		}
		s.mu.Lock()
		s.conn, s.connDropped = conn, 0
		s.clientID, s.sessionPresent, s.state, s.problem, s.busy = conn.ClientID(), conn.SessionPresent(), "Connected", "", true
		s.systemLocked("Connected", mqttConnectedNote(cfg, conn))
		s.mu.Unlock()
		s.changed()
		s.applySubscriptions(conn, cfg, firstConnect)
		firstConnect = false
		incoming := conn.Incoming()
	read:
		for {
			select {
			case <-s.ctx.Done():
				conn.Close()
				s.finish("Disconnected", "")
				return
			case <-conn.Done():
				err := conn.Err()
				conn.Close()
				s.mu.Lock()
				s.accountDroppedLocked()
				s.conn = nil
				s.mu.Unlock()
				if s.ctx.Err() != nil {
					s.finish("Disconnected", "")
					return
				}
				if !cfg.AutoReconnects() {
					s.finish("Connection lost", mqttErrorText(err))
					return
				}
				reconnects++
				if reconnects > mqttReconnectLimit {
					s.finish("Failed", fmt.Sprintf("connection lost; gave up after %d reconnects: %v", mqttReconnectLimit, err))
					return
				}
				delay := reconnectDelay(reconnects)
				s.mu.Lock()
				s.state = "Reconnecting"
				s.systemLocked("Reconnecting", fmt.Sprintf("%v · reconnect %d/%d in %s", err, reconnects, mqttReconnectLimit, delay))
				s.mu.Unlock()
				s.changed()
				if !s.wait(delay) {
					s.finish("Disconnected", "")
					return
				}
				break read
			case msg, ok := <-incoming:
				if !ok {
					incoming = nil
					continue
				}
				s.mu.Lock()
				s.recordIncomingLocked(msg)
				s.accountDroppedLocked()
				s.mu.Unlock()
				s.changed()
			}
		}

	}
}

// mqttEndpointLabel keeps a WebSocket query or path token out of the
// timeline; only the scheme and host identify the broker.
func mqttEndpointLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "broker"
	}
	return u.Scheme + "://" + u.Host
}

func mqttConnectedNote(cfg request.MQTTConfig, conn *request.MQTTClient) string {
	note := request.MQTTVersionLabel(cfg.Version) + " · client " + conn.ClientID()
	if cfg.CleanStartEnabled() {
		note += " · clean start"
	} else {
		note += " · persistent session"
	}
	if conn.SessionPresent() {
		note += " · session present"
	}
	return note
}

func reconnectDelay(attempt int) time.Duration {
	delay := time.Second << min(attempt-1, 4)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

func (s *mqttSession) wait(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// subscriptionsFor selects what a connection subscribes to. The first
// connection starts from the document's checked topics; a reconnect restores
// only what is actually active, so a topic the user unsubscribed stays off.
func (s *mqttSession) subscriptionsFor(cfg request.MQTTConfig, first bool) []request.MQTTSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !first {
		subs := make([]request.MQTTSubscription, 0, len(s.subscriptions))
		for _, sub := range s.subscriptions {
			subs = append(subs, sub)
		}
		slices.SortFunc(subs, func(a, b request.MQTTSubscription) int {
			return strings.Compare(a.Topic, b.Topic)
		})
		return subs
	}
	subs := make([]request.MQTTSubscription, 0, len(cfg.Topics)+len(s.subscriptions))
	seen := map[string]bool{}
	for _, sub := range cfg.TopicsToSubscribe() {
		subs, seen[sub.Topic] = append(subs, sub), true
	}
	var extra []request.MQTTSubscription
	for topic, sub := range s.subscriptions {
		if !seen[topic] {
			extra = append(extra, sub)
		}
	}
	slices.SortFunc(extra, func(a, b request.MQTTSubscription) int {
		return strings.Compare(a.Topic, b.Topic)
	})
	return append(subs, extra...)
}

// applySubscriptions subscribes to the selected topics. Retained messages may
// arrive again after a reconnect; the timeline shows them as delivered.
func (s *mqttSession) applySubscriptions(conn *request.MQTTClient, cfg request.MQTTConfig, first bool) {
	subs := s.subscriptionsFor(cfg, first)
	if len(subs) == 0 {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
		s.changed()
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	grants, err := conn.Subscribe(ctx, subs)
	s.mu.Lock()
	if s.finished || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.busy = false
	note := mqttSubscriptionNote(subs, grants)
	for i, sub := range subs {
		if i < len(grants) && grants[i] < 0x80 {
			s.subscriptions[sub.Topic] = sub
		}
	}
	s.systemLocked("Subscribed", note)
	if err != nil {
		s.problem = err.Error()
		s.systemLocked("Subscribe failed", err.Error())
	}
	s.mu.Unlock()
	s.changed()
}

func mqttSubscriptionNote(subs []request.MQTTSubscription, grants []byte) string {
	var lines []string
	for i, sub := range subs {
		granted := "no result"
		if i < len(grants) {
			granted = fmt.Sprintf("QoS %d", grants[i])
			if grants[i] >= 0x80 {
				granted = "failed"
			}
		}
		lines = append(lines, fmt.Sprintf("%s · %s", sub.Topic, granted))
	}
	return strings.Join(lines, "\n")
}

func (s *mqttSession) recordIncomingLocked(msg request.MQTTIncoming) {
	payload := msg.Payload
	if len(payload) > mqttRetainedPayload {
		payload = payload[:mqttRetainedPayload]
	}
	size := msg.Size
	if size == 0 {
		size = len(msg.Payload)
	}
	s.recordLocked(mqttEvent{direction: "In", name: msg.Topic, qos: msg.QoS, retain: msg.Retain,
		payload: slices.Clone(payload), size: size, properties: mqttIncomingProperties(msg.Properties)})
}

func (s *mqttSession) recordOutgoingLocked(msg request.MQTTPublish, name string) {
	payload := msg.Payload
	if len(payload) > mqttRetainedPayload {
		payload = payload[:mqttRetainedPayload]
	}
	s.recordLocked(mqttEvent{direction: "Out", name: msg.Topic, note: name, qos: msg.QoS, retain: msg.Retain,
		payload: slices.Clone(payload), size: len(msg.Payload), properties: mqttOutgoingProperties(msg.Properties)})
}

func mqttIncomingProperties(p request.MQTTIncomingProperties) string {
	var b strings.Builder
	if p.PayloadFormat {
		mqttPropertyLine(&b, "Payload format", "UTF-8")
	}
	mqttPropertyLine(&b, "Content type", p.ContentType)
	mqttPropertyLine(&b, "Response topic", p.ResponseTopic)
	if len(p.CorrelationData) > 0 {
		mqttPropertyLine(&b, "Correlation data", mqttBinaryText(p.CorrelationData))
	}
	if p.MessageExpiry != 0 {
		mqttPropertyLine(&b, "Message expiry", strconv.Itoa(p.MessageExpiry)+" s")
	}
	if p.SubscriptionIdentifier != 0 {
		mqttPropertyLine(&b, "Subscription ID", strconv.Itoa(p.SubscriptionIdentifier))
	}
	for _, user := range p.UserProperties {
		mqttPropertyLine(&b, "User property", user.Key+" = "+user.Value)
	}
	return b.String()
}

func mqttOutgoingProperties(p request.MQTTMessageProperties) string {
	var b strings.Builder
	if p.PayloadFormat {
		mqttPropertyLine(&b, "Payload format", "UTF-8")
	}
	mqttPropertyLine(&b, "Content type", p.ContentType)
	mqttPropertyLine(&b, "Response topic", p.ResponseTopic)
	mqttPropertyLine(&b, "Correlation data", p.CorrelationData)
	if p.MessageExpiry != 0 {
		mqttPropertyLine(&b, "Message expiry", strconv.Itoa(p.MessageExpiry)+" s")
	}
	if p.TopicAlias != 0 {
		mqttPropertyLine(&b, "Topic alias", strconv.Itoa(p.TopicAlias))
	}
	for _, user := range p.UserProperties {
		mqttPropertyLine(&b, "User property", user.Key+" = "+user.Value)
	}
	return b.String()
}

// mqttPropertyLine bounds the property text the timeline keeps.
func mqttPropertyLine(b *strings.Builder, name, value string) {
	if value == "" || b.Len() > 8<<10 {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(name)
	b.WriteString(": ")
	if len(value) > 512 {
		value = value[:512] + "…"
	}
	b.WriteString(value)
}

func mqttBinaryText(data []byte) string {
	if utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
		return string(data)
	}
	return "hex " + hex.EncodeToString(data)
}

// publish sends one prepared message. The network work is off the UI thread.
func (s *mqttSession) publish(msg request.MQTTPublish, name string) {
	s.mu.Lock()
	if s.finished || s.busy || s.conn == nil || s.state != "Connected" {
		s.mu.Unlock()
		return
	}
	s.busy, s.problem = true, ""
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		defer cancel()
		err := conn.Publish(ctx, msg)
		s.mu.Lock()
		if !s.finished {
			s.busy = false
			if err != nil {
				s.problem = mqttErrorText(err)
				s.systemLocked("Publish failed", mqttErrorText(err))
			} else {
				s.recordOutgoingLocked(msg, name)
			}
		}
		s.mu.Unlock()
		s.changed()
	}()
}

func (s *mqttSession) subscribe(subs []request.MQTTSubscription) {
	s.mu.Lock()
	if s.finished || s.busy || s.conn == nil || s.state != "Connected" {
		s.mu.Unlock()
		return
	}
	s.busy, s.problem = true, ""
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		defer cancel()
		grants, err := conn.Subscribe(ctx, subs)
		s.mu.Lock()
		if !s.finished {
			s.busy = false
			for i, sub := range subs {
				if i < len(grants) && grants[i] < 0x80 {
					s.subscriptions[sub.Topic] = sub
				}
			}
			s.systemLocked("Subscribed", mqttSubscriptionNote(subs, grants))
			if err != nil {
				s.problem = mqttErrorText(err)
				s.systemLocked("Subscribe failed", mqttErrorText(err))
			}
		}
		s.mu.Unlock()
		s.changed()
	}()
}

func (s *mqttSession) unsubscribe(topics []string) {
	if len(topics) == 0 {
		return
	}
	s.mu.Lock()
	if s.finished || s.busy || s.conn == nil || s.state != "Connected" {
		s.mu.Unlock()
		return
	}
	s.busy, s.problem = true, ""
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		defer cancel()
		err := conn.Unsubscribe(ctx, topics)
		s.mu.Lock()
		if !s.finished {
			s.busy = false
			if err != nil {
				s.problem = mqttErrorText(err)
				s.systemLocked("Unsubscribe failed", mqttErrorText(err))
			} else {
				for _, topic := range topics {
					delete(s.subscriptions, topic)
				}
				s.systemLocked("Unsubscribed", strings.Join(topics, "\n"))
			}
		}
		s.mu.Unlock()
		s.changed()
	}()
}

func (s *mqttSession) unsubscribeAll() {
	s.mu.Lock()
	topics := slices.Sorted(func(yield func(string) bool) {
		for topic := range s.subscriptions {
			if !yield(topic) {
				return
			}
		}
	})
	s.mu.Unlock()
	s.unsubscribe(topics)
}

// mqttErrorText avoids showing a cancelled operation as a broker failure.
func mqttErrorText(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return err.Error()
}
