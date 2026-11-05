//go:build !darwin

package clientsetup

// AddSimulatorCA is available only on macOS, where the iOS simulator runs.
func AddSimulatorCA(string) (Argv, error) { return Argv{}, ErrUnsupported }
