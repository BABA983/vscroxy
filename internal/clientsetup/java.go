package clientsetup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// JavaPlan is the reviewable Java setup: the keytool executable, the JDK's
// cacerts source and the commands that build the app-owned truststore.
type JavaPlan struct {
	Keytool  string
	CACerts  string
	Commands []Argv
}

// JavaTruststorePlan resolves keytool and the JDK's cacerts, then builds the
// commands that copy cacerts and add the vscroxy CA. It runs nothing.
func JavaTruststorePlan(p Proxy, keystore string) (JavaPlan, error) {
	bin, err := toolPath("keytool")
	if err != nil {
		return JavaPlan{}, err
	}
	cacerts, err := JavaCACertsPath(bin, os.Getenv("JAVA_HOME"))
	if err != nil {
		return JavaPlan{}, err
	}
	commands, err := JavaTruststoreCommands(p, keystore, cacerts)
	if err != nil {
		return JavaPlan{}, err
	}
	return JavaPlan{Keytool: bin, CACerts: cacerts, Commands: commands}, nil
}

// JavaCACertsPath finds the JDK's cacerts from JAVA_HOME or the keytool path.
// It follows symlinks because /usr/bin/keytool is often a launcher symlink.
func JavaCACertsPath(keytoolPath, javaHome string) (string, error) {
	var candidates []string
	if javaHome = strings.TrimSpace(javaHome); javaHome != "" {
		candidates = append(candidates,
			filepath.Join(javaHome, "lib", "security", "cacerts"),
			filepath.Join(javaHome, "jre", "lib", "security", "cacerts"))
	}
	if resolved, err := filepath.EvalSymlinks(keytoolPath); err == nil {
		keytoolPath = resolved
	}
	if keytoolPath != "" {
		jdk := filepath.Dir(filepath.Dir(keytoolPath))
		candidates = append(candidates,
			filepath.Join(jdk, "lib", "security", "cacerts"),
			filepath.Join(jdk, "jre", "lib", "security", "cacerts"))
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("JDK cacerts not found; set JAVA_HOME or use a JDK with lib/security/cacerts (tried %s)", strings.Join(candidates, ", "))
}

// JavaTruststoreCommands copies the JDK's cacerts into a temporary PKCS#12
// truststore, then adds the vscroxy CA. The caller renames the temporary file
// only after both commands succeed.
func JavaTruststoreCommands(p Proxy, keystore, cacerts string) ([]Argv, error) {
	if strings.TrimSpace(cacerts) == "" {
		return nil, errors.New("JDK cacerts path is empty")
	}
	if strings.TrimSpace(keystore) == "" {
		return nil, errors.New("Java truststore path is empty")
	}
	temporary := keystore + ".import"
	copyCommand := Argv{Name: "keytool", Args: []string{
		"-importkeystore", "-noprompt",
		"-srckeystore", cacerts,
		"-srcstorepass", "changeit",
		"-destkeystore", temporary,
		"-deststoretype", "PKCS12",
		"-deststorepass", "changeit",
	}}
	importArgs, err := KeytoolArgs(p, temporary)
	if err != nil {
		return nil, err
	}
	return []Argv{copyCommand, {Name: "keytool", Args: importArgs}}, nil
}

// ImportJavaCA builds the truststore and replaces the old one only after every
// keytool step succeeds. No JDK file is modified.
func ImportJavaCA(p Proxy, keystore string) ([]Argv, error) {
	plan, err := JavaTruststorePlan(p, keystore)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keystore), 0o700); err != nil {
		return plan.Commands, err
	}
	temporary := keystore + ".import"
	_ = os.Remove(temporary)
	for _, command := range plan.Commands {
		out, err := exec.Command(plan.Keytool, command.Args...).CombinedOutput()
		if err != nil {
			_ = os.Remove(temporary)
			line := command.String(runtime.GOOS == "windows")
			if msg := strings.TrimSpace(string(out)); msg != "" {
				return plan.Commands, fmt.Errorf("%s: %s", line, firstLine(msg))
			}
			return plan.Commands, fmt.Errorf("%s: %w", line, err)
		}
	}
	if err := replaceFile(temporary, keystore); err != nil {
		_ = os.Remove(temporary)
		return plan.Commands, err
	}
	return plan.Commands, nil
}

// PrepareFirefoxProfile creates a temporary profile with user.js and, where
// NSS tools are meaningful, adds the CA to its own NSS store. A missing
// certutil is returned as a warning, not a failure: Firefox still opens, and
// the caller can explain that trust is not set up in that profile.
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
	if !nssSupported(runtime.GOOS) {
		return nil, nil
	}
	if err := trustNSS(profile, p.CAPath); err != nil {
		return err, nil
	}
	return nil, nil
}

// nssSupported reports whether a certutil found on this system is the NSS tool.
// Windows ships an unrelated certutil in System32; Firefox there uses the
// Windows root store through enterprise roots instead.
func nssSupported(goos string) bool { return goos != "windows" }

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
