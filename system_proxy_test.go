package main

import (
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/egoist/mygo/ui"
)

// fakeSystemProxy stands in for the OS: it records writes and answers readbacks
// from the configuration those writes would leave behind, so these tests never
// change this machine's settings.
type fakeSystemProxy struct {
	check    system.ProxyCheck
	writes   int
	lastOn   bool
	lastAddr string
	writeErr error
	block    chan struct{} // when set, a write waits for it to be closed
	reads    int
}

func (f *fakeSystemProxy) read(target string) system.ProxyCheck {
	f.reads++
	check := f.check
	check.Target = target
	return check
}

func (f *fakeSystemProxy) write(on bool, addr string) error {
	if f.block != nil {
		<-f.block
	}
	f.writes++
	f.lastOn, f.lastAddr = on, addr
	return f.writeErr
}

// proxyTestApp is an app with a live capture listener whose system proxy calls
// are faked.
func proxyTestApp(t *testing.T, fake *fakeSystemProxy) *app {
	t.Helper()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	if fake.block != nil {
		t.Cleanup(func() {
			select {
			case <-fake.block:
			default:
				close(fake.block)
			}
		})
	}
	a := workspaceApp()
	a.srv = srv
	a.systemProxy.read, a.systemProxy.write = fake.read, fake.write
	return a
}

// settleProxy waits for the panel's worker and lets the view apply its result.
// The worker reports through the channel the panel drains, so no test sleeps.
func settleProxy(t *testing.T, a *app, tt *ui.Tester) {
	t.Helper()
	s := &a.systemProxy
	if s.checking || s.applying {
		select {
		case result := <-s.pending:
			s.pending <- result
		case <-t.Context().Done():
			t.Fatal("system proxy operation did not finish")
		}
	}
	tt.Frame()
}

// openProxySettings opens the Settings window on the Proxy tab the way a user
// does, so the check that entering the tab starts is part of the test.
func openProxySettings(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for _, label := range []string{"Settings", "Proxy"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
}

func manualProxy(t *testing.T, scope, addr string) system.ProxyConfig {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	ep := system.ProxyEndpoint{Enabled: true, Host: host, Port: n}
	return system.ProxyConfig{Scope: scope, Mode: system.ProxyModeManual, HTTP: ep, HTTPS: ep}
}

func TestProxySettingsCheckTheOSOnEntry(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{
		Status:  system.ProxyMatches,
		Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", "127.0.0.1:7890")},
	}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	if fake.reads != 1 {
		t.Fatalf("entering Proxy read the OS %d times, want once", fake.reads)
	}
	if !tt.HasText("HTTP and HTTPS point at this app") {
		t.Fatalf("a matching configuration was not reported: %q", tt.Texts())
	}
	if !tt.HasText("Wi-Fi · manual · HTTP 127.0.0.1:7890 · HTTPS 127.0.0.1:7890") {
		t.Fatalf("the panel did not show what the OS holds: %q", tt.Texts())
	}
	if !tt.HasText("Read from this computer's OS settings") {
		t.Fatalf("the panel must separate OS verification from application behavior: %q", tt.Texts())
	}
}

func TestProxySettingsDistinguishEveryOSState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		check   system.ProxyCheck
		status  string
		details []string
	}{
		{
			name:    "off",
			check:   system.ProxyCheck{Status: system.ProxyOff, Configs: []system.ProxyConfig{{Scope: "Wi-Fi"}}},
			status:  "No system proxy is set",
			details: []string{"Wi-Fi · no proxy"},
		},
		{
			name:    "elsewhere",
			check:   system.ProxyCheck{Status: system.ProxyMismatch, Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", "10.0.0.2:8080")}},
			status:  "Points at another proxy",
			details: []string{"Wi-Fi · manual · HTTP 10.0.0.2:8080 · HTTPS 10.0.0.2:8080"},
		},
		{
			name: "automatic",
			check: system.ProxyCheck{Status: system.ProxyAutomatic, Configs: []system.ProxyConfig{
				{Scope: "Wi-Fi", Mode: system.ProxyModeAutomatic, PAC: "http://pac.example/proxy.pac"},
			}},
			status:  "Automatic configuration takes precedence",
			details: []string{"Wi-Fi · automatic script http://pac.example/proxy.pac"},
		},
		{
			name:    "unreadable",
			check:   system.ProxyCheck{Status: system.ProxyUnknown, Err: errors.New("gsettings not found")},
			status:  "Could not read OS settings",
			details: []string{"Read failed: gsettings not found"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := proxyTestApp(t, &fakeSystemProxy{check: tc.check})
			tt := ui.NewTester(a.view, 1280, 800)
			openProxySettings(t, tt)
			settleProxy(t, a, tt)
			if !tt.HasText(tc.status) {
				t.Fatalf("status %q missing: %q", tc.status, tt.Texts())
			}
			for _, detail := range tc.details {
				if !tt.HasText(detail) {
					t.Fatalf("detail %q missing: %q", detail, tt.Texts())
				}
			}
		})
	}
}

func TestEnableSystemProxyWritesThenVerifies(t *testing.T) {
	fake := &fakeSystemProxy{
		check: system.ProxyCheck{Status: system.ProxyOff, Configs: []system.ProxyConfig{{Scope: "Wi-Fi"}}},
		block: make(chan struct{}),
	}
	a := proxyTestApp(t, fake)
	addr := a.srv.Addr()
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)

	if err := tt.Click("Enable system proxy"); err != nil {
		t.Fatal(err)
	}
	if !a.systemProxy.applying || !tt.HasText("Applying to the OS…") {
		t.Fatalf("a pending write must show that it is running: %q", tt.Texts())
	}
	// A second click, from the button or from elsewhere, must not write again
	// while the first write is in flight.
	if err := tt.Click("Enable system proxy"); err != nil {
		t.Fatal(err)
	}
	a.setSystemProxy(true)
	if fake.writes != 0 {
		t.Fatalf("a pending write ran %d times", fake.writes)
	}

	// The OS would hold the new address once the write finishes.
	fake.check = system.ProxyCheck{Status: system.ProxyMatches, Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", addr)}}
	close(fake.block)
	settleProxy(t, a, tt)

	if fake.writes != 1 || !fake.lastOn || fake.lastAddr != addr {
		t.Fatalf("writes = %d, on = %v, addr = %q; want one enable for %q", fake.writes, fake.lastOn, fake.lastAddr, addr)
	}
	if fake.reads != 2 {
		t.Fatalf("read the OS %d times, want a check on entry and one after the change", fake.reads)
	}
	if a.systemProxy.writeErr != nil || a.systemProxy.check.Status != system.ProxyMatches {
		t.Fatalf("state = %+v, want the verified configuration", &a.systemProxy)
	}
	if !tt.HasText("HTTP and HTTPS point at this app") {
		t.Fatalf("the panel did not keep the verified result: %q", tt.Texts())
	}
	if a.message != "System proxy: HTTP and HTTPS now point at "+addr {
		t.Fatalf("message = %q, want the verified address", a.message)
	}
}

func TestEnableSystemProxyReportsWhatTheOSStillHolds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		check   system.ProxyCheck
		message string
		status  string
	}{
		{
			name:    "automatic configuration wins",
			check:   system.ProxyCheck{Status: system.ProxyAutomatic, Configs: []system.ProxyConfig{{Scope: "Wi-Fi", Mode: system.ProxyModeAutomatic}}},
			message: "System proxy: automatic configuration still takes precedence",
			status:  "Automatic configuration takes precedence",
		},
		{
			name:    "another proxy is applied",
			check:   system.ProxyCheck{Status: system.ProxyMismatch, Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", "10.0.0.2:8080")}},
			message: "System proxy: OS settings still point elsewhere",
			status:  "Points at another proxy",
		},
		{
			name:    "unreadable afterwards",
			check:   system.ProxyCheck{Status: system.ProxyUnknown, Err: errors.New("networksetup not found")},
			message: "System proxy: change sent; OS settings could not be read",
			status:  "Could not read OS settings",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyOff}}
			a := proxyTestApp(t, fake)
			tt := ui.NewTester(a.view, 1280, 800)
			openProxySettings(t, tt)
			settleProxy(t, a, tt)
			fake.check = tc.check
			if err := tt.Click("Enable system proxy"); err != nil {
				t.Fatal(err)
			}
			settleProxy(t, a, tt)
			if a.message != tc.message {
				t.Fatalf("message = %q, want %q", a.message, tc.message)
			}
			if !tt.HasText(tc.status) {
				t.Fatalf("status %q missing: %q", tc.status, tt.Texts())
			}
		})
	}
}

func TestSystemProxyWriteFailureStaysVisible(t *testing.T) {
	fake := &fakeSystemProxy{
		check:    system.ProxyCheck{Status: system.ProxyOff},
		writeErr: errors.New("authorization denied"),
	}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	if err := tt.Click("Enable system proxy"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if !tt.HasText("Change failed") || !tt.HasText("Change failed: authorization denied") {
		t.Fatalf("a failed change must stay in the panel: %q", tt.Texts())
	}
	if a.message != "System proxy: authorization denied" {
		t.Fatalf("message = %q", a.message)
	}
}

func TestDisableSystemProxyVerifiesThatItIsOff(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyMatches}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	fake.check = system.ProxyCheck{Status: system.ProxyOff, Configs: []system.ProxyConfig{{Scope: "Wi-Fi"}}}
	if err := tt.Click("Disable system proxy"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if fake.writes != 1 || fake.lastOn || fake.lastAddr != "" {
		t.Fatalf("writes = %d, on = %v, addr = %q; want one clear", fake.writes, fake.lastOn, fake.lastAddr)
	}
	if a.message != "System proxy: off in OS settings" {
		t.Fatalf("message = %q, want the verified off state", a.message)
	}
	if !tt.HasText("No system proxy is set") {
		t.Fatalf("the panel did not keep the off state: %q", tt.Texts())
	}
}

func TestDisableSystemProxyReportsAnotherProxy(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyMatches}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	fake.check = system.ProxyCheck{Status: system.ProxyMismatch, Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", "10.0.0.2:8080")}}
	if err := tt.Click("Disable system proxy"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if a.message != "System proxy: another proxy is still configured" {
		t.Fatalf("message = %q; turning this app's proxy off must not claim the machine has none", a.message)
	}
}

func TestCheckAgainReadsTheOSWithoutChangingIt(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyOff}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	fake.check = system.ProxyCheck{Status: system.ProxyMatches, Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", a.srv.Addr())}}
	if err := tt.Click("Check again"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if fake.reads != 2 || fake.writes != 0 {
		t.Fatalf("reads = %d, writes = %d; checking must read only", fake.reads, fake.writes)
	}
	if !tt.HasText("HTTP and HTTPS point at this app") {
		t.Fatalf("checking again did not refresh the verdict: %q", tt.Texts())
	}
}

func TestSystemProxyNeedsALiveListenerToEnable(t *testing.T) {
	a := workspaceApp()
	a.settingsOpen, a.settingsTab = true, 1
	a.setSystemProxy(true)
	if a.message != "System proxy: start capture successfully first" {
		t.Fatalf("message = %q; an unstarted proxy must not change system settings", a.message)
	}
	if a.systemProxy.applying || a.systemProxy.pending != nil {
		t.Fatal("a refused enable must not start a worker")
	}
}

func TestProxySettingsReadTheOSOnEveryVisit(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyOff}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	// The machine may have changed while Settings was closed.
	fake.check = system.ProxyCheck{Status: system.ProxyMatches, Configs: []system.ProxyConfig{manualProxy(t, "Wi-Fi", a.srv.Addr())}}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if fake.reads != 2 {
		t.Fatalf("read the OS %d times, want a check on each visit", fake.reads)
	}
	if !tt.HasText("HTTP and HTTPS point at this app") {
		t.Fatalf("reopening Settings did not refresh the verdict: %q", tt.Texts())
	}
}

func TestProxySettingsRecheckWhenTheAddressChanges(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyOff}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	openProxySettings(t, tt)
	settleProxy(t, a, tt)
	// The listener's address is what a verdict is about: a restarted capture
	// on another port must not keep describing the old one.
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a.srv = srv
	tt.Frame()
	settleProxy(t, a, tt)
	if fake.reads != 2 {
		t.Fatalf("read the OS %d times, want a check for the new address", fake.reads)
	}
	if a.systemProxy.check.Target != srv.Addr() {
		t.Fatalf("target = %q, want %q", a.systemProxy.check.Target, srv.Addr())
	}
}

func TestProxyQuitWaitsForAnInFlightEnable(t *testing.T) {
	const target = "127.0.0.1:7890"
	started, release := make(chan struct{}), make(chan struct{})
	enabled := false
	s := &proxySetup{clear: func(addr string) error {
		if addr != target {
			t.Errorf("cleanup target = %q, want %q", addr, target)
		}
		enabled = false
		return nil
	}}
	written := make(chan error, 1)
	go func() {
		written <- s.change(true, target, func(bool, string) error {
			close(started)
			<-release
			enabled = true
			return nil
		})
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- s.close() }()
	select {
	case err := <-closed:
		close(release)
		t.Fatalf("quit completed before the enable finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("quit left the proxy enabled after its listener would have closed")
	}
	if err := s.change(true, target, func(bool, string) error {
		enabled = true
		return nil
	}); err == nil || enabled {
		t.Fatal("a late enable was allowed after quit cleanup")
	}
}

func TestProxyQuitCleansPartialWritesAndCanRetry(t *testing.T) {
	enabled := false
	denied := errors.New("authorization denied")
	cleanupErr := denied
	s := &proxySetup{clear: func(string) error {
		if cleanupErr != nil {
			return cleanupErr
		}
		enabled = false
		return nil
	}}
	if err := s.change(true, "127.0.0.1:7890", func(bool, string) error {
		enabled = true // one service changed before the next helper failed
		return denied
	}); !errors.Is(err, denied) {
		t.Fatalf("enable error = %v", err)
	}
	if err := s.close(); !errors.Is(err, denied) || !enabled {
		t.Fatalf("cleanup error = %v; failed cleanup must not claim success", err)
	}
	cleanupErr = nil
	if err := s.close(); err != nil || enabled {
		t.Fatalf("retry error = %v, enabled = %v", err, enabled)
	}
	s.clear = func(string) error { t.Fatal("completed cleanup must not run again"); return nil }
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
}

func TestProxyQuitWithoutOwnershipLeavesSystemSettingsAlone(t *testing.T) {
	s := &proxySetup{clear: func(string) error {
		t.Fatal("quitting without enabling the proxy must not change OS settings")
		return nil
	}}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
}
