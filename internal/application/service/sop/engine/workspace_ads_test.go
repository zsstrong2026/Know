package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// Win32 备用数据流（ADS）：`name:stream` 写的是同一文件的隐藏流，落盘后既不会
// 出现在目录列表里，也不会被产物收集按文件名读到——等于把产物写进了黑洞。
// 盘符位（C:）之外的冒号一律拒绝。
func TestSafeWorkspacePathRejectsWin32ADS(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run-ads")
	for _, bad := range []string{
		"out/a.md:hidden",
		`out\a.md:hidden`,
		"out:stream",
		"a:b:c",
	} {
		_, err := SafeWorkspacePath(ws, bad)
		require.Error(t, err, "path %q 必须被拒绝", bad)
	}
}

// 正常相对路径不得误伤（含带点的文件名与子目录）。
func TestSafeWorkspacePathAllowsNormalNames(t *testing.T) {
	ws, _ := PrepareWorkspace(t.TempDir(), "run-ok")
	for _, good := range []string{
		"out/a.md", "out/report.v2.md", "work/repos/readme.md", "./out/a.md",
	} {
		_, err := SafeWorkspacePath(ws, good)
		require.NoError(t, err, "path %q 应被接受", good)
	}
}

// 产物声明里的 ADS 也要在发布/运行前的定义校验阶段就被挡下，
// 不能等节点跑完才发现文件根本读不到。
func TestValidateDefinitionRejectsADSArtifactPath(t *testing.T) {
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Name: "实现", Type: types.SopNodeTypeAgent,
		Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{
			Name: "a", Path: "out/a.md:stream",
		}},
	}}}
	errs := ValidateDefinition(&def, false)
	require.NotEmpty(t, errs)
	require.True(t, containsSub(errs, "冒号"), "错误要说清是冒号问题：%v", errs)
}

// 带盘符前缀的 Windows 绝对路径仍旧按「越界」拒绝（原有行为不能退化）。
func TestEscapesWorkspaceStillRejectsDriveAndParent(t *testing.T) {
	for _, p := range []string{`C:\tmp\a.md`, "C:/tmp/a.md", "../a.md", "/abs/a.md"} {
		require.True(t, escapesWorkspace(p), "path %q 应判为越界", p)
	}
	require.False(t, escapesWorkspace("out/a.md"))
	require.False(t, escapesWorkspace(filepath.Join("out", "sub", "a.md")))
	require.True(t, strings.Contains(filepath.Join("out", "a"), "out"))
}
