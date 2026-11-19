package app

import (
	"net/netip"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/lan"
	"github.com/egoist/mygo/ui"
)

func TestDeviceAskPromptAllowsAndDismissingDenies(t *testing.T) {
	a := workspaceApp()
	if a.deviceUI.askUnknown {
		t.Fatal("asking about unknown devices is on by default")
	}
	allow := &deviceAskRequest{addr: "192.168.1.9", reply: make(chan lan.Verdict, 1)}
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
	case verdict := <-allow.reply:
		if verdict != lan.VerdictAllow {
			t.Fatalf("Allow answered %v", verdict)
		}
	default:
		t.Fatal("Allow did not answer the waiting gateway")
	}
	if a.deviceAskPending != nil {
		t.Fatal("the answered prompt stayed open")
	}
	first := &deviceAskRequest{addr: "192.168.1.11", reply: make(chan lan.Verdict, 1)}
	a.deviceAskPending = first
	if verdict := a.askDevice(netip.MustParseAddr("192.168.1.12")); verdict != lan.VerdictUndecided {
		t.Fatalf("a second device while a prompt is open answered %v", verdict)
	}
	if a.deviceAskPending != first {
		t.Fatal("a second device replaced the open prompt")
	}
	a.answerDeviceAsk(lan.VerdictUndecided)
	deny := &deviceAskRequest{addr: "192.168.1.10", reply: make(chan lan.Verdict, 1)}
	a.deviceAskPending = deny
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	select {
	case verdict := <-deny.reply:
		if verdict != lan.VerdictUndecided {
			t.Fatalf("dismissing the prompt answered %v", verdict)
		}
	default:
		t.Fatal("dismissing the prompt left the gateway waiting")
	}
}

func TestDeviceAskTimeoutIsNotRemembered(t *testing.T) {
	old := deviceAskTimeout
	defer func() { deviceAskTimeout = old }()
	a := workspaceApp()
	addr := netip.MustParseAddr("192.168.1.11")
	deviceAskTimeout = 10 * time.Millisecond
	if verdict := a.askDevice(addr); verdict != lan.VerdictUndecided {
		t.Fatalf("a timed-out prompt answered %v", verdict)
	}
	if a.deviceAskPending != nil {
		t.Fatal("a timed-out prompt stayed open")
	}
	deviceAskTimeout = time.Second
	go func() {
		// A late answer is the case under test: it must not reuse the timed-out prompt.
		time.Sleep(20 * time.Millisecond)
		a.answerDeviceAsk(lan.VerdictAllow)
	}()
	if verdict := a.askDevice(addr); verdict != lan.VerdictAllow {
		t.Fatalf("the next connection did not ask again: %v", verdict)
	}
}
