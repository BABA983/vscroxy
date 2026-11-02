//go:build unix

package control

import (
	"os"
	"syscall"
)

func createPrivateTemp(dir string) (*os.File, error) {
	f, err := os.CreateTemp(dir, ".control-*")
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

func privateFile(_ *os.File, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0o077 == 0
}
