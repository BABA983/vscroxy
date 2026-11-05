//go:build !linux && !darwin && !windows

package clientsetup

// OpenTerminal is unavailable on this platform.
func OpenTerminal(Proxy) (Argv, error) { return Argv{}, ErrUnsupported }

// OpenChrome is unavailable on this platform.
func OpenChrome(Proxy, string) (Argv, error) { return Argv{}, ErrUnsupported }

// OpenFirefox is unavailable on this platform.
func OpenFirefox(string) (Argv, error) { return Argv{}, ErrUnsupported }

// OpenElectron is unavailable on this platform.
func OpenElectron(string, Proxy) (Argv, error) { return Argv{}, ErrUnsupported }
