package mcpserver_test

import (
	"testing"

	"github.com/baba983/vscroxy/internal/leakcheck"
)

func TestMain(m *testing.M) { leakcheck.Run(m) }
