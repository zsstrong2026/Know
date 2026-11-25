package sop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// installFakeCLI 在临时目录里放一个「可执行且 --version 有输出」的假 CLI，
// 并把该目录置于 PATH 最前；返回二进制名。
func installFakeCLI(t *testing.T, binary string) {
	t.Helper()
	dir := t.TempDir()
	if os.PathSeparator == '/' {
		require.NoError(t, os.WriteFile(filepath.Join(dir, binary),
			[]byte("#!/bin/sh\necho fake 1.2.3\n"), 0o755))
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(dir, binary+".bat"),
			[]byte("@echo off\r\necho fake 1.2.3\r\n"), 0o755))
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Status 每次调用会对 10 个 CLI 各做一次 LookPath 与 `--version`；设置页轮询
// 或多人同时打开时开销叠加。结果进 60s 进程内缓存：一分钟内不得重复探测。
//
// 断言用「版本串」而不是「是否安装」：开发机上可能真的装了 zcode，
// 只有版本串能区分「读的是缓存」与「重新探测到了刚放的假 CLI」。
func TestCLIStatusCachesForAMinute(t *testing.T) {
	svc := NewCredentialService(nil)
	ctx := context.Background()

	first := svc.Status(ctx)
	require.Len(t, first, 10, "10 个适配器都要有状态")
	firstZcode := findCLIStatus(t, first, "zcode")

	installFakeCLI(t, "zcode")
	svc.cliStatusMu.Lock()
	svc.cliStatusAt = time.Now()
	svc.cliStatusMu.Unlock()

	cached := findCLIStatus(t, svc.Status(ctx), "zcode")
	require.Equal(t, firstZcode.Version, cached.Version, "TTL 内必须命中缓存")
	require.NotContains(t, cached.Version, "1.2.3", "TTL 内不得看到新装的假 CLI")

	// 过期后重新探测：应读到假 CLI 的版本号。
	svc.cliStatusMu.Lock()
	svc.cliStatusAt = time.Now().Add(-2 * time.Minute)
	svc.cliStatusMu.Unlock()

	fresh := findCLIStatus(t, svc.Status(ctx), "zcode")
	require.Contains(t, fresh.Version, "1.2.3", "缓存过期后应重新探测")
}

// findCLIStatus 按 kind 取一条状态（找不到直接失败）。
func findCLIStatus(t *testing.T, list []CLIStatus, kind string) *CLIStatus {
	t.Helper()
	for i := range list {
		if list[i].Kind == kind {
			return &list[i]
		}
	}
	t.Fatalf("状态里没有 %s：%+v", kind, list)
	return nil
}

// 返回的切片必须是副本：调用方（handler 直接 JSON 化）不该有机会改到缓存。
func TestCLIStatusReturnsCopy(t *testing.T) {
	svc := NewCredentialService(nil)
	ctx := context.Background()
	first := svc.Status(ctx)
	first[0].Installed = !first[0].Installed
	first[0].Kind = "tampered"

	second := svc.Status(ctx)
	require.NotEqual(t, "tampered", second[0].Kind, "缓存不得被调用方改写")
}
