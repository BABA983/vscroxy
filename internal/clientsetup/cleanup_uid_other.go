//go:build !unix && !windows

package clientsetup

import "os"

func platformOwnedByCurrentUser(os.FileInfo) bool { return true }
