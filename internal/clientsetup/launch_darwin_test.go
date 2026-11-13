//go:build darwin

package clientsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macOS opens the script with /usr/bin/open -a Terminal, and the script is
// left on disk for it to run. Nothing here launches a window.
func TestOpenTerminalBuildsTheOpenCommand(t *testing.T) {
	argv, err := OpenTerminal(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	if argv.Name != "/usr/bin/open" || len(argv.Args) != 3 || argv.Args[0] != "-a" || argv.Args[1] != "Terminal" {
		t.Fatalf("command = %q %q, want /usr/bin/open -a Terminal <script>", argv.Name, argv.Args)
	}
	script := argv.Args[2]
	if !strings.HasSuffix(script, "terminal.command") {
		t.Fatalf("script = %q, want a terminal.command", script)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("script was not written: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Fatalf("script %q is not executable", script)
	}
	t.Cleanup(func() { RemoveTerminalScript(script) })
	if filepath.Dir(script) == "" {
		t.Fatal("script has no directory")
	}
}
