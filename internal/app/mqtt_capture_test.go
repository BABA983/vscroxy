package app

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/testcert"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

type captureMQTTHook struct {
	mqtt.HookBase
	mu       sync.Mutex
	connects []packets.Packet
}

func (h *captureMQTTHook) ID() string { return "app-capture-mqtt-hook" }
func (h *captureMQTTHook) Provides(byte) bool {
	return true
}
func (h *captureMQTTHook) OnConnect(_ *mqtt.Client, packet packets.Packet) error {
	h.mu.Lock()
	h.connects = append(h.connects, packet)
	h.mu.Unlock()
	return nil
}
func (h *captureMQTTHook) connectsFor(clientID string) []packets.Packet {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []packets.Packet
	for _, packet := range h.connects {
		if packet.Connect.ClientIdentifier == clientID {
			out = append(out, packet)
		}
	}
	return out
}

func freeMQTTAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func startCaptureMQTTBroker(t *testing.T) (string, *captureMQTTHook, testcert.CA) {
	t.Helper()
	ca := testcert.NewCA(t)
	_, _, certificate := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	server := mqtt.New(&mqtt.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	hook := new(captureMQTTHook)
	if err := server.AddHook(hook, nil); err != nil {
		t.Fatal(err)
	}
	address := freeMQTTAddress(t)
	if err := server.AddListener(listeners.NewWebsocket(listeners.Config{
		ID: "wss", Address: address,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}},
	})); err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	waitForCaptureMQTT(t, address)
	return address, hook, *ca
}

func waitForCaptureMQTT(t *testing.T, address string) {
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

func TestMQTTWSSThroughCaptureHonorsBrokerVerification(t *testing.T) {
	// Start the capture proxy before the Mochi listener: capture.New installs
	// a process-wide DNS resolver, and a listener starting concurrently would
	// race with that assignment.
	proxy, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	proxy.SetSSL(capture.SSLConfig{All: true})
	broker, hook, brokerCA := startCaptureMQTTBroker(t)
	port := broker[strings.LastIndex(broker, ":")+1:]
	target := "localhost:" + port
	tlsConfig, err := (certs.Settings{}).TLSConfig(target, proxy.CACertificate(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := request.MQTTConfig{Version: request.MQTT5, ClientID: "capture-verify",
		Username: "alice", Password: "s3cret-to-impostor"}
	if _, err := request.DialMQTT(ctx, "wss://"+target, cfg, tlsConfig, proxy.Addr(), true); err == nil {
		t.Fatal("untrusted broker was accepted through the capture proxy with verification on")
	}
	if got := hook.connectsFor(cfg.ClientID); len(got) != 0 {
		t.Fatalf("untrusted broker received %d MQTT CONNECTs", len(got))
	}

	// Verification off keeps the capture proxy's inspection behavior.
	cfg.ClientID = "capture-insecure"
	client, err := request.DialMQTT(ctx, "wss://"+target, cfg, tlsConfig, proxy.Addr(), false)
	if err != nil {
		t.Fatalf("verification off: %v", err)
	}
	client.Close()
	if got := hook.connectsFor(cfg.ClientID); len(got) != 1 || string(got[0].Connect.Username) != "alice" {
		t.Fatalf("verification-off CONNECT = %+v", got)
	}

	// Verification on succeeds once the capture proxy trusts the broker CA.
	proxy.SetCertificates(certs.Settings{RootCAs: []string{brokerCA.WritePEM(t, t.TempDir())}})
	cfg.ClientID = "capture-trusted"
	client, err = request.DialMQTT(ctx, "wss://"+target, cfg, tlsConfig, proxy.Addr(), true)
	if err != nil {
		t.Fatalf("trusted broker: %v", err)
	}
	client.Close()
	if got := hook.connectsFor(cfg.ClientID); len(got) != 1 || string(got[0].Connect.Username) != "alice" {
		t.Fatalf("verification-on CONNECT = %+v", got)
	}
}
