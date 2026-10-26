//go:build darwin

package system

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

type proxySnapshot []ProxyConfig

func snapshotProxy(target string) (proxySnapshot, error) {
	configs, err := readProxy(commandOutput)
	if err != nil {
		return nil, err
	}
	for _, cfg := range configs {
		if cfg.HTTP.sameAs(target) || cfg.HTTPS.sameAs(target) {
			return nil, errors.New("system proxy already points at this address without a recovery record; disable the stale proxy first")
		}
		for _, flag := range []string{"-getwebproxy", "-getsecurewebproxy"} {
			raw, err := commandOutput("networksetup", flag, cfg.Scope)
			if err != nil {
				return nil, err
			}
			if auth := networkSetupFields(string(raw))["Authenticated Proxy Enabled"]; auth != "0" {
				return nil, fmt.Errorf("%s has an authenticated proxy; its credentials cannot be safely restored, so automatic takeover is disabled", cfg.Scope)
			}
		}
	}
	return configs, nil
}

func restoreProxy(before proxySnapshot, target string) error {
	host, rawPort, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		return err
	}
	var failures []error
	for _, cfg := range before {
		for _, item := range []struct {
			read, set, state string
			before           ProxyEndpoint
		}{
			{"-getwebproxy", "-setwebproxy", "-setwebproxystate", cfg.HTTP},
			{"-getsecurewebproxy", "-setsecurewebproxy", "-setsecurewebproxystate", cfg.HTTPS},
		} {
			current, err := readServiceEndpoint(commandOutput, item.read, cfg.Scope)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", cfg.Scope, err))
				continue
			}
			if (current.Host != host && current.Host != item.before.Host) ||
				(current.Port != port && current.Port != item.before.Port) {
				continue // a replacement proxy is not ours to restore
			}
			if current == item.before {
				continue
			}
			if item.before.Host != "" && item.before.Port > 0 {
				if err := run("networksetup", item.set, cfg.Scope, item.before.Host, strconv.Itoa(item.before.Port)); err != nil {
					failures = append(failures, err)
					continue
				}
			}
			state := "off"
			expected := item.before
			expected.Enabled = item.before.Enabled && current.Enabled
			if expected.Enabled {
				state = "on"
			}
			if err := run("networksetup", item.state, cfg.Scope, state); err != nil {
				failures = append(failures, err)
				continue
			}
			actual, err := readServiceEndpoint(commandOutput, item.read, cfg.Scope)
			if err != nil {
				failures = append(failures, err)
			} else if actual.Enabled != expected.Enabled || expected.Enabled && actual != expected {
				failures = append(failures, fmt.Errorf("%s proxy restoration was not accepted by macOS", cfg.Scope))
			}
		}
	}
	return errors.Join(failures...)
}
