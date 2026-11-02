package app

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/system"
)

type proxyControlWrite struct {
	on   bool
	addr string
}

// Every OS operation is injected. Channels hold each operation until the test
// supplies its outcome, so wire assertions never depend on worker timing.
type proxyControlOS struct {
	reads    chan string
	readback chan system.ProxyCheck
	writes   chan proxyControlWrite
	applied  chan error
	clears   chan string
}

func proxyControlClient(t *testing.T, a *app) (*control.Client, *proxyControlOS) {
	t.Helper()
	f := &proxyControlOS{
		reads: make(chan string, 8), readback: make(chan system.ProxyCheck, 8),
		writes: make(chan proxyControlWrite, 8), applied: make(chan error, 8),
		clears: make(chan string, 8),
	}
	ctx := t.Context()
	a.systemProxy.pending = make(chan proxyResult, 1)
	a.systemProxy.read = func(target string) system.ProxyCheck {
		f.reads <- target
		select {
		case check := <-f.readback:
			check.Target = target
			return check
		case <-ctx.Done():
			return system.ProxyCheck{Target: target, Err: ctx.Err()}
		}
	}
	a.systemProxy.write = func(on bool, addr string) error {
		f.writes <- proxyControlWrite{on, addr}
		select {
		case err := <-f.applied:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a.systemProxy.clear = func(addr string) error { f.clears <- addr; return nil }
	return desktopClient(t, a), f
}

// Match the public schema independently of its implementation type, including
// fields whose false/empty values must still be present on the wire.
type proxyControlState struct {
	Checked  bool   `json:"checked"`
	Checking bool   `json:"checking"`
	Applying bool   `json:"applying"`
	Status   string `json:"status"`
	Target   string `json:"target"`
	Problem  string `json:"problem"`
}

func proxyControlGet(t *testing.T, client *control.Client, params any) proxyControlState {
	t.Helper()
	value := desktopCall(t, client, "system-proxy.get", params)
	var state proxyControlState
	if err := json.Unmarshal(value, &state, json.RejectUnknownMembers(true)); err != nil {
		t.Fatalf("snapshot: %s: %v", value, err)
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(value, &fields); err != nil || len(fields) != 6 {
		t.Fatalf("snapshot must include all six fields: %s", value)
	}
	return state
}

func proxyControlSet(t *testing.T, client *control.Client, enabled bool) proxyControlState {
	t.Helper()
	value := desktopCall(t, client, "system-proxy.set-enabled", map[string]bool{"enabled": enabled, "confirm": true})
	var result struct {
		Accepted bool              `json:"accepted"`
		Snapshot proxyControlState `json:"snapshot"`
	}
	if err := json.Unmarshal(value, &result, json.RejectUnknownMembers(true)); err != nil || !result.Accepted || !result.Snapshot.Applying {
		t.Fatalf("expected an accepted attempt, not verified success: %s: %v", value, err)
	}
	return result.Snapshot
}

func proxyControlError(t *testing.T, client *control.Client, action string, params any, want string) {
	t.Helper()
	value := desktopCall(t, client, action, params)
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(value, &result, json.RejectUnknownMembers(true)); err != nil || result.Error != want {
		t.Fatalf("%s: %s: %v; want error %q", action, value, err, want)
	}
}

// Wait only for publication, leaving the result for the next public get/set to
// drain. No test calls drainSystemProxy or fabricates a worker's completion.
func proxyControlPublished(t *testing.T, a *app) {
	t.Helper()
	select {
	case result := <-a.systemProxy.pending:
		a.systemProxy.pending <- result
	case <-t.Context().Done():
		t.Fatal("proxy worker did not publish its result")
	}
}

func TestDesktopControlSystemProxyReadOnlyPolling(t *testing.T) {
	a := &app{srv: requestProxy(t), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	client, f := proxyControlClient(t, a)
	// Even with a recovery file present, diagnostics must not recover it.
	const recovery = "private interrupted-session recovery data"
	if err := os.WriteFile(a.proxyRecoveryPath(), []byte(recovery), 0o600); err != nil {
		t.Fatal(err)
	}
	first := proxyControlGet(t, client, nil)
	if first.Checked || !first.Checking || first.Applying || first.Status != "unknown" || first.Target != a.srv.Addr() || first.Problem != "" {
		t.Fatalf("initial get must start one read-only check: %+v", first)
	}
	if target := <-f.reads; target != a.srv.Addr() {
		t.Fatalf("read target = %q", target)
	}
	if state := proxyControlGet(t, client, map[string]bool{"refresh": true}); state != first {
		t.Fatalf("refresh did not coalesce: %+v", state)
	}
	proxyControlError(t, client, "system-proxy.set-enabled", map[string]bool{"enabled": false, "confirm": true}, "system proxy operation is already active")
	f.readback <- system.ProxyCheck{Status: system.ProxyOff}
	proxyControlPublished(t, a)
	settled := proxyControlGet(t, client, nil)
	if !settled.Checked || settled.Checking || settled.Applying || settled.Status != "off" || settled.Problem != "" {
		t.Fatalf("completed read: %+v", settled)
	}
	if state := proxyControlGet(t, client, map[string]bool{"refresh": false}); state != settled {
		t.Fatalf("ordinary poll changed state: %+v", state)
	}
	select {
	case <-f.reads:
		t.Fatal("polling or coalescing started another read")
	default:
	}
	if state := proxyControlGet(t, client, map[string]bool{"refresh": true}); !state.Checking || !state.Checked || state.Status != "off" {
		t.Fatalf("refresh must retain the previous completed verdict: %+v", state)
	}
	<-f.reads
	f.readback <- system.ProxyCheck{Status: system.ProxyMatches}
	proxyControlPublished(t, a)
	if state := proxyControlGet(t, client, nil); state.Status != "matches" || state.Checking {
		t.Fatalf("refreshed read: %+v", state)
	}
	select {
	case <-f.writes:
		t.Fatal("get wrote OS settings")
	case <-f.clears:
		t.Fatal("get cleared OS settings")
	default:
	}
	if data, err := os.ReadFile(a.proxyRecoveryPath()); err != nil || string(data) != recovery {
		t.Fatalf("diagnostics changed recovery data: %q: %v", data, err)
	}
}

func TestDesktopControlSystemProxyChangedTargetNeedsRefresh(t *testing.T) {
	a := &app{srv: requestProxy(t)}
	client, f := proxyControlClient(t, a)
	proxyControlGet(t, client, nil)
	<-f.reads
	f.readback <- system.ProxyCheck{Status: system.ProxyMatches}
	proxyControlPublished(t, a)
	if state := proxyControlGet(t, client, nil); !state.Checked || state.Status != "matches" {
		t.Fatalf("initial verdict: %+v", state)
	}
	oldTarget := a.srv.Addr()
	a.srv = requestProxy(t)
	if a.srv.Addr() == oldTarget {
		t.Fatal("test requires distinct live capture addresses")
	}
	state := proxyControlGet(t, client, nil)
	if state.Checked || state.Checking || state.Status != "unknown" || state.Target != a.srv.Addr() || state.Problem != "Capture target changed; refresh system proxy status" {
		t.Fatalf("stale verdict must not describe the new target: %+v", state)
	}
	proxyControlGet(t, client, map[string]bool{"refresh": true})
	if target := <-f.reads; target != a.srv.Addr() {
		t.Fatalf("refresh target = %q, want %q", target, a.srv.Addr())
	}
	f.readback <- system.ProxyCheck{Status: system.ProxyMismatch}
	proxyControlPublished(t, a)
	if state := proxyControlGet(t, client, nil); !state.Checked || state.Checking || state.Status != "mismatch" {
		t.Fatalf("new target was not checked: %+v", state)
	}
}

func TestDesktopControlSystemProxyAcceptedThenVerified(t *testing.T) {
	a := &app{srv: requestProxy(t)}
	client, f := proxyControlClient(t, a)
	initial := proxyControlSet(t, client, true)
	if initial.Checked || initial.Status != "unknown" || initial.Checking || initial.Target != a.srv.Addr() {
		t.Fatalf("accepted must not manufacture verification: %+v", initial)
	}
	if write := <-f.writes; !write.on || write.addr != a.srv.Addr() {
		t.Fatalf("enable must use capture's address: %+v", write)
	}
	if state := proxyControlGet(t, client, map[string]bool{"refresh": true}); state != initial {
		t.Fatalf("get did not preserve the pending write: %+v", state)
	}
	proxyControlError(t, client, "system-proxy.set-enabled", map[string]bool{"enabled": false, "confirm": true}, "system proxy operation is already active")
	f.applied <- nil
	<-f.reads
	if state := proxyControlGet(t, client, nil); !state.Applying || state.Checked {
		t.Fatalf("write completion alone must not claim readback succeeded: %+v", state)
	}
	f.readback <- system.ProxyCheck{Status: system.ProxyMatches}
	proxyControlPublished(t, a)
	// Set must drain a completed result before checking busy. Explicit false
	// disables, and does not require a running capture listener.
	a.srv.Close()
	disabling := proxyControlSet(t, client, false)
	if !disabling.Checked || disabling.Status != "matches" {
		t.Fatalf("set did not drain the completed enable: %+v", disabling)
	}
	if write := <-f.writes; write.on || write.addr != "" {
		t.Fatalf("disable was not sent to the existing worker: %+v", write)
	}
	f.applied <- nil
	<-f.reads
	f.readback <- system.ProxyCheck{Status: system.ProxyOff}
	proxyControlPublished(t, a)
	if state := proxyControlGet(t, client, nil); !state.Checked || state.Applying || state.Status != "off" || state.Problem != "" {
		t.Fatalf("disable outcome: %+v", state)
	}
	if err := a.systemProxy.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.clears:
		t.Fatal("successful disable retained ownership at quit")
	default:
	}
}

func TestDesktopControlSystemProxyClosedParametersAndGuards(t *testing.T) {
	a := &app{}
	client, f := proxyControlClient(t, a)
	for _, params := range []any{
		nil, map[string]bool{"confirm": true}, map[string]bool{"enabled": false},
		map[string]bool{"enabled": true, "confirm": false},
		map[string]any{"enabled": nil, "confirm": true},
		map[string]any{"enabled": "false", "confirm": true},
		map[string]any{"enabled": true, "confirm": true, "target": "private.example:8080"},
	} {
		proxyControlError(t, client, "system-proxy.set-enabled", params, "enabled and confirm:true are required")
	}
	for _, params := range []any{map[string]string{"target": "private.example:8080"}, map[string]string{"refresh": "true"}, map[string]bool{"confirm": true}} {
		proxyControlError(t, client, "system-proxy.get", params, "invalid parameters")
	}
	params := map[string]bool{"enabled": true, "confirm": true}
	proxyControlError(t, client, "system-proxy.set-enabled", params, "local proxy is not running")
	a.srv = requestProxy(t)
	a.srv.Close()
	proxyControlError(t, client, "system-proxy.set-enabled", params, "local proxy is not running")
	params["enabled"] = false
	a.systemProxy.quitting = true
	proxyControlError(t, client, "system-proxy.set-enabled", params, "application is quitting")
	a.systemProxy.quitting, a.systemProxy.quitReady = false, true
	proxyControlError(t, client, "system-proxy.set-enabled", params, "application is quitting")
	a.systemProxy.quitReady = false
	// The shutdown mutex may be held by a helper blocked on authorization;
	// the API must reject rather than wait for it on the UI thread.
	a.systemProxy.mu.Lock()
	proxyControlError(t, client, "system-proxy.set-enabled", params, "system proxy operation is already active")
	a.systemProxy.mu.Unlock()
	if err := a.systemProxy.close(); err != nil {
		t.Fatal(err)
	}
	proxyControlError(t, client, "system-proxy.set-enabled", params, "application is quitting")
	select {
	case <-f.reads:
		t.Fatal("rejected parameters or mutation started a read")
	case <-f.writes:
		t.Fatal("rejected mutation started a write")
	case <-f.clears:
		t.Fatal("quit without ownership changed OS settings")
	default:
	}
}

func TestDesktopControlSystemProxyOutcomesExcludeSensitiveDiagnostics(t *testing.T) {
	const secret = "sensitive-OS-diagnostic"
	for _, tc := range []struct {
		name     string
		status   system.ProxyStatus
		readErr  error
		writeErr error
		want     string
		problem  string
	}{
		{"matches", system.ProxyMatches, nil, nil, "matches", ""},
		{"off", system.ProxyOff, nil, nil, "off", ""},
		{"mismatch", system.ProxyMismatch, nil, nil, "mismatch", "System proxy points elsewhere"},
		{"automatic", system.ProxyAutomatic, nil, nil, "automatic", "Automatic proxy configuration takes precedence"},
		{"unknown", system.ProxyUnknown, nil, nil, "unknown", "Could not read OS proxy settings"},
		{"read failure", system.ProxyUnknown, errors.New(secret), nil, "unknown", "Could not read OS proxy settings"},
		{"partial write failure", system.ProxyMatches, nil, errors.New(secret), "matches", "System proxy change failed"},
		{"write and read failure", system.ProxyUnknown, errors.New(secret), errors.New(secret), "unknown", "System proxy change failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{srv: requestProxy(t)}
			client, f := proxyControlClient(t, a)
			proxyControlSet(t, client, true)
			<-f.writes
			f.applied <- tc.writeErr
			<-f.reads
			f.readback <- system.ProxyCheck{Status: tc.status, Err: tc.readErr, Configs: []system.ProxyConfig{
				{Scope: secret, Mode: system.ProxyModeAutomatic, PAC: "https://private-user:private-password@pac.example/script?token=private-token"},
				{Scope: "private-service", Mode: system.ProxyModeManual, HTTP: system.ProxyEndpoint{Enabled: true, Host: "private-proxy", Port: 8888}},
			}}
			proxyControlPublished(t, a)
			value := desktopCall(t, client, "system-proxy.get", nil)
			var state proxyControlState
			if err := json.Unmarshal(value, &state, json.RejectUnknownMembers(true)); err != nil || !state.Checked || state.Applying || state.Checking || state.Status != tc.want || state.Problem != tc.problem {
				t.Fatalf("outcome: %s: %v", value, err)
			}
			for _, sensitive := range []string{secret, "private-", "pac.example", "configs", "PAC", "Err"} {
				if strings.Contains(string(value), sensitive) {
					t.Fatalf("wire snapshot leaked %q: %s", sensitive, value)
				}
			}
			if tc.writeErr != nil {
				if state := proxyControlGet(t, client, map[string]bool{"refresh": true}); !state.Checking || state.Problem != "System proxy change failed" {
					t.Fatalf("refresh discarded the last completed error too early: %+v", state)
				}
				<-f.reads
				f.readback <- system.ProxyCheck{Status: system.ProxyOff}
				proxyControlPublished(t, a)
				if state := proxyControlGet(t, client, nil); state.Checking || state.Status != "off" || state.Problem != "" {
					t.Fatalf("completed refresh did not replace the previous error: %+v", state)
				}
			}
			if err := a.systemProxy.close(); err != nil {
				t.Fatal(err)
			}
			if target := <-f.clears; target != a.srv.Addr() {
				t.Fatalf("even partial/failed enables must retain cleanup ownership: %q", target)
			}
		})
	}
}

func TestDesktopControlSystemProxyQuitOwnsUndrainedWrite(t *testing.T) {
	a := &app{srv: requestProxy(t)}
	client, f := proxyControlClient(t, a)
	proxyControlSet(t, client, true)
	<-f.writes // change holds the ownership lock before the OS helper returns
	closing := make(chan error, 1)
	go func() { closing <- a.systemProxy.close() }()
	f.applied <- errors.New("partial change with private diagnostics")
	<-f.reads
	// Cleanup cannot depend on a UI drain (or on the readback finishing).
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if target := <-f.clears; target != a.srv.Addr() {
		t.Fatalf("quit released %q, want the owned capture address", target)
	}
	f.readback <- system.ProxyCheck{Status: system.ProxyOff}
	proxyControlPublished(t, a)
	proxyControlError(t, client, "system-proxy.set-enabled", map[string]bool{"enabled": true, "confirm": true}, "application is quitting")
	if state := proxyControlGet(t, client, nil); state.Applying || state.Status != "off" || state.Problem != "System proxy change failed" {
		t.Fatalf("undrained result lost at quit: %+v", state)
	}
	if err := a.systemProxy.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.clears:
		t.Fatal("repeated quit cleanup was not idempotent")
	case <-f.writes:
		t.Fatal("late command wrote after shutdown")
	default:
	}
}
