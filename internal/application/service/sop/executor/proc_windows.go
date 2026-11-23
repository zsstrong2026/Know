//go:build windows

package executor

import (
	"os/exec"
	"syscall"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor/proc"
)

// shellBinary 是脚本解释器：Windows 上用 Git Bash（仓库与文档都以它为准）。
func shellBinary() string { return "bash" }

// shellArgs 以 `bash -c <script>` 方式执行一整段脚本。
func shellArgs(script string) []string { return []string{"-c", script} }

// attachProcessGroup 让子进程自成一组（杀树时按组收）。
func attachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup 委托公共进程治理（taskkill /T，公共包内实现）。
func killProcessGroup(cmd *exec.Cmd) { _ = proc.KillProcessTree(cmd) }
