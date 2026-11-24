package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildProjectContextSections(t *testing.T) {
	items := []types.SopItem{
		{No: "K-1", Title: "已完成项", Status: types.SopItemStatusClosed},
		{No: "K-2", Title: "推进中", Status: types.SopItemStatusInProgress},
		{No: "K-3", Title: "待处理", Status: types.SopItemStatusTodo},
	}
	notes := []types.SopProjectNote{
		{Kind: types.SopNoteKindDecision, Title: "采用方案 B", Body: "因为可逆", Pinned: true, Seq: 1},
		{Kind: types.SopNoteKindRisk, Title: "索引延迟", Body: "需观察", Seq: 5},
	}
	out := BuildProjectContext(items, notes, projectBriefPath)
	require.Contains(t, out, "最近决策")
	require.Contains(t, out, "采用方案 B")
	require.Contains(t, out, "索引延迟")
	require.Contains(t, out, "1/3 已完成")
	require.Contains(t, out, "K-2 推进中")
	require.Contains(t, out, "inputs/project-brief.md")
	require.Less(t, strings.Index(out, "最近决策"), strings.Index(out, "兄弟事项进度"),
		"决策在前、进度在后")
}

// 超硬顶时按决策条数递减收敛到 1500 字以内。
func TestBuildProjectContextCap(t *testing.T) {
	notes := make([]types.SopProjectNote, 0, 40)
	for i := 0; i < 40; i++ {
		notes = append(notes, types.SopProjectNote{
			Kind: types.SopNoteKindRisk, Title: strings.Repeat("风险", 30),
			Body: strings.Repeat("正文", 60), Seq: int64(i),
		})
	}
	items := make([]types.SopItem, 0, 30)
	for i := 0; i < 30; i++ {
		items = append(items, types.SopItem{No: "K-" + string(rune('A'+i)), Title: strings.Repeat("事项", 20)})
	}
	out := BuildProjectContext(items, notes, projectBriefPath)
	require.LessOrEqual(t, len([]rune(out)), projectContextCap+40, "硬顶只允许少量溢出")
	require.Contains(t, out, "项目简报全文", "尾注必须保留指路信息")
}

func TestBuildProjectContextEmpty(t *testing.T) {
	require.Empty(t, BuildProjectContext(nil, nil, projectBriefPath))
}

// 物化简报：写进 inputs/project-brief.md，路径在输入目录下。
func TestMaterializeProjectBrief(t *testing.T) {
	root := t.TempDir()
	ws, err := PrepareWorkspace(root, "run-materialize")
	require.NoError(t, err)

	path, err := ws.MaterializeProjectBrief("# 项目简报\n\n正文")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(filepath.ToSlash(path), "inputs/project-brief.md"), path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "正文")
	require.Contains(t, string(raw), "自动生成", "物化文件带来源说明")
}

func TestMaterializeProjectBriefEmpty(t *testing.T) {
	root := t.TempDir()
	ws, err := PrepareWorkspace(root, "run-materialize-empty")
	require.NoError(t, err)
	_, err = ws.MaterializeProjectBrief("   ")
	require.Error(t, err, "空简报不物化")
}
