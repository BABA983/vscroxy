package clientsetup

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// AndroidDevice is one entry from adb devices -l.
type AndroidDevice struct {
	Serial string
	State  string
	Model  string
}

// Label is the device as a picker spells it.
func (d AndroidDevice) Label() string {
	switch {
	case d.Model != "" && d.State != "":
		return d.Serial + " · " + d.Model + " · " + d.State
	case d.Model != "":
		return d.Serial + " · " + d.Model
	case d.State != "":
		return d.Serial + " · " + d.State
	default:
		return d.Serial
	}
}

// ParseAndroidDevices reads adb devices -l. Lines for unauthorized or offline
// devices are kept so the picker can explain why they cannot be configured.
func ParseAndroidDevices(output string) []AndroidDevice {
	var devices []AndroidDevice
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !androidState(fields[1]) {
			continue // a daemon or version line, not a device row
		}
		device := AndroidDevice{Serial: fields[0], State: fields[1]}
		for _, field := range fields[2:] {
			if model, ok := strings.CutPrefix(field, "model:"); ok {
				device.Model = model
			}
		}
		devices = append(devices, device)
	}
	return devices
}

func androidState(state string) bool {
	switch state {
	case "device", "offline", "unauthorized", "recovery", "sideload", "bootloader", "host", "no":
		return true
	default:
		return false
	}
}

// AndroidDevices lists what adb currently sees.
func AndroidDevices() ([]AndroidDevice, error) {
	out, err := RunOutput(Argv{Name: "adb", Args: []string{"devices", "-l"}})
	if err != nil {
		return nil, err
	}
	return ParseAndroidDevices(string(out)), nil
}

// AndroidProxyQueryCommand reads the device's current global proxy so undo can
// restore it instead of assuming there was none.
func AndroidProxyQueryCommand(serial string) Argv {
	return adb(serial, "shell", "settings", "get", "global", "http_proxy")
}

// AndroidProxyCommands points the device's global HTTP proxy at the host
// through adb reverse, so 127.0.0.1 inside the device reaches this computer.
// Both commands are needed: the reverse listener first, then the setting.
func AndroidProxyCommands(serial string, port int) ([]Argv, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("proxy port %d is outside 1–65535", port)
	}
	p := strconv.Itoa(port)
	return []Argv{
		adb(serial, "reverse", "tcp:"+p, "tcp:"+p),
		adb(serial, "shell", "settings", "put", "global", "http_proxy", "127.0.0.1:"+p),
	}, nil
}

// AndroidUndoCommands restore the proxy value that was present before the app
// changed it, then remove the reverse listener it added. An empty or "null"
// previous value clears the setting.
func AndroidUndoCommands(serial string, port int, previous string) ([]Argv, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("proxy port %d is outside 1–65535", port)
	}
	p := strconv.Itoa(port)
	restore := strings.TrimSpace(previous)
	if restore == "" {
		restore = ":0" // no record: clear rather than guess
	}
	if !validAndroidProxy(restore) {
		return nil, fmt.Errorf("unexpected Android proxy value %q; refusing to pass it to the device shell", previous)
	}
	if strings.EqualFold(restore, "null") {
		restore = ":0"
	}
	return []Argv{
		adb(serial, "shell", "settings", "put", "global", "http_proxy", restore),
		adb(serial, "reverse", "--remove", "tcp:"+p),
	}, nil
}

// validAndroidProxy accepts only the values Android's settings command returns
// for an unset or configured proxy. The value is passed to the device shell, so
// hostnames are restricted to letters, digits, dots and hyphens.
func validAndroidProxy(value string) bool {
	value = strings.TrimSpace(value)
	if value == "null" || value == ":0" {
		return true
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return false
	}
	if net.ParseIP(host) == nil {
		for _, r := range host {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
				return false
			}
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// AndroidCACopyCommand copies only the public CA to the device's Downloads
// folder. Installing it is a device-side confirmation; see the guide.
func AndroidCACopyCommand(serial, caPath string) (Argv, error) {
	if strings.TrimSpace(caPath) == "" {
		return Argv{}, fmt.Errorf("CA path is empty")
	}
	return adb(serial, "push", caPath, "/sdcard/Download/vscroxy-ca.crt"), nil
}

// ApplyAndroidProxy reads the previous proxy, then runs the reverse and proxy
// commands in order. The previous value is returned so undo can restore it.
func ApplyAndroidProxy(serial string, port int) (string, error) {
	out, err := RunOutput(AndroidProxyQueryCommand(serial))
	if err != nil {
		return "", err
	}
	previous := strings.TrimSpace(string(out))
	if !validAndroidProxy(previous) {
		return "", fmt.Errorf("Android returned an unexpected http_proxy value %q; refusing to change it", previous)
	}
	commands, err := AndroidProxyCommands(serial, port)
	if err != nil {
		return previous, err
	}
	return previous, runAll(commands)
}

// ClearAndroidProxy restores the previous proxy and removes the reverse
// listener.
func ClearAndroidProxy(serial string, port int, previous string) error {
	commands, err := AndroidUndoCommands(serial, port, previous)
	if err != nil {
		return err
	}
	return runAll(commands)
}

// PushAndroidCA copies the public CA to Downloads.
func PushAndroidCA(serial, caPath string) error {
	command, err := AndroidCACopyCommand(serial, caPath)
	if err != nil {
		return err
	}
	return command.Run()
}

func runAll(commands []Argv) error {
	for _, command := range commands {
		if err := command.Run(); err != nil {
			return err
		}
	}
	return nil
}

func adb(serial string, args ...string) Argv {
	full := make([]string, 0, len(args)+2)
	if serial != "" {
		full = append(full, "-s", serial)
	}
	full = append(full, args...)
	return Argv{Name: "adb", Args: full}
}
