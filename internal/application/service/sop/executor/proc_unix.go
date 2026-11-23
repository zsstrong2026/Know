//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

// shellBinary 是脚本解释器：POSIX 环境直接用 bash。
func shellBinary() string { return "bash" }

// shellArgs 以 `bash -c <script>` 方式执行一整段脚本。
func shellArgs(script string) []string { return []string{"-c", script} }

// attachProcessGroup 让子进程自成一组（Setpgid）：超时按组杀，子孙进程一起走。
func attachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 杀整个进程组（负 pid 即组号）。
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
