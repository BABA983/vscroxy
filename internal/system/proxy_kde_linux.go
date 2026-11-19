//go:build linux

package system

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	kdeProxyFile  = "kioslaverc"
	kdeProxyGroup = "Proxy Settings"
	kdeTypeKey    = "ProxyType"
	kdePACKey     = "Proxy Config Script"
	kdeBypassKey  = "NoProxyFor"
	// KConfig writes an immutable group as "[Group][$i]" and recognises
	// exactly that suffix when reading; see src/core/kconfigini.cpp in
	// https://github.com/KDE/kconfig (parseConfig and writeEntries).
	kdeImmutableOption = "[$i]"
)

// kdeCommands is the seam the KDE backend uses for the tools and files it
// touches. Tests replace it to assert the exact commands and the edits they
// make to kioslaverc, without a Plasma session or KDE installed.
type kdeCommands struct {
	lookPath    func(string) (string, error)
	run         func(string, ...string) error
	env         func(string) string
	readFile    func(string) ([]byte, error)
	atomicWrite func(string, []byte) error
}

var kdeCmd = kdeCommands{
	lookPath:    exec.LookPath,
	run:         run,
	env:         os.Getenv,
	readFile:    os.ReadFile,
	atomicWrite: kdeAtomicWrite,
}

// kdeSnapshot is the Proxy Settings section as it was read, including keys we
// never write. Values are KConfig's logical values: file escapes are decoded,
// because kwriteconfig escapes them again when it writes. Flags records the
// original key suffix, for example [$e] or [$i], and Lines keeps the exact
// original lines so restoration can put flags back. kwriteconfig cannot write
// a flag: it turns the bracketed key into a literal, escaped key name.
type kdeSnapshot struct {
	Path        string
	Values      map[string]*string
	Flags       map[string]string   `json:",omitempty"`
	Lines       map[string][]string `json:",omitempty"`
	GroupLocked bool                `json:",omitempty"`
}

type kdeWrite struct {
	key   string
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

type kdeConfigData struct {
	Values      map[string]string
	Flags       map[string]string
	Lines       map[string][]string
	GroupLocked bool
}

func kdeReadConfigAt(path string) (kdeSnapshot, error) {
	data, err := kdeCmd.readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return kdeSnapshot{Path: path, Values: map[string]*string{}, Flags: map[string]string{}, Lines: map[string][]string{}}, nil
	}
	if err != nil {
		return kdeSnapshot{}, err
	}
	parsed := parseKDEProxyConfig(data)
	values := map[string]*string{}
	for key, text := range parsed.Values {
		value := text
		values[key] = &value
	}
	return kdeSnapshot{Path: path, Values: values, Flags: parsed.Flags, Lines: parsed.Lines, GroupLocked: parsed.GroupLocked}, nil
}

// parseKDEProxyConfig reads the Proxy Settings section while keeping each
// original line. Values are decoded to logical text; a literal [$e]/[$i]/[$d]
// suffix is recorded as a flag. Key escapes are not decoded here: KIO treats
// kwriteconfig's escaped bracket spelling as a different, literal key name.
func parseKDEProxyConfig(data []byte) kdeConfigData {
	parsed := kdeConfigData{
		Values: map[string]string{},
		Flags:  map[string]string{},
		Lines:  map[string][]string{},
	}
	inSection := false
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if name, locked, ok := kdeGroupHeader(trimmed); ok {
			inSection = name == kdeProxyGroup
			if inSection && locked {
				parsed.GroupLocked = true
			}
			continue
		}
		keyText, valueText, ok := kdeLineKey(line)
		if !inSection || !ok {
			continue
		}
		key, flag := splitKDEKeyFlag(keyText)
		parsed.Values[key] = decodeKDEEscapes(strings.TrimSuffix(valueText, "\r"))
		if flag != "" {
			parsed.Flags[key] = flag
		} else {
			delete(parsed.Flags, key)
		}
		parsed.Lines[key] = append(parsed.Lines[key], line)
	}
	return parsed
}

// kdeGroupHeader reads a KConfig group header and its optional immutable
// suffix. KConfig marks a locked group with exactly "[Group][$i]" after the
// group name; other suffixes are locales or subgroup names.
func kdeGroupHeader(line string) (name string, locked bool, ok bool) {
	rest, ok := strings.CutPrefix(line, "[")
	if !ok || !strings.HasSuffix(rest, "]") {
		return "", false, false
	}
	name, suffix, found := strings.Cut(rest, "]")
	if !found {
		return "", false, false
	}
	return name, suffix == kdeImmutableOption, true
}

// kdeLineKey reads the key and value of one config line; '#', ';' and group
// headers are not entries.
func kdeLineKey(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") ||
		strings.HasPrefix(trimmed, "[") {
		return "", "", false
	}
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), value, true
}

// splitKDEKeyFlag separates a literal KConfig option suffix from the key name.
// kwriteconfig cannot write such a suffix; it escapes the brackets and creates
// a different key that KIO will not recognise as the original flagged key.
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
	if err := s.lockedWrite(writes); err != nil {
		return err
	}
	binary, err := kdeWriteBinary()
	if err != nil {
		return err
	}
	for _, item := range writes {
		args := []string{"--file", kdeProxyFile, "--group", kdeProxyGroup, "--key", item.key}
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

// lockedWrite refuses a write that KConfig would reject with kwriteconfig's
// exit status 2: an immutable key, or the whole Proxy Settings group marked
// [$i]. It runs before the first command so no setting is half written.
func (s kdeSnapshot) lockedWrite(writes []kdeWrite) error {
	for _, item := range writes {
		if strings.Contains(s.Flags[item.key], "i") {
			return &KDELockedError{Key: item.key}
		}
	}
	if s.GroupLocked && len(writes) > 0 {
		return &KDELockedError{Key: writes[0].key, Group: kdeProxyGroup}
	}
	return nil
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

// kdeAtomicWrite replaces a config file without truncating it in place, so a
// crash cannot leave a half-restored proxy setting behind.
func kdeAtomicWrite(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".kioslaverc-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	done := false
	defer func() {
		if !done {
			os.Remove(name)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	done = true
	return nil
}

// kdeReplaceProxyLines puts the original lines back inside [Proxy Settings],
// leaving the rest of the file byte for byte. A key with no original line is
// removed. Lines are exact file bytes, so flags and escapes are preserved too.
func kdeReplaceProxyLines(data []byte, replacements map[string][]string) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+len(replacements))
	inSection := false
	emitted := map[string]bool{}
	missing := func() {
		for _, key := range slices.Sorted(maps.Keys(replacements)) {
			if !emitted[key] {
				out = append(out, replacements[key]...)
				emitted[key] = true
			}
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if name, _, isGroup := kdeGroupHeader(trimmed); isGroup {
			if inSection {
				missing()
				inSection = false
			}
			inSection = name == kdeProxyGroup
			out = append(out, line)
			continue
		}
		if inSection {
			if keyText, _, ok := kdeLineKey(line); ok {
				key, _ := splitKDEKeyFlag(keyText)
				if original, wanted := replacements[key]; wanted {
					if !emitted[key] {
						out = append(out, original...)
						emitted[key] = true
					}
					continue
				}
			}
		}
		out = append(out, line)
	}
	if inSection {
		missing()
	}
	return []byte(strings.Join(out, "\n"))
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
	order := append(endpoints, kdeBypassKey, kdeTypeKey)
	replacements := map[string][]string{}
	for _, key := range order {
		if !kdeSame(current.Values[key], expected[key]) {
			replacements[key] = before.Lines[key]
		}
	}
	if len(replacements) == 0 {
		return nil
	}
	data, err := kdeCmd.readFile(before.Path)
	if err != nil {
		return err
	}
	restored := kdeReplaceProxyLines(data, replacements)
	if slices.Equal(data, restored) {
		return nil
	}
	if err := kdeCmd.atomicWrite(before.Path, restored); err != nil {
		return err
	}
	if err := kdeNotify(); err != nil {
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
	raw, err := kdeCmd.readFile(before.Path)
	if err != nil {
		return err
	}
	afterLines := parseKDEProxyConfig(raw).Lines
	for key, original := range replacements {
		if !slices.Equal(afterLines[key], original) {
			return fmt.Errorf("KDE %s proxy line restoration was not accepted", key)
		}
	}
	return nil
}
