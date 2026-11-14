package app

import "testing"

func TestProtocolDefaultURL(t *testing.T) {
	for _, tc := range []struct{ protocol, current, want string }{
		{"MQTT", "https://", "mqtts://"},
		{"WebSocket", "https://", "wss://"},
		{"Socket.IO", "https://", "https://"},
		{"gRPC", "https://", "https://"},
		{"HTTP", "https://", "https://"},
		{"MQTT", "https://broker.test", "https://broker.test"},
		{"MQTT", "", ""},
	} {
		if got := protocolDefaultURL(tc.protocol, tc.current); got != tc.want {
			t.Errorf("%s from %q = %q, want %q", tc.protocol, tc.current, got, tc.want)
		}
	}
}
