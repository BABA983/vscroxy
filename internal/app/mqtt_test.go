package app

import (
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

func startAppMQTTBroker(t *testing.T) string {
	t.Helper()
	server := mqtt.New(&mqtt.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	tcp := listeners.NewTCP(listeners.Config{ID: "tcp", Address: "127.0.0.1:0"})
	if err := server.AddListener(tcp); err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return tcp.Address()
}

func waitMQTT(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestMQTTSessionConnectsPublishesAndSubscribes(t *testing.T) {
	address := startAppMQTTBroker(t)
	input := request.Request{Protocol: "MQTT", URL: "mqtt://" + address, MQTT: request.MQTTConfig{
		Version: request.MQTT5, ClientID: "app-session",
		Topics:   []request.MQTTTopic{{Name: "app/#", QoS: 1}},
		Messages: []request.MQTTMessage{{Topic: "app/out", Payload: "hello", Type: "text", QoS: 1}},
	}}
	scope := request.Context{}
	prepared, cfg, err := input.PrepareMQTT(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := openMQTTSession(prepared.URL, input, scope, nil, cfg, nil, nil)
	t.Cleanup(s.stop)
	waitMQTT(t, "connection", func() bool { return s.snapshot().ready })
	waitMQTT(t, "subscription", func() bool { return len(s.snapshot().subscriptions) == 1 })
	s.publish(request.MQTTPublish{Topic: "app/out", Payload: []byte("hello"), QoS: 1}, "greeting")
	waitMQTT(t, "outgoing event", func() bool {
		for _, e := range s.snapshot().events {
			if e.direction == "Out" && e.name == "app/out" && string(e.payload) == "hello" {
				return true
			}
		}
		return false
	})
	snapshot := s.snapshot()
	if snapshot.clientID != "app-session" || snapshot.state != "Connected" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	var connected bool
	for _, e := range snapshot.events {
		if e.direction == "System" && e.name == "Connected" && strings.Contains(e.note, "MQTT 5.0") {
			connected = true
		}
	}
	if !connected {
		t.Fatalf("no Connected event: %+v", snapshot.events)
	}
}

func TestMQTTDocumentRoundTripsThroughLibrary(t *testing.T) {
	a := libraryApp(t)
	clean, auto := false, true
	document := request.MQTTConfig{
		Version: request.MQTT5, ClientID: "vscroxy-roundtrip", KeepAlive: 45, ConnectTimeoutMs: 7000,
		CleanStart: &clean, AutoReconnect: &auto,
		Properties: request.MQTTConnectProperties{SessionExpiry: 60, UserProperties: []request.MQTTProperty{{Key: "app", Value: "demo"}}},
		Topics:     []request.MQTTTopic{{Name: "sensors/+/temp", QoS: 1, SubscriptionIdentifier: 9}},
		Messages:   []request.MQTTMessage{{Name: "Publish", Topic: "sensors/out", Payload: `{"ok":true}`, Type: "json", QoS: 2, Retain: true}},
		Will: request.MQTTWill{Enabled: true, Topic: "clients/gone", Payload: "bye", Type: "text", QoS: 1,
			Properties: request.MQTTWillProperties{WillDelay: 30}},
	}
	r := savedRequest{Request: request.Request{ID: "mqtt-1", Name: "Telemetry", Collection: "Shop", Protocol: "MQTT",
		URL: "mqtts://broker.example.com:8883", Method: "GET", MQTT: document}}
	if err := a.writeLibrary([]savedRequest{r}, []request.Folder{{Path: "Shop"}}); err != nil {
		t.Fatal(err)
	}
	loaded, _, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Protocol != "MQTT" || loaded[0].URL != r.URL {
		t.Fatalf("loaded = %+v", loaded)
	}
	if !reflect.DeepEqual(loaded[0].MQTT, document) {
		t.Fatalf("MQTT config after reload = %+v, want %+v", loaded[0].MQTT, document)
	}
}

func TestMQTTTimelineBounds(t *testing.T) {
	s := &mqttSession{}
	for i := 0; i < 250; i++ {
		s.recordLocked(mqttEvent{direction: "In", name: "a/b", payload: []byte("x"), size: 1})
	}
	if len(s.events) != mqttTimelineLimit || s.evicted != 50 || s.seq != 250 {
		t.Fatalf("events = %d, evicted = %d, seq = %d", len(s.events), s.evicted, s.seq)
	}
	if s.events[0].seq != 51 || s.events[len(s.events)-1].seq != 250 {
		t.Fatalf("event sequence = %d..%d", s.events[0].seq, s.events[len(s.events)-1].seq)
	}
}

func TestMQTTIncomingPayloadPreview(t *testing.T) {
	s := &mqttSession{}
	payload := []byte(strings.Repeat("a", 1<<20))
	s.recordIncomingLocked(request.MQTTIncoming{Topic: "big/topic", Payload: payload, Size: len(payload)})
	if len(s.events) != 1 || len(s.events[0].payload) != mqttRetainedPayload || s.events[0].size != len(payload) {
		t.Fatalf("event = %+v", s.events)
	}
	text := mqttEventText(s.events[0], 0)
	if !strings.Contains(text, "Retained 65536 of 1048576 bytes") {
		t.Fatalf("preview = %q", text[:min(len(text), 100)])
	}
	if !strings.HasPrefix(mqttEventText(s.events[0], 2), "00000000") {
		t.Fatal("hex view is not a hex dump")
	}
}
