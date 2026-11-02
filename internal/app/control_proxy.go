package app

import (
	"errors"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/system"
)

// controlProxySnapshot describes the last completed OS read, not whether all
// applications use the system proxy. While checking or applying, that read may
// predate the pending operation. Only our capture address is disclosed: OS
// configurations, PAC URLs and helper errors must never cross this boundary.
type controlProxySnapshot struct {
	Checked  bool   `json:"checked"`
	Checking bool   `json:"checking"`
	Applying bool   `json:"applying"`
	Status   string `json:"status"`
	Target   string `json:"target"`
	Problem  string `json:"problem"`
}

// controlSystemProxy runs on the UI thread. The first get starts a read-only
// check if no read has completed yet. Thereafter get only drains/polls unless
// refresh:true is supplied; refresh coalesces with an existing check or write.
// Neither form of get performs interrupted-session recovery. A changed capture
// address invalidates the old verdict until an explicit refresh or write.
// Set accepts an asynchronous attempt, never a claim that OS settings changed.
func (a *app) controlSystemProxy(command control.Command) (any, error) {
	s := &a.systemProxy
	switch command.Action {
	case "system-proxy.get":
		p, err := controlParams[struct {
			Refresh bool `json:"refresh"`
		}](command)
		if err != nil {
			return nil, err
		}
		a.drainSystemProxy()
		if (p.Refresh || !s.checked) && !s.quitting && !s.quitReady {
			a.checkSystemProxy(false)
		}
		return a.controlProxySnapshot(), nil
	case "system-proxy.set-enabled":
		p, err := controlParams[struct {
			Enabled *bool `json:"enabled"`
			Confirm bool  `json:"confirm"`
		}](command)
		if err != nil || p.Enabled == nil || !p.Confirm {
			return nil, errors.New("enabled and confirm:true are required")
		}
		a.drainSystemProxy()
		if s.quitting || s.quitReady {
			return nil, errors.New("application is quitting")
		}
		if s.checking || s.applying {
			return nil, errors.New("system proxy operation is already active")
		}
		// close owns this mutex across OS cleanup. Never wait for it on the
		// UI thread, even when shutdown was initiated outside beforeProxyQuit.
		if !s.mu.TryLock() {
			return nil, errors.New("system proxy operation is already active")
		}
		closing := s.closing
		s.mu.Unlock()
		if closing {
			return nil, errors.New("application is quitting")
		}
		if *p.Enabled && (a.srv == nil || !a.srv.Running()) {
			return nil, errors.New("local proxy is not running")
		}
		a.setSystemProxy(*p.Enabled)
		return struct {
			Accepted bool                 `json:"accepted"`
			Snapshot controlProxySnapshot `json:"snapshot"`
		}{Accepted: true, Snapshot: a.controlProxySnapshot()}, nil
	default:
		return nil, errors.New("unknown action")
	}
}

func (a *app) controlProxySnapshot() controlProxySnapshot {
	s := &a.systemProxy
	target := a.systemProxyTarget()
	value := controlProxySnapshot{
		Checked:  s.checked && s.check.Target == target,
		Checking: s.checking, Applying: s.applying,
		Status: "unknown", Target: target,
	}
	switch {
	case !s.checked:
		// There is no completed read to describe yet.
	case !value.Checked:
		value.Problem = "Capture target changed; refresh system proxy status"
	case s.check.Err != nil:
		value.Problem = "Could not read OS proxy settings"
	default:
		switch s.check.Status {
		case system.ProxyOff:
			value.Status = "off"
		case system.ProxyMatches:
			value.Status = "matches"
		case system.ProxyMismatch:
			value.Status = "mismatch"
			value.Problem = "System proxy points elsewhere"
		case system.ProxyAutomatic:
			value.Status = "automatic"
			value.Problem = "Automatic proxy configuration takes precedence"
		default:
			value.Problem = "Could not read OS proxy settings"
		}
	}
	if s.writeErr != nil {
		value.Problem = "System proxy change failed"
	}
	return value
}
