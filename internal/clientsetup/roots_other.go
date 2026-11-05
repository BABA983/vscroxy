//go:build !linux && !darwin && !windows

package clientsetup

func platformSystemRoots() ([]byte, error) { return nil, ErrNoSystemRoots }
