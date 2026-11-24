//go:build windows

package engine_test

import (
	"os/exec"
	"strings"
)

// processAlive 用 tasklist 探测 pid 是否存在（Windows 无 kill -0）。
func processAlive(_ interface{ Helper() }, pid string) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/NH").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), pid)
}
