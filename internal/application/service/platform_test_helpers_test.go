package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// skipIfNoSymlinkPrivilege skips the caller when the OS refuses to create a
// symlink. Windows requires SeCreateSymbolicLinkPrivilege (admin or Developer
// Mode); without it os.Symlink fails with "A required privilege is not held
// by the client" and the test would fail for a reason unrelated to the code
// under test.
func skipIfNoSymlinkPrivilege(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	dir := t.TempDir()
	if err := os.Symlink(dir, filepath.Join(dir, "probe")); err != nil {
		t.Skipf("symlink unavailable on this Windows host (需要管理员或开发者模式): %v", err)
	}
}

// skipIfNoPOSIXPermissions skips the caller when the OS has no POSIX file
// permission model: chmod 000 does not make a file unreadable on Windows, and
// a freshly created file reports 0666 rather than the 0644/0755 the code sets.
func skipIfNoPOSIXPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 POSIX 权限位：chmod 000 仍然可读，文件模式也不按 0755 上报")
	}
}

// skipIfNoPOSIXShell skips the caller when the OS cannot run the POSIX shell
// tooling these cases rely on: GNU tar available on PATH mis-handles Windows
// drive paths (`-C C:\...` is read as a remote host), and #! scripts are not
// directly executable without a launcher.
func skipIfNoPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 缺少 POSIX shell 语义：tar 不接受盘符路径、#! 脚本不能直接执行")
	}
}
