package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

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

// The bar has one dropdown before the URL. An HTTP request shows its method
// there and keeps every protocol at the foot of the same menu; a document that
// speaks another protocol shows that protocol there and can return to HTTP.
func TestRequestBarKeepsProtocolsInTheMethodMenu(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{Name: "Create", Method: "POST", URL: "https://api.example.com/projects"}, "")
	d := a.requestDocument
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("Method"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("Protocol", "gRPC"); err != nil {
		t.Fatalf("the method menu does not list the protocols: %v", err)
	}
	tt.Frame()
	if d.protocol != "gRPC" {
		t.Fatalf("choosing gRPC left the document on %q", d.protocol)
	}
	if !tt.HasText("gRPC") {
		t.Fatalf("the bar does not name the protocol: %q", tt.Texts())
	}
	if err := tt.Click("Request protocol"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("HTTP"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.protocol != "" {
		t.Fatalf("choosing HTTP left the document on %q", d.protocol)
	}
	if !tt.HasText("POST") {
		t.Fatalf("the bar does not show the method again: %q", tt.Texts())
	}
}
