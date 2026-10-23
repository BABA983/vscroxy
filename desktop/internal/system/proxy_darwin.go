//go:build darwin

package system

import (
	"crypto/x509"
	"fmt"
	"strconv"
	"strings"
)

// applyProxy changes only the services whose previous settings were saved.
func (before proxySnapshot) applyProxy(addr string) error {
	host, port, ok := splitAddr(addr)
	if !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	for _, cfg := range before {
		svc := cfg.Scope
		for _, args := range [][]string{
			{"-setwebproxy", svc, host, port},
			{"-setsecurewebproxy", svc, host, port},
			{"-setwebproxystate", svc, "on"},
			{"-setsecurewebproxystate", svc, "on"},
		} {
			if err := run("networksetup", args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearProxy turns the proxy off on every enabled network service.
func ClearProxy() error {
	services, err := services()
	if err != nil {
		return err
	}
	for _, svc := range services {
		for _, flag := range []string{"-setwebproxystate", "-setsecurewebproxystate"} {
			if err := run("networksetup", flag, svc, "off"); err != nil {
				return err
			}
		}
	}
	return nil
}

// readProxy reads every enabled network service's proxy settings. A service
// with an enabled automatic configuration applies that script or discovery
// instead of its fixed servers.
func readProxy(out outputFunc) ([]ProxyConfig, error) {
	services, err := listServices(out)
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("no enabled network services")
	}
	configs := make([]ProxyConfig, 0, len(services))
	for _, svc := range services {
		cfg, err := readServiceProxy(out, svc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", svc, err)
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// readServiceProxy reads one service's fixed proxies and automatic setup.
func readServiceProxy(out outputFunc, svc string) (ProxyConfig, error) {
	cfg := ProxyConfig{Scope: svc}
	var err error
	if cfg.HTTP, err = readServiceEndpoint(out, "-getwebproxy", svc); err != nil {
		return cfg, err
	}
	if cfg.HTTPS, err = readServiceEndpoint(out, "-getsecurewebproxy", svc); err != nil {
		return cfg, err
	}
	pac, enabled, err := readAutoProxyURL(out, svc)
	if err != nil {
		return cfg, err
	}
	discovery, err := readProxyDiscovery(out, svc)
	if err != nil {
		return cfg, err
	}
	switch {
	case enabled && pac != "":
		cfg.Mode, cfg.PAC = ProxyModeAutomatic, pac
	case discovery:
		cfg.Mode = ProxyModeAutomatic
	case cfg.HTTP.Enabled || cfg.HTTPS.Enabled:
		cfg.Mode = ProxyModeManual
	}
	return cfg, nil
}

// readServiceEndpoint reads one fixed proxy of a network service.
func readServiceEndpoint(out outputFunc, flag, svc string) (ProxyEndpoint, error) {
	raw, err := out("networksetup", flag, svc)
	if err != nil {
		return ProxyEndpoint{}, err
	}
	fields := networkSetupFields(string(raw))
	if fields["Enabled"] != "Yes" && fields["Enabled"] != "No" {
		return ProxyEndpoint{}, fmt.Errorf("unrecognized networksetup proxy response: %s", strings.TrimSpace(string(raw)))
	}
	port, err := strconv.Atoi(fields["Port"])
	if err != nil {
		return ProxyEndpoint{}, fmt.Errorf("read proxy port: %w", err)
	}
	host := fields["Server"]
	if host == "(null)" {
		host = ""
	}
	return ProxyEndpoint{
		Enabled: fields["Enabled"] == "Yes",
		Host:    host,
		Port:    port,
	}, nil
}

// readAutoProxyURL reads a service's automatic configuration script.
func readAutoProxyURL(out outputFunc, svc string) (url string, enabled bool, err error) {
	raw, err := out("networksetup", "-getautoproxyurl", svc)
	if err != nil {
		return "", false, err
	}
	fields := networkSetupFields(string(raw))
	if fields["Enabled"] != "Yes" && fields["Enabled"] != "No" {
		return "", false, fmt.Errorf("unrecognized networksetup PAC response: %s", strings.TrimSpace(string(raw)))
	}
	url = fields["URL"]
	if url == "(null)" {
		url = ""
	}
	return url, fields["Enabled"] == "Yes", nil
}

// readProxyDiscovery reads whether a service discovers its proxy itself.
func readProxyDiscovery(out outputFunc, svc string) (bool, error) {
	raw, err := out("networksetup", "-getproxyautodiscovery", svc)
	if err != nil {
		return false, err
	}
	switch networkSetupFields(string(raw))["Auto Proxy Discovery"] {
	case "On":
		return true, nil
	case "Off":
		return false, nil
	default:
		return false, fmt.Errorf("unrecognized networksetup discovery response: %s", strings.TrimSpace(string(raw)))
	}
}

// networkSetupFields parses networksetup's "Key: value" lines.
func networkSetupFields(raw string) map[string]string {
	fields := make(map[string]string)
	for line := range strings.SplitSeq(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields
}

func verifyCA(path string, _ *x509.Certificate) error {
	// Check CA trust, not TLS leaf policy (which requires a server leaf's EKU).
	// Query the current keychains without network access or adding
	// the certificate as an explicit trust anchor (which would prove nothing).
	return run("security", "verify-cert", "-L", "-l", "-p", "basic", "-c", path)
}

// RevealCA selects the CA in Finder.
func RevealCA(path string) error {
	return run("open", "-R", path)
}

// services lists the enabled network services of this machine.
func services() ([]string, error) {
	return listServices(commandOutput)
}

// listServices lists the enabled network services, skipping the header and the
// services macOS marks disabled with a leading asterisk.
func listServices(out outputFunc) ([]string, error) {
	raw, err := out("networksetup", "-listallnetworkservices")
	if err != nil {
		return nil, fmt.Errorf("list network services: %w", err)
	}
	var list []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		list = append(list, line)
	}
	return list, nil
}
