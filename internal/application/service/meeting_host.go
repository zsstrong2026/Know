package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// speakerCandidate 主持人筛选时的候选智能体视图
type speakerCandidate struct {
	ID          string
	Name        string
	Description string   // 取自 agent 描述，供主持人判断相关性
	Skills      []string // 已选技能名（技能能反映该智能体的能力边界）
	// KnowledgeScope 知识库范围摘要（"全部"/"未绑定"/"3 个知识库"），避免传入 ID 噪音
	KnowledgeScope string
}

// resolveHostModelID 取租户默认 KnowledgeQA 模型 ID（开场与产物执行智能体复用同一模型解析）
func (e *meetingEngine) resolveHostModelID(ctx context.Context) (string, error) {
	models, err := e.modelService.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	for _, m := range models {
		if m.Type == types.ModelTypeKnowledgeQA && m.IsDefault {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("no default KnowledgeQA model for meeting host")
}

// resolveHostChat 获取租户默认对话模型作为主持人大脑。
// 模式仿 agent_eval.go resolveJudgeModel：ListModels → KnowledgeQA 默认模型 → GetChatModel。
func (e *meetingEngine) resolveHostChat(ctx context.Context) (chat.Chat, error) {
	modelID, err := e.resolveHostModelID(ctx)
	if err != nil {
		return nil, err
	}
	return e.modelService.GetChatModel(ctx, modelID)
}

// selectSpeakersByLLM 让主持人从候选中挑选本轮发言者；失败/解析失败返回 nil 由调用方回退全员。
func (e *meetingEngine) selectSpeakersByLLM(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, candidates []speakerCandidate) []string {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "[meeting] host model unavailable, fallback to all agents: %v", err)
		return nil
	}
	prompt := buildSpeakerSelectionPrompt(effectiveTopic(room), room.Description, trigger.Content, candidates)
	resp, err := hostChat.Chat(ctx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{Temperature: 0})
	if err != nil {
		logger.Warnf(ctx, "[meeting] host speaker selection failed, fallback to all agents: %v", err)
		return nil
	}
	if resp == nil {
		return nil
	}
	return parseSpeakerSelection(resp.Content, candidateIDs(candidates))
}

var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\[.*?\\])\\s*```")

// parseSpeakerSelection 从模型输出中提取 JSON 字符串数组并过滤非法 id。
func parseSpeakerSelection(output string, validIDs []string) []string {
	text := strings.TrimSpace(output)
	if m := jsonBlockRe.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	var picked []string
	if err := json.Unmarshal([]byte(text), &picked); err != nil {
		return nil
	}
	valid := make(map[string]bool, len(validIDs))
	for _, id := range validIDs {
		valid[id] = true
	}
	out := make([]string, 0, len(picked))
	seen := make(map[string]bool, len(picked))
	for _, id := range picked {
		if valid[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildSpeakerSelectionPrompt 主持人挑选发言者的提示词。
// candidates 携带技能与知识库范围，用于判断"谁最适合回答当前子议题"，而不只看描述。
func buildSpeakerSelectionPrompt(topic, meetingDescription, userMessage string, candidates []speakerCandidate) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人。会议主题：" + topic + "\n")
	if d := strings.TrimSpace(meetingDescription); d != "" {
		b.WriteString("会议描述：" + d + "\n")
	}
	b.WriteString("用户最新发言：" + userMessage + "\n\n")
	b.WriteString("参会智能体列表：\n")
	for _, c := range candidates {
		fmt.Fprintf(&b, "- id=%q 名称=%q 专长=%q", c.ID, c.Name, c.Description)
		if len(c.Skills) > 0 {
			b.WriteString(" 技能=" + strings.Join(c.Skills, "/"))
		}
		if c.KnowledgeScope != "" {
			b.WriteString(" 知识库=" + c.KnowledgeScope)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n请挑选最需要对这条发言表态的 1-3 个智能体（优先选技能与知识库最能支撑该议题的），只输出 JSON 数组（元素为 id），不要输出其他内容。")
	return b.String()
}

// agentSkillNames 返回智能体的已选技能名（all 模式返回空，避免把全量技能塞进选人提示词）
func agentSkillNames(agent *types.CustomAgent) []string {
	if agent == nil || agent.Config.SkillsSelectionMode == "none" {
		return nil
	}
	if agent.Config.SkillsSelectionMode != "selected" {
		return nil
	}
	out := make([]string, 0, len(agent.Config.SelectedSkills))
	for _, name := range agent.Config.SelectedSkills {
		if strings.TrimSpace(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

// describeKnowledgeScope 把知识库选择模式压成一句可读摘要（不暴露 ID）
func describeKnowledgeScope(cfg types.CustomAgentConfig) string {
	switch cfg.KBSelectionMode {
	case "all":
		return "全部"
	case "none":
		return "未绑定"
	default:
		if n := len(cfg.KnowledgeBases); n > 0 {
			return fmt.Sprintf("%d 个", n)
		}
		return ""
	}
}

// generateRedirection 裁判判定漂移后，由主持人生成一次"拉回当前子议题"的提示。
// 与 generateInterventionMessage 的差别：明确点名漂移而非泛泛总结，且不邀请用户决策。
func (e *meetingEngine) generateRedirection(
	ctx context.Context, room *types.MeetingRoom, item *types.MeetingAgendaItem, recent []*types.MeetingMessage,
) string {
	topic := effectiveTopic(room)
	if item != nil {
		topic = item.Title
	}
	fallback := fmt.Sprintf("主持人：讨论已偏离「%s」，请回到该议题继续。", topic)
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n当前子议题：%s\n", effectiveTopic(room), topic)
	if item != nil && item.Goal != "" {
		fmt.Fprintf(&b, "本议题要产出的结论：%s\n", item.Goal)
	}
	b.WriteString("最近讨论（截取）：\n")
	b.WriteString(e.renderRecentForHost(ctx, room.ID, recent, 6))
	b.WriteString("\n请以主持人身份用 1-2 句话：指出讨论已偏离当前子议题，并明确把发言拉回到该子议题尚未回答的部分。不要引入新议题，不要总结全文。")
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: b.String()}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return resp.Content
}

func candidateIDs(cs []speakerCandidate) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

// renderRecentForHost 把最近 n 条消息渲染为"名称: 内容"要点行（主持人收敛/打断提示复用）
func (e *meetingEngine) renderRecentForHost(ctx context.Context, roomID string, recent []*types.MeetingMessage, n int) string {
	var b strings.Builder
	for _, m := range tailN(recent, n) {
		name := senderLabel(m)
		if m.SenderKind == types.MeetingSenderAgent {
			name = e.agentDisplayName(ctx, roomID, m.SenderID)
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, truncate(m.Content, 200))
	}
	return b.String()
}

// generateGuardMessage 护栏触发后由主持人生成"回到正题"收敛消息；主持人不可用或生成失败时返回兜底文案。
func (e *meetingEngine) generateGuardMessage(ctx context.Context, room *types.MeetingRoom, nameA, nameB string, recent []*types.MeetingMessage) string {
	fallback := fmt.Sprintf("主持人提醒：「%s」与「%s」的讨论已较深入，我们先回到主题「%s」。请用户指示下一步方向。", nameA, nameB, effectiveTopic(room))
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	// 组装收敛提示：主题 + 互呛双方 + 最近若干条发言（截断），要求主持人中立引导回主题
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n智能体「%s」和「%s」已连续多轮互相回应，内容如下（截取）：\n", effectiveTopic(room), nameA, nameB)
	b.WriteString(e.renderRecentForHost(ctx, room.ID, recent, 6))
	b.WriteString("\n请以主持人身份用 2-3 句话：肯定双方观点中有价值的部分，指出分歧焦点，然后把讨论引导回主题并向用户提问下一步。语气中立、简洁。")
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: b.String()}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return resp.Content
}

// generateInterruptMessage 用户主动打断（"回到正题"）后由主持人生成收敛消息；不可用/失败时返回兜底文案。
func (e *meetingEngine) generateInterruptMessage(ctx context.Context, room *types.MeetingRoom, recent []*types.MeetingMessage) string {
	fallback := fmt.Sprintf("主持人：已收到打断，我们先暂停当前讨论，回到主题「%s」。请用户指示下一步方向。", effectiveTopic(room))
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n用户已打断当前讨论并要求回到正题。最近讨论内容（截取）：\n", effectiveTopic(room))
	b.WriteString(e.renderRecentForHost(ctx, room.ID, recent, 6))
	b.WriteString("\n请以主持人身份用 2-3 句话：简要总结当前讨论的分歧焦点，宣布暂停争论，并邀请用户给出下一步指示。语气中立、简洁。")
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: b.String()}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return resp.Content
}

// generateInterventionMessage 每 N 轮介入：主持人生成「阶段总结 + 重定向」消息，防止讨论跑偏。
// 主持人模型不可用或生成失败时返回兜底文案。
func (e *meetingEngine) generateInterventionMessage(ctx context.Context, room *types.MeetingRoom, recent []*types.MeetingMessage, completed int) string {
	fallback := fmt.Sprintf("主持人：讨论已完成 %d 轮，先做阶段小结。请大家围绕主题「%s」聚焦尚未对齐的分歧继续推进。", completed, effectiveTopic(room))
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n讨论已完成 %d 轮，以下是最近的发言（截取）：\n", effectiveTopic(room), completed)
	b.WriteString(e.renderRecentForHost(ctx, room.ID, recent, 6))
	b.WriteString("\n请以主持人身份用 2-3 句话：概括目前已形成的共识与尚存的分歧，指出偏离主题之处（如有），并将讨论重新聚焦到主题上。语气中立、简洁，不要引入新议题。")
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: b.String()}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return resp.Content
}

// tailN 取切片最后 n 条（不足则原样返回）
func tailN(msgs []*types.MeetingMessage, n int) []*types.MeetingMessage {
	if len(msgs) <= n {
		return msgs
	}
	return msgs[len(msgs)-n:]
}

// truncate 截断到 n 个字符（按 rune 计，避免中文截半）
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
