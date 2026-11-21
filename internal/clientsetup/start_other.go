//go:build !windows

package clientsetup

// startDetached launches a long-lived client and reaps it in the background.
// NewConsole is a Windows concern; other platforms start their terminal through
// a launcher that owns the window.
func startDetached(c Argv, bin string) error {
	cmd := c.command(bin)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
