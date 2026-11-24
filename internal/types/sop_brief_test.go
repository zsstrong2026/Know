package types

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRenderProjectBriefSectionsAndCap(t *testing.T) {
	t0 := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	in := BriefInput{
		Project: SopProject{Name: "知识库改造", Code: "KNOW", DefaultTemplateID: "t1"},
		Items: []BriefItem{
			{No: "KNOW-1", Title: "登录改造", Status: "in_progress", ExecState: "running"},
			{No: "KNOW-2", Title: "检索优化", Status: "done", ExecState: "closed"},
			{No: "KNOW-3", Title: "导出", Status: "todo", ExecState: "none", ChildDone: 3, ChildTotal: 7},
		},
		Notes: []SopProjectNote{
			{Kind: SopNoteKindDecision, Title: "采用方案 B", Body: "因为可逆", Pinned: true, Seq: 9, CreatedAt: t0},
			{Kind: SopNoteKindRisk, Title: "索引延迟", Body: "需观察", Seq: 8, CreatedAt: t0},
			{Kind: SopNoteKindRunSummary, Title: "运行 #12", Body: "全部节点通过", Seq: 7, CreatedAt: t0},
		},
	}
	out := RenderProjectBrief(in)

	require.Contains(t, out, "知识库改造")
	require.Contains(t, out, "正在推进")
	require.Contains(t, out, "采用方案 B")
	require.Contains(t, out, "索引延迟")
	require.Contains(t, out, "全部节点通过", "运行结论段落必须渲染")
	require.Contains(t, out, "KNOW", "概况带编号前缀")
	require.Contains(t, out, "（子项 3/7）")
	require.NotContains(t, out, "检索优化", "已闭环事项不进「在进行的事项」")

	require.Less(t, strings.Index(out, "## 在进行的事项"), strings.Index(out, "## 最近决策"))
	require.Less(t, strings.Index(out, "## 最近决策"), strings.Index(out, "## 最近风险"))
	require.Less(t, strings.Index(out, "## 最近风险"), strings.Index(out, "## 最近运行结论"))
	require.NotContains(t, out, "{{")
}

// 决策段落：置顶的全带 + 其余按 seq 降序补足 8 条；每条带来源与时间。
func TestRenderProjectBriefDecisionOrder(t *testing.T) {
	t0 := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	notes := []SopProjectNote{
		{Kind: SopNoteKindDecision, Title: "置顶旧决策", Body: "b", Pinned: true, Seq: 1, CreatedAt: t0},
	}
	for i := 2; i <= 11; i++ {
		notes = append(notes, SopProjectNote{
			Kind: SopNoteKindDecision, Title: fmt.Sprintf("决策-%d", i), Body: "b", Seq: int64(i), CreatedAt: t0,
		})
	}
	out := RenderProjectBrief(BriefInput{Project: SopProject{Name: "P"}, Notes: notes})

	require.Contains(t, out, "置顶旧决策")
	require.Contains(t, out, "决策-11")
	require.Contains(t, out, "决策-4", "8 个决策位：置顶 1 条 + seq 最大的 7 条")
	require.NotContains(t, out, "决策-3", "更早的未置顶决策被挤出")
	require.Contains(t, out, "人工", "决策带来源中文")
	require.Contains(t, out, "2025-06-01", "决策带时间")
}

func TestRenderProjectBriefTruncatesLowPriority(t *testing.T) {
	items := make([]BriefItem, 0, 60)
	for i := 0; i < 60; i++ {
		items = append(items, BriefItem{
			No: fmt.Sprintf("K-%d", i), Title: strings.Repeat("长标题", 20),
			Status: "todo", ExecState: "none",
		})
	}
	out := RenderProjectBrief(BriefInput{Project: SopProject{Name: "P"}, Items: items, MaxChars: 300})
	require.LessOrEqual(t, len([]rune(out)), 340, "硬顶允许少量尾注溢出")
	require.Contains(t, out, "完整内容见项目动态")
}

// 默认硬顶 4000 字：MaxChars<=0 时生效。
func TestRenderProjectBriefDefaultCap(t *testing.T) {
	items := make([]BriefItem, 0, 80)
	for i := 0; i < 80; i++ {
		items = append(items, BriefItem{
			No: fmt.Sprintf("K-%d", i), Title: strings.Repeat("长标题", 20),
			Status: "todo", ExecState: "none",
		})
	}
	out := RenderProjectBrief(BriefInput{Project: SopProject{Name: "P"}, Items: items})
	require.LessOrEqual(t, len([]rune(out)), briefDefaultMaxChars+40)
}

// 空项目也能渲染：结构完整、无「{{」占位泄漏。
func TestRenderProjectBriefEmpty(t *testing.T) {
	out := RenderProjectBrief(BriefInput{Project: SopProject{Name: "空项目"}})
	require.Contains(t, out, "# 空项目")
	require.Contains(t, out, "## 项目概况")
	require.NotContains(t, out, "{{")
}
