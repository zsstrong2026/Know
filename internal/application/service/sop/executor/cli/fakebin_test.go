package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestFakeBinaryLookPath 验证「假 CLI 二进制 + Windows .cmd 包装」这一测试基线成立：
// CLIProvider.Prepare 依赖 exec.LookPath(BinaryName())，Windows 上裸 bash 脚本不带扩展名
// 找不到，必须额外写 zcode.cmd 包装（E2E 的假 zcode 同款做法）。
func TestFakeBinaryLookPath(t *testing.T) {
	binDir := t.TempDir()
	writeFakeBinary(t, binDir, "zcode")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	path, err := exec.LookPath("zcode")
	require.NoError(t, err, "假二进制应能被 LookPath 找到")
	require.NotEmpty(t, path)

	provider := &CLIProvider{A: ZcodeAdapter{}}
	require.NoError(t, provider.Prepare(context.Background(), &types.SopNode{ID: "n1"}, t.TempDir()))
}

// TestMissingBinaryGivesPlainError 未安装时必须给白话指引，而不是等运行时报 exec 错误。
func TestMissingBinaryGivesPlainError(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // 空 PATH：一定找不到
	provider := &CLIProvider{A: ZcodeAdapter{}}
	err := provider.Prepare(context.Background(), &types.SopNode{ID: "n1"}, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "未检测到 zcode 命令")
	require.Contains(t, err.Error(), "设置页")
}

// writeFakeBinary 写一个可被 LookPath 命中的假可执行文件（Windows 附 .cmd 包装）。
func writeFakeBinary(t *testing.T, dir, name string) {
	t.Helper()
	body := "#!/usr/bin/env bash\necho '{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"ok\"}'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755))
	if runtime.GOOS == "windows" {
		wrapper := "@bash \"%~dp0" + name + "\" %*\r\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".cmd"), []byte(wrapper), 0o644))
	}
}

// TestCaptureSwitchWritesRawFrames 录制开关非空时，原始帧逐行落 <dir>/<kind>.jsonl。
func TestCaptureSwitchWritesRawFrames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureEnvDir, dir)

	writer, closeFn := openCaptureFile("zcode")
	require.NotNil(t, writer)
	raw := "{\"type\":\"result\",\"subtype\":\"success\"}\n"
	_, err := writer.Write([]byte(raw))
	require.NoError(t, err)
	closeFn()

	got, err := os.ReadFile(filepath.Join(dir, "zcode.jsonl"))
	require.NoError(t, err)
	require.Equal(t, raw, string(got))
}

// TestCaptureSwitchOffWritesNothing 生产默认关：开关为空时不落任何文件。
func TestCaptureSwitchOffWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureEnvDir, "")

	writer, closeFn := openCaptureFile("zcode")
	require.Nil(t, writer)
	closeFn()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "开关关闭时不得落盘")
}
