package request

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/eclipse/paho.golang/packets"
	paho "github.com/eclipse/paho.golang/paho"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

// The engine keeps at most this much of one incoming payload in memory. Size
// on MQTTIncoming still says how much the broker sent.
const mqttIncomingKeep = 64 << 10

// mqttQueue bounds messages waiting for the session reader. A busy topic
// drops the oldest waiting message rather than growing without limit.
const mqttQueue = 128

const (
	// mqttWriteChunk bounds one socket write. A slow broker can take longer
	// than the timeout for a whole 1 MiB packet, but each chunk must make
	// progress within the timeout.
	mqttWriteChunk = 32 << 10
	// mqttWriteTimeout is the progress timeout for one chunk, covering Paho's
	// own writes as well as publishes. Paho's QoS 0 publish does not watch the
	// context, so the socket deadline is the only way out.
	mqttWriteTimeout = 5 * time.Second
	// mqttDisconnectTimeout lets Close send DISCONNECT even while a publish
	// holds Paho's write lock.
	mqttDisconnectTimeout = time.Second
	// mqttProxyVerifyHeader asks the capture proxy to verify the real broker
	// certificate instead of accepting any upstream certificate.
	mqttProxyVerifyHeader = "X-Vscroxy-Verify-TLS"
)

// MQTTClient is one live broker connection. It is the only protocol engine
// that has two implementations: MQTT 3.1.1 uses Paho's v3 client and MQTT 5.0
// uses Paho's v5 client, both over the same dialer and TLS settings.
type MQTTClient struct {
	version int
	conn    net.Conn
	v3      mqtt.Client
	v5      *paho.Client

	incoming chan MQTTIncoming
	done     chan struct{}
	once     sync.Once
	errMu    sync.Mutex
	err      error
	closed   atomic.Bool
	dropped  atomic.Int64

	clientID          string
	sessionPresent    bool
	topicAliasMaximum uint16
}

// DialMQTT connects to rawURL and completes the MQTT handshake. tlsConfig is
// used for mqtts:// and wss://; it carries the configured roots and client
// certificate. proxy is an HTTP proxy for the ws:// and wss:// upgrade (the
// capture proxy); mqtt:// and mqtts:// are raw TCP and connect directly.
// verifyUpstream asks the capture proxy to verify the real broker certificate
// instead of its default insecure inspection.
func DialMQTT(ctx context.Context, rawURL string, cfg MQTTConfig, tlsConfig *tls.Config, proxy string, verifyUpstream bool) (*MQTTClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, err := parseMQTTURL(rawURL)
	if err != nil {
		return nil, err
	}
	timeout := cfg.ConnectTimeout()
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialMQTTConn(dialCtx, u, tlsConfig, timeout, proxy, verifyUpstream)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	limit := 1 << 20
	if cfg.Version == MQTT5 && cfg.Properties.MaximumPacketSize > limit {
		limit = cfg.Properties.MaximumPacketSize
	}
	conn = &mqttWriteConn{Conn: &mqttLimitConn{Conn: conn, limit: limit}}
	c := &MQTTClient{version: cfg.Version, conn: conn, incoming: make(chan MQTTIncoming, mqttQueue), done: make(chan struct{})}
	if cfg.Version == MQTT5 {
		err = c.connect5(dialCtx, cfg)
	} else {
		err = c.connect3(dialCtx, rawURL, cfg)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *MQTTClient) connect3(ctx context.Context, rawURL string, cfg MQTTConfig) error {
	opts := mqtt.NewClientOptions().AddBroker(rawURL).SetProtocolVersion(MQTT311).
		SetClientID(cfg.ClientID).SetCleanSession(cfg.CleanStartEnabled()).
		SetKeepAlive(time.Duration(cfg.KeepAlive) * time.Second).
		SetConnectTimeout(cfg.ConnectTimeout()).
		SetAutoReconnect(false).SetConnectRetry(false).SetOrderMatters(true)
	opts.SetDefaultPublishHandler(c.onV3Message)
	opts.SetUsername(cfg.Username)
	opts.SetPassword(cfg.Password)
	if cfg.Will.Enabled {
		payload, err := MQTTPayload(cfg.Will.Payload, cfg.Will.Type)
		if err != nil {
			return err
		}
		opts.SetBinaryWill(cfg.Will.Topic, payload, cfg.Will.QoS, cfg.Will.Retain)
	}
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) { c.fail(err) })
	// The connection was dialed with the shared TLS/certificate settings
	// above; Paho only has to speak MQTT on it.
	opts.SetCustomOpenConnectionFn(func(*url.URL, mqtt.ClientOptions) (net.Conn, error) { return c.conn, nil })
	client := mqtt.NewClient(opts)
	token := client.Connect()
	select {
	case <-ctx.Done():
		_ = c.conn.Close()
		return ctx.Err()
	case <-token.Done():
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("MQTT connect: %w", err)
	}
	if connected, ok := token.(*mqtt.ConnectToken); ok {
		c.sessionPresent = connected.SessionPresent()
	}
	c.v3, c.clientID = client, cfg.ClientID
	return nil
}

func (c *MQTTClient) connect5(ctx context.Context, cfg MQTTConfig) error {
	connect := mqtt5Connect(cfg)
	props := &paho.ConnectProperties{RequestProblemInfo: true}
	if cfg.Properties.SessionExpiry > 0 {
		props.SessionExpiryInterval = uint32Ptr(uint32(cfg.Properties.SessionExpiry))
	}
	if cfg.Properties.ReceiveMaximum > 0 {
		props.ReceiveMaximum = uint16Ptr(uint16(cfg.Properties.ReceiveMaximum))
	}
	// Advertise a bound even when the document leaves it at 0: a broker must
	// not send a packet larger than the client accepts.
	maximum := cfg.Properties.MaximumPacketSize
	if maximum <= 0 {
		maximum = 1 << 20
	}
	props.MaximumPacketSize = uint32Ptr(uint32(maximum))
	props.RequestResponseInfo = cfg.Properties.RequestResponseInformation
	props.User = userProperties(cfg.Properties.UserProperties)
	connect.Properties = props
	if cfg.Will.Enabled {
		payload, err := MQTTPayload(cfg.Will.Payload, cfg.Will.Type)
		if err != nil {
			return err
		}
		connect.WillMessage = &paho.WillMessage{Topic: cfg.Will.Topic, Payload: payload, QoS: cfg.Will.QoS, Retain: cfg.Will.Retain}
		will := &paho.WillProperties{ContentType: cfg.Will.Properties.ContentType,
			ResponseTopic: cfg.Will.Properties.ResponseTopic, User: userProperties(cfg.Will.Properties.UserProperties)}
		if cfg.Will.Properties.PayloadFormat {
			will.PayloadFormat = bytePtr(1)
		}
		if cfg.Will.Properties.MessageExpiry > 0 {
			will.MessageExpiry = uint32Ptr(uint32(cfg.Will.Properties.MessageExpiry))
		}
		if cfg.Will.Properties.WillDelay > 0 {
			will.WillDelayInterval = uint32Ptr(uint32(cfg.Will.Properties.WillDelay))
		}
		if cfg.Will.Properties.CorrelationData != "" {
			will.CorrelationData = []byte(cfg.Will.Properties.CorrelationData)
		}
		connect.WillProperties = will
	}
	conn := c.conn
	if cfg.Will.Enabled && cfg.Will.Properties.nonZero() {
		// Paho v0.23 encodes will properties with the CONNECT property
		// context: content type, response topic, correlation data, payload
		// format and message expiry are lost, while user properties and the
		// will delay interval survive. Substitute the first packet (the
		// CONNECT) with a correctly encoded one; the client's own read loop
		// and later packets are untouched.
		replacement := mqtt5ConnectBytes(connect)
		// Paho's Client.Connect sets these on the packet it writes, after this
		// point; the length measured here must include them.
		packet := connect.Packet()
		packet.ProtocolName, packet.ProtocolVersion = "MQTT", 5
		var original bytes.Buffer
		if _, err := packet.WriteTo(&original); err != nil {
			return err
		}
		conn = &replaceFirstWriteConn{Conn: c.conn, want: original.Len(), replacement: replacement}
	}
	client := paho.NewClient(paho.ClientConfig{
		Conn:          packets.NewThreadSafeConn(conn),
		PacketTimeout: cfg.ConnectTimeout(),
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
			c.push(incoming5(p.Packet))
			return false, nil
		}},
		OnServerDisconnect: func(d *paho.Disconnect) {
			reason := mqtt5Reason(d.ReasonCode)
			if d.Properties != nil && d.Properties.ReasonString != "" {
				reason += ": " + d.Properties.ReasonString
			}
			c.failServer(fmt.Errorf("broker disconnected: %s", reason))
		},
		OnClientError: func(err error) { c.fail(err) },
	})
	connack, err := client.Connect(ctx, connect)
	// Paho returns the CONNACK together with the error when the broker
	// refuses the connection; the reason code is the useful part.
	if connack != nil && connack.ReasonCode >= 0x80 {
		reason := mqtt5Reason(connack.ReasonCode)
		if connack.Properties != nil && connack.Properties.ReasonString != "" {
			reason += ": " + connack.Properties.ReasonString
		}
		return fmt.Errorf("MQTT connect refused: %s", reason)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MQTT connect: %w", err)
	}
	if connack == nil {
		return errors.New("MQTT connect: the broker returned no CONNACK")
	}
	c.v5, c.clientID, c.sessionPresent = client, client.ClientID(), connack.SessionPresent
	if connack.Properties != nil && connack.Properties.TopicAliasMaximum != nil {
		c.topicAliasMaximum = *connack.Properties.TopicAliasMaximum
	}
	return nil
}

func mqtt5Connect(cfg MQTTConfig) *paho.Connect {
	return &paho.Connect{
		ClientID: cfg.ClientID, CleanStart: cfg.CleanStartEnabled(), KeepAlive: uint16(cfg.KeepAlive),
		Username: cfg.Username, Password: []byte(cfg.Password),
		UsernameFlag: cfg.Username != "", PasswordFlag: cfg.Password != "",
	}
}

// mqtt5ConnectBytes encodes the CONNECT packet with the will properties in
// their own context, which Paho v0.23 does not do.
func mqtt5ConnectBytes(c *paho.Connect) []byte {
	packet := c.Packet()
	var flags byte
	if c.CleanStart {
		flags |= 0x02
	}
	if c.WillMessage != nil {
		flags |= 0x04 | c.WillMessage.QoS<<3
		if c.WillMessage.Retain {
			flags |= 0x20
		}
	}
	if c.PasswordFlag {
		flags |= 0x40
	}
	if c.UsernameFlag {
		flags |= 0x80
	}
	var body bytes.Buffer
	mqttString(&body, "MQTT")
	body.WriteByte(5)
	body.WriteByte(flags)
	var keepAlive [2]byte
	binary.BigEndian.PutUint16(keepAlive[:], c.KeepAlive)
	body.Write(keepAlive[:])
	properties := packet.Properties.Pack(packets.CONNECT)
	mqttVBI(&body, len(properties))
	body.Write(properties)
	mqttString(&body, c.ClientID)
	if c.WillMessage != nil {
		willProperties := mqttWillProperties(c.WillProperties)
		mqttVBI(&body, len(willProperties))
		body.Write(willProperties)
		mqttString(&body, c.WillMessage.Topic)
		mqttBinary(&body, c.WillMessage.Payload)
	}
	if c.UsernameFlag {
		mqttString(&body, c.Username)
	}
	if c.PasswordFlag {
		mqttBinary(&body, c.Password)
	}
	var out bytes.Buffer
	out.WriteByte(0x10)
	mqttVBI(&out, body.Len())
	out.Write(body.Bytes())
	return out.Bytes()
}

func mqttWillProperties(p *paho.WillProperties) []byte {
	if p == nil {
		return nil
	}
	var out bytes.Buffer
	if p.PayloadFormat != nil {
		out.WriteByte(packets.PropPayloadFormat)
		out.WriteByte(*p.PayloadFormat)
	}
	if p.MessageExpiry != nil {
		out.WriteByte(packets.PropMessageExpiry)
		var value [4]byte
		binary.BigEndian.PutUint32(value[:], *p.MessageExpiry)
		out.Write(value[:])
	}
	if p.ContentType != "" {
		out.WriteByte(packets.PropContentType)
		mqttString(&out, p.ContentType)
	}
	if p.ResponseTopic != "" {
		out.WriteByte(packets.PropResponseTopic)
		mqttString(&out, p.ResponseTopic)
	}
	if len(p.CorrelationData) > 0 {
		out.WriteByte(packets.PropCorrelationData)
		mqttBinary(&out, p.CorrelationData)
	}
	if p.WillDelayInterval != nil {
		out.WriteByte(packets.PropWillDelayInterval)
		var value [4]byte
		binary.BigEndian.PutUint32(value[:], *p.WillDelayInterval)
		out.Write(value[:])
	}
	for _, user := range p.User {
		out.WriteByte(packets.PropUser)
		mqttString(&out, user.Key)
		mqttString(&out, user.Value)
	}
	return out.Bytes()
}

func mqttString(b *bytes.Buffer, value string) {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	b.Write(length[:])
	b.WriteString(value)
}

func mqttBinary(b *bytes.Buffer, value []byte) {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	b.Write(length[:])
	b.Write(value)
}

func mqttVBI(b *bytes.Buffer, value int) {
	for {
		digit := byte(value % 128)
		value /= 128
		if value > 0 {
			digit |= 0x80
		}
		b.WriteByte(digit)
		if value == 0 {
			return
		}
	}
}

// mqttWriteConn applies a progress deadline to every write, including Paho's
// own PINGREQ, PUBACK and DISCONNECT packets. Splitting a large packet keeps a
// slow but healthy broker alive: only a chunk with no progress for the timeout
// fails. Close switches to the shorter disconnect timeout.
type mqttWriteConn struct {
	net.Conn
	closing atomic.Bool
}

func (c *mqttWriteConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := min(len(p), mqttWriteChunk)
		timeout := mqttWriteTimeout
		if c.closing.Load() {
			timeout = mqttDisconnectTimeout
		}
		if err := c.Conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return total, err
		}
		n, err := c.Conn.Write(p[:chunk])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
		p = p[n:]
	}
	_ = c.Conn.SetWriteDeadline(time.Time{})
	return total, nil
}

// closeWrite switches subsequent writes to the disconnect budget and
// interrupts a write already waiting on the longer publish budget.
func (c *mqttWriteConn) closeWrite() {
	c.closing.Store(true)
	_ = c.Conn.SetWriteDeadline(time.Now().Add(mqttDisconnectTimeout))
}

// mqttLimitConn rejects a broker packet larger than limit before Paho
// allocates its payload. MQTT 3.1.1 has no maximum-packet-size negotiation,
// so this is the client-side bound; MQTT 5.0 also advertises the same limit.
// Reads are a byte stream: a packet is a fixed header, a variable-length
// remaining length, then that many bytes.
type mqttLimitConn struct {
	net.Conn
	limit      int
	state      int // 0 header, 1 remaining length, 2 payload
	remaining  int
	multiplier int
	over       bool
}

func (c *mqttLimitConn) Read(p []byte) (int, error) {
	if c.over {
		return 0, c.errLimit()
	}
	n, err := c.Conn.Read(p)
	for i := 0; i < n; i++ {
		c.consume(p[i])
		if c.over {
			// Stop before the byte that proved the packet is too large. Paho
			// ignores a read error when it also gets bytes, then allocates the
			// declared body; a short read with the error prevents that.
			return i, c.errLimit()
		}
	}
	return n, err
}

func (c *mqttLimitConn) errLimit() error {
	return fmt.Errorf("MQTT packet exceeds the %d-byte client limit", c.limit)
}

func (c *mqttLimitConn) consume(b byte) {
	switch c.state {
	case 0:
		c.remaining, c.multiplier, c.state = 0, 1, 1
	case 1:
		c.remaining += int(b&0x7f) * c.multiplier
		if b&0x80 != 0 {
			c.multiplier *= 128
			if c.multiplier > 128*128*128 {
				c.over = true
			}
			return
		}
		if c.remaining > c.limit {
			c.over = true
			return
		}
		if c.remaining == 0 {
			c.state = 0
		} else {
			c.state = 2
		}
	case 2:
		c.remaining--
		if c.remaining == 0 {
			c.state = 0
		}
	}
}

// replaceFirstWriteConn discards the first MQTT packet Paho writes (the
// CONNECT) and sends replacement instead. Bytes past that packet pass through
// unchanged; Connect waits for CONNACK, so no later packet can arrive first.
type replaceFirstWriteConn struct {
	net.Conn
	mu          sync.Mutex
	want, got   int
	replacement []byte
	done        bool
}

func (c *replaceFirstWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return c.Conn.Write(p)
	}
	if c.got+len(p) < c.want {
		c.got += len(p)
		return len(p), nil
	}
	over := c.got + len(p) - c.want
	c.done = true
	if _, err := c.Conn.Write(c.replacement); err != nil {
		return 0, err
	}
	if over > 0 {
		if _, err := c.Conn.Write(p[len(p)-over:]); err != nil {
			return len(p) - over, err
		}
	}
	return len(p), nil
}

func uint16Ptr(v uint16) *uint16 { return &v }
func uint32Ptr(v uint32) *uint32 { return &v }
func bytePtr(v byte) *byte       { return &v }

func userProperties(props []MQTTProperty) paho.UserProperties {
	if len(props) == 0 {
		return nil
	}
	out := make(paho.UserProperties, 0, len(props))
	for _, p := range props {
		out = append(out, paho.UserProperty{Key: p.Key, Value: p.Value})
	}
	return out
}

func incoming5(p *paho.Publish) MQTTIncoming {
	msg := MQTTIncoming{Topic: p.Topic, Payload: p.Payload, Size: len(p.Payload), QoS: p.QoS, Retain: p.Retain, Duplicate: p.Duplicate()}
	if p.Properties == nil {
		return msg
	}
	props := &msg.Properties
	if p.Properties.PayloadFormat != nil {
		props.PayloadFormat = *p.Properties.PayloadFormat == 1
	}
	if p.Properties.MessageExpiry != nil {
		props.MessageExpiry = int(*p.Properties.MessageExpiry)
	}
	props.ContentType = clipMQTTText(p.Properties.ContentType, 4096)
	props.ResponseTopic = clipMQTTText(p.Properties.ResponseTopic, 4096)
	props.CorrelationData = p.Properties.CorrelationData
	if len(props.CorrelationData) > 4096 {
		props.CorrelationData = props.CorrelationData[:4096]
	}
	if p.Properties.SubscriptionIdentifier != nil {
		props.SubscriptionIdentifier = *p.Properties.SubscriptionIdentifier
	}
	for _, u := range p.Properties.User {
		if len(props.UserProperties) >= mqttMaxProperties {
			break
		}
		props.UserProperties = append(props.UserProperties, MQTTProperty{Key: clipMQTTText(u.Key, 1024), Value: clipMQTTText(u.Value, 1024)})
	}
	return msg
}

func clipMQTTText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// push bounds the payload retained for the UI and never blocks the protocol
// reader: a full queue drops the new message and counts it.
func (c *MQTTClient) push(msg MQTTIncoming) {
	if len(msg.Payload) > mqttIncomingKeep {
		msg.Payload = slices.Clone(msg.Payload[:mqttIncomingKeep])
	} else {
		msg.Payload = slices.Clone(msg.Payload)
	}
	select {
	case c.incoming <- msg:
	default:
		c.dropped.Add(1)
	}
}

func (c *MQTTClient) fail(err error) {
	if c.closed.Load() {
		return
	}
	if err == nil {
		err = errors.New("MQTT connection closed")
	}
	c.errMu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.errMu.Unlock()
	c.once.Do(func() { close(c.done) })
}

// failServer records a broker DISCONNECT reason even when the transport
// teardown reported a generic error first.
func (c *MQTTClient) failServer(err error) {
	if c.closed.Load() {
		return
	}
	c.errMu.Lock()
	c.err = err
	c.errMu.Unlock()
	c.once.Do(func() { close(c.done) })
}

// Done closes when the broker connection ends, whether by a network error, a
// broker disconnect or Close.
func (c *MQTTClient) Done() <-chan struct{} { return c.done }

// Err is why Done closed, or a generic error for a clean Close.
func (c *MQTTClient) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("MQTT connection closed")
}

// Incoming is the bounded delivery queue.
func (c *MQTTClient) Incoming() <-chan MQTTIncoming { return c.incoming }

// Dropped counts messages the bounded queue discarded.
func (c *MQTTClient) Dropped() int64 { return c.dropped.Load() }

// ClientID is the ID the broker knows, including a v5 assigned ID.
func (c *MQTTClient) ClientID() string { return c.clientID }

// SessionPresent reports whether the broker resumed an existing session.
func (c *MQTTClient) SessionPresent() bool { return c.sessionPresent }

// Close sends DISCONNECT when the transport still works, then closes it.
func (c *MQTTClient) Close() {
	if c.closed.Swap(true) {
		return
	}
	// Disconnect writes on the same connection as a publish. If the broker
	// stopped reading, a publish may hold Paho's write lock; the write wrapper
	// interrupts it and gives DISCONNECT a shorter budget.
	if writeConn, ok := c.conn.(*mqttWriteConn); ok {
		writeConn.closeWrite()
	}
	switch {
	case c.v3 != nil && c.v3.IsConnected():
		// Quiesce 0 can return before Paho's outbound goroutine writes
		// DISCONNECT; a short wait gives the packet a chance to leave.
		c.v3.Disconnect(100)
	case c.v5 != nil:
		_ = c.v5.Disconnect(&paho.Disconnect{ReasonCode: 0})
	}
	_ = c.conn.Close()
	c.once.Do(func() { close(c.done) })
}

// Subscribe adds filters and returns the granted QoS for each, in order. One
// failing filter does not hide the grants of the others.
func (c *MQTTClient) Subscribe(ctx context.Context, subs []MQTTSubscription) ([]byte, error) {
	if len(subs) == 0 {
		return nil, nil
	}
	if len(subs) > mqttMaxTopics {
		return nil, fmt.Errorf("MQTT subscribes to at most %d filters at once", mqttMaxTopics)
	}
	for _, sub := range subs {
		if err := ValidateMQTTTopicFilter(sub.Topic); err != nil {
			return nil, err
		}
		if sub.QoS > 2 {
			return nil, fmt.Errorf("topic %q QoS must be 0, 1 or 2", sub.Topic)
		}
	}
	if c.v5 != nil {
		return c.subscribe5(ctx, subs)
	}
	filters := make(map[string]byte, len(subs))
	for _, sub := range subs {
		filters[sub.Topic] = sub.QoS
	}
	// The default handler receives one copy of a message even when several
	// filters match it; a per-filter callback would receive one copy per
	// matching filter.
	token := c.v3.SubscribeMultiple(filters, nil)
	if err := waitMQTTToken(ctx, token); err != nil {
		return nil, err
	}
	result := token.(*mqtt.SubscribeToken).Result()
	grants := make([]byte, 0, len(subs))
	for _, sub := range subs {
		grants = append(grants, result[sub.Topic])
	}
	return grants, nil
}

func (c *MQTTClient) subscribe5(ctx context.Context, subs []MQTTSubscription) ([]byte, error) {
	grants := make([]byte, 0, len(subs))
	var errs []error
	for _, sub := range subs {
		packet := &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{
			Topic: sub.Topic, QoS: sub.QoS, NoLocal: sub.NoLocal,
			RetainAsPublished: sub.RetainAsPublished, RetainHandling: sub.RetainHandling,
		}}}
		if sub.SubscriptionIdentifier > 0 || len(sub.UserProperties) > 0 {
			props := &paho.SubscribeProperties{User: userProperties(sub.UserProperties)}
			if sub.SubscriptionIdentifier > 0 {
				id := sub.SubscriptionIdentifier
				props.SubscriptionIdentifier = &id
			}
			packet.Properties = props
		}
		ack, err := c.v5.Subscribe(ctx, packet)
		// Paho returns the SUBACK together with its error for a refused
		// filter; keep the broker's reason instead of only Paho's text.
		if ack != nil && len(ack.Reasons) == 1 {
			reason := ack.Reasons[0]
			grants = append(grants, reason)
			if reason >= 0x80 {
				errs = append(errs, fmt.Errorf("%s: %s", sub.Topic, mqtt5Reason(reason)))
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sub.Topic, err))
			}
			continue
		}
		grants = append(grants, 0x80)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sub.Topic, err))
		} else if ack != nil {
			errs = append(errs, fmt.Errorf("%s: broker returned %d subscription results", sub.Topic, len(ack.Reasons)))
		} else {
			errs = append(errs, fmt.Errorf("%s: broker returned no subscription result", sub.Topic))
		}
	}
	return grants, errors.Join(errs...)
}

func (c *MQTTClient) onV3Message(_ mqtt.Client, m mqtt.Message) {
	c.push(MQTTIncoming{Topic: m.Topic(), Payload: m.Payload(), Size: len(m.Payload()), QoS: m.Qos(), Retain: m.Retained(), Duplicate: m.Duplicate()})
}

func (c *MQTTClient) Unsubscribe(ctx context.Context, topics []string) error {
	if len(topics) == 0 {
		return nil
	}
	for _, topic := range topics {
		if err := ValidateMQTTTopicFilter(topic); err != nil {
			return err
		}
	}
	if c.v5 != nil {
		ack, err := c.v5.Unsubscribe(ctx, &paho.Unsubscribe{Topics: topics})
		if err != nil {
			return err
		}
		for i, reason := range ack.Reasons {
			if reason >= 0x80 {
				name := topicAt(topics, i)
				return fmt.Errorf("%s: %s", name, mqtt5Reason(reason))
			}
		}
		return nil
	}
	return waitMQTTToken(ctx, c.v3.Unsubscribe(topics...))
}

func topicAt(topics []string, i int) string {
	if i < len(topics) {
		return topics[i]
	}
	return "topic"
}

func (c *MQTTClient) Publish(ctx context.Context, msg MQTTPublish) error {
	if c.closed.Load() {
		return errors.New("MQTT connection is closed")
	}
	if msg.QoS > 2 {
		return fmt.Errorf("MQTT publish QoS must be 0, 1 or 2")
	}
	if len(msg.Payload) > mqttMaxPayload {
		return fmt.Errorf("MQTT payloads are limited to %d KiB", mqttMaxPayload>>10)
	}
	if c.version == MQTT311 && msg.Properties.nonZero() {
		return errors.New("MQTT 5 publish properties require protocol version 5.0")
	}
	if msg.Topic == "" {
		if msg.Properties.TopicAlias == 0 {
			return errors.New("the MQTT topic is empty; set a topic alias to publish without one")
		}
	} else if err := ValidateMQTTTopicName(msg.Topic); err != nil {
		return err
	}
	if msg.Properties.TopicAlias < 0 || msg.Properties.TopicAlias > 65535 {
		return errors.New("the MQTT topic alias must be between 1 and 65535")
	}
	if msg.Properties.TopicAlias > 0 {
		if c.topicAliasMaximum == 0 {
			return errors.New("the broker does not support topic aliases")
		}
		if msg.Properties.TopicAlias > int(c.topicAliasMaximum) {
			return fmt.Errorf("topic alias %d exceeds the broker's maximum %d", msg.Properties.TopicAlias, c.topicAliasMaximum)
		}
	}
	if c.v5 != nil {
		return c.publish5(ctx, msg)
	}
	return waitMQTTToken(ctx, c.v3.Publish(msg.Topic, msg.QoS, msg.Retain, msg.Payload))
}

func (c *MQTTClient) publish5(ctx context.Context, msg MQTTPublish) error {
	p := &paho.Publish{Topic: msg.Topic, QoS: msg.QoS, Retain: msg.Retain, Payload: msg.Payload}
	if msg.Properties.nonZero() {
		props := &paho.PublishProperties{
			ContentType: msg.Properties.ContentType, ResponseTopic: msg.Properties.ResponseTopic,
			User: userProperties(msg.Properties.UserProperties),
		}
		if msg.Properties.PayloadFormat {
			props.PayloadFormat = bytePtr(1)
		}
		if msg.Properties.MessageExpiry > 0 {
			props.MessageExpiry = uint32Ptr(uint32(msg.Properties.MessageExpiry))
		}
		if msg.Properties.TopicAlias > 0 {
			props.TopicAlias = uint16Ptr(uint16(msg.Properties.TopicAlias))
		}
		if msg.Properties.CorrelationData != "" {
			props.CorrelationData = []byte(msg.Properties.CorrelationData)
		}
		p.Properties = props
	}
	response, err := c.v5.Publish(ctx, p)
	if err != nil {
		return err
	}
	if response != nil && response.ReasonCode >= 0x80 {
		return fmt.Errorf("broker rejected publish: %s", mqtt5Reason(response.ReasonCode))
	}
	return nil
}

func waitMQTTToken(ctx context.Context, token mqtt.Token) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
	}
	return token.Error()
}

// mqtt5Reason names the reason codes the UI can show. Unknown codes stay
// numeric, never guessed.
func mqtt5Reason(code byte) string {
	names := map[byte]string{
		0x00: "success", 0x01: "granted QoS 1", 0x02: "granted QoS 2",
		0x80: "unspecified error", 0x81: "malformed packet", 0x82: "protocol error",
		0x83: "implementation specific error", 0x84: "unsupported protocol version",
		0x85: "client identifier not valid", 0x86: "bad user name or password",
		0x87: "not authorized", 0x88: "server unavailable", 0x89: "server busy",
		0x8A: "banned", 0x8B: "server shutting down", 0x8C: "bad authentication method",
		0x8D: "keep alive timeout", 0x8E: "session taken over", 0x8F: "topic filter invalid",
		0x90: "topic name invalid", 0x91: "packet identifier in use",
		0x92: "packet identifier not found", 0x93: "receive maximum exceeded",
		0x94: "topic alias invalid", 0x95: "packet too large", 0x96: "message rate too high",
		0x97: "quota exceeded", 0x98: "administrative action", 0x99: "payload format invalid",
		0x9A: "retain not supported", 0x9B: "QoS not supported", 0x9C: "use another server",
		0x9D: "server moved", 0x9E: "shared subscriptions not supported",
		0x9F: "connection rate exceeded", 0xA0: "maximum connect time",
		0xA1: "subscription identifiers not supported", 0xA2: "wildcard subscriptions not supported",
	}
	if name, ok := names[code]; ok {
		return fmt.Sprintf("%s (0x%02x)", name, code)
	}
	return fmt.Sprintf("reason code 0x%02x", code)
}

// dialMQTTConn opens TCP or WebSocket and completes TLS where the scheme asks
// for it. TLS names the broker host and uses the configured roots and client
// certificate, never an unverified fallback. A WebSocket upgrade is HTTP, so
// it can use the capture proxy; raw MQTT TCP cannot.
func dialMQTTConn(ctx context.Context, u *url.URL, tlsConfig *tls.Config, timeout time.Duration, proxy string, verifyUpstream bool) (net.Conn, error) {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
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
	address := net.JoinHostPort(u.Hostname(), port)
	switch scheme {
	case "mqtt", "mqtts":
		dialer := net.Dialer{Timeout: timeout}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		if scheme == "mqtts" {
			if tlsConfig == nil {
				_ = conn.Close()
				return nil, errors.New("mqtts:// needs a TLS configuration")
			}
			config := tlsConfig.Clone()
			if config.ServerName == "" {
				config.ServerName = u.Hostname()
			}
			tlsConn := tls.Client(conn, config)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("TLS handshake: %w", err)
			}
			return tlsConn, nil
		}
		return conn, nil
	case "ws", "wss":
		dialer := websocket.Dialer{HandshakeTimeout: timeout, Subprotocols: []string{"mqtt"}}
		if proxy != "" {
			proxyAddress := proxy
			// gorilla's built-in proxy dialer sends a CONNECT without the
			// capture header, which makes the proxy inspect an upstream
			// certificate it was asked to verify. Tunnel explicitly instead.
			dialer.NetDialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
				return dialMQTTProxy(ctx, network, proxyAddress, target, timeout, verifyUpstream)
			}
		}
		if scheme == "wss" {
			if tlsConfig == nil {
				return nil, errors.New("wss:// needs a TLS configuration")
			}
			dialer.TLSClientConfig = tlsConfig
		}
		conn, response, err := dialer.DialContext(ctx, u.String(), http.Header{})
		if err != nil {
			if response != nil {
				return nil, fmt.Errorf("%s: %w", response.Status, err)
			}
			return nil, err
		}
		if selected := conn.Subprotocol(); selected != "" && selected != "mqtt" {
			_ = conn.Close()
			return nil, fmt.Errorf("broker selected the %q WebSocket subprotocol instead of mqtt", selected)
		}
		return &mqttWSConn{conn: conn}, nil
	}
	return nil, errors.New("unsupported MQTT transport")
}

// dialMQTTProxy opens a CONNECT tunnel through the capture proxy. The
// explicit header is what makes the proxy verify the broker certificate when
// the request asks for it; gorilla's proxy dialer cannot carry it.
func dialMQTTProxy(ctx context.Context, network, proxyAddress, target string, timeout time.Duration, verify bool) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, network, proxyAddress)
	if err != nil {
		return nil, fmt.Errorf("proxy %s: %w", proxyAddress, err)
	}
	// Bound the CONNECT exchange: a stalled proxy must not hold the dial
	// past its timeout or a cancelled connect.
	_ = conn.SetDeadline(time.Now().Add(timeout))
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	var request strings.Builder
	fmt.Fprintf(&request, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if verify {
		request.WriteString(mqttProxyVerifyHeader + ": 1\r\n")
	}
	request.WriteString("\r\n")
	if _, err := io.WriteString(conn, request.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %w", target, err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %w", target, err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %s", target, response.Status)
	}
	if !stop() {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %w", target, context.Cause(ctx))
	}
	_ = conn.SetDeadline(time.Time{})
	// Do not close a 200 CONNECT response body: Go models the tunnel as an
	// unbounded body, so closing it would close the connection we return.
	if reader.Buffered() > 0 {
		return &mqttProxyConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// mqttProxyConn preserves bytes the CONNECT response reader buffered before
// the WebSocket or TLS handshake starts.
type mqttProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *mqttProxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// mqttWSConn is a stream over binary WebSocket messages. MQTT packets may be
// written in more than one Write call; a broker treats the messages as one
// byte stream, as Paho's own WebSocket connector does.
type mqttWSConn struct {
	conn    *websocket.Conn
	readMu  sync.Mutex
	writeMu sync.Mutex
	reader  io.Reader
	// writeDeadline is applied to gorilla under writeMu. Calling gorilla's
	// SetWriteDeadline from another goroutine races with WriteMessage's frame
	// flush; the underlying NetConn deadline is safe and interrupts a write.
	writeDeadline atomic.Int64
}

func (c *mqttWSConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if c.reader == nil {
			kind, reader, err := c.conn.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			c.reader = reader
		}
		n, err := c.reader.Read(p)
		if errors.Is(err, io.EOF) {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *mqttWSConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var deadline time.Time
	if ns := c.writeDeadline.Load(); ns != 0 {
		deadline = time.Unix(0, ns)
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	if err := c.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *mqttWSConn) Close() error                      { return c.conn.Close() }
func (c *mqttWSConn) LocalAddr() net.Addr               { return c.conn.LocalAddr() }
func (c *mqttWSConn) RemoteAddr() net.Addr              { return c.conn.RemoteAddr() }
func (c *mqttWSConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

func (c *mqttWSConn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.writeDeadline.Store(0)
	} else {
		c.writeDeadline.Store(t.UnixNano())
	}
	// gorilla owns a writeDeadline field that WriteMessage reads; setting it
	// here races with an in-flight flush. Set the socket deadline instead so a
	// blocked write returns, and apply the value under writeMu in Write.
	return c.conn.NetConn().SetWriteDeadline(t)
}
func (c *mqttWSConn) SetDeadline(t time.Time) error {
	if err := c.conn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}
