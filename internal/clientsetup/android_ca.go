package clientsetup

import (
	"crypto/md5"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
)

// AndroidCAPath is where the public CA waits on the device. The shell user can
// write it; only root can copy it into a system store.
const AndroidCAPath = "/data/local/tmp/vscroxy-ca.pem"

// androidCABackup keeps the store's own certificates while a tmpfs replaces
// them, so the device keeps trusting every CA it shipped with.
const androidCABackup = "/data/local/tmp/vscroxy-ca-backup"

// AndroidSystemStore is the directory where Android keeps the CAs that apps
// trust. Android 14 moved it into the Conscrypt APEX.
type AndroidSystemStore string

const (
	// AndroidSystemStoreApex is the Conscrypt APEX store, used from Android 14.
	// It is mounted read-only, so the app mounts a tmpfs over it in the zygote
	// mount namespaces; a reboot brings the original store back.
	AndroidSystemStoreApex AndroidSystemStore = "/apex/com.android.conscrypt/cacerts"
	// AndroidSystemStoreSystem is the system image's store, used through Android
	// 13. Writing to it needs a writable system partition.
	AndroidSystemStoreSystem AndroidSystemStore = "/system/etc/security/cacerts"
)

// AndroidRoot is how adb reaches a root shell.
type AndroidRoot int

const (
	// AndroidRootAdb is an emulator or a userdebug build, where adb root works.
	AndroidRootAdb AndroidRoot = iota
	// AndroidRootSu is a production build with a root manager: shell commands
	// run through su -c.
	AndroidRootSu
)

// AndroidRootCommands ask adbd to restart as root. A production build refuses;
// AndroidRootMode then tries su.
func AndroidRootCommands(serial string) []Argv {
	return []Argv{adb(serial, "root"), adb(serial, "wait-for-device")}
}

// AndroidRootMode finds a root shell: adbd itself, then su.
func AndroidRootMode(serial string) (AndroidRoot, error) {
	for _, command := range AndroidRootCommands(serial) {
		_ = command.Run() // "cannot run as root in production builds" is expected
	}
	if out, err := RunOutput(adb(serial, "shell", "id -u")); err == nil && strings.TrimSpace(string(out)) == "0" {
		return AndroidRootAdb, nil
	}
	if out, err := RunOutput(adb(serial, "shell", "su -c 'id -u'")); err == nil && strings.TrimSpace(string(out)) == "0" {
		return AndroidRootSu, nil
	}
	return AndroidRootAdb, errors.New("installing a system CA needs root: use an emulator with a Google APIs image (not Play Store), or a rooted device")
}

// AndroidShellCommand runs one shell script on the device, through su when adbd
// cannot be root. The script is one argument, so the device shell expands its
// globs and pipes.
func AndroidShellCommand(serial string, root AndroidRoot, script string) Argv {
	if root == AndroidRootSu {
		return adb(serial, "shell", "su -c "+POSIXQuote(script))
	}
	return adb(serial, "shell", script)
}

// AndroidStoreCommand reports which system store the device reads.
func AndroidStoreCommand(serial string) Argv {
	return adb(serial, "shell", "if ls "+string(AndroidSystemStoreApex)+"/*.0 >/dev/null 2>&1; then echo apex; else echo system; fi")
}

// ParseAndroidStore reads AndroidStoreCommand's answer.
func ParseAndroidStore(out string) (AndroidSystemStore, error) {
	switch strings.TrimSpace(out) {
	case "apex":
		return AndroidSystemStoreApex, nil
	case "system":
		return AndroidSystemStoreSystem, nil
	}
	return "", fmt.Errorf("unexpected Android certificate store %q", strings.TrimSpace(out))
}

// AndroidZygoteCommand lists the processes whose mount namespaces apps inherit.
// A mount made in them is visible to every app started afterwards.
func AndroidZygoteCommand(serial string) Argv {
	return adb(serial, "shell", "pidof zygote64 zygote")
}

// ParseAndroidPids reads a pidof answer.
func ParseAndroidPids(out string) []int {
	var pids []int
	for field := range strings.FieldsSeq(out) {
		if pid, err := strconv.Atoi(field); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// AndroidPushCACommand copies the public CA to AndroidCAPath.
func AndroidPushCACommand(serial, caPath string) (Argv, error) {
	if strings.TrimSpace(caPath) == "" {
		return Argv{}, errors.New("CA path is empty")
	}
	return adb(serial, "push", caPath, AndroidCAPath), nil
}

// AndroidRemountCommand asks adb for a writable system partition. It needs an
// emulator started with -writable-system, or a rooted device with an unlocked
// bootloader.
func AndroidRemountCommand(serial string) Argv {
	return adb(serial, "remount")
}

// AndroidSystemCACommands install the CA in the writable system store used
// through Android 13.
func AndroidSystemCACommands(serial string, root AndroidRoot, name string) ([]Argv, error) {
	if err := checkAndroidCAName(name); err != nil {
		return nil, err
	}
	destination := string(AndroidSystemStoreSystem) + "/" + name
	return []Argv{
		AndroidShellCommand(serial, root, "cp "+AndroidCAPath+" "+destination),
		AndroidShellCommand(serial, root, "chmod 644 "+destination),
		AndroidShellCommand(serial, root, "chown root:root "+destination),
		AndroidShellCommand(serial, root, "chcon u:object_r:system_file:s0 "+destination),
	}, nil
}

// AndroidAPEXCACommands install the CA in the Conscrypt APEX store. The store
// is read-only, so each zygote mount namespace gets a tmpfs holding the store's
// own certificates plus the app's CA. Apps started after this trust it until
// the device reboots; apps that are already running keep their old namespace
// and must be restarted.
func AndroidAPEXCACommands(serial string, root AndroidRoot, name string, zygotes []int) ([]Argv, error) {
	if err := checkAndroidCAName(name); err != nil {
		return nil, err
	}
	if len(zygotes) == 0 {
		return nil, errors.New("no zygote process found; the device may still be booting")
	}
	store := string(AndroidSystemStoreApex)
	commands := []Argv{
		AndroidShellCommand(serial, root, "mkdir -p "+androidCABackup),
	}
	for _, pid := range zygotes {
		ns := "nsenter -t " + strconv.Itoa(pid) + " -m -- "
		mount := "if grep -q \"^tmpfs " + store + " \" /proc/mounts; then umount " + store + "; fi; mount -t tmpfs tmpfs " + store
		// The glob in the last script must expand inside the zygote namespace,
		// where the new file exists; the same glob outside it would only match
		// the store's own certificates and leave the CA with the tmpfs label.
		label := "chown root:root " + store + "/* && chmod 644 " + store + "/* && chcon u:object_r:system_file:s0 " + store + "/*"
		commands = append(commands,
			AndroidShellCommand(serial, root, ns+"cp "+store+"/* "+androidCABackup+"/"),
			AndroidShellCommand(serial, root, ns+"sh -c "+POSIXQuote(mount)),
			AndroidShellCommand(serial, root, ns+"cp "+androidCABackup+"/* "+store+"/"),
			AndroidShellCommand(serial, root, ns+"cp "+AndroidCAPath+" "+store+"/"+name),
			AndroidShellCommand(serial, root, ns+"chmod 755 "+store),
			AndroidShellCommand(serial, root, ns+"chcon u:object_r:system_file:s0 "+store),
			AndroidShellCommand(serial, root, ns+"sh -c "+POSIXQuote(label)),
		)
	}
	return commands, nil
}

// AndroidCAVerifyCommands check that the installed file is in the store the
// apps read. APEX stores are verified inside each zygote namespace, because
// that is where the tmpfs lives.
func AndroidCAVerifyCommands(serial string, store AndroidSystemStore, name string, zygotes []int) ([]Argv, error) {
	if err := checkAndroidCAName(name); err != nil {
		return nil, err
	}
	switch store {
	case AndroidSystemStoreApex:
		if len(zygotes) == 0 {
			return nil, errors.New("no zygote process found; the device may still be booting")
		}
		commands := make([]Argv, 0, len(zygotes))
		for _, pid := range zygotes {
			// The label decides whether an app can read the file, so a store
			// that holds it with the tmpfs label is not a successful install.
			check := "ls -Zd " + string(store) + " | grep -q u:object_r:system_file:s0 && ls -Z " + string(store) + "/" + name + " | grep -q u:object_r:system_file:s0"
			commands = append(commands, adb(serial, "shell", "nsenter -t "+strconv.Itoa(pid)+" -m -- sh -c "+POSIXQuote(check)))
		}
		return commands, nil
	case AndroidSystemStoreSystem:
		return []Argv{adb(serial, "shell", "ls "+string(store)+"/"+name)}, nil
	}
	return nil, fmt.Errorf("unexpected Android certificate store %q", store)
}

// AndroidUnmountCACommands put the original read-only store back in every
// zygote namespace. InstallAndroidSystemCA runs them when a step fails after the
// tmpfs was mounted, so a half-written store does not hide every system CA.
// They unmount only a tmpfs, never the APEX mount itself.
func AndroidUnmountCACommands(serial string, root AndroidRoot, zygotes []int) []Argv {
	store := string(AndroidSystemStoreApex)
	commands := make([]Argv, 0, len(zygotes))
	for _, pid := range zygotes {
		unmount := "if grep -q \"^tmpfs " + store + " \" /proc/mounts; then umount " + store + "; fi"
		commands = append(commands, AndroidShellCommand(serial, root, "nsenter -t "+strconv.Itoa(pid)+" -m -- sh -c "+POSIXQuote(unmount)))
	}
	return commands
}

// AndroidCAInstall records what InstallAndroidSystemCA did.
type AndroidCAInstall struct {
	Store    AndroidSystemStore
	Name     string
	Commands []Argv
}

// InstallAndroidSystemCA installs the public CA at caPath as a system
// certificate. Apps started afterwards trust it; Chrome 115 and later uses the
// Chrome Root Store on Android and is not affected. The install lasts until the
// device reboots, and already-running apps must be restarted.
func InstallAndroidSystemCA(serial, caPath string) (AndroidCAInstall, error) {
	install := AndroidCAInstall{}
	certPEM, err := os.ReadFile(caPath)
	if err != nil {
		return install, fmt.Errorf("CA file: %w", err)
	}
	name, err := AndroidCertFileName(certPEM)
	if err != nil {
		return install, err
	}
	install.Name = name
	root, err := AndroidRootMode(serial)
	if err != nil {
		return install, err
	}
	out, err := RunOutput(AndroidStoreCommand(serial))
	if err != nil {
		return install, err
	}
	store, err := ParseAndroidStore(string(out))
	if err != nil {
		return install, err
	}
	install.Store = store
	push, err := AndroidPushCACommand(serial, caPath)
	if err != nil {
		return install, err
	}
	if err := push.Run(); err != nil {
		return install, err
	}
	if store == AndroidSystemStoreApex {
		install.Commands, err = installAndroidAPEXCA(serial, root, name)
		return install, err
	}
	install.Commands, err = installAndroidSystemCA(serial, root, name)
	return install, err
}

func installAndroidAPEXCA(serial string, root AndroidRoot, name string) ([]Argv, error) {
	out, err := RunOutput(AndroidZygoteCommand(serial))
	if err != nil {
		return nil, err
	}
	zygotes := ParseAndroidPids(string(out))
	commands, err := AndroidAPEXCACommands(serial, root, name, zygotes)
	if err != nil {
		return commands, err
	}
	for _, command := range commands {
		if err := command.Run(); err != nil {
			restore, err := restoreAndroidAPEX(serial, root, zygotes, err)
			return append(commands, restore...), err
		}
	}
	verify, err := AndroidCAVerifyCommands(serial, AndroidSystemStoreApex, name, zygotes)
	if err != nil {
		return commands, err
	}
	commands = append(commands, verify...)
	for _, command := range verify {
		if err := command.Run(); err != nil {
			restore, err := restoreAndroidAPEX(serial, root, zygotes, fmt.Errorf("the CA is not in the store apps read: %w", err))
			return append(commands, restore...), err
		}
	}
	if err := checkAndroidCACount(serial, zygotes); err != nil {
		restore, err := restoreAndroidAPEX(serial, root, zygotes, err)
		return append(commands, restore...), err
	}
	return commands, nil
}

// restoreAndroidAPEX unmounts the tmpfs a failed install left behind, so the
// device keeps the store it shipped with. Only a tmpfs is unmounted; the APEX
// mount itself is never touched.
func restoreAndroidAPEX(serial string, root AndroidRoot, zygotes []int, cause error) ([]Argv, error) {
	restore := AndroidUnmountCACommands(serial, root, zygotes)
	runAll(restore) // best effort: the device may already be gone
	return restore, fmt.Errorf("%w; the original certificate store is back", cause)
}

func installAndroidSystemCA(serial string, root AndroidRoot, name string) ([]Argv, error) {
	remount := AndroidRemountCommand(serial)
	commands := []Argv{remount}
	if err := remount.Run(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "reboot") {
			return commands, errors.New("the device needs a reboot before its system partition can be written; reboot it and install again")
		}
		return commands, err
	}
	install, err := AndroidSystemCACommands(serial, root, name)
	if err != nil {
		return commands, err
	}
	commands = append(commands, install...)
	if err := runAll(install); err != nil {
		return commands, err
	}
	verify, err := AndroidCAVerifyCommands(serial, AndroidSystemStoreSystem, name, nil)
	if err != nil {
		return commands, err
	}
	commands = append(commands, verify...)
	return commands, runAll(verify)
}

// checkAndroidCACount makes sure every zygote namespace sees at least the
// certificates the device had, so a partial copy cannot silently remove system
// trust.
func checkAndroidCACount(serial string, zygotes []int) error {
	backup, err := RunOutput(adb(serial, "shell", "ls "+androidCABackup+" | wc -l"))
	if err != nil {
		return err
	}
	saved, err := strconv.Atoi(strings.TrimSpace(string(backup)))
	if err != nil || saved == 0 {
		return fmt.Errorf("the certificate backup holds %q files; unmounting restores the original store", strings.TrimSpace(string(backup)))
	}
	for _, pid := range zygotes {
		out, err := RunOutput(androidCACountCommand(serial, pid))
		if err != nil {
			return err
		}
		installed, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil || installed < saved {
			return fmt.Errorf("zygote %d sees %q certificates after installing, fewer than the %d it had; unmounting restores the original store", pid, strings.TrimSpace(string(out)), saved)
		}
	}
	return nil
}

func androidCACountCommand(serial string, pid int) Argv {
	script := "ls " + string(AndroidSystemStoreApex) + " | wc -l"
	return adb(serial, "shell", "nsenter -t "+strconv.Itoa(pid)+" -m -- sh -c "+POSIXQuote(script))
}

// checkAndroidCAName rejects a store file name that is not the eight hex digits
// and ".0" AndroidCertFileName produces, because the name reaches the device
// shell.
func checkAndroidCAName(name string) error {
	if len(name) != 10 || !strings.HasSuffix(name, ".0") {
		return fmt.Errorf("unexpected Android certificate name %q", name)
	}
	for _, r := range name[:8] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return fmt.Errorf("unexpected Android certificate name %q", name)
		}
	}
	return nil
}

// AndroidCertFileName is the name Android's system store needs for a PEM
// certificate: OpenSSL's subject_hash_old, the first four bytes of the MD5 of
// the DER subject in little-endian order, as eight hex digits and ".0". Android
// still looks certificates up by that old hash, not by the newer SHA-1 one. The
// MD5 is the file name, not a security decision.
func AndroidCertFileName(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("the CA file does not contain a PEM certificate")
	}
	var certificate struct {
		TBS       asn1.RawValue
		Algorithm asn1.RawValue
		Signature asn1.BitString
	}
	if _, err := asn1.Unmarshal(block.Bytes, &certificate); err != nil {
		return "", fmt.Errorf("parse the CA certificate: %w", err)
	}
	var tbs struct {
		Version         int `asn1:"optional,explicit,default:0,tag:0"`
		SerialNumber    *big.Int
		Signature       pkix.AlgorithmIdentifier
		Issuer          asn1.RawValue
		Validity        asn1.RawValue
		Subject         asn1.RawValue
		PublicKey       asn1.RawValue
		IssuerUniqueID  asn1.BitString   `asn1:"optional,tag:1"`
		SubjectUniqueID asn1.BitString   `asn1:"optional,tag:2"`
		Extensions      []pkix.Extension `asn1:"optional,explicit,tag:3"`
	}
	if _, err := asn1.Unmarshal(certificate.TBS.FullBytes, &tbs); err != nil {
		return "", fmt.Errorf("parse the CA certificate: %w", err)
	}
	sum := md5.Sum(tbs.Subject.FullBytes)
	hash := uint32(sum[0]) | uint32(sum[1])<<8 | uint32(sum[2])<<16 | uint32(sum[3])<<24
	return fmt.Sprintf("%08x.0", hash), nil
}

// AndroidNetworkSecurityConfig is a snippet for the user's own debug builds. It
// trusts the app's CA from res/raw/vscroxy_ca.pem and the user store, and only
// while the app is debuggable, so release builds keep the system roots alone.
func AndroidNetworkSecurityConfig() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<network-security-config>
    <debug-overrides>
        <trust-anchors>
            <certificates src="@raw/vscroxy_ca" />
            <certificates src="user" />
        </trust-anchors>
    </debug-overrides>
</network-security-config>
`
}
