//go:build !windows

package proc

import (
	"os/exec"
	"sort"
	"syscall"
)

// sortStrings 让环境变量的追加顺序稳定。
func sortStrings(keys []string) { sort.Strings(keys) }

// attachGroup 让子进程自成一组（Setpgid），kill(-pgid) 才能连坐孙进程。
func attachGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// attachJob POSIX 没有作业对象，进程组已足够（返回 0）。
func attachJob(*exec.Cmd) uintptr { return 0 }

// killTree 杀整个进程组（负 pid 即组号）；失败再兜底杀直接子进程。
func killTree(cmd *exec.Cmd, pid int, _ uintptr) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == nil {
		return nil
	}
	if cmd.Process != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			return err
		}
		return nil
	}
	return err
}

// releaseJob POSIX 无需释放。
func releaseJob(uintptr) {}
