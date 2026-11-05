//go:build darwin

package clientsetup

import (
	"os/exec"
	"strings"
)

// platformJavaHome resolves the JDK selected by /usr/libexec/java_home. A
// Finder-launched app usually has no JAVA_HOME, and /usr/bin/keytool is a stub
// rather than a symlink into a JDK.
func platformJavaHome() string {
	out, err := exec.Command("/usr/libexec/java_home").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
