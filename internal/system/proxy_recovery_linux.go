//go:build linux

package system

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
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

// parseGVariantList reads gsettings' string array output, including the
// @as [] form of an empty list and double-quoted strings that contain an
// apostrophe. It returns false for output it cannot parse.
func parseGVariantList(raw string) ([]string, bool) {
	s := strings.TrimSpace(raw)
	if s == "@as []" || s == "[]" {
		return nil, true
	}
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, false
	}
	body := s[1 : len(s)-1]
	var out []string
	for i := 0; i < len(body); {
		for i < len(body) && (body[i] == ' ' || body[i] == ',' || body[i] == '\t') {
			i++
		}
		if i >= len(body) {
			break
		}
		if quote := body[i]; quote == '\'' || quote == '"' {
			i++
			var value strings.Builder
			closed := false
			for i < len(body) {
				switch body[i] {
				case '\\':
					if i+1 >= len(body) {
						return nil, false
					}
					value.WriteByte(body[i+1])
					i += 2
				case quote:
					i++
					closed = true
				default:
					value.WriteByte(body[i])
					i++
				}
				if closed {
					break
				}
			}
			if !closed {
				return nil, false
			}
			out = append(out, value.String())
			continue
		}
		start := i
		for i < len(body) && body[i] != ',' {
			i++
		}
		if value := strings.TrimSpace(body[start:i]); value != "" {
			out = append(out, value)
		}
	}
	return out, true
}

// sameBypassList compares host lists without depending on order or case.
func sameBypassList(a, b []string) bool {
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

func restoreProxy(before proxySnapshot, target string, applied []string, set bool) error {
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
	if allOwned && set {
		currentList, currentOK := parseGVariantList(current["ignore-hosts"])
		_, beforeOK := parseGVariantList(before["ignore-hosts"])
		if currentOK && beforeOK && sameBypassList(currentList, applied) {
			expected["ignore-hosts"] = before["ignore-hosts"]
		} else if current["ignore-hosts"] == gvariantList(applied) {
			expected["ignore-hosts"] = before["ignore-hosts"]
		}
	}
	if allOwned && gvariant(current["mode"]) == "manual" && current["mode"] != before["mode"] {
		expected["mode"] = before["mode"]
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
