//go:build windows

package proc

import (
	"os/exec"
	"sort"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sortStrings 让环境变量的追加顺序稳定（同一份配置每次执行的字节一致）。
func sortStrings(keys []string) { sort.Strings(keys) }

// attachGroup 让子进程自成一组（CREATE_NEW_PROCESS_GROUP），配合 taskkill /T 杀树。
func attachGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		// HideWindow：CLI 是后台进程，弹黑窗会让运维以为中了病毒。
		HideWindow: true,
	}
}

// attachJob 创建作业对象并挂上当前进程，返回作业句柄（失败返回 0，退化为 taskkill）。
//
// KILL_ON_JOB_CLOSE：句柄一关（进程退出/崩溃/被杀）作业里的所有进程一起结束，
// 这是 Windows 上没有「进程组信号」的等价物。JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK
// 关掉的话，子进程自己再拉起的进程也会被留在作业里——正是我们要的。
func attachJob(cmd *exec.Cmd) uintptr {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	return uintptr(job)
}

// killTree 先终止作业（连带所有孙进程），失败再 taskkill /T，最后兜底直接 Kill。
func killTree(cmd *exec.Cmd, pid int, job uintptr) error {
	var firstErr error
	if job != 0 {
		if err := windows.TerminateJobObject(windows.Handle(job), 1); err == nil {
			return nil
		} else {
			firstErr = err
		}
	}
	// taskkill 由系统提供，带上 /T 才管孙进程。
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	done := make(chan error, 1)
	if err := kill.Start(); err == nil {
		go func() { done <- kill.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				return nil
			}
			firstErr = err
		case <-time.After(5 * time.Second):
			_ = kill.Process.Kill()
		}
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return firstErr
}

// releaseJob 关闭作业句柄（KILL_ON_JOB_CLOSE 生效，残留进程随之结束）。
func releaseJob(job uintptr) {
	if job != 0 {
		_ = windows.CloseHandle(windows.Handle(job))
	}
}
