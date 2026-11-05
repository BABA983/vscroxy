package clientsetup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ImportJavaCA writes the public CA into the caller's own PKCS#12 truststore.
// It imports into a temporary file and renames it only after keytool succeeds,
// so a failed import leaves a working truststore in place. The path belongs to
// this app, never to a JDK installation.
func ImportJavaCA(p Proxy, keystore string) (Argv, error) {
	args, err := KeytoolArgs(p, keystore)
	if err != nil {
		return Argv{}, err
	}
	bin, err := toolPath("keytool")
	if err != nil {
		return Argv{}, err
	}
	if err := os.MkdirAll(filepath.Dir(keystore), 0o700); err != nil {
		return Argv{}, err
	}
	temporary := keystore + ".import"
	_ = os.Remove(temporary)
	importArgs := slices.Clone(args)
	for i := range importArgs {
		if importArgs[i] == keystore {
			importArgs[i] = temporary
		}
	}
	command := Argv{Name: "keytool", Args: args}
	out, err := exec.Command(bin, importArgs...).CombinedOutput()
	if err != nil {
		_ = os.Remove(temporary)
		line := command.String(runtime.GOOS == "windows")
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return command, fmt.Errorf("%s: %s", line, firstLine(msg))
		}
		return command, fmt.Errorf("%s: %w", line, err)
	}
	if err := os.Rename(temporary, keystore); err != nil {
		_ = os.Remove(temporary)
		return command, err
	}
	return command, nil
}

// PrepareFirefoxProfile creates a temporary profile with user.js and tries to
// add the CA to its own NSS store. A missing certutil is returned as a warning,
// not a failure: Firefox still opens, and the caller can explain that trust is
// not set up in that profile.
func PrepareFirefoxProfile(p Proxy, profile string) (warning error, err error) {
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	userJS, err := FirefoxUserJS(p)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(userJS), 0o600); err != nil {
		return nil, err
	}
	if err := trustNSS(profile, p.CAPath); err != nil {
		return err, nil
	}
	return nil, nil
}

// PrepareChromeProfile creates the temporary profile directory. Chrome uses the
// system trust store on macOS and Windows and NSS on Linux; the profile itself
// is not where Linux Chrome reads CAs.
func PrepareChromeProfile(profile string) error {
	if strings.TrimSpace(profile) == "" {
		return errors.New("Chrome profile directory is empty")
	}
	return os.MkdirAll(profile, 0o700)
}

func trustNSS(profile, caPath string) error {
	bin, err := toolPath("certutil")
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(profile, "cert9.db")); errors.Is(err, fs.ErrNotExist) {
		out, err := exec.Command(bin, "-N", "--empty-password", "-d", "sql:"+profile).CombinedOutput()
		if err != nil {
			return fmt.Errorf("initialize Firefox certificate store: %s: %w", firstLine(string(out)), err)
		}
	}
	out, err := exec.Command(bin, "-A", "-n", "vscroxy-ca", "-t", "C,,", "-i", caPath, "-d", "sql:"+profile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add CA to Firefox profile: %s: %w", firstLine(string(out)), err)
	}
	return nil
}
