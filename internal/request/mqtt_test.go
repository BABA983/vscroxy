package request

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	paho3 "github.com/eclipse/paho.mqtt.golang"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

// testBroker is an in-process MQTT 3.1.1/5.0 broker. The tests use the real
// wire protocol, not a mock client.
type testBroker struct {
	server   *mqtt.Server
	tcp      string
	ws       string
	recorder *connectRecorder
}

// connectRecorder keeps the CONNECT packet of each client, so a test can see
// properties a broker may not forward (such as will properties).
type connectRecorder struct {
	mqtt.HookBase
	mu       sync.Mutex
	connects map[string]packets.Packet
}

func (h *connectRecorder) ID() string { return "test-connect-recorder" }
func (h *connectRecorder) Provides(b byte) bool {
	return b == mqtt.OnConnect
}
func (h *connectRecorder) OnConnect(_ *mqtt.Client, pk packets.Packet) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connects == nil {
		h.connects = map[string]packets.Packet{}
	}
	h.connects[pk.Connect.ClientIdentifier] = pk
	return nil
}
func (h *connectRecorder) connect(clientID string) (packets.Packet, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pk, ok := h.connects[clientID]
	return pk, ok
}

func startTestBroker(t *testing.T, tlsConfig *tls.Config, websocket bool) *testBroker {
	t.Helper()
	server := mqtt.New(&mqtt.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	recorder := &connectRecorder{}
	if err := server.AddHook(recorder, nil); err != nil {
		t.Fatal(err)
	}
	tcp := listeners.NewTCP(listeners.Config{ID: "tcp", Address: "127.0.0.1:0", TLSConfig: tlsConfig})
	if err := server.AddListener(tcp); err != nil {
		t.Fatal(err)
	}
	broker := &testBroker{server: server, tcp: tcp.Address(), recorder: recorder}
	if websocket {
		// The WebSocket listener builds its http.Server from a fixed address,
		// so its port is reserved before Serve starts.
		broker.ws = freeAddress(t)
		if err := server.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: broker.ws, TLSConfig: tlsConfig})); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	if websocket {
		waitForListener(t, broker.ws)
	}
	t.Cleanup(func() { _ = server.Close() })
	return broker
}

func waitForListener(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener %s did not start", address)
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func mqttContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dialTestMQTT(t *testing.T, ctx context.Context, rawURL string, cfg MQTTConfig, tlsConfig *tls.Config) *MQTTClient {
	t.Helper()
	return dialTestMQTTVia(t, ctx, rawURL, cfg, tlsConfig, "", false)
}

func dialTestMQTTVia(t *testing.T, ctx context.Context, rawURL string, cfg MQTTConfig, tlsConfig *tls.Config, proxy string, verifyUpstream bool) *MQTTClient {
	t.Helper()
	client, err := DialMQTT(ctx, rawURL, cfg, tlsConfig, proxy, verifyUpstream)
	if err != nil {
		t.Fatalf("dial %s: %v", rawURL, err)
	}
	t.Cleanup(client.Close)
	return client
}

// readMQTTPacket reads one complete packet, including its fixed header and
// variable-length remaining-length field.
func readMQTTPacket(conn net.Conn) ([]byte, error) {
	header := make([]byte, 1)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	remaining, multiplier := 0, 1
	var length []byte
	for {
		digit := make([]byte, 1)
		if _, err := io.ReadFull(conn, digit); err != nil {
			return nil, err
		}
		length = append(length, digit[0])
		remaining += int(digit[0]&0x7f) * multiplier
		if digit[0]&0x80 == 0 {
			break
		}
		multiplier *= 128
		if multiplier > 128*128*128 {
			return nil, errors.New("malformed remaining length")
		}
	}
	body := make([]byte, remaining)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return append(append(header, length...), body...), nil
}

// mqttPacketBody returns the variable header and payload without the fixed
// header or remaining-length field.
func mqttPacketBody(packet []byte) []byte {
	i := 1
	for i < len(packet) && packet[i]&0x80 != 0 {
		i++
	}
	if i+1 > len(packet) {
		return nil
	}
	return packet[i+1:]
}

func writeMQTTPacket(conn net.Conn, header byte, body []byte) error {
	var out bytes.Buffer
	out.WriteByte(header)
	mqttVBI(&out, len(body))
	out.Write(body)
	_, err := conn.Write(out.Bytes())
	return err
}

func writeMQTTConnack(conn net.Conn, version int, reason byte, properties []byte) error {
	if version == MQTT5 {
		body := append([]byte{0, reason, byte(len(properties))}, properties...)
		return writeMQTTPacket(conn, 0x20, body)
	}
	return writeMQTTPacket(conn, 0x20, []byte{0, 0})
}

// startRawMQTTBroker accepts one connection, reads its CONNECT, and hands it
// to handle. The handler error is returned on the channel.
func startRawMQTTBroker(t *testing.T, handle func(net.Conn, []byte) error) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	errs := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errs <- err
			return
		}
		defer conn.Close()
		connect, err := readMQTTPacket(conn)
		if err != nil {
			errs <- err
			return
		}
		errs <- handle(conn, connect)
	}()
	return listener.Addr().String(), errs
}

func brokerError(t *testing.T, errs <-chan error) {
	t.Helper()
	select {
	case err := <-errs:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("raw broker did not finish")
	}
}

func receive(t *testing.T, client *MQTTClient, timeout time.Duration) MQTTIncoming {
	t.Helper()
	select {
	case msg := <-client.Incoming():
		return msg
	case <-client.Done():
		t.Fatalf("connection closed while waiting for a message: %v", client.Err())
	case <-time.After(timeout):
		t.Fatal("timed out waiting for an MQTT message")
	}
	return MQTTIncoming{}
}

func expectNoMessage(t *testing.T, client *MQTTClient, wait time.Duration) {
	t.Helper()
	select {
	case msg := <-client.Incoming():
		t.Fatalf("unexpected MQTT message on %q: %q", msg.Topic, msg.Payload)
	case <-client.Done():
		t.Fatalf("connection closed while expecting no message: %v", client.Err())
	case <-time.After(wait):
	}
}

func TestMQTTVersionsQoSRetainAndWildcards(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	for _, version := range []int{MQTT311, MQTT5} {
		t.Run(MQTTVersionLabel(version), func(t *testing.T) {
			ctx := mqttContext(t)
			prefix := fmt.Sprintf("v%d/", version)
			sub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: version, ClientID: "sub-" + MQTTVersionLabel(version)}, nil)
			pub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: version, ClientID: "pub-" + MQTTVersionLabel(version)}, nil)
			if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: prefix + "test/+/value", QoS: 2}, {Topic: prefix + "retained/#", QoS: 1}}); err != nil {
				t.Fatal(err)
			}
			for _, qos := range []byte{0, 1, 2} {
				if err := pub.Publish(ctx, MQTTPublish{Topic: prefix + "test/a/value", Payload: []byte{byte('0' + qos)}, QoS: qos}); err != nil {
					t.Fatalf("publish QoS %d: %v", qos, err)
				}
				msg := receive(t, sub, 5*time.Second)
				if msg.Topic != prefix+"test/a/value" || string(msg.Payload) != string([]byte{byte('0' + qos)}) || msg.QoS != qos || msg.Retain {
					t.Fatalf("QoS %d message = %+v", qos, msg)
				}
			}
			// A retained publish is delivered with the retain flag to a later
			// subscriber, while the live subscriber sees it without retain.
			if err := pub.Publish(ctx, MQTTPublish{Topic: prefix + "retained/one", Payload: []byte("kept"), QoS: 1, Retain: true}); err != nil {
				t.Fatal(err)
			}
			// Mochi forwards the retain flag to live subscribers too, so only
			// the topic and payload are asserted here.
			if msg := receive(t, sub, 5*time.Second); msg.Topic != prefix+"retained/one" || string(msg.Payload) != "kept" {
				t.Fatalf("live retained delivery = %+v", msg)
			}
			late := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: version, ClientID: "late-" + MQTTVersionLabel(version)}, nil)
			if _, err := late.Subscribe(ctx, []MQTTSubscription{{Topic: prefix + "retained/#", QoS: 1}}); err != nil {
				t.Fatal(err)
			}
			if msg := receive(t, late, 5*time.Second); msg.Topic != prefix+"retained/one" || string(msg.Payload) != "kept" || !msg.Retain {
				t.Fatalf("retained delivery = %+v", msg)
			}
			// Unsubscribing stops delivery on this connection.
			if err := sub.Unsubscribe(ctx, []string{prefix + "test/+/value"}); err != nil {
				t.Fatal(err)
			}
			if err := pub.Publish(ctx, MQTTPublish{Topic: prefix + "test/b/value", Payload: []byte("gone"), QoS: 0}); err != nil {
				t.Fatal(err)
			}
			expectNoMessage(t, sub, 500*time.Millisecond)
		})
	}
}

func TestMQTT5PropertiesAndSubscriptionOptions(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	ctx := mqttContext(t)
	sub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT5, ClientID: "props-sub"}, nil)
	other := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT5, ClientID: "props-other"}, nil)
	grants, err := sub.Subscribe(ctx, []MQTTSubscription{{
		Topic: "props/topic", QoS: 1, NoLocal: true, RetainAsPublished: true, SubscriptionIdentifier: 17,
		UserProperties: []MQTTProperty{{Key: "team", Value: "telemetry"}},
	}})
	if err != nil || len(grants) != 1 || grants[0] != 1 {
		t.Fatalf("subscribe = %v, %v", grants, err)
	}
	msg := MQTTPublish{
		Topic: "props/topic", Payload: []byte(`{"ok":true}`), QoS: 1, Retain: false,
		Properties: MQTTMessageProperties{
			PayloadFormat: true, MessageExpiry: 30, ContentType: "application/json",
			ResponseTopic: "props/reply", CorrelationData: "corr-7",
			UserProperties: []MQTTProperty{{Key: "source", Value: "test"}, {Key: "source", Value: "again"}},
		},
	}
	// NoLocal withholds this client's own publish.
	if err := sub.Publish(ctx, msg); err != nil {
		t.Fatal(err)
	}
	expectNoMessage(t, sub, 500*time.Millisecond)
	// A different client's publish arrives with every property.
	if err := other.Publish(ctx, msg); err != nil {
		t.Fatal(err)
	}
	got := receive(t, sub, 5*time.Second)
	if got.Topic != "props/topic" || string(got.Payload) != `{"ok":true}` || !got.Properties.PayloadFormat ||
		got.Properties.MessageExpiry == 0 || got.Properties.MessageExpiry > 30 || got.Properties.ContentType != "application/json" ||
		got.Properties.ResponseTopic != "props/reply" || string(got.Properties.CorrelationData) != "corr-7" ||
		got.Properties.SubscriptionIdentifier != 17 || len(got.Properties.UserProperties) != 2 ||
		got.Properties.UserProperties[0].Key != "source" || got.Properties.UserProperties[1].Value != "again" {
		t.Fatalf("v5 message = %+v", got)
	}
	// Retain handling 2 never sends the retained message.
	if err := other.Publish(ctx, MQTTPublish{Topic: "props/retained", Payload: []byte("old"), QoS: 1, Retain: true}); err != nil {
		t.Fatal(err)
	}
	noRetained := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT5, ClientID: "props-noretain"}, nil)
	if _, err := noRetained.Subscribe(ctx, []MQTTSubscription{{Topic: "props/retained", QoS: 1, RetainHandling: 2}}); err != nil {
		t.Fatal(err)
	}
	expectNoMessage(t, noRetained, 500*time.Millisecond)
}

func TestMQTTLastWill(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	for _, version := range []int{MQTT311, MQTT5} {
		t.Run(MQTTVersionLabel(version), func(t *testing.T) {
			ctx := mqttContext(t)
			watcher := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: version, ClientID: "will-watch-" + MQTTVersionLabel(version)}, nil)
			if _, err := watcher.Subscribe(ctx, []MQTTSubscription{{Topic: "will/#", QoS: 1}}); err != nil {
				t.Fatal(err)
			}
			will := MQTTWill{Enabled: true, Topic: "will/gone", Payload: `{"state":"offline"}`, Type: "json", QoS: 1, Retain: false}
			if version == MQTT5 {
				will.Properties = MQTTWillProperties{ContentType: "application/json", PayloadFormat: true,
					UserProperties: []MQTTProperty{{Key: "reason", Value: "crash"}}}
			}
			clientID := "will-client-" + MQTTVersionLabel(version)
			client := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: version, ClientID: clientID, Will: will}, nil)
			if version == MQTT5 {
				// Mochi does not forward will properties beyond user properties,
				// so inspect the CONNECT packet the client sent.
				var pk packets.Packet
				var ok bool
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if pk, ok = broker.recorder.connect(clientID); ok {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !ok {
					t.Fatal("broker did not record the CONNECT packet")
				}
				props := pk.Connect.WillProperties
				if props.ContentType != "application/json" || props.PayloadFormat != 1 ||
					props.ResponseTopic != "" || len(props.User) != 1 || props.User[0].Val != "crash" {
					t.Fatalf("sent will properties = %+v", props)
				}
			}
			// Stay connected long enough to subscribe and publish: the will
			// properties must not corrupt the CONNECT and make the broker
			// disconnect immediately.
			if _, err := client.Subscribe(ctx, []MQTTSubscription{{Topic: "alive/#", QoS: 1}}); err != nil {
				t.Fatalf("subscribe after the will CONNECT: %v", err)
			}
			if err := client.Publish(ctx, MQTTPublish{Topic: "alive/ping", Payload: []byte("pong"), QoS: 1}); err != nil {
				t.Fatalf("publish after the will CONNECT: %v", err)
			}
			if msg := receive(t, client, 5*time.Second); msg.Topic != "alive/ping" || string(msg.Payload) != "pong" {
				t.Fatalf("live message after the will CONNECT = %+v", msg)
			}
			// Kill the transport without DISCONNECT, which is what a crash or
			// network loss looks like to the broker.
			_ = client.conn.Close()
			msg := receive(t, watcher, 5*time.Second)
			if msg.Topic != "will/gone" || string(msg.Payload) != `{"state":"offline"}` || msg.QoS != 1 {
				t.Fatalf("will message = %+v", msg)
			}
			if version == MQTT5 && (len(msg.Properties.UserProperties) != 1 || msg.Properties.UserProperties[0].Value != "crash") {
				t.Fatalf("forwarded will user properties = %+v", msg.Properties)
			}
		})
	}
}

func TestMQTT5WillConnectSendsOnePacket(t *testing.T) {
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if len(connect) == 0 || connect[0] != 0x10 {
			return fmt.Errorf("first packet is % x, want CONNECT", connect)
		}
		// A will-properties CONNECT is substituted by the client. If the
		// measured length is short, Paho's own CONNECT tail follows as a
		// second packet; the password's last four bytes are the usual clue.
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		trailing := make([]byte, 8)
		n, err := conn.Read(trailing)
		if n > 0 {
			return fmt.Errorf("trailing bytes after CONNECT: % x", trailing[:n])
		}
		var timeout net.Error
		if err != nil && (!errors.As(err, &timeout) || !timeout.Timeout()) {
			return err
		}
		_ = conn.SetReadDeadline(time.Time{})
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, conn)
		return nil
	})
	cfg := MQTTConfig{Version: MQTT5, ClientID: "will-connect", Username: "user", Password: "secretpw",
		Will: MQTTWill{Enabled: true, Topic: "will/topic", Payload: "bye", Type: "text",
			Properties: MQTTWillProperties{ContentType: "text/plain"}}}
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address, cfg, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	brokerError(t, errs)
}

type passwordHook struct {
	mqtt.HookBase
}

func (h *passwordHook) ID() string { return "test-password" }
func (h *passwordHook) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate || b == mqtt.OnACLCheck
}
func (h *passwordHook) OnConnectAuthenticate(_ *mqtt.Client, pk packets.Packet) bool {
	return string(pk.Connect.Username) == "demo" && string(pk.Connect.Password) == "secret"
}
func (h *passwordHook) OnACLCheck(*mqtt.Client, string, bool) bool { return true }

func TestMQTTAuthenticationFailure(t *testing.T) {
	server := mqtt.New(&mqtt.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.AddHook(new(passwordHook), nil); err != nil {
		t.Fatal(err)
	}
	tcp := listeners.NewTCP(listeners.Config{ID: "tcp", Address: "127.0.0.1:0"})
	if err := server.AddListener(tcp); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })
	for _, version := range []int{MQTT311, MQTT5} {
		t.Run(MQTTVersionLabel(version), func(t *testing.T) {
			ctx := mqttContext(t)
			cfg := MQTTConfig{Version: version, ClientID: "auth-" + MQTTVersionLabel(version), Username: "demo", Password: "wrong"}
			if _, err := DialMQTT(ctx, "mqtt://"+tcp.Address(), cfg, nil, "", false); err == nil {
				t.Fatal("wrong password connected")
			}
			cfg.Password = "secret"
			client, err := DialMQTT(ctx, "mqtt://"+tcp.Address(), cfg, nil, "", false)
			if err != nil {
				t.Fatalf("correct password: %v", err)
			}
			client.Close()
		})
	}
}

// testPKI is a CA, a server certificate for 127.0.0.1 and a client
// certificate, all written where the certificate settings expect files.
type testPKI struct {
	caPEM, serverCertPEM, serverKeyPEM []byte
	clientCertPEM, clientKeyPEM        []byte
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "vscroxy MQTT test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(serial int64, common string, usage x509.ExtKeyUsage, ip net.IP, dns []string) ([]byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: common},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			DNSNames: dns,
		}
		if ip != nil {
			template.IPAddresses = []net.IP{ip}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	}
	pki := testPKI{caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})}
	pki.serverCertPEM, pki.serverKeyPEM = issue(2, "broker.test", x509.ExtKeyUsageServerAuth, net.ParseIP("127.0.0.1"), []string{"localhost"})
	pki.clientCertPEM, pki.clientKeyPEM = issue(3, "vscroxy client", x509.ExtKeyUsageClientAuth, nil, nil)
	return pki
}

func (p testPKI) serverTLS(t *testing.T) *tls.Config {
	t.Helper()
	cert, err := tls.X509KeyPair(p.serverCertPEM, p.serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(p.caPEM) {
		t.Fatal("CA did not parse")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
}

func (p testPKI) settings(t *testing.T, address string, client bool) certs.Settings {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	settings := certs.Settings{RootCAs: []string{write("ca.pem", p.caPEM)}}
	if client {
		settings.Clients = []certs.Client{{ID: "client", Host: address, Format: certs.FormatPEM,
			CertFile: write("client.pem", p.clientCertPEM), KeyFile: write("client.key", p.clientKeyPEM)}}
	}
	return settings
}

func TestMQTTTLSClientCertificate(t *testing.T) {
	pki := newTestPKI(t)
	broker := startTestBroker(t, pki.serverTLS(t), true)
	ctx := mqttContext(t)
	for _, scheme := range []string{"mqtts", "wss"} {
		t.Run(scheme, func(t *testing.T) {
			address := broker.tcp
			rawURL := scheme + "://" + address
			if scheme == "wss" {
				rawURL = scheme + "://" + broker.ws
			}
			settings := pki.settings(t, address, true)
			tlsConfig, err := settings.TLSConfig(address, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			sub := dialTestMQTT(t, ctx, rawURL, MQTTConfig{Version: MQTT5, ClientID: "tls-sub-" + scheme}, tlsConfig)
			pub := dialTestMQTT(t, ctx, rawURL, MQTTConfig{Version: MQTT5, ClientID: "tls-pub-" + scheme}, tlsConfig)
			if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "tls/topic", QoS: 1}}); err != nil {
				t.Fatal(err)
			}
			if err := pub.Publish(ctx, MQTTPublish{Topic: "tls/topic", Payload: []byte("secure"), QoS: 1}); err != nil {
				t.Fatal(err)
			}
			if msg := receive(t, sub, 5*time.Second); msg.Topic != "tls/topic" || string(msg.Payload) != "secure" {
				t.Fatalf("TLS message = %+v", msg)
			}
			// Without the client certificate the broker must refuse the
			// handshake, not silently connect.
			without, err := pki.settings(t, address, false).TLSConfig(address, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DialMQTT(ctx, rawURL, MQTTConfig{Version: MQTT5, ClientID: "no-cert-" + scheme}, without, "", false); err == nil {
				t.Fatal("connected without the required client certificate")
			}
			// An empty root set does not trust the test CA.
			wrongRoots, err := (certs.Settings{RootCAs: nil, TrustSystemRoots: new(bool)}).TLSConfig(address, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DialMQTT(ctx, rawURL, MQTTConfig{Version: MQTT5, ClientID: "bad-root-" + scheme}, wrongRoots, "", false); err == nil {
				t.Fatal("connected with an untrusted server certificate")
			}
		})
	}
}

func TestMQTTWebsocketTransport(t *testing.T) {
	broker := startTestBroker(t, nil, true)
	ctx := mqttContext(t)
	sub := dialTestMQTT(t, ctx, "ws://"+broker.ws, MQTTConfig{Version: MQTT311, ClientID: "ws-sub"}, nil)
	pub := dialTestMQTT(t, ctx, "ws://"+broker.ws, MQTTConfig{Version: MQTT5, ClientID: "ws-pub"}, nil)
	if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "ws/topic", QoS: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, MQTTPublish{Topic: "ws/topic", Payload: []byte("over websocket"), QoS: 2}); err != nil {
		t.Fatal(err)
	}
	if msg := receive(t, sub, 5*time.Second); msg.Topic != "ws/topic" || string(msg.Payload) != "over websocket" || msg.QoS != 2 {
		t.Fatalf("WebSocket message = %+v", msg)
	}
}

// startCONNECTProxy is a minimal HTTP CONNECT proxy; it records the target of
// each tunnel so a test can prove the WebSocket dial went through it.
func startCONNECTProxy(t *testing.T) (string, <-chan string) {
	t.Helper()
	targets := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		targets <- r.Host
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hijack, ok := w.(http.Hijacker)
		if !ok {
			_ = upstream.Close()
			http.Error(w, "hijacking is unavailable", http.StatusInternalServerError)
			return
		}
		client, _, err := hijack.Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		defer client.Close()
		defer upstream.Close()
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, client) }()
		_, _ = io.Copy(client, upstream)
	}))
	t.Cleanup(server.Close)
	return server.URL, targets
}

func TestMQTTWebsocketUsesProxy(t *testing.T) {
	broker := startTestBroker(t, nil, true)
	proxyURL, targets := startCONNECTProxy(t)
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mqttContext(t)
	sub := dialTestMQTTVia(t, ctx, "ws://"+broker.ws, MQTTConfig{Version: MQTT311, ClientID: "proxy-sub"}, nil, u.Host, false)
	pub := dialTestMQTTVia(t, ctx, "ws://"+broker.ws, MQTTConfig{Version: MQTT5, ClientID: "proxy-pub"}, nil, u.Host, false)
	if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "proxy/topic", QoS: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, MQTTPublish{Topic: "proxy/topic", Payload: []byte("through proxy"), QoS: 1}); err != nil {
		t.Fatal(err)
	}
	if msg := receive(t, sub, 5*time.Second); msg.Topic != "proxy/topic" || string(msg.Payload) != "through proxy" {
		t.Fatalf("proxied WebSocket message = %+v", msg)
	}
	select {
	case target := <-targets:
		if target != broker.ws {
			t.Fatalf("proxy target = %q, want %q", target, broker.ws)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the WebSocket dial did not use the proxy")
	}
}

// A proxy that accepts the connection but never answers CONNECT must not
// hold the dial past cancellation or its timeout.
func TestMQTTProxyCONNECTStallIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	for _, tc := range []struct {
		name    string
		cancel  time.Duration
		timeout time.Duration
	}{
		{"cancelled", 200 * time.Millisecond, time.Minute},
		{"timeout", time.Minute, 300 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), tc.cancel)
			defer cancel()
			start := time.Now()
			conn, err := dialMQTTProxy(ctx, "tcp", listener.Addr().String(), "broker.test:443", tc.timeout, true)
			if err == nil {
				_ = conn.Close()
				t.Fatal("dial through a silent proxy succeeded")
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("dial returned after %v: %v", elapsed, err)
			}
		})
	}
}

func TestMQTTWebsocketLargePublish(t *testing.T) {
	broker := startTestBroker(t, nil, true)
	ctx := mqttContext(t)
	sub := dialTestMQTT(t, ctx, "ws://"+broker.ws, MQTTConfig{Version: MQTT5, ClientID: "ws-large-sub"}, nil)
	pub := dialTestMQTT(t, ctx, "ws://"+broker.ws, MQTTConfig{Version: MQTT5, ClientID: "ws-large-pub"}, nil)
	if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "ws/large", QoS: 2}}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 700<<10)
	if err := pub.Publish(ctx, MQTTPublish{Topic: "ws/large", Payload: payload, QoS: 2}); err != nil {
		t.Fatalf("700 KiB WebSocket publish: %v", err)
	}
	msg := receive(t, sub, 10*time.Second)
	if msg.Topic != "ws/large" || msg.Size != len(payload) || len(msg.Payload) != mqttIncomingKeep {
		t.Fatalf("large WebSocket message = topic %q, size %d, kept %d", msg.Topic, msg.Size, len(msg.Payload))
	}
}

func TestMQTT311DeliveryOrderAndOverlappingFilters(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	ctx := mqttContext(t)
	sub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT311, ClientID: "v3-delivery-sub"}, nil)
	pub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT311, ClientID: "v3-delivery-pub"}, nil)
	if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "order/topic", QoS: 0}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		payload := fmt.Sprintf("%03d", i)
		if err := pub.Publish(ctx, MQTTPublish{Topic: "order/topic", Payload: []byte(payload), QoS: 0}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		msg := receive(t, sub, 5*time.Second)
		if want := fmt.Sprintf("%03d", i); string(msg.Payload) != want {
			t.Fatalf("message %d = %q, want %q", i, msg.Payload, want)
		}
	}
	// A message that matches two filters must be delivered once.
	if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "ov/#", QoS: 0}, {Topic: "ov/b", QoS: 0}}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, MQTTPublish{Topic: "ov/b", Payload: []byte("once"), QoS: 0}); err != nil {
		t.Fatal(err)
	}
	if msg := receive(t, sub, 5*time.Second); msg.Topic != "ov/b" || string(msg.Payload) != "once" {
		t.Fatalf("overlapping filter message = %+v", msg)
	}
	expectNoMessage(t, sub, 500*time.Millisecond)
}

// startTCPProxy forwards one address to another and can drop every live
// connection, which looks like a network loss to both ends.
func startTCPProxy(t *testing.T, target string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			upstream, err := net.Dial("tcp", target)
			if err != nil {
				_ = client.Close()
				continue
			}
			mu.Lock()
			conns = append(conns, client, upstream)
			mu.Unlock()
			go func() {
				_, _ = io.Copy(upstream, client)
				_ = upstream.Close()
				_ = client.Close()
			}()
			go func() {
				_, _ = io.Copy(client, upstream)
				_ = client.Close()
				_ = upstream.Close()
			}()
		}
	}()
	drop := func() {
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		conns = nil
		mu.Unlock()
	}
	t.Cleanup(func() {
		_ = listener.Close()
		drop()
	})
	return listener.Addr().String(), drop
}

func TestMQTT311PersistentSessionQueuedMessages(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	ctx := mqttContext(t)
	clean := false
	cfg := MQTTConfig{Version: MQTT311, ClientID: "v3-persistent", CleanStart: &clean}
	proxy, drop := startTCPProxy(t, broker.tcp)
	first := dialTestMQTT(t, ctx, "mqtt://"+proxy, cfg, nil)
	if _, err := first.Subscribe(ctx, []MQTTSubscription{{Topic: "queue/topic", QoS: 1}}); err != nil {
		t.Fatal(err)
	}
	// Drop the network path without DISCONNECT; the broker keeps the session.
	drop()
	select {
	case <-first.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("first connection did not close; connected=%v err=%v", first.v3.IsConnected(), first.Err())
	}
	pub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT311, ClientID: "v3-persistent-pub"}, nil)
	for i := 0; i < 3; i++ {
		if err := pub.Publish(ctx, MQTTPublish{Topic: "queue/topic", Payload: []byte(fmt.Sprintf("queued-%d", i)), QoS: 1}); err != nil {
			t.Fatal(err)
		}
	}
	second := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, cfg, nil)
	if !second.SessionPresent() {
		t.Fatal("broker did not report a resumed session")
	}
	// No Subscribe call: the broker's session delivers the queued messages,
	// and the default handler must acknowledge them.
	got := map[string]bool{}
	for range 3 {
		msg := receive(t, second, 5*time.Second)
		got[string(msg.Payload)] = true
	}
	for i := 0; i < 3; i++ {
		if !got[fmt.Sprintf("queued-%d", i)] {
			t.Fatalf("queued messages = %v", got)
		}
	}
}

func TestMQTTReconnectState(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	ctx := mqttContext(t)
	client := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT5, ClientID: "reconnect"}, nil)
	_ = client.conn.Close()
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection loss was not reported")
	}
	if client.Err() == nil {
		t.Fatal("connection loss has no error")
	}
	client.Close() // idempotent
}

func TestMQTTIncomingPacketLimitBeforeAllocation(t *testing.T) {
	for _, version := range []int{MQTT311, MQTT5} {
		t.Run(MQTTVersionLabel(version), func(t *testing.T) {
			address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
				if err := writeMQTTConnack(conn, version, 0, nil); err != nil {
					return err
				}
				// Declare the largest possible PUBLISH but send no body.
				if _, err := conn.Write([]byte{0x30, 0xff, 0xff, 0xff, 0x7f}); err != nil {
					return err
				}
				_, _ = io.Copy(io.Discard, conn)
				return nil
			})
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
				MQTTConfig{Version: version, ClientID: "limit-header-" + MQTTVersionLabel(version)}, nil, "", false)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("oversized header did not fail before the body arrived")
			}
			if err := client.Err(); err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("packet limit error = %v", err)
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			if growth := after.TotalAlloc - before.TotalAlloc; growth > 64<<20 {
				t.Fatalf("allocated %d bytes for a rejected packet header", growth)
			}
			client.Close()
			brokerError(t, errs)
		})
	}
}

func TestMQTT5ConnectTimeoutIsNotCappedAtTenSeconds(t *testing.T) {
	t.Parallel()
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		time.Sleep(11 * time.Second)
		return writeMQTTConnack(conn, MQTT5, 0, nil)
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "slow-connack", ConnectTimeoutMs: 15000}, nil, "", false)
	if err != nil {
		t.Fatalf("a CONNACK inside the 15 s connect timeout failed: %v", err)
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5ConnackReasonCode(t *testing.T) {
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		return writeMQTTConnack(conn, MQTT5, 0x87, []byte{0x1f, 0x00, 0x06, 'd', 'e', 'n', 'i', 'e', 'd'})
	})
	_, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "connack-reason"}, nil, "", false)
	if err == nil || !strings.Contains(err.Error(), "not authorized (0x87)") || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("connect error = %v", err)
	}
	brokerError(t, errs)
}

func TestMQTT5SubackReasonCode(t *testing.T) {
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		packet, err := readMQTTPacket(conn)
		if err != nil {
			return err
		}
		if len(packet) == 0 || packet[0]>>4 != 8 {
			return fmt.Errorf("expected SUBSCRIBE, got % x", packet)
		}
		body := mqttPacketBody(packet)
		if len(body) < 2 {
			return fmt.Errorf("short SUBSCRIBE: % x", packet)
		}
		if err := writeMQTTPacket(conn, 0x90, []byte{body[0], body[1], 0x00, 0x87}); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, conn)
		return nil
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "suback-reason"}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := client.Subscribe(mqttContext(t), []MQTTSubscription{{Topic: "denied/topic", QoS: 1}})
	if err == nil || !strings.Contains(err.Error(), "not authorized (0x87)") {
		t.Fatalf("subscribe error = %v", err)
	}
	if len(grants) != 1 || grants[0] != 0x87 {
		t.Fatalf("grants = %v", grants)
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5ServerDisconnectReasonCode(t *testing.T) {
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		body := []byte{0x98, 0x09, 0x1f, 0x00, 0x06, 'k', 'i', 'c', 'k', 'e', 'd'}
		if err := writeMQTTPacket(conn, 0xE0, body); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, conn)
		return nil
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "disconnect-reason"}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("broker DISCONNECT did not close the client")
	}
	if err := client.Err(); !strings.Contains(err.Error(), "administrative action (0x98)") || !strings.Contains(err.Error(), "kicked") {
		t.Fatalf("disconnect error = %v", err)
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5SlowBrokerPublishKeepsConnection(t *testing.T) {
	t.Parallel()
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(16 << 10)
		}
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		// Read about 100 KiB/s, slower than a 5 s whole-packet budget but
		// faster than the per-chunk progress budget.
		buf := make([]byte, 4<<10)
		for {
			if _, err := conn.Read(buf); err != nil {
				return nil
			}
			time.Sleep(40 * time.Millisecond)
		}
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "slow-broker", KeepAlive: 0, ConnectTimeoutMs: 5000}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if writeConn, ok := client.conn.(*mqttWriteConn); ok {
		if limited, ok := writeConn.Conn.(*mqttLimitConn); ok {
			if tcp, ok := limited.Conn.(*net.TCPConn); ok {
				_ = tcp.SetWriteBuffer(16 << 10)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Publish(ctx, MQTTPublish{Topic: "slow/topic", Payload: bytes.Repeat([]byte("x"), 800<<10), QoS: 0}); err != nil {
		t.Fatalf("800 KiB to a 100 KiB/s broker: %v", err)
	}
	select {
	case <-client.Done():
		t.Fatalf("connection closed after a slow publish: %v", client.Err())
	default:
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5DelayedPubackDoesNotExpireTheNextWrite(t *testing.T) {
	t.Parallel()
	downAck := make(chan []byte, 1)
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		packet, err := readMQTTPacket(conn)
		if err != nil {
			return err
		}
		if len(packet) == 0 || packet[0]>>4 != 3 {
			return fmt.Errorf("expected PUBLISH, got % x", packet)
		}
		body := mqttPacketBody(packet)
		if len(body) < 2 {
			return fmt.Errorf("short PUBLISH: % x", packet)
		}
		topicLength := int(binary.BigEndian.Uint16(body[:2]))
		idAt := 2 + topicLength
		if len(body) < idAt+2 {
			return fmt.Errorf("short PUBLISH: % x", packet)
		}
		id := body[idAt : idAt+2]
		// A PUBACK after the old whole-publish 5 s deadline, followed by a
		// broker PUBLISH that requires the client to write its own PUBACK.
		time.Sleep(6 * time.Second)
		if err := writeMQTTPacket(conn, 0x40, id); err != nil {
			return err
		}
		var down bytes.Buffer
		down.Write([]byte{0x00, 0x04})
		down.WriteString("down")
		down.Write([]byte{0x00, 0x07})
		down.WriteByte(0)
		down.WriteString("from-broker")
		if err := writeMQTTPacket(conn, 0x32, down.Bytes()); err != nil {
			return err
		}
		for {
			packet, err := readMQTTPacket(conn)
			if err != nil {
				return nil
			}
			if packet[0]>>4 == 4 {
				downAck <- mqttPacketBody(packet)
				return nil
			}
		}
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "delayed-puback", ConnectTimeoutMs: 10000}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Publish(ctx, MQTTPublish{Topic: "up/topic", Payload: []byte("up"), QoS: 1}); err != nil {
		t.Fatalf("delayed PUBACK publish: %v", err)
	}
	select {
	case ack := <-downAck:
		if len(ack) < 2 || ack[0] != 0 || ack[1] != 7 {
			t.Fatalf("down PUBACK = % x", ack)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not acknowledge the broker PUBLISH after a delayed PUBACK")
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5TopicAliasRespectsBrokerMaximum(t *testing.T) {
	published := make(chan []byte, 1)
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if err := writeMQTTConnack(conn, MQTT5, 0, []byte{0x22, 0x00, 0x05}); err != nil {
			return err
		}
		packet, err := readMQTTPacket(conn)
		if err != nil {
			return err
		}
		published <- packet
		_, _ = io.Copy(io.Discard, conn)
		return nil
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "topic-alias"}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mqttContext(t)
	err = client.Publish(ctx, MQTTPublish{Topic: "alias/topic", Payload: []byte("x"), QoS: 0,
		Properties: MQTTMessageProperties{TopicAlias: 6}})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("alias above the broker maximum = %v", err)
	}
	if err := client.Publish(ctx, MQTTPublish{Topic: "", Payload: []byte("alias only"), QoS: 0,
		Properties: MQTTMessageProperties{TopicAlias: 5}}); err != nil {
		t.Fatalf("alias-only publish: %v", err)
	}
	select {
	case packet := <-published:
		body := mqttPacketBody(packet)
		if len(body) < 2 || body[0] != 0 || body[1] != 0 {
			t.Fatalf("alias-only PUBLISH body = % x", body)
		}
		if !bytes.Contains(body, []byte{0x23, 0x00, 0x05}) {
			t.Fatalf("PUBLISH does not carry topic alias 5: % x", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("alias-only publish did not reach the broker")
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5TopicAliasUnsupportedByBroker(t *testing.T) {
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, conn)
		return nil
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "no-topic-alias"}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Publish(mqttContext(t), MQTTPublish{Topic: "alias/topic", Payload: []byte("x"), QoS: 0,
		Properties: MQTTMessageProperties{TopicAlias: 1}})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("alias without broker support = %v", err)
	}
	client.Close()
	brokerError(t, errs)
}

func TestMQTT5PublishAndCloseAreBoundedWhenBrokerStopsReading(t *testing.T) {
	var releaseOnce sync.Once
	release := make(chan struct{})
	releaseBroker := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBroker()
	address, errs := startRawMQTTBroker(t, func(conn net.Conn, connect []byte) error {
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(16 << 10)
		}
		if err := writeMQTTConnack(conn, MQTT5, 0, nil); err != nil {
			return err
		}
		// Stop reading. The client's socket buffer fills and its write blocks.
		<-release
		return nil
	})
	client, err := DialMQTT(mqttContext(t), "mqtt://"+address,
		MQTTConfig{Version: MQTT5, ClientID: "blocked-writer", KeepAlive: 0, ConnectTimeoutMs: 5000}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if writeConn, ok := client.conn.(*mqttWriteConn); ok {
		if limited, ok := writeConn.Conn.(*mqttLimitConn); ok {
			if tcp, ok := limited.Conn.(*net.TCPConn); ok {
				_ = tcp.SetWriteBuffer(16 << 10)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Publish(ctx, MQTTPublish{Topic: "big/topic", Payload: bytes.Repeat([]byte("x"), 1<<20), QoS: 0})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("publish to a broker that stopped reading succeeded")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("publish blocked past its write deadline")
	}
	closed := make(chan struct{})
	go func() {
		client.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a blocked publish")
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close after Close")
	}
	releaseBroker()
	brokerError(t, errs)
}

func TestMQTTIncomingPacketLimit(t *testing.T) {
	broker := startTestBroker(t, nil, false)
	ctx := mqttContext(t)
	sub := dialTestMQTT(t, ctx, "mqtt://"+broker.tcp, MQTTConfig{Version: MQTT311, ClientID: "limit-sub"}, nil)
	if _, err := sub.Subscribe(ctx, []MQTTSubscription{{Topic: "limit/topic", QoS: 0}}); err != nil {
		t.Fatal(err)
	}
	raw := paho3.NewClient(paho3.NewClientOptions().AddBroker("tcp://" + broker.tcp).SetClientID("limit-pub").SetProtocolVersion(MQTT311))
	token := raw.Connect()
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatalf("raw publisher connect: %v", token.Error())
	}
	defer raw.Disconnect(0)
	token = raw.Publish("limit/topic", 0, false, bytes.Repeat([]byte("x"), 2<<20))
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatalf("raw publisher publish: %v", token.Error())
	}
	select {
	case <-sub.Done():
		if err := sub.Err(); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("packet limit error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("oversized broker packet was accepted")
	}
}

func TestMQTTValidatePasswordWithoutUsername(t *testing.T) {
	if err := (MQTTConfig{Version: MQTT5, ClientID: "password-only", Password: "secret"}).Validate(); err != nil {
		t.Fatalf("MQTT 5 password without username: %v", err)
	}
	if err := (MQTTConfig{Version: MQTT311, ClientID: "password-only", Password: "secret"}).Validate(); err == nil {
		t.Fatal("MQTT 3.1.1 accepted a password without a username")
	}
}

func TestMQTTValidateAliasOnlyMessage(t *testing.T) {
	cfg := MQTTConfig{Version: MQTT5, ClientID: "alias-only", Messages: []MQTTMessage{{
		Topic: "", Payload: "x", Type: "text", Properties: MQTTMessageProperties{TopicAlias: 1},
	}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("MQTT 5 alias-only message: %v", err)
	}
	cfg.Version = MQTT311
	if err := cfg.Validate(); err == nil {
		t.Fatal("MQTT 3.1.1 accepted an alias-only message")
	}
	cfg.Version = MQTT5
	cfg.Messages[0].Properties.TopicAlias = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("an empty topic without an alias was accepted")
	}
}

func TestMQTTPayloadAndTopics(t *testing.T) {
	if data, err := MQTTPayload("48656c6c6f", "hex"); err != nil || string(data) != "Hello" {
		t.Fatalf("hex = %q, %v", data, err)
	}
	if data, err := MQTTPayload("SGVsbG8=", "base64"); err != nil || string(data) != "Hello" {
		t.Fatalf("base64 = %q, %v", data, err)
	}
	if _, err := MQTTPayload("xyz", "hex"); err == nil {
		t.Fatal("invalid hex accepted")
	}
	if _, err := MQTTPayload("SGVsbG8", "base64"); err == nil {
		t.Fatal("unpadded base64 accepted")
	}
	for _, filter := range []string{"a/+/b", "a/#", "#", "+", "a/+/#"} {
		if err := ValidateMQTTTopicFilter(filter); err != nil {
			t.Errorf("filter %q: %v", filter, err)
		}
	}
	for _, filter := range []string{"", "a/#/b", "a/b#", "a+/b", "a/b+/c"} {
		if err := ValidateMQTTTopicFilter(filter); err == nil {
			t.Errorf("filter %q accepted", filter)
		}
	}
	for _, name := range []string{"a/b", "a b", "$SYS/broker"} {
		if err := ValidateMQTTTopicName(name); err != nil {
			t.Errorf("topic %q: %v", name, err)
		}
	}
	for _, name := range []string{"", "a/+", "a/#"} {
		if err := ValidateMQTTTopicName(name); err == nil {
			t.Errorf("topic %q accepted", name)
		}
	}
	if address, secure, err := MQTTEndpoint("mqtts://broker.test"); err != nil || !secure || address != "broker.test:8883" {
		t.Fatalf("endpoint = %q, %v, %v", address, secure, err)
	}
	if address, secure, err := MQTTEndpoint("wss://broker.test/mqtt"); err != nil || !secure || address != "broker.test:443" {
		t.Fatalf("wss endpoint = %q, %v, %v", address, secure, err)
	}
	if _, _, err := MQTTEndpoint("mqtt://broker.test:1883/path"); err == nil {
		t.Fatal("mqtt URL with a path accepted")
	}
}
