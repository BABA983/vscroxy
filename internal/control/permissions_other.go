//go:build !unix && !windows

package control

import (
	"errors"
	"os"
)

func createPrivateTemp(string) (*os.File, error) {
	return nil, errors.New("private discovery files are unsupported on this platform")
}

func privateFile(*os.File, os.FileInfo) bool { return false }
