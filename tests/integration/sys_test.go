package integration

import (
	"os"
	"runtime"
	"strings"
)

func runtimeNumCPU() int { return runtime.NumCPU() }

func runtimeVer() string {
	b, _ := os.ReadFile("/proc/cpuinfo")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "model name") {
			return strings.TrimSpace(strings.SplitN(l, ":", 2)[1]) + ", " + runtime.GOARCH
		}
	}
	return runtime.GOARCH
}
