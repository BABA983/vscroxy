package system

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ProxyMode is how one scope of the machine decides the proxy applications use.
type ProxyMode int

const (
	// ProxyModeOff leaves applications to connect directly.
	ProxyModeOff ProxyMode = iota
	// ProxyModeManual applies the fixed HTTP and HTTPS servers.
	ProxyModeManual
	// ProxyModeAutomatic applies a proxy auto-configuration script or
	// discovery, which the OS prefers over the fixed servers.
	ProxyModeAutomatic
)

// ProxyEndpoint is one protocol's fixed proxy server as the OS stores it. The
// server may stay configured while the mode leaves it unused.
type ProxyEndpoint struct {
	Enabled bool
	Host    string
	Port    int
}

// Addr is the server as host:port, empty when none is set.
func (e ProxyEndpoint) Addr() string {
	host := strings.Trim(e.Host, "[]")
	switch {
	case host == "":
		return ""
	case e.Port == 0:
		return host
	default:
		return net.JoinHostPort(host, strconv.Itoa(e.Port))
	}
}

// String is the endpoint as the settings panel spells it.
func (e ProxyEndpoint) String() string {
	switch {
	case !e.Enabled:
		return "off"
	case e.Addr() == "":
		return "on, no server"
	default:
		return e.Addr()
	}
}

// sameAs reports whether this endpoint holds the server addr names. Hosts
// compare without case, as names do.
func (e ProxyEndpoint) sameAs(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	if err != nil || !e.Enabled || e.Port != n {
		return false
	}
	return strings.EqualFold(strings.Trim(e.Host, "[]"), strings.Trim(host, "[]"))
}

// ProxyConfig is one scope's proxy configuration read back from the OS. A scope
// is a network service on macOS, the desktop settings on Linux, or the current
// user's WinINet settings on Windows.
type ProxyConfig struct {
	Scope string
	Mode  ProxyMode
	HTTP  ProxyEndpoint
	HTTPS ProxyEndpoint
	// PAC is the automatic configuration URL when Mode is ProxyModeAutomatic
	// and the OS fetches a script rather than discovering one.
	PAC string
}

// ProxyStatus is what a readback says about the address this app listens on.
type ProxyStatus int

const (
	// ProxyUnknown means the configuration could not be read, so nothing is
	// claimed about it.
	ProxyUnknown ProxyStatus = iota
	// ProxyOff means no proxy applies: applications connect directly.
	ProxyOff
	// ProxyMatches means the fixed HTTP and HTTPS proxies both point at the
	// address this app listens on.
	ProxyMatches
	// ProxyMismatch means a fixed proxy applies, but not for this app.
	ProxyMismatch
	// ProxyAutomatic means an automatic configuration applies and takes
	// precedence, so the fixed servers this app wrote may go unused.
	ProxyAutomatic
)

// ProxyCheck is the machine's proxy configuration and what it means for one
// address. It describes the OS setting alone: an application with its own proxy
// settings, or one that ignores the system proxy, may still connect directly,
// and traffic that reaches this app does not prove every application is proxied.
type ProxyCheck struct {
	// Target is the address the configuration was compared with.
	Target  string
	Status  ProxyStatus
	Configs []ProxyConfig
	// Err explains a configuration that could not be read.
	Err error
}

// CheckProxy reads the proxy configuration of every scope of this machine and
// compares it with target, the address this app listens on. An empty target
// asks whether a fixed proxy applies at all.
func CheckProxy(target string) ProxyCheck {
	configs, err := readProxy(commandOutput)
	return checkProxy(target, configs, err)
}

// checkProxy compares configurations that were already read, so tests can
// supply them instead of reading the machine.
func checkProxy(target string, configs []ProxyConfig, err error) ProxyCheck {
	if err != nil {
		return ProxyCheck{Target: target, Status: ProxyUnknown, Err: err}
	}
	check := ProxyCheck{Target: target, Configs: configs}
	if len(configs) == 0 {
		check.Status = ProxyUnknown
		check.Err = errors.New("no network service reports a proxy setting")
		return check
	}
	var matches, off int
	automatic := false
	for _, cfg := range configs {
		switch cfg.status(target) {
		case ProxyAutomatic:
			// One scope following an automatic script is enough to report a
			// conflict: the fixed proxy may not be what applications use.
			automatic = true
		case ProxyMatches:
			matches++
		case ProxyOff:
			off++
		}
	}
	switch {
	case automatic:
		check.Status = ProxyAutomatic
	case matches == len(configs):
		check.Status = ProxyMatches
	case off == len(configs):
		check.Status = ProxyOff
	default:
		check.Status = ProxyMismatch
	}
	return check
}

// status compares one scope's configuration with target.
func (c ProxyConfig) status(target string) ProxyStatus {
	switch c.Mode {
	case ProxyModeAutomatic:
		return ProxyAutomatic
	case ProxyModeManual:
		switch {
		case !c.HTTP.Enabled && !c.HTTPS.Enabled:
			return ProxyOff
		case target != "" && c.HTTP.sameAs(target) && c.HTTPS.sameAs(target):
			return ProxyMatches
		default:
			return ProxyMismatch
		}
	default:
		return ProxyOff
	}
}

// readEndpoint reads one protocol's fixed server from a settings source.
func readEndpoint(get func(section, key string) (string, error), section string) (ProxyEndpoint, error) {
	host, err := get(section, "host")
	if err != nil {
		return ProxyEndpoint{}, err
	}
	port, err := get(section, "port")
	if err != nil {
		return ProxyEndpoint{}, err
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return ProxyEndpoint{}, fmt.Errorf("%s port %q: %w", section, port, err)
	}
	return ProxyEndpoint{Enabled: host != "" && n != 0, Host: host, Port: n}, nil
}

// parseEndpointAddr reads host:port as the OS stores a fixed server.
func parseEndpointAddr(addr string) ProxyEndpoint {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return ProxyEndpoint{}
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return ProxyEndpoint{}
	}
	return ProxyEndpoint{Enabled: true, Host: host, Port: n}
}
