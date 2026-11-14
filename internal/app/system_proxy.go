package app

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// proxyResult is what a worker returns: the write it attempted, if any, and the
// OS configuration read back afterwards.
type proxyResult struct {
	wrote   bool
	on      bool
	applied error
	check   system.ProxyCheck
}

// proxySetup is the System proxy section's state. Only the UI accesses these
// fields; a worker returns its result through pending. A check and a write
// never run at once, so the panel can say what the OS holds at every moment.
type proxySetup struct {
	check    system.ProxyCheck
	checked  bool
	checking bool
	applying bool
	writeErr error
	pending  chan proxyResult
	// shown records that the panel was drawn last frame, so entering it again
	// reads the OS again.
	shown bool
	// read and write stand in for the system package in tests, which must never
	// change the machine. Nil uses the real calls.
	read  func(target string) system.ProxyCheck
	write func(on bool, addr string) error
	clear func(addr string) error

	// Protect the OS write and its ownership together, including writes whose
	// UI result has not been drained when the user quits.
	mu          sync.Mutex
	session     *system.ProxySession
	ownedTarget string
	ownedNow    atomic.Bool
	closing     bool
	quitting    bool // UI thread only
	quitReady   bool // UI thread only
}

// systemProxyTarget is the address applications are pointed at: the capture
// listener's address, which the server keeps while it is stopped.
func (a *app) systemProxyTarget() string {
	if a.srv == nil {
		return ""
	}
	return a.srv.Addr()
}

// checkSystemProxy reads the machine's proxy configuration off the UI thread.
// Diagnostics pass false: observing settings must not restore a prior session.
func (a *app) checkSystemProxy(recoverInterrupted bool) {
	s := &a.systemProxy
	if s.checking || s.applying {
		return
	}
	if s.pending == nil {
		s.pending = make(chan proxyResult, 1)
	}
	target, read, pending := a.systemProxyTarget(), s.read, s.pending
	path := a.proxyRecoveryPath()
	recover := recoverInterrupted && read == nil
	if read == nil {
		read = system.CheckProxy
	}
	s.checking = true
	go func() {
		var recoveryErr error
		if recover {
			s.mu.Lock()
			if s.session == nil {
				recoveryErr = system.RecoverProxy(path)
			}
			s.mu.Unlock()
		}
		pending <- proxyResult{check: read(target), applied: recoveryErr}
		a.invalidate()
	}()
}

func (a *app) proxyRecoveryPath() string {
	if a.settingsPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.settingsPath), "proxy-recovery.json")
}

// Called under proxySetup.mu. Disabling a session restores what it replaced;
// an explicit Disable without ownership still lets the user turn a proxy off.
func (a *app) writeSystemProxy(on bool, addr string) error {
	s := &a.systemProxy
	if on {
		if s.session != nil {
			if err := s.session.Restore(); err != nil {
				return err
			}
			s.session = nil
		}
		var err error
		s.session, err = system.BeginProxy(a.proxyRecoveryPath(), addr, a.systemProxyBypass, a.systemProxyBypassSet)
		return err
	}
	if s.session != nil {
		if err := s.session.Restore(); err != nil {
			return err
		}
		s.session = nil
		return nil
	}
	path := a.proxyRecoveryPath()
	if _, err := os.Stat(path); err == nil {
		return system.RecoverProxy(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return system.ClearProxy()
}

// owned reports whether this session replaced the OS proxy, so a bypass edit
// should reapply it.
func (s *proxySetup) owned() bool {
	return s.ownedNow.Load()
}

// saveSystemProxyBypass persists the list written on the next enable. When
// this session owns the proxy, it is re-enabled so the OS gets the new list.
func (a *app) saveSystemProxyBypass() {
	a.systemProxyBypass = parseSystemProxyBypass(a.systemProxyBypassText)
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil {
		a.fail(locales.SaveSystemProxyBypass(), err)
		return
	}
	if a.systemProxy.owned() {
		a.setSystemProxy(true)
	}
	a.done(locales.SystemProxyBypassSaved())
}

func parseSystemProxyBypass(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, part := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' }) {
			if part = strings.TrimSpace(part); part != "" && !strings.HasPrefix(part, "#") {
				out = append(out, part)
			}
		}
	}
	return out
}

// setSystemProxy turns the machine's proxy on or off, then reads the OS back so
// the panel reports what the system actually holds. The platform helpers may
// block on an authorization prompt, so they run off the UI thread.
func (a *app) setSystemProxy(on bool) {
	s := &a.systemProxy
	if s.checking || s.applying || s.quitting {
		return
	}
	var addr string
	if on {
		if a.srv == nil || !a.srv.Running() {
			a.warn(locales.SystemProxyStartCaptureSuccessfully())
			return
		}
		addr = a.srv.Addr()
	}
	if s.pending == nil {
		s.pending = make(chan proxyResult, 1)
	}
	write, read, pending := s.write, s.read, s.pending
	if write == nil {
		write = a.writeSystemProxy
	}
	if read == nil {
		read = system.CheckProxy
	}
	target := a.systemProxyTarget()
	s.applying, s.writeErr = true, nil
	go func() {
		applied := s.change(on, addr, write)
		pending <- proxyResult{wrote: true, on: on, applied: applied, check: read(target)}
		a.invalidate()
	}()
}

func (s *proxySetup) change(on bool, addr string, write func(bool, string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return errors.New("application is quitting")
	}
	if on {
		// A failed helper can still have changed one protocol or service.
		s.ownedTarget = addr
		s.ownedNow.Store(true)
	}
	err := write(on, addr)
	if !on && err == nil {
		s.ownedTarget = ""
		s.ownedNow.Store(false)
	}
	return err
}

// close waits for an in-flight write before releasing our proxy. It is also
// called synchronously from OnQuit and the main defer as an idempotent fallback.
func (s *proxySetup) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	if s.ownedTarget == "" {
		return nil
	}
	var err error
	if s.clear != nil {
		err = s.clear(s.ownedTarget)
	} else if s.session != nil {
		err = s.session.Restore()
	}
	if err != nil {
		s.closing = false // keep capture alive; let the user repair/retry
		return err
	}
	s.session = nil
	s.ownedTarget = ""
	s.ownedNow.Store(false)
	return nil
}

// beforeProxyQuit cancels the first quit while OS cleanup runs without
// blocking the native event loop (networksetup may need authorization).
func (a *app) beforeProxyQuit(e *mygo.QuitEvent) {
	s := &a.systemProxy
	if s.quitReady || e.DefaultPrevented() {
		return
	}
	e.PreventDefault()
	if s.quitting {
		return
	}
	s.quitting = true
	a.invalidate()
	go func() {
		err := s.close()
		mygo.RunOnMain(func() {
			s.quitting = false
			if err != nil {
				a.fail(locales.QuitCancelledSystemProxyCleanup(), err)
				a.invalidate()
				go mygo.Dialog.Error(locales.CouldNotReleaseSystemProxy(), "Capture is still running to keep your network working. Disable the system proxy in Settings and retry quitting.\n\n"+err.Error())
				return
			}
			s.quitReady = true
			mygo.App.Quit()
		})
	}()
}

// drainSystemProxy applies a finished worker's result to the panel.
func (a *app) drainSystemProxy() {
	s := &a.systemProxy
	select {
	case result := <-s.pending:
		s.check, s.writeErr = result.check, result.applied
		s.checked, s.checking, s.applying = true, false, false
		if result.wrote {
			a.inform(proxyOutcome(result))
		} else if result.applied != nil {
			a.fail(locales.SystemProxyRecovery(), result.applied)
		}
	default:
	}
}

// proxyOutcome words a write and its readback for the status bar.
func proxyOutcome(result proxyResult) string {
	if result.applied != nil {
		return "System proxy: " + result.applied.Error()
	}
	if !result.on {
		switch result.check.Status {
		case system.ProxyOff:
			return "System proxy: off in OS settings"
		case system.ProxyAutomatic:
			return "System proxy: automatic configuration is still on"
		case system.ProxyUnknown:
			return "System proxy: change sent; OS settings could not be read"
		default:
			return "System proxy: another proxy is still configured"
		}
	}
	switch result.check.Status {
	case system.ProxyMatches:
		return "System proxy: HTTP and HTTPS now point at " + result.check.Target
	case system.ProxyAutomatic:
		return "System proxy: automatic configuration still takes precedence"
	case system.ProxyOff:
		return "System proxy: OS settings still show no proxy"
	case system.ProxyUnknown:
		return "System proxy: change sent; OS settings could not be read"
	default:
		return "System proxy: OS settings still point elsewhere"
	}
}

// systemProxyStatus is the panel's verdict in words.
func (a *app) systemProxyStatus(t *ui.Theme) (string, ui.Color) {
	s := &a.systemProxy
	switch {
	case s.applying:
		return "Applying to the OS…", t.TextMuted
	case s.checking:
		return "Checking OS settings…", t.TextMuted
	case !s.checked:
		return "Not checked yet", t.TextMuted
	case s.writeErr != nil:
		return "Change failed", t.Danger
	case s.check.Status == system.ProxyUnknown:
		return "Could not read OS settings", t.Warning
	case s.check.Status == system.ProxyMatches:
		return "HTTP and HTTPS point at this app", t.Success
	case s.check.Status == system.ProxyAutomatic:
		return "Automatic configuration takes precedence", t.Warning
	case s.check.Status == system.ProxyOff:
		return "No system proxy is set", t.TextMuted
	default:
		return "Points at another proxy", t.Warning
	}
}

// proxyScopeLine spells one scope's configuration as it was read.
func proxyScopeLine(cfg system.ProxyConfig) string {
	switch cfg.Mode {
	case system.ProxyModeAutomatic:
		if cfg.PAC != "" {
			return cfg.Scope + " · automatic script " + cfg.PAC
		}
		return cfg.Scope + " · automatic discovery"
	case system.ProxyModeManual:
		return cfg.Scope + " · manual · HTTP " + cfg.HTTP.String() + " · HTTPS " + cfg.HTTPS.String()
	default:
		return cfg.Scope + " · no proxy"
	}
}

// systemProxyDetail is the one line under the status: the failure that matters,
// or what the OS holds for the address this app listens on. It always returns a
// line, so a result cannot change the height of the section.
func (a *app) systemProxyDetail(target string) (line, tooltip string) {
	s := &a.systemProxy
	held := make([]string, 0, len(s.check.Configs))
	for _, cfg := range s.check.Configs {
		held = append(held, proxyScopeLine(cfg))
	}
	scopes := strings.Join(held, " · ")
	switch {
	case s.writeErr != nil:
		line = "Change failed: " + s.writeErr.Error()
	case s.check.Err != nil:
		line = "Read failed: " + s.check.Err.Error()
	case len(s.check.Configs) > 0:
		line = proxySummary(s.check.Configs)
	case target != "":
		line = "This app's proxy address: " + target
	default:
		line = "Capture has no address yet"
	}
	if scopes != "" {
		return line, line + " · " + scopes
	}
	return line, line
}

// proxySummary says in one line how many network services point at a proxy:
// the full list belongs in the tip, not in the panel.
func proxySummary(configs []system.ProxyConfig) string {
	var pointed []string
	for _, cfg := range configs {
		ep := cfg.HTTP
		if !ep.Enabled {
			ep = cfg.HTTPS
		}
		if ep.Enabled {
			pointed = append(pointed, cfg.Scope+" points at "+net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)))
		}
	}
	switch len(pointed) {
	case 0:
		if len(configs) == 1 {
			return "1 network service has no proxy"
		}
		return fmt.Sprintf("%d network services have no proxy", len(configs))
	case 1:
		return fmt.Sprintf("%s; the other %d have no proxy", pointed[0], len(configs)-1)
	default:
		return fmt.Sprintf("%d of %d network services point at a proxy", len(pointed), len(configs))
	}
}

// systemProxyHint says why the switch cannot be used while capture is
// stopped, and otherwise what the OS holds.
func systemProxyHint(detail string, running bool) string {
	if !running {
		return locales.StartCaptureFirst() + " · " + detail
	}
	return detail + " · " + locales.ReadFromThisComputerS()
}

// systemProxySettings reports the machine's proxy configuration and offers the
// actions that change it. It verifies the OS setting, not whether applications
// use the proxy.
func (a *app) systemProxySettings(c *ui.Context, t *ui.Theme) {
	s := &a.systemProxy
	target := a.systemProxyTarget()
	// Check on every visit to the panel and when the address this app listens
	// on changes, so a verdict never describes a stale address or a stale
	// machine.
	entered := !s.shown
	s.shown = true
	if !s.checking && !s.applying && (entered || !s.checked || s.check.Target != target) {
		a.checkSystemProxy(true)
	}
	running := a.srv != nil && a.srv.Running()
	busy := s.checking || s.applying || s.quitting
	status, _ := a.systemProxyStatus(t)
	detail, detailTip := a.systemProxyDetail(target)
	uikit.SettingsGroup(c, locales.SystemProxy(), func() {
		// One row: the name, the switch that changes the OS, what the OS
		// says, and the check. The scope and the sentence read beneath it,
		// so the panel does not stack two labels on top of each other.
		uikit.SettingsRow(c, locales.UseSystemProxy(), func() ui.Element {
			return ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
				// One switch, not a pair of buttons that has to be read
				// to tell which way is left: the switch shows what the OS
				// holds, and turning it over is the change.
				on := s.check.Status == system.ProxyMatches
				toggle := ui.Switch(c, &on).Label(locales.UseThisAppsProxy()).Disabled(!running || busy)
				if !running {
					uikit.Tip(c, toggle, locales.StartCaptureFirst())
				}
				if toggle.Changed() {
					a.setSystemProxy(on)
				}
				ui.Text(c, status).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.TextMuted)
				if busy {
					ui.Spinner(c).Label(locales.SystemProxy())
				}
				if uikit.SecondaryButton(c, locales.CheckAgain()).Disabled(busy).Clicked() {
					a.checkSystemProxy(true)
				}
			})
		}, systemProxyHint(detail, running), systemProxyHint(detailTip, running))
		uikit.SettingsRowMultiline(c, locales.BypassList(), func() ui.Element {
			return ui.Column(c).Gap(t.Space(2)).Children(func() {
				ui.Checkbox(c, &a.systemProxyBypassSet, "Replace the system bypass list")
				ui.TextArea(c, &a.systemProxyBypassText).Label(locales.SystemProxyBypassList()).Placeholder("localhost, *.internal.test, 10.0.0.0/8").Height(64).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Disabled(!a.systemProxyBypassSet)
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					if uikit.SecondaryButton(c, locales.SaveBypassList()).Clicked() {
						a.saveSystemProxyBypass()
					}
					uikit.Hint(c, locales.RestoredWhenTheSystemProxy())
				})
			})
		}, locales.OffKeepsTheOsList())
	})
}
