package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeMeetingArtifact(t *testing.T) {
	// 历史行（迁移前数据）：kind/status 为空 → 兜底 solution/ready
	legacy := &types.MeetingArtifact{ID: "a1", SolutionMessageID: "m1", Version: 1}
	out := types.NormalizeMeetingArtifact(legacy)
	assert.Equal(t, types.MeetingArtifactKindSolution, out.Kind)
	assert.Equal(t, types.MeetingArtifactStatusReady, out.Status)

	// 新行原样保留
	fresh := &types.MeetingArtifact{
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
		Status: types.MeetingArtifactStatusGenerating,
	}
	out2 := types.NormalizeMeetingArtifact(fresh)
	assert.Equal(t, types.MeetingArtifactKindGenerated, out2.Kind)
	assert.Equal(t, types.MeetingArtifactStatusGenerating, out2.Status)
}

func TestValidMeetingArtifactType(t *testing.T) {
	for _, k := range []string{
		types.MeetingArtifactTypeReportMarkdown, types.MeetingArtifactTypeMinutes,
		types.MeetingArtifactTypeTodoList, types.MeetingArtifactTypeDocx, types.MeetingArtifactTypeXlsx,
		types.MeetingArtifactTypePptx, types.MeetingArtifactTypeHtmlReport, types.MeetingArtifactTypeChartImage,
	} {
		assert.True(t, types.ValidMeetingArtifactType(k), k)
	}
	for _, bad := range []string{"", "pdf", "docx ", "Solution"} {
		assert.False(t, types.ValidMeetingArtifactType(bad), bad)
	}
}

func TestArtifactKindHelpers(t *testing.T) {
	// 文件形态必须产出文件；文本形态不需要
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeDocx))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeXlsx))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypePptx))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeHtmlReport))
	assert.True(t, artifactNeedsFile(types.MeetingArtifactTypeChartImage))
	assert.False(t, artifactNeedsFile(types.MeetingArtifactTypeReportMarkdown))
	assert.False(t, artifactNeedsFile(types.MeetingArtifactTypeMinutes))
	assert.False(t, artifactNeedsFile(types.MeetingArtifactTypeTodoList))

	// 期望扩展名映射
	assert.Equal(t, []string{".docx"}, artifactExpectedExts(types.MeetingArtifactTypeDocx))
	assert.Equal(t, []string{".html", ".htm"}, artifactExpectedExts(types.MeetingArtifactTypeHtmlReport))
}

func TestPickArtifactFile(t *testing.T) {
	arts := types.MessageArtifacts{
		{FileName: "notes.md"}, {FileName: "report.docx"}, {FileName: "data.xlsx"},
	}
	// 期望扩展优先命中
	got, ok := pickArtifactFile(arts, types.MeetingArtifactTypeDocx)
	assert.True(t, ok)
	assert.Equal(t, "report.docx", got.FileName)
	// 无期望扩展（文本形态）取第一个
	got2, ok2 := pickArtifactFile(arts, types.MeetingArtifactTypeMinutes)
	assert.True(t, ok2)
	assert.Equal(t, "notes.md", got2.FileName)
	// 期望扩展未命中 → 降级取第一个，不整体失败
	got3, ok3 := pickArtifactFile(arts, types.MeetingArtifactTypeChartImage)
	assert.True(t, ok3)
	assert.Equal(t, "notes.md", got3.FileName)
	// 无文件
	_, ok4 := pickArtifactFile(nil, types.MeetingArtifactTypeDocx)
	assert.False(t, ok4)
}

func TestSanitizeArtifactFileName(t *testing.T) {
	assert.Equal(t, "artifact", sanitizeArtifactFileName(""), "空标题回退")
	assert.Equal(t, "发布-评审-v2", sanitizeArtifactFileName("发布 评审 v2"), "空白折叠为连字符")
	assert.Equal(t, "a-b-c", sanitizeArtifactFileName("a/b\\c"), "路径分隔符替换")
	assert.Equal(t, "artifact", sanitizeArtifactFileName("###"), "纯符号回退")
}

func TestDefaultArtifactTitle(t *testing.T) {
	assert.Equal(t, "会议纪要", defaultArtifactTitle(types.MeetingArtifactTypeMinutes))
	assert.Equal(t, "Markdown 报告", defaultArtifactTitle(types.MeetingArtifactTypeReportMarkdown))
}

func TestBuildArtifactPrompt(t *testing.T) {
	room := &types.MeetingRoom{ID: "r1", Title: "发布评审", Description: "风险"}
	recent := []*types.MeetingMessage{
		{SenderKind: types.MeetingSenderUser, Content: "性能是首要关切"},
	}
	adopted := []*types.MeetingMessage{
		{SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "先做压测"},
	}
	labelOf := func(m *types.MeetingMessage) string { return "某参会人" }
	p := buildArtifactPrompt(room, recent, adopted, types.MeetingArtifactTypeDocx, "发布评审报告", "包含性能章节",
		labelOf, artifactKindInstruction(types.MeetingArtifactTypeDocx, "发布评审报告"))
	assert.Contains(t, p, "发布评审")
	assert.Contains(t, p, "产物标题：发布评审报告")
	assert.Contains(t, p, "用户补充要求：包含性能章节")
	assert.Contains(t, p, "性能是首要关切")
	assert.Contains(t, p, "先做压测")
	assert.Contains(t, p, "发布评审报告.docx", "文件形态指令应含建议文件名")
}

func TestBuildMeetingArtifactAgent(t *testing.T) {
	agent := buildMeetingArtifactAgent(7, "model-1", "sbx-1")
	assert.Equal(t, "model-1", agent.Config.ModelID, "exec agent 必须显式绑定模型（resolveChatModelID 硬性要求）")
	assert.Equal(t, "sbx-1", agent.Config.SandboxConfigID, "沙箱配置是 AgentConfig.SandboxConfigID 的唯一来源")
	assert.Equal(t, "all", agent.Config.SkillsSelectionMode, "打开全部已装技能")
	assert.Equal(t, "none", agent.Config.KBSelectionMode, "知识库不注入，且避开 AgentQA 的 rerank 模型硬性校验")
	assert.False(t, agent.Config.MultiTurnEnabled, "单次生成不挂多轮历史")
	assert.NotZero(t, agent.Config.MaxIterations)
	assert.NotEmpty(t, agent.Config.SystemPrompt)
}
