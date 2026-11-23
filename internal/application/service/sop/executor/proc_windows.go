//go:build windows

package executor

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// shellBinary 是脚本解释器：Windows 上用 Git Bash（仓库与文档都以它为准）。
func shellBinary() string { return "bash" }

// shellArgs 以 `bash -c <script>` 方式执行一整段脚本。
func shellArgs(script string) []string { return []string{"-c", script} }

// attachProcessGroup 让子进程自成一组：超时/取消时按组杀，孤儿孙进程不会继续吃机器。
//
// CREATE_NEW_PROCESS_GROUP 只建立分组；真正结束整棵树由 killProcessGroup 的
// taskkill /T 完成（Go 的 Process.Kill 只杀直接子进程）。
func attachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup 结束整棵进程树。
//
// 先 taskkill（带 /T 才管孙进程）；失败或超时再退化为直接 Kill，至少保证本进程不做
// 悬挂的僵尸。taskkill 由系统提供，不存在「没装工具」的分支。
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	done := make(chan error, 1)
	if err := kill.Start(); err == nil {
		go func() { done <- kill.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = kill.Process.Kill()
		}
	}
	_ = cmd.Process.Kill()
}
