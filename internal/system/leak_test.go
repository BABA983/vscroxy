//go:build !linux

package system

import (
	"testing"

	"github.com/baba983/vscroxy/internal/leakcheck"
)

func TestMain(m *testing.M) { leakcheck.Run(m) }
