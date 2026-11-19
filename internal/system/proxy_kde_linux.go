//go:build linux

package system

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	kdeProxyFile  = "kioslaverc"
	kdeProxyGroup = "Proxy Settings"
	kdeTypeKey    = "ProxyType"
	kdePACKey     = "Proxy Config Script"
	kdeBypassKey  = "NoProxyFor"
)

// kdeCommands is the seam the KDE backend uses for the tools and files it
// touches. Tests replace it to assert the exact commands and the edits they
// make to kioslaverc, without a Plasma session or KDE installed.
type kdeCommands struct {
	lookPath func(string) (string, error)
	run      func(string, ...string) error
	env      func(string) string
	readFile func(string) ([]byte, error)
}

var kdeCmd = kdeCommands{
	lookPath: exec.LookPath,
	run:      run,
	env:      os.Getenv,
	readFile: os.ReadFile,
}

// kdeSnapshot is the Proxy Settings section as it was read, including keys we
// never write. Values are KConfig's logical values: file escapes are decoded,
// because kwriteconfig escapes them again when it writes. Flags records the
// original key suffix, for example [$e] or [$i], by logical key name. A nil
// value means the key was absent, which restoration deletes.
type kdeSnapshot struct {
	Path   string
	Values map[string]*string
	Flags  map[string]string `json:",omitempty"`
}

type kdeWrite struct {
	key   string
	flag  string // original KConfig key suffix, empty for a plain key
	value *string
}

// kdeConfigPath follows KConfig's writable location: an absolute
// XDG_CONFIG_HOME when set, otherwise ~/.config.
func kdeConfigPath(getenv func(string) string) (string, error) {
	if dir := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, kdeProxyFile), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set; cannot locate the KDE proxy configuration")
	}
	return filepath.Join(home, ".config", kdeProxyFile), nil
}

func kdeReadConfig() (kdeSnapshot, error) {
	path, err := kdeConfigPath(kdeCmd.env)
	if err != nil {
		return kdeSnapshot{}, err
	}
	return kdeReadConfigAt(path)
}

func kdeReadConfigAt(path string) (kdeSnapshot, error) {
	data, err := kdeCmd.readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return kdeSnapshot{Path: path, Values: map[string]*string{}, Flags: map[string]string{}}, nil
	}
	if err != nil {
		return kdeSnapshot{}, err
	}
	raw, flags := parseKDEProxyConfig(data)
	values := map[string]*string{}
	for key, text := range raw {
		value := text
		values[key] = &value
	}
	return kdeSnapshot{Path: path, Values: values, Flags: flags}, nil
}

// parseKDEProxyConfig reads the Proxy Settings section as KConfig writes it,
// returning logical keys and values. A key's [$e]/[$i]/[$d] suffix is recorded
// separately so restoration can put the same flag back.
func parseKDEProxyConfig(data []byte) (map[string]string, map[string]string) {
	values, flags := map[string]string{}, map[string]string{}
	inSection := false
	for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inSection = strings.TrimSpace(trimmed[1:len(trimmed)-1]) == kdeProxyGroup
			continue
		}
		if !inSection || trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		keyText, valueText, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, flag := splitKDEKeyFlag(decodeKDEEscapes(strings.TrimSpace(keyText)))
		values[key] = decodeKDEEscapes(valueText)
		if flag != "" {
			flags[key] = flag
		} else {
			delete(flags, key)
		}
	}
	return values, flags
}

// splitKDEKeyFlag separates KConfig's option suffix from the key name. KConfig
// stores both a literal "[...]" suffix and kwriteconfig's escaped "\x5b...\x5d"
// spelling as a flag; any other bracketed suffix is part of the key.
func splitKDEKeyFlag(key string) (string, string) {
	start := strings.LastIndex(key, "[$")
	if start < 0 || !strings.HasSuffix(key, "]") {
		return key, ""
	}
	options := key[start+2 : len(key)-1]
	if options == "" {
		return key, ""
	}
	for _, option := range options {
		if option != 'e' && option != 'i' && option != 'd' {
			return key, ""
		}
	}
	return key[:start], key[start:]
}

// decodeKDEEscapes mirrors KConfigIniBackend::printableToString. kwriteconfig
// escapes values again when writing, so snapshots and comparisons must use the
// logical value. KConfig deliberately keeps backslash-semicolon and
// backslash-comma as written; they are not escape sequences there.
func decodeKDEEscapes(text string) string {
	data := []byte(text)
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' || i+1 >= len(data) {
			out = append(out, data[i])
			continue
		}
		i++
		switch data[i] {
		case 's':
			out = append(out, ' ')
		case 't':
			out = append(out, '\t')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case '\\':
			out = append(out, '\\')
		case ';', ',':
			out = append(out, '\\', data[i])
		case 'x':
			if i+2 < len(data) {
				value, err := strconv.ParseUint(string(data[i+1:i+3]), 16, 8)
				if err != nil {
					out = append(out, 'x') // KConfig reports the invalid hex and drops it
				} else {
					out = append(out, byte(value))
				}
				i += 2
			} else {
				out = append(out, 'x')
			}
		default:
			out = append(out, '\\', data[i])
		}
	}
	return string(out)
}

// kdeEndpoint reads KIO's "http://host port" spelling of a fixed server. KIO
// itself splits that string at its last space; the URL supplies the host.
func kdeEndpoint(raw string) ProxyEndpoint {
	raw = strings.TrimSpace(raw)
	index := strings.LastIndex(raw, " ")
	if index < 0 {
		return ProxyEndpoint{}
	}
	server, err := url.Parse(strings.TrimSpace(raw[:index]))
	if err != nil || server.Hostname() == "" {
		return ProxyEndpoint{}
	}
	port, err := strconv.Atoi(strings.TrimSpace(raw[index+1:]))
	if err != nil || port < 1 || port > 65535 {
		return ProxyEndpoint{}
	}
	return ProxyEndpoint{Enabled: true, Host: server.Hostname(), Port: port}
}

// kdeReadProxy reports what KIO would apply. ProxyType 2 uses the PAC URL, 3
// discovers one, and 4 follows environment variables instead of the fixed
// servers, so none of those modes uses httpProxy or httpsProxy.
func kdeReadProxy() (ProxyConfig, error) {
	state, err := kdeReadConfig()
	if err != nil {
		return ProxyConfig{}, err
	}
	cfg := ProxyConfig{Scope: "KDE"}
	switch value := state.Values[kdeTypeKey]; ptrText(value) {
	case "1":
		cfg.Mode = ProxyModeManual
		cfg.HTTP = kdeEndpoint(ptrText(state.Values["httpProxy"]))
		cfg.HTTPS = kdeEndpoint(ptrText(state.Values["httpsProxy"]))
	case "2":
		cfg.Mode, cfg.PAC = ProxyModeAutomatic, ptrText(state.Values[kdePACKey])
	case "3":
		cfg.Mode = ProxyModeAutomatic
	case "4":
		cfg.Mode, cfg.Scope = ProxyModeAutomatic, "KDE environment"
	}
	return cfg, nil
}

func ptrText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// kdeSnapshotProxy refuses to take over a proxy already pointing at our own
// listener without a journal to put it back.
func kdeSnapshotProxy(target string) (kdeSnapshot, error) {
	state, err := kdeReadConfig()
	if err != nil {
		return state, err
	}
	if ptrText(state.Values[kdeTypeKey]) != "1" {
		return state, nil
	}
	for _, key := range []string{"httpProxy", "httpsProxy"} {
		if kdeEndpoint(ptrText(state.Values[key])).sameAs(target) {
			return state, errors.New("system proxy already points at this address without a recovery record; disable the stale proxy first")
		}
	}
	return state, nil
}

func kdeWriteBinary() (string, error) {
	for _, name := range []string{"kwriteconfig6", "kwriteconfig5"} {
		if _, err := kdeCmd.lookPath(name); err == nil {
			return name, nil
		} else if !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: kwriteconfig6 or kwriteconfig5 not found", ErrKDEHelpersMissing)
}

// kdeNotify asks KIO's scheduler to make running applications reread
// kioslaverc. dbus-send spells an empty string argument as string:.
func kdeNotify() error {
	if _, err := kdeCmd.lookPath("dbus-send"); err != nil {
		return fmt.Errorf("%w: dbus-send not found", ErrKDEHelpersMissing)
	}
	return kdeCmd.run("dbus-send", "--type=signal", "/KIO/Scheduler",
		"org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:")
}

func kdeApplyWrites(s kdeSnapshot, writes []kdeWrite) error {
	binary, err := kdeWriteBinary()
	if err != nil {
		return err
	}
	for _, item := range writes {
		args := []string{"--file", kdeProxyFile, "--group", kdeProxyGroup, "--key", item.key + item.flag}
		if item.value == nil {
			args = append(args, "--delete")
		} else {
			args = append(args, *item.value)
		}
		if err := kdeCmd.run(binary, args...); err != nil {
			return err
		}
	}
	return kdeNotify()
}

// kdeFlagDeletes clears the original flagged spelling of each key before a
// plain write. A plain kwriteconfig write leaves a [$e]/[$i] entry in place,
// and that entry would keep deciding what KDE reads.
func kdeFlagDeletes(before kdeSnapshot, writes []kdeWrite) []kdeWrite {
	var deletes []kdeWrite
	seen := map[string]bool{}
	for _, item := range writes {
		key := item.key
		if before.Flags[key] != "" && !seen[key] {
			seen[key] = true
			deletes = append(deletes, kdeWrite{key: key}) // delete every spelling of the key
		}
	}
	return deletes
}

func kdeSame(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// applyProxy writes the fixed servers for every protocol KIO reads, then turns
// manual mode on. A configured bypass list replaces NoProxyFor; the PAC URL is
// left alone because manual mode ignores it and restoration must find it as it
// was. Changing ProxyType last avoids pointing applications at a half-written
// set of servers.
func (s kdeSnapshot) applyProxy(addr string, bypass []string, set bool) error {
	host, port, ok := splitAddr(addr)
	if !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	server := "http://" + host + " " + port
	writes := []kdeWrite{
		{key: "httpProxy", value: &server},
		{key: "httpsProxy", value: &server},
		{key: "ftpProxy", value: &server},
		{key: "socksProxy", value: &server},
	}
	if set {
		list := strings.Join(bypass, ",")
		writes = append(writes, kdeWrite{key: kdeBypassKey, value: &list})
	}
	manual := "1"
	writes = append(writes, kdeWrite{key: kdeTypeKey, value: &manual})
	if err := kdeApplyWrites(s, append(kdeFlagDeletes(s, writes), writes...)); err != nil {
		return err
	}
	current, err := kdeReadConfigAt(s.Path)
	if err != nil {
		return err
	}
	for _, item := range writes {
		if !kdeSame(current.Values[item.key], item.value) {
			return fmt.Errorf("KDE %s proxy write was not accepted", item.key)
		}
	}
	return nil
}

func kdeClearProxy() error {
	state, err := kdeReadConfig()
	if err != nil {
		return err
	}
	none := "0"
	writes := []kdeWrite{{key: kdeTypeKey, value: &none}}
	return kdeApplyWrites(state, append(kdeFlagDeletes(state, writes), writes...))
}

// restoreKDEProxy puts back each value this session wrote if the desktop still
// holds it, leaving a replacement installed by another program alone. Keys we
// never touched, notably the PAC URL, are verified against the snapshot too.
func restoreKDEProxy(before kdeSnapshot, target string, applied []string, set bool) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	current, err := kdeReadConfigAt(before.Path)
	if err != nil {
		return err
	}
	server := "http://" + host + " " + port
	list := strings.Join(applied, ",")
	owned := func(key, written string) bool {
		return kdeSame(current.Values[key], before.Values[key]) ||
			kdeSame(current.Values[key], &written)
	}
	expected := make(map[string]*string, len(current.Values))
	for key, value := range current.Values {
		expected[key] = value
	}
	endpoints := []string{"httpProxy", "httpsProxy", "ftpProxy", "socksProxy"}
	for _, key := range endpoints {
		if owned(key, server) {
			expected[key] = before.Values[key]
		}
	}
	if set && owned(kdeBypassKey, list) {
		expected[kdeBypassKey] = before.Values[kdeBypassKey]
	}
	if owned(kdeTypeKey, "1") {
		expected[kdeTypeKey] = before.Values[kdeTypeKey]
	}
	order := append(endpoints, kdeBypassKey, kdePACKey, kdeTypeKey)
	var writes []kdeWrite
	for _, key := range order {
		if kdeSame(current.Values[key], expected[key]) {
			continue
		}
		if expected[key] == nil {
			writes = append(writes, kdeWrite{key: key}) // remove the key we added
			continue
		}
		// An earlier [$e] or [$i] key was deleted before apply; write it back
		// with its original flag, so KDE sees the original expansion or
		// immutability, not a plain key with the same text.
		if before.Flags[key] != "" {
			writes = append(writes, kdeWrite{key: key}) // clear our plain entry first
		}
		writes = append(writes, kdeWrite{key: key, flag: before.Flags[key], value: expected[key]})
	}
	if len(writes) == 0 {
		return nil
	}
	if err := kdeApplyWrites(before, writes); err != nil {
		return err
	}
	after, err := kdeReadConfigAt(before.Path)
	if err != nil {
		return err
	}
	for key, want := range expected {
		wantFlag := current.Flags[key]
		if kdeSame(want, before.Values[key]) && !kdeSame(current.Values[key], before.Values[key]) {
			wantFlag = before.Flags[key]
		}
		if !kdeSame(after.Values[key], want) || after.Flags[key] != wantFlag {
			return fmt.Errorf("KDE %s proxy restoration was not accepted", key)
		}
	}
	return nil
}
