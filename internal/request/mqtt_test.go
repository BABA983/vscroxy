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
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
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
	client, err := DialMQTT(ctx, rawURL, cfg, tlsConfig)
	if err != nil {
		t.Fatalf("dial %s: %v", rawURL, err)
	}
	t.Cleanup(client.Close)
	return client
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
			if _, err := DialMQTT(ctx, "mqtt://"+tcp.Address(), cfg, nil); err == nil {
				t.Fatal("wrong password connected")
			}
			cfg.Password = "secret"
			client, err := DialMQTT(ctx, "mqtt://"+tcp.Address(), cfg, nil)
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
			if _, err := DialMQTT(ctx, rawURL, MQTTConfig{Version: MQTT5, ClientID: "no-cert-" + scheme}, without); err == nil {
				t.Fatal("connected without the required client certificate")
			}
			// An empty root set does not trust the test CA.
			wrongRoots, err := (certs.Settings{RootCAs: nil, TrustSystemRoots: new(bool)}).TLSConfig(address, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DialMQTT(ctx, rawURL, MQTTConfig{Version: MQTT5, ClientID: "bad-root-" + scheme}, wrongRoots); err == nil {
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
