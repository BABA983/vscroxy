//go:build linux

package system

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"strings"
)

// Store raw GVariants so bypass lists, inactive servers and mode survive a
// round trip. Only these keys are written by applyProxy; PAC and credentials are
// not copied to the recovery file or modified during restoration.
type proxySnapshot map[string]string

var proxySnapshotKeys = []string{"http.host", "http.port", "https.host", "https.port", "ignore-hosts", "mode"}

func linuxProxyKey(key string) (schema, name string) {
	section, name, ok := strings.Cut(key, ".")
	if ok {
		return proxySchema + "." + section, name
	}
	return proxySchema, key
}

func readProxySnapshot() (proxySnapshot, error) {
	state := make(proxySnapshot)
	for _, key := range proxySnapshotKeys {
		schema, name := linuxProxyKey(key)
		raw, err := commandOutput("gsettings", "get", schema, name)
		if err != nil {
			return nil, err
		}
		state[key] = strings.TrimSpace(string(raw))
	}
	return state, nil
}

func snapshotProxy(target string) (proxySnapshot, error) {
	state, err := readProxySnapshot()
	if err != nil {
		return nil, err
	}
	if gvariant(state["mode"]) == "manual" {
		for _, protocol := range []string{"http", "https"} {
			addr := net.JoinHostPort(gvariant(state[protocol+".host"]), state[protocol+".port"])
			if addr == target {
				return nil, errors.New("system proxy already points at this address without a recovery record; disable the stale proxy first")
			}
		}
	}
	return state, nil
}

func restoreProxy(before proxySnapshot, target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	for _, key := range proxySnapshotKeys {
		if _, ok := before[key]; !ok {
			return fmt.Errorf("proxy recovery is missing %s", key)
		}
	}
	current, err := readProxySnapshot()
	if err != nil {
		return err
	}
	expected := maps.Clone(current)
	owned := make(map[string]bool)
	allOwned := true
	for _, protocol := range []string{"http", "https"} {
		hostKey, portKey := protocol+".host", protocol+".port"
		// Accept the intermediate state of a partially failed applyProxy too,
		// but preserve a replacement endpoint installed by another program.
		ours := (current[hostKey] == before[hostKey] || gvariant(current[hostKey]) == host) &&
			(current[portKey] == before[portKey] || current[portKey] == port)
		owned[protocol] = ours
		if !ours {
			allOwned = false
			continue
		}
		for _, key := range []string{hostKey, portKey} {
			expected[key] = before[key]
		}
	}
	if !allOwned && gvariant(before["mode"]) != "manual" {
		// Manual mode must stay on for the replacement. Do not accidentally
		// activate our old, previously unused server for the other protocol.
		for protocol, ours := range owned {
			if ours {
				expected[protocol+".host"] = "''"
			}
		}
	}
	if allOwned {
		if current["ignore-hosts"] == "['localhost', '127.0.0.1', '::1']" && current["ignore-hosts"] != before["ignore-hosts"] {
			expected["ignore-hosts"] = before["ignore-hosts"]
		}
		if gvariant(current["mode"]) == "manual" && current["mode"] != before["mode"] {
			expected["mode"] = before["mode"]
		}
	}
	var failures []error
	for _, key := range proxySnapshotKeys {
		if expected[key] != current[key] {
			schema, name := linuxProxyKey(key)
			failures = append(failures, run("gsettings", "set", schema, name, expected[key]))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	// A successful command is not proof that dconf accepted the change.
	after, err := readProxySnapshot()
	if err != nil {
		return err
	}
	for _, key := range proxySnapshotKeys {
		if after[key] != expected[key] {
			return fmt.Errorf("%s proxy restoration was not accepted by the desktop", key)
		}
	}
	return nil
}
