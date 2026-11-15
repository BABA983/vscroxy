package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDeviceAskPromptAllowsAndDismissingDenies(t *testing.T) {
	a := workspaceApp()
	if a.deviceUI.askUnknown {
		t.Fatal("asking about unknown devices is on by default")
	}
	allow := &deviceAskRequest{addr: "192.168.1.9", reply: make(chan bool, 1)}
	a.deviceAskPending = allow
	tt := ui.NewTester(a.view, 1100, 800)
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("192.168.1.9") {
		t.Fatalf("the prompt does not name the device: %q", tt.Texts())
	}
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-allow.reply:
		if !ok {
			t.Fatal("Allow answered deny")
		}
	default:
		t.Fatal("Allow did not answer the waiting gateway")
	}
	if a.deviceAskPending != nil {
		t.Fatal("the answered prompt stayed open")
	}
	deny := &deviceAskRequest{addr: "192.168.1.10", reply: make(chan bool, 1)}
	a.deviceAskPending = deny
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	select {
	case ok := <-deny.reply:
		if ok {
			t.Fatal("dismissing the prompt allowed the device")
		}
	default:
		t.Fatal("dismissing the prompt left the gateway waiting")
	}
}
