//go:build darwin

package system

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

// proxySnapshotEntry keeps a service's proxy configuration and its bypass
// domains. They are separate because ProxyConfig stays comparable.
type proxySnapshotEntry struct {
	Config ProxyConfig
	Bypass []string
}

type proxySnapshot []proxySnapshotEntry

func snapshotProxy(target string) (proxySnapshot, error) {
	configs, err := readProxy(commandOutput)
	if err != nil {
		return nil, err
	}
	snapshot := make(proxySnapshot, 0, len(configs))
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
		bypass, err := readServiceBypass(commandOutput, cfg.Scope)
		if err != nil {
			return nil, err
		}
		snapshot = append(snapshot, proxySnapshotEntry{Config: cfg, Bypass: bypass})
	}
	return snapshot, nil
}

func restoreProxy(before proxySnapshot, target string, applied []string, set bool) error {
	host, rawPort, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range before {
		cfg := entry.Config
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
		if !set {
			continue
		}
		current, err := readServiceBypass(commandOutput, cfg.Scope)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s bypass: %w", cfg.Scope, err))
			continue
		}
		if !sameBypass(current, applied) && !sameBypass(current, entry.Bypass) {
			continue // a replacement list is not ours to restore
		}
		if sameBypass(current, entry.Bypass) {
			continue
		}
		if err := setServiceBypass(cfg.Scope, entry.Bypass); err != nil {
			failures = append(failures, err)
			continue
		}
		actual, err := readServiceBypass(commandOutput, cfg.Scope)
		if err != nil {
			failures = append(failures, err)
		} else if !sameBypass(actual, entry.Bypass) {
			failures = append(failures, fmt.Errorf("%s bypass restoration was not accepted by macOS", cfg.Scope))
		}
	}
	return errors.Join(failures...)
}

func sameBypass(a, b []string) bool {
	normalize := func(list []string) []string {
		out := make([]string, len(list))
		for i, value := range list {
			out[i] = strings.ToLower(strings.TrimSpace(value))
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(normalize(a), normalize(b))
}
