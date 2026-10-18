package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// artifactLockKey 房间级产物生成互斥锁（同房间串行，避免采集器 diff 去重互相干扰）
func artifactLockKey(roomID string) string { return fmt.Sprintf("meeting:%s:artifact_lock", roomID) }

// artifactNeedsFile 该形态的交付物是否必须为沙箱产出的文件
func artifactNeedsFile(kindType string) bool {
	switch kindType {
	case types.MeetingArtifactTypeDocx, types.MeetingArtifactTypeXlsx, types.MeetingArtifactTypePptx,
		types.MeetingArtifactTypeHtmlReport, types.MeetingArtifactTypeChartImage:
		return true
	}
	return false
}

// artifactExpectedExts 形态 → 期望文件扩展名（命中者优先作为交付物）
func artifactExpectedExts(kindType string) []string {
	switch kindType {
	case types.MeetingArtifactTypeDocx:
		return []string{".docx"}
	case types.MeetingArtifactTypeXlsx:
		return []string{".xlsx"}
	case types.MeetingArtifactTypePptx:
		return []string{".pptx"}
	case types.MeetingArtifactTypeHtmlReport:
		return []string{".html", ".htm"}
	case types.MeetingArtifactTypeChartImage:
		return []string{".png", ".jpg", ".jpeg", ".svg", ".webp"}
	}
	return nil
}

// pickArtifactFile 从本次采集结果中挑出交付物：期望扩展优先，否则降级取第一个；无文件返回 false
func pickArtifactFile(arts types.MessageArtifacts, kindType string) (types.MessageArtifact, bool) {
	if len(arts) == 0 {
		return types.MessageArtifact{}, false
	}
	for _, ext := range artifactExpectedExts(kindType) {
		for _, a := range arts {
			if strings.EqualFold(filepath.Ext(a.FileName), ext) {
				return a, true
			}
		}
	}
	return arts[0], true
}

// sanitizeArtifactFileName 标题 → 安全文件名基：仅保留字母/数字/CJK，其余折叠为连字符
func sanitizeArtifactFileName(title string) string {
	if title == "" {
		return "artifact"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "artifact"
	}
	if r := []rune(out); len(r) > 60 { // 文件名长度保险
		out = string(r[:60])
	}
	return out
}

// defaultArtifactTitle 未填标题时按形态回缺省名
func defaultArtifactTitle(kindType string) string {
	switch kindType {
	case types.MeetingArtifactTypeMinutes:
		return "会议纪要"
	case types.MeetingArtifactTypeTodoList:
		return "待办事项"
	case types.MeetingArtifactTypeDocx:
		return "Word 文档"
	case types.MeetingArtifactTypeXlsx:
		return "Excel 表格"
	case types.MeetingArtifactTypePptx:
		return "PPT 演示"
	case types.MeetingArtifactTypeHtmlReport:
		return "网页报告"
	case types.MeetingArtifactTypeChartImage:
		return "图表"
	}
	return "Markdown 报告"
}

// artifactKindInstruction 形态 → 执行指令（注入生成 prompt；文件形态给出建议文件名）
func artifactKindInstruction(kindType, title string) string {
	base := sanitizeArtifactFileName(title)
	switch kindType {
	case types.MeetingArtifactTypeReportMarkdown:
		return "请在最终回答中直接输出完整的 Markdown 报告正文，不要生成文件。"
	case types.MeetingArtifactTypeMinutes:
		return "请在最终回答中直接输出完整的会议纪要（Markdown，含议题、结论、分歧、行动项），不要生成文件。"
	case types.MeetingArtifactTypeTodoList:
		return "请在最终回答中直接输出待办事项清单（Markdown 复选框列表，含负责人建议与优先级），不要生成文件。"
	case types.MeetingArtifactTypeDocx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 Word 文档，文件名建议为 %s.docx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeXlsx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 Excel 表格，文件名建议为 %s.xlsx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypePptx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 PPT 演示文稿，文件名建议为 %s.pptx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeHtmlReport:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成单文件 HTML 报告（自包含样式、无外部依赖），文件名建议为 %s.html，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeChartImage:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成图表图片（优先 PNG），文件名建议为 %s.png，并在最终回答中说明图表结论。", base)
	}
	return ""
}

// buildArtifactPrompt 组装产物生成 prompt（纯函数，单测覆盖）
func buildArtifactPrompt(
	room *types.MeetingRoom, recent, adopted []*types.MeetingMessage,
	kindType, title, requirement string,
	labelOf func(*types.MeetingMessage) string, instruction string,
) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人，现在需要为本会议产出一份交付物。\n")
	fmt.Fprintf(&b, "会议主题：%s\n", room.Title)
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	fmt.Fprintf(&b, "产物标题：%s\n产物形态：%s\n", title, kindType)
	if requirement != "" {
		b.WriteString("用户补充要求：" + requirement + "\n")
	}
	if len(recent) > 0 {
		b.WriteString("\n【会议讨论记录（截取）】\n")
		for _, m := range recent {
			fmt.Fprintf(&b, "%s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	if len(adopted) > 0 {
		b.WriteString("\n【用户已采纳的意见】\n")
		for _, m := range adopted {
			fmt.Fprintf(&b, "- %s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	b.WriteString("\n【执行要求】\n")
	b.WriteString(instruction + "\n")
	b.WriteString("内容必须忠于会议记录与采纳意见，不得编造未讨论过的事实。")
	return b.String()
}

// buildMeetingArtifactAgent 构造产物执行智能体（合成、不落库）。
// 模型 = 租户默认 KnowledgeQA（resolveChatModelID 对非空 ModelID 强校验类型）；
// 沙箱 = 调用方解析的租户具名配置（空则 AgentQA 走部署级 lite/禁用策略）；
// SkillsSelectionMode=all 打开沙箱内已装技能，兑现「可调用工具箱技能」。
func buildMeetingArtifactAgent(tenantID uint64, modelID, sandboxConfigID string) *types.CustomAgent {
	agent := &types.CustomAgent{
		ID:       "meeting-artifact-executor",
		TenantID: tenantID,
		Name:     "会议产物生成器",
		Config: types.CustomAgentConfig{
			AgentMode:           "smart-reasoning",
			ModelID:             modelID,
			SandboxConfigID:     sandboxConfigID,
			SystemPrompt:        "你是会议室的产物生成助手，负责把会议讨论沉淀为指定形态的交付物。严格遵循用户的形态与文件要求，善用可用的工具与技能，最终回答用中文。",
			SkillsSelectionMode: "all",
			MCPSelectionMode:    "none",
			// 产物素材仅来自会议记录与采纳意见，不注入知识库；同时避开 rerank 模型硬性校验
			KBSelectionMode: "none",
			MaxIterations:   20,
			Temperature:     0.3,
		},
	}
	agent.EnsureDefaults()
	// EnsureDefaults 对 smart-reasoning 会强制开启多轮历史；产物生成是单次任务，须在其后显式关闭
	agent.Config.MultiTurnEnabled = false
	return agent
}
