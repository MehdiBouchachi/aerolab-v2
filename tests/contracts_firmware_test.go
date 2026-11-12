package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

// The firmware's C encoders must emit messages the Go contract validators
// accept. The C host tests write their output; this test validates it.
func TestFirmwareEncodedMessagesSatisfyTheGoContract(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not installed")
	}
	root := ".."
	host := filepath.Join(root, "firmware", "esp32", "test", "host")
	out, err := exec.Command("make", "-C", host, "test").CombinedOutput()
	if err != nil {
		t.Fatalf("firmware host tests failed:\n%s", out)
	}
	files, _ := filepath.Glob(filepath.Join(host, "build", "out", "*.json"))
	if len(files) < 8 {
		t.Fatalf("expected the firmware to emit its sample messages, got %d files", len(files))
	}
	for _, f := range files {
		kind := strings.SplitN(filepath.Base(f), ".", 2)[0]
		raw, _ := os.ReadFile(f)
		if err := mc.ValidatePayload(kind, raw); err != nil {
			t.Errorf("%s: firmware output rejected by the Go validator: %v\n%s", filepath.Base(f), err, raw)
		}
	}
}
