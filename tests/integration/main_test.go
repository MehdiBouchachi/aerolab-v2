package integration

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Stop()
	os.Exit(code)
}
