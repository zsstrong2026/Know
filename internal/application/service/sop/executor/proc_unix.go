//go:build !windows

package executor

import (
	"os/exec"
	"syscall"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor/proc"
)

// shellBinary 是脚本解释器：POSIX 环境直接用 bash。
func shellBinary() string { return "bash" }

// shellArgs 以 `bash -c <script>` 方式执行一整段脚本。
func shellArgs(script string) []string { return []string{"-c", script} }

// attachProcessGroup 让子进程自成一组（Setpgid）。
func attachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 委托公共进程治理（kill(-pgid)，公共包内实现）。
func killProcessGroup(cmd *exec.Cmd) { _ = proc.KillProcessTree(cmd) }
