//go:build linux

package system

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	caName      = "vscroxy.crt"
	proxySchema = "org.gnome.system.proxy"
)

// linuxDesktops are the independent proxy stores this session must keep in
// step. KDE applications can run inside a GNOME session and GNOME applications
// inside Plasma, so a session reporting both gets both stores: writing only one
// would leave some applications on the old proxy. Each store is journaled
// separately and restored from that journal.
type linuxDesktops struct {
	gnome bool
	kde   bool
}

// activeDesktops reads the desktop identifiers the session exports. KDE's own
// libproxy module selects kioslaverc from XDG_CURRENT_DESKTOP; KDE_FULL_SESSION
// also marks a Plasma session that omits the identifier. With no KDE signal the
// gsettings path stays enabled as it was before KDE support.
func activeDesktops(getenv func(string) string) linuxDesktops {
	kde := kdeFullSession(getenv("KDE_FULL_SESSION"))
	gnome := false
	for _, name := range strings.Split(getenv("XDG_CURRENT_DESKTOP"), ":") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "kde", "plasma":
			kde = true
		case "gnome":
			gnome = true
		}
	}
	return linuxDesktops{gnome: gnome || !kde, kde: kde}
}

func kdeFullSession(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// applyProxy points every journaled desktop store at addr. A configured bypass
// list replaces its exception list; without one the desktop's existing list is
// kept, matching the "Off keeps the OS list" setting.
func (s proxySnapshot) applyProxy(addr string, bypass []string, set bool) error {
	if _, _, ok := splitAddr(addr); !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	var failures []error
	if s.GNOME != nil {
		failures = append(failures, s.GNOME.applyProxy(addr, bypass, set))
	}
	if s.KDE.Path != "" {
		failures = append(failures, s.KDE.applyProxy(addr, bypass, set))
	}
	return errors.Join(failures...)
}

// applyProxy points the GNOME desktop proxy settings at addr after journaling.
// A configured bypass list replaces ignore-hosts; without one the desktop's
// existing list is kept, matching the "Off keeps the OS list" setting.
func (s gnomeSnapshot) applyProxy(addr string, bypass []string, set bool) error {
	host, port, ok := splitAddr(addr)
	if !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	args := [][]string{
		{"set", proxySchema + ".http", "host", host},
		{"set", proxySchema + ".http", "port", port},
		{"set", proxySchema + ".https", "host", host},
		{"set", proxySchema + ".https", "port", port},
	}
	if set {
		args = append(args, []string{"set", proxySchema, "ignore-hosts", gvariantList(bypass)})
	}
	args = append(args, []string{"set", proxySchema, "mode", "manual"})
	for _, args := range args {
		if err := run("gsettings", args...); err != nil {
			return err
		}
	}
	return nil
}

// ClearProxy turns the desktop proxy off in every active store.
func ClearProxy() error {
	desktops := activeDesktops(kdeCmd.env)
	var failures []error
	if desktops.gnome {
		failures = append(failures, run("gsettings", "set", proxySchema, "mode", "none"))
	}
	if desktops.kde {
		failures = append(failures, kdeClearProxy())
	}
	return errors.Join(failures...)
}

// readProxy reads the active desktop proxy settings. It reports what each
// desktop applies: a manual mode uses the fixed servers, an automatic mode uses
// the configuration URL, and use-same-proxy makes HTTPS follow HTTP.
func readProxy(out outputFunc) ([]ProxyConfig, error) {
	desktops := activeDesktops(kdeCmd.env)
	var configs []ProxyConfig
	if desktops.gnome {
		cfg, err := readGnomeProxy(out)
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	if desktops.kde {
		cfg, err := kdeReadProxy()
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	if len(configs) == 0 {
		return nil, errors.New("no desktop reports a proxy setting")
	}
	return configs, nil
}

// readGnomeProxy reads gsettings. It reports what the desktop applies: a manual
// mode uses the fixed servers, an automatic mode uses the configuration URL,
// and use-same-proxy makes HTTPS follow HTTP.
func readGnomeProxy(out outputFunc) (ProxyConfig, error) {
	get := func(section, key string) (string, error) {
		raw, err := out("gsettings", "get", section, key)
		if err != nil {
			return "", err
		}
		return gvariant(string(raw)), nil
	}
	mode, err := get(proxySchema, "mode")
	if err != nil {
		return ProxyConfig{}, err
	}
	cfg := ProxyConfig{Scope: "Desktop"}
	switch mode {
	case "manual":
		cfg.Mode = ProxyModeManual
	case "auto":
		cfg.Mode = ProxyModeAutomatic
		if cfg.PAC, err = get(proxySchema, "autoconfig-url"); err != nil {
			return ProxyConfig{}, err
		}
	}
	if cfg.Mode != ProxyModeManual {
		return cfg, nil
	}
	if cfg.HTTP, err = readEndpoint(get, proxySchema+".http"); err != nil {
		return ProxyConfig{}, err
	}
	same, err := get(proxySchema, "use-same-proxy")
	if err != nil {
		return ProxyConfig{}, err
	}
	if same == "true" {
		cfg.HTTPS = cfg.HTTP
	} else if cfg.HTTPS, err = readEndpoint(get, proxySchema+".https"); err != nil {
		return ProxyConfig{}, err
	}
	return cfg, nil
}

// gvariant reads a value as gsettings prints it: strings are quoted, numbers
// and booleans are bare.
func gvariant(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return s
	}
	body := s[1 : len(s)-1]
	body = strings.ReplaceAll(body, `\'`, `'`)
	return strings.ReplaceAll(body, `\\`, `\`)
}

// gvariantList writes a GVariant string array as gsettings accepts it.
func gvariantList(list []string) string {
	parts := make([]string, len(list))
	for i, value := range list {
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `'`, `\'`)
		parts[i] = "'" + value + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// trustHelperDirs are where distributions keep the trust refresh helpers but
// desktop sessions leave out of PATH: Debian and Ubuntu install
// update-ca-certificates in /usr/sbin, which a GUI app's PATH does not hold.
var trustHelperDirs = []string{"/usr/sbin", "/sbin", "/usr/local/sbin"}

// lookTrustHelper finds a trust refresh helper in PATH or in the system
// directories a desktop PATH omits.
func lookTrustHelper(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, dir := range trustHelperDirs {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

// InstallCA copies the CA into the system trust store and refreshes it.
func InstallCA(path string) error {
	cert, err := readCA(path)
	if err != nil {
		return err
	}
	var target, update string
	for _, store := range []struct{ command, dir string }{
		{"update-ca-certificates", "/usr/local/share/ca-certificates"},
		{"update-ca-trust", "/etc/pki/ca-trust/source/anchors"},
		{"update-ca-trust", "/etc/ca-certificates/trust-source/anchors"},
	} {
		bin, err := lookTrustHelper(store.command)
		info, statErr := os.Stat(store.dir)
		if err == nil && statErr == nil && info.IsDir() {
			target, update = filepath.Join(store.dir, caName), bin
			break
		}
	}
	if update == "" {
		return errors.New("automatic CA installation requires update-ca-certificates or update-ca-trust and its system anchor directory; import the public CA manually for this distribution")
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("pkexec not found; copy the public CA to %s and run %s as administrator", target, update)
	}
	install, err := exec.LookPath("install")
	if err != nil {
		return err
	}
	snapshot, err := snapshotCA(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	if err != nil {
		return err
	}
	defer os.Remove(snapshot)
	// One authorization for both steps; paths are arguments, never shell code.
	err = run("pkexec", "/bin/sh", "-c", `"$1" -m 0644 -- "$2" "$3" && "$4"`, "vscroxy-ca", install, snapshot, target, update)
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 126 {
		return ErrInstallCancelled
	}
	return err
}

func verifyCA(path string, _ *x509.Certificate) error {
	// A fresh process sees trust-store updates; Go caches Unix system roots.
	// This is a root CA, not a TLS server leaf with digitalSignature usage.
	return run("openssl", "verify", path)
}

// RevealCA opens the folder that holds the CA.
func RevealCA(path string) error {
	return run("xdg-open", filepath.Dir(path))
}
