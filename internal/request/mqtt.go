package request

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MQTT versions as they appear on the wire. MQTT 3.1 is not supported: the
// protocol names it "3.1.1" in the document model.
const (
	MQTT311 = 4
	MQTT5   = 5
)

const (
	mqttMaxTopicBytes = 65535
	mqttMaxTopics     = 128
	mqttMaxMessages   = 128
	mqttMaxProperties = 64
	mqttMaxPayload    = 1 << 20
)

// MQTTConfig is saved with an MQTT request document. URL, credentials and the
// connect timeout describe the broker connection; Topics and Messages are the
// subscriptions and publish drafts kept with the document.
type MQTTConfig struct {
	// Version is 4 for MQTT 3.1.1 and 5 for MQTT 5.0.
	Version  int    `json:"version,omitempty"`
	ClientID string `json:"clientId,omitempty"`
	// Username and Password are expanded from the request's Basic auth; they
	// are never serialized separately from it.
	Username string `json:"-"`
	Password string `json:"-"`
	// CleanStart nil means true, as Postman's cleanSession does.
	CleanStart *bool `json:"cleanStart,omitempty"`
	// KeepAlive is in seconds; 0 disables keep-alive.
	KeepAlive int `json:"keepAlive,omitempty"`
	// ConnectTimeoutMs bounds the TCP/TLS/WebSocket handshake and CONNACK.
	ConnectTimeoutMs int `json:"connectTimeoutMs,omitempty"`
	// AutoReconnect nil means true: an established connection that drops is
	// retried with backoff. An initial connection failure is never retried.
	AutoReconnect *bool                 `json:"autoReconnect,omitempty"`
	Will          MQTTWill              `json:"will,omitzero"`
	Properties    MQTTConnectProperties `json:"properties,omitzero"`
	Topics        []MQTTTopic           `json:"topics,omitempty"`
	Messages      []MQTTMessage         `json:"messages,omitempty"`
}

// MQTTConnectProperties are the MQTT 5 CONNECT properties.
type MQTTConnectProperties struct {
	SessionExpiry              int            `json:"sessionExpiry,omitempty"`
	ReceiveMaximum             int            `json:"receiveMaximum,omitempty"`
	MaximumPacketSize          int            `json:"maximumPacketSize,omitempty"`
	RequestResponseInformation bool           `json:"requestResponseInformation,omitzero"`
	UserProperties             []MQTTProperty `json:"userProperties,omitempty"`
}

// MQTTWill is the last will published by the broker when the connection ends
// unexpectedly.
type MQTTWill struct {
	Enabled    bool               `json:"enabled,omitzero"`
	Topic      string             `json:"topic,omitempty"`
	Payload    string             `json:"payload,omitempty"`
	Type       string             `json:"type,omitempty"`
	QoS        byte               `json:"qos,omitempty"`
	Retain     bool               `json:"retain,omitzero"`
	Properties MQTTWillProperties `json:"properties,omitzero"`
}

// MQTTWillProperties are the MQTT 5 will properties.
type MQTTWillProperties struct {
	PayloadFormat   bool           `json:"payloadFormat,omitzero"`
	MessageExpiry   int            `json:"messageExpiry,omitempty"`
	WillDelay       int            `json:"willDelay,omitempty"`
	ResponseTopic   string         `json:"responseTopic,omitempty"`
	CorrelationData string         `json:"correlationData,omitempty"`
	ContentType     string         `json:"contentType,omitempty"`
	UserProperties  []MQTTProperty `json:"userProperties,omitempty"`
}

// MQTTTopic is one subscription filter and its delivery options.
type MQTTTopic struct {
	Name        string `json:"name"`
	QoS         byte   `json:"qos,omitempty"`
	Description string `json:"description,omitempty"`
	// Subscribe nil means true: the topic is subscribed on connect and by
	// "Subscribe listed topics".
	Subscribe              *bool          `json:"subscribe,omitempty"`
	NoLocal                bool           `json:"noLocal,omitzero"`
	RetainAsPublished      bool           `json:"retainAsPublished,omitzero"`
	RetainHandling         byte           `json:"retainHandling,omitempty"`
	SubscriptionIdentifier int            `json:"subscriptionIdentifier,omitempty"`
	UserProperties         []MQTTProperty `json:"userProperties,omitempty"`
}

// MQTTMessage is a publish draft saved with the document.
type MQTTMessage struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Topic       string `json:"topic"`
	Payload     string `json:"payload,omitempty"`
	// Type is text, json, hex or base64 and decides how Payload becomes bytes.
	Type       string                `json:"type,omitempty"`
	QoS        byte                  `json:"qos,omitempty"`
	Retain     bool                  `json:"retain,omitzero"`
	Properties MQTTMessageProperties `json:"properties,omitzero"`
}

// MQTTMessageProperties are the MQTT 5 PUBLISH properties.
type MQTTMessageProperties struct {
	PayloadFormat   bool           `json:"payloadFormat,omitzero"`
	MessageExpiry   int            `json:"messageExpiry,omitempty"`
	TopicAlias      int            `json:"topicAlias,omitempty"`
	ResponseTopic   string         `json:"responseTopic,omitempty"`
	CorrelationData string         `json:"correlationData,omitempty"`
	ContentType     string         `json:"contentType,omitempty"`
	UserProperties  []MQTTProperty `json:"userProperties,omitempty"`
}

// MQTTProperty is one user property; the order is kept.
type MQTTProperty struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MQTTSubscription is a subscribe request for one filter.
type MQTTSubscription struct {
	Topic                  string
	QoS                    byte
	NoLocal                bool
	RetainAsPublished      bool
	RetainHandling         byte
	SubscriptionIdentifier int
	UserProperties         []MQTTProperty
}

// MQTTIncoming is a message delivered by the broker. Payload is at most
// 64 KiB; Size is the original payload length, so the UI can say what was
// not retained.
type MQTTIncoming struct {
	Topic      string
	Payload    []byte
	Size       int
	QoS        byte
	Retain     bool
	Duplicate  bool
	Properties MQTTIncomingProperties
}

// MQTTIncomingProperties are the MQTT 5 PUBLISH properties the broker sent.
type MQTTIncomingProperties struct {
	PayloadFormat          bool
	MessageExpiry          int
	ContentType            string
	ResponseTopic          string
	CorrelationData        []byte
	SubscriptionIdentifier int
	UserProperties         []MQTTProperty
}

// MQTTPublish is one publish request to the live connection.
type MQTTPublish struct {
	Topic      string
	Payload    []byte
	QoS        byte
	Retain     bool
	Properties MQTTMessageProperties
}

// CleanStartEnabled reports the effective clean-start choice.
func (c MQTTConfig) CleanStartEnabled() bool {
	return c.CleanStart == nil || *c.CleanStart
}

// AutoReconnects reports the effective reconnect choice.
func (c MQTTConfig) AutoReconnects() bool {
	return c.AutoReconnect == nil || *c.AutoReconnect
}

// ConnectTimeout is the handshake and CONNACK budget.
func (c MQTTConfig) ConnectTimeout() time.Duration {
	if c.ConnectTimeoutMs <= 0 {
		return 10 * time.Second
	}
	return time.Duration(c.ConnectTimeoutMs) * time.Millisecond
}

// VersionLabel names the protocol version for the UI and timeline.
func MQTTVersionLabel(version int) string {
	if version == MQTT5 {
		return "MQTT 5.0"
	}
	return "MQTT 3.1.1"
}

// MQTTEndpoint validates a broker URL and returns the host:port a TLS
// certificate is matched against, plus whether the scheme is encrypted.
// mqtt:// defaults to 1883, mqtts:// to 8883, ws:// to 80 and wss:// to 443.
func MQTTEndpoint(raw string) (address string, secure bool, err error) {
	u, err := parseMQTTURL(raw)
	if err != nil {
		return "", false, err
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "mqtt":
			port = "1883"
		case "mqtts":
			port = "8883"
		case "ws":
			port = "80"
		case "wss":
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), strings.EqualFold(u.Scheme, "mqtts") || strings.EqualFold(u.Scheme, "wss"), nil
}

func parseMQTTURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid broker URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "mqtt", "mqtts":
		if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, errors.New("mqtt:// and mqtts:// broker URLs take a host and optional port only")
		}
	case "ws", "wss":
		if u.Fragment != "" || u.User != nil {
			return nil, errors.New("ws:// and wss:// broker URLs cannot carry credentials or a fragment; use the Connection tab")
		}
	default:
		return nil, errors.New("the broker URL must start with mqtt://, mqtts://, ws:// or wss://")
	}
	if u.Hostname() == "" {
		return nil, errors.New("the broker URL needs a host")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("broker port %q is invalid", port)
		}
	}
	return u, nil
}

// PrepareMQTT shares HTTP's variables, inherited Basic credentials and
// host-bound Vault access. HTTP scripts, Checks and token bindings are
// rejected rather than silently skipped. MQTT properties that only MQTT 5.0
// carries are rejected for 3.1.1.
func (r Request) PrepareMQTT(in Context, vars map[string]string) (*Prepared, MQTTConfig, error) {
	if r.Protocol != "MQTT" {
		return nil, MQTTConfig{}, fmt.Errorf("not an MQTT document")
	}
	if r.Pre != "" || r.Post != "" || r.Checks != (Checks{}) || len(r.TokenSources) != 0 {
		return nil, MQTTConfig{}, fmt.Errorf("MQTT does not run HTTP scripts, Checks or token bindings; remove them first")
	}
	for _, f := range in.Folders {
		if f.Pre != "" || f.Post != "" {
			return nil, MQTTConfig{}, fmt.Errorf("MQTT does not run inherited HTTP scripts; use a collection without scripts")
		}
	}
	authType := r.Auth.Type
	r.Auth, _ = in.InheritAuth(r.Auth)
	// A collection's HTTP auth is not MQTT credentials. Only Basic is a
	// sensible inherited credential set; anything else is left unused.
	if authType == AuthInherit && r.Auth.Type != "Basic" {
		r.Auth = Auth{}
	}
	switch r.Auth.Type {
	case "", "None":
	case "Basic":
	default:
		return nil, MQTTConfig{}, fmt.Errorf("MQTT supports None, Basic or inherited Basic credentials; set them on the Connection tab")
	}
	values := in.Values(vars)
	r = r.withDynamic(values)
	cfg := r.MQTT.clone()
	cfg.expandDynamic(values)
	// The variable check below must see the dynamic names already resolved,
	// as the HTTP, gRPC and Socket.IO paths do.
	r.MQTT = cfg
	if err := in.unlock(r, values); err != nil {
		return nil, MQTTConfig{}, err
	}
	for _, name := range r.Variables() {
		if _, ok := values[name]; !ok {
			return nil, MQTTConfig{}, fmt.Errorf("set variable {{%s}} before connecting MQTT", name)
		}
	}
	cfg.expand(values)
	cfg.Username, cfg.Password = ExpandVars(r.Auth.Username, values), ExpandVars(r.Auth.Password, values)
	if err := cfg.Validate(); err != nil {
		return nil, MQTTConfig{}, err
	}
	prepared := &Prepared{Method: "GET", URL: strings.TrimSpace(ExpandVars(r.URL, values)), Header: http.Header{}}
	if _, _, err := MQTTEndpoint(prepared.URL); err != nil {
		return nil, MQTTConfig{}, err
	}
	return prepared, cfg, nil
}

// Validate checks the values the protocol engine cannot repair: version,
// topic filters and names, QoS, property limits and MQTT 5-only fields.
func (c MQTTConfig) Validate() error {
	if c.Version != MQTT311 && c.Version != MQTT5 {
		return errors.New("choose MQTT 3.1.1 or MQTT 5.0")
	}
	if len(c.ClientID) > mqttMaxTopicBytes || !utf8.ValidString(c.ClientID) || strings.ContainsRune(c.ClientID, 0) {
		return errors.New("the MQTT client ID must be valid UTF-8 without NUL, at most 65535 bytes")
	}
	if c.Version == MQTT311 && c.ClientID == "" && !c.CleanStartEnabled() {
		return errors.New("an empty MQTT 3.1.1 client ID requires clean start")
	}
	if c.Password != "" && c.Username == "" {
		return errors.New("MQTT requires a username when a password is set")
	}
	if len(c.Username) > 65535 || len(c.Password) > 65535 {
		return errors.New("the MQTT username and password are limited to 65535 bytes")
	}
	if c.KeepAlive < 0 || c.KeepAlive > 65535 {
		return errors.New("MQTT keep-alive must be between 0 and 65535 seconds")
	}
	if c.ConnectTimeoutMs < 0 || c.ConnectTimeoutMs > 600000 {
		return errors.New("the MQTT connect timeout must be between 0 and 600000 ms")
	}
	if c.Properties.SessionExpiry < 0 || c.Properties.SessionExpiry > 4294967295 {
		return errors.New("the MQTT session expiry must be between 0 and 4294967295 seconds")
	}
	if c.Properties.ReceiveMaximum < 0 || c.Properties.ReceiveMaximum > 65535 {
		return errors.New("the MQTT receive maximum must be between 0 and 65535")
	}
	if c.Properties.MaximumPacketSize < 0 || int64(c.Properties.MaximumPacketSize) > 268435455 {
		return errors.New("the MQTT maximum packet size must be between 0 and 268435455 bytes")
	}
	if len(c.Properties.UserProperties) > mqttMaxProperties || len(c.Topics) > mqttMaxTopics || len(c.Messages) > mqttMaxMessages {
		return fmt.Errorf("MQTT documents keep at most %d topics, %d messages and %d user properties", mqttMaxTopics, mqttMaxMessages, mqttMaxProperties)
	}
	if err := validateMQTTProperties(c.Properties.UserProperties, "connect user property"); err != nil {
		return err
	}
	if c.Version == MQTT311 && c.hasV5ConnectFields() {
		return errors.New("MQTT 5.0 connect properties require protocol version 5.0")
	}
	if c.Will.Enabled {
		if err := ValidateMQTTTopicName(c.Will.Topic); err != nil {
			return fmt.Errorf("last will: %w", err)
		}
		if c.Will.QoS > 2 {
			return errors.New("last will QoS must be 0, 1 or 2")
		}
		willPayload, err := MQTTPayload(c.Will.Payload, c.Will.Type)
		if err != nil {
			return fmt.Errorf("last will payload: %w", err)
		}
		if len(willPayload) > 65535 {
			return errors.New("the MQTT last will payload is limited to 65535 bytes")
		}
		if err := validateMQTTUTF8(c.Will.Properties.ResponseTopic, "last will response topic"); err != nil {
			return err
		}
		if err := validateMQTTUTF8(c.Will.Properties.ContentType, "last will content type"); err != nil {
			return err
		}
		if err := validateMQTTUTF8(c.Will.Properties.CorrelationData, "last will correlation data"); err != nil {
			return err
		}
		if c.Will.Properties.MessageExpiry < 0 || int64(c.Will.Properties.MessageExpiry) > 4294967295 {
			return errors.New("the last will message expiry must be between 0 and 4294967295 seconds")
		}
		if c.Will.Properties.WillDelay < 0 || int64(c.Will.Properties.WillDelay) > 4294967295 {
			return errors.New("the last will delay must be between 0 and 4294967295 seconds")
		}
		if c.Version == MQTT311 && c.Will.Properties.nonZero() {
			return errors.New("last will MQTT 5 properties require protocol version 5.0")
		}
		if err := validateMQTTProperties(c.Will.Properties.UserProperties, "last will user property"); err != nil {
			return err
		}
	}
	for i := range c.Topics {
		t := &c.Topics[i]
		if err := ValidateMQTTTopicFilter(t.Name); err != nil {
			return fmt.Errorf("topic %d: %w", i+1, err)
		}
		if t.QoS > 2 {
			return fmt.Errorf("topic %q QoS must be 0, 1 or 2", t.Name)
		}
		if t.RetainHandling > 2 {
			return fmt.Errorf("topic %q retain handling must be 0, 1 or 2", t.Name)
		}
		if t.SubscriptionIdentifier < 0 || t.SubscriptionIdentifier > 268435455 {
			return fmt.Errorf("topic %q subscription identifier must be between 0 and 268435455", t.Name)
		}
		if err := validateMQTTProperties(t.UserProperties, "topic user property"); err != nil {
			return err
		}
		if c.Version == MQTT311 && (t.NoLocal || t.RetainAsPublished || t.RetainHandling != 0 || t.SubscriptionIdentifier != 0 || len(t.UserProperties) > 0) {
			return fmt.Errorf("topic %q uses MQTT 5 subscription options; choose protocol version 5.0", t.Name)
		}
	}
	for i := range c.Messages {
		m := &c.Messages[i]
		if err := ValidateMQTTTopicName(m.Topic); err != nil {
			return fmt.Errorf("message %d: %w", i+1, err)
		}
		if m.QoS > 2 {
			return fmt.Errorf("message %q QoS must be 0, 1 or 2", m.Topic)
		}
		if _, err := MQTTPayload(m.Payload, m.Type); err != nil {
			return fmt.Errorf("message %q payload: %w", m.Topic, err)
		}
		if c.Version == MQTT311 && m.Properties.nonZero() {
			return fmt.Errorf("message %q uses MQTT 5 properties; choose protocol version 5.0", m.Topic)
		}
		if m.Properties.MessageExpiry < 0 || int64(m.Properties.MessageExpiry) > 4294967295 {
			return fmt.Errorf("message %q message expiry must be between 0 and 4294967295 seconds", m.Topic)
		}
		if err := validateMQTTUTF8(m.Properties.ResponseTopic, "message response topic"); err != nil {
			return err
		}
		if err := validateMQTTUTF8(m.Properties.ContentType, "message content type"); err != nil {
			return err
		}
		if err := validateMQTTUTF8(m.Properties.CorrelationData, "message correlation data"); err != nil {
			return err
		}
		if m.Properties.TopicAlias < 0 || m.Properties.TopicAlias > 65535 {
			return fmt.Errorf("message %q topic alias must be between 0 and 65535", m.Topic)
		}
		if err := validateMQTTProperties(m.Properties.UserProperties, "message user property"); err != nil {
			return err
		}
	}
	return nil
}

// HasV5Properties reports whether the document carries any MQTT 5.0-only
// field. A 3.1.1 document with one is rejected at connect time; the v3
// importer also reports it.
func (c MQTTConfig) HasV5Properties() bool {
	if c.hasV5ConnectFields() || c.Will.Properties.nonZero() {
		return true
	}
	for _, t := range c.Topics {
		if t.NoLocal || t.RetainAsPublished || t.RetainHandling != 0 || t.SubscriptionIdentifier != 0 || len(t.UserProperties) > 0 {
			return true
		}
	}
	for _, m := range c.Messages {
		if m.Properties.nonZero() {
			return true
		}
	}
	return false
}

func (c MQTTConfig) hasV5ConnectFields() bool {
	return c.Properties.SessionExpiry != 0 || c.Properties.ReceiveMaximum != 0 || c.Properties.MaximumPacketSize != 0 ||
		c.Properties.RequestResponseInformation || len(c.Properties.UserProperties) > 0
}

func (p MQTTMessageProperties) nonZero() bool {
	return p.PayloadFormat || p.MessageExpiry != 0 || p.TopicAlias != 0 || p.ResponseTopic != "" ||
		p.CorrelationData != "" || p.ContentType != "" || len(p.UserProperties) > 0
}

func (p MQTTWillProperties) nonZero() bool {
	return p.PayloadFormat || p.MessageExpiry != 0 || p.WillDelay != 0 || p.ResponseTopic != "" ||
		p.CorrelationData != "" || p.ContentType != "" || len(p.UserProperties) > 0
}

func validateMQTTProperties(props []MQTTProperty, what string) error {
	if len(props) > mqttMaxProperties {
		return fmt.Errorf("%ss are limited to %d entries", what, mqttMaxProperties)
	}
	for _, p := range props {
		if p.Key == "" || !utf8.ValidString(p.Key) || strings.ContainsRune(p.Key, 0) ||
			!utf8.ValidString(p.Value) || strings.ContainsRune(p.Value, 0) {
			return fmt.Errorf("%s needs a non-empty UTF-8 key without NUL", what)
		}
		if len(p.Key) > 65535 || len(p.Value) > 65535 {
			return fmt.Errorf("%ss are limited to 65535 bytes", what)
		}
	}
	return nil
}

// validateMQTTUTF8 checks a length-prefixed MQTT string. Empty is valid.
func validateMQTTUTF8(value, what string) error {
	if len(value) > 65535 {
		return fmt.Errorf("the MQTT %s is limited to 65535 bytes", what)
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("the MQTT %s must be valid UTF-8 without NUL", what)
	}
	return nil
}

// ValidateMQTTTopicName checks a publish topic: no wildcards and no NUL.
func ValidateMQTTTopicName(topic string) error {
	if topic == "" {
		return errors.New("the MQTT topic is empty")
	}
	if len(topic) > mqttMaxTopicBytes {
		return errors.New("the MQTT topic exceeds 65535 bytes")
	}
	if !utf8.ValidString(topic) || strings.ContainsRune(topic, 0) {
		return errors.New("the MQTT topic must be valid UTF-8 without NUL")
	}
	if strings.ContainsAny(topic, "+#") {
		return errors.New("a publish topic cannot contain + or #; those are subscription wildcards")
	}
	return nil
}

// ValidateMQTTTopicFilter checks a subscription filter. # must be the last
// whole level; + must be a whole level.
func ValidateMQTTTopicFilter(filter string) error {
	if filter == "" {
		return errors.New("the MQTT topic filter is empty")
	}
	if len(filter) > mqttMaxTopicBytes {
		return errors.New("the MQTT topic filter exceeds 65535 bytes")
	}
	if !utf8.ValidString(filter) || strings.ContainsRune(filter, 0) {
		return errors.New("the MQTT topic filter must be valid UTF-8 without NUL")
	}
	levels := strings.Split(filter, "/")
	for i, level := range levels {
		switch {
		case level == "#":
			if i != len(levels)-1 {
				return errors.New("# must be the last whole level of an MQTT topic filter")
			}
		case strings.Contains(level, "#"):
			return errors.New("# must occupy a whole level of an MQTT topic filter")
		case level == "+":
		case strings.Contains(level, "+"):
			return errors.New("+ must occupy a whole level of an MQTT topic filter")
		}
	}
	return nil
}

// MQTTPayload turns an authored payload into bytes. text and json are UTF-8
// as written; hex ignores whitespace; base64 requires canonical padding.
func MQTTPayload(text, kind string) ([]byte, error) {
	var data []byte
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "text", "json":
		data = []byte(text)
	case "hex":
		clean := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, text)
		if len(clean)%2 != 0 {
			return nil, errors.New("hex payload needs an even number of digits")
		}
		decoded, err := hex.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("hex payload: %w", err)
		}
		data = decoded
	case "base64":
		clean := strings.Join(strings.Fields(text), "")
		decoded, err := base64.StdEncoding.Strict().DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("base64 payload: %w", err)
		}
		data = decoded
	default:
		return nil, fmt.Errorf("unknown MQTT payload type %q; use text, json, hex or base64", kind)
	}
	if len(data) > mqttMaxPayload {
		return nil, fmt.Errorf("MQTT payloads are limited to %d KiB", mqttMaxPayload>>10)
	}
	return data, nil
}

// clone returns a copy whose slices can be expanded without changing the
// authored document.
func (c MQTTConfig) clone() MQTTConfig {
	out := c
	out.Topics = slices.Clone(c.Topics)
	for i := range out.Topics {
		out.Topics[i].UserProperties = slices.Clone(out.Topics[i].UserProperties)
	}
	out.Messages = slices.Clone(c.Messages)
	for i := range out.Messages {
		out.Messages[i].Properties.UserProperties = slices.Clone(out.Messages[i].Properties.UserProperties)
	}
	out.Properties.UserProperties = slices.Clone(c.Properties.UserProperties)
	out.Will.Properties.UserProperties = slices.Clone(c.Will.Properties.UserProperties)
	return out
}

// expandDynamic resolves $random* names in every authored MQTT string.
func (c *MQTTConfig) expandDynamic(defined map[string]string) {
	c.visitStrings(func(s *string) { *s = expandDynamic(*s, defined) })
}

// expand resolves {{variable}} references in every authored MQTT string.
func (c *MQTTConfig) expand(vars map[string]string) {
	c.visitStrings(func(s *string) { *s = ExpandVars(*s, vars) })
}

func (c *MQTTConfig) visitStrings(f func(*string)) {
	f(&c.ClientID)
	f(&c.Will.Topic)
	f(&c.Will.Payload)
	f(&c.Will.Properties.ResponseTopic)
	f(&c.Will.Properties.CorrelationData)
	f(&c.Will.Properties.ContentType)
	for i := range c.Will.Properties.UserProperties {
		f(&c.Will.Properties.UserProperties[i].Key)
		f(&c.Will.Properties.UserProperties[i].Value)
	}
	for i := range c.Properties.UserProperties {
		f(&c.Properties.UserProperties[i].Key)
		f(&c.Properties.UserProperties[i].Value)
	}
	for i := range c.Topics {
		t := &c.Topics[i]
		f(&t.Name)
		f(&t.Description)
		for j := range t.UserProperties {
			f(&t.UserProperties[j].Key)
			f(&t.UserProperties[j].Value)
		}
	}
	for i := range c.Messages {
		m := &c.Messages[i]
		f(&m.Name)
		f(&m.Description)
		f(&m.Topic)
		f(&m.Payload)
		f(&m.Properties.ResponseTopic)
		f(&m.Properties.CorrelationData)
		f(&m.Properties.ContentType)
		for j := range m.Properties.UserProperties {
			f(&m.Properties.UserProperties[j].Key)
			f(&m.Properties.UserProperties[j].Value)
		}
	}
}

// mqttStrings lists every authored MQTT string for variable discovery.
func (c MQTTConfig) mqttStrings() []string {
	var out []string
	c.visitStrings(func(s *string) { out = append(out, *s) })
	return out
}

// TopicsToSubscribe returns the document's topics that connect and the
// "Subscribe listed topics" action use.
func (c MQTTConfig) TopicsToSubscribe() []MQTTSubscription {
	var out []MQTTSubscription
	for _, t := range c.Topics {
		if t.Subscribe != nil && !*t.Subscribe {
			continue
		}
		out = append(out, MQTTSubscription{
			Topic: t.Name, QoS: t.QoS, NoLocal: t.NoLocal, RetainAsPublished: t.RetainAsPublished,
			RetainHandling: t.RetainHandling, SubscriptionIdentifier: t.SubscriptionIdentifier,
			UserProperties: slices.Clone(t.UserProperties),
		})
	}
	return out
}
