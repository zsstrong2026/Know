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

// speakerChoice 主持人点名下一位发言者的结果。
// 不只给"谁"，还给"请他回答什么"，避免点名后仍是无目的的自由发挥。
type speakerChoice struct {
	AgentID  string `json:"next_speaker_id"`
	Question string `json:"question"`
}

var speakerChoiceJSONRe = regexp.MustCompile(`(?s)\{.*\}`)

// buildNextSpeakerPrompt 组装"选下一位发言人"的提示词（纯函数，单测覆盖）。
// 规则只保留三条：不选上一位、优先未发言且与当前分歧相关、必须附一个具体问题。
func buildNextSpeakerPrompt(
	room *types.MeetingRoom, triggerContent, recentText, lastSpeakerName string, candidates []speakerCandidate,
) string {
	var b strings.Builder
	b.WriteString("你是会议主持人，只决定下一位由谁发言，不发表观点、不做总结。\n")
	b.WriteString("会议主题：" + effectiveTopic(room) + "\n")
	if d := strings.TrimSpace(room.Description); d != "" {
		b.WriteString("会议描述：" + d + "\n")
	}
	if recentText != "" {
		b.WriteString("最近讨论：\n" + recentText)
	}
	if t := strings.TrimSpace(triggerContent); t != "" {
		b.WriteString("最新待回应的发言：" + truncate(t, 300) + "\n")
	}
	if lastSpeakerName != "" {
		b.WriteString("上一位发言者：" + lastSpeakerName + "（本轮不要再选他）\n")
	}
	b.WriteString("\n可选智能体：\n")
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
	b.WriteString("\n选择规则：\n")
	b.WriteString("1. 不要选择上一位发言者；\n")
	b.WriteString("2. 优先选择尚未发言、且其专长/知识库最能回应当前未解决问题的人；\n")
	b.WriteString("3. 只选一人，并给出你想请他回答的一个具体问题（不超过 30 字）。\n")
	b.WriteString("只输出如下 JSON，不要输出其他任何内容：\n")
	b.WriteString(`{"next_speaker_id": "候选人 id", "question": "想请他回答的具体问题"}`)
	return b.String()
}

// parseNextSpeakerChoice 容错解析主持人选择结果（裸 JSON 或 ```json 围栏）。
// 非法 id、缺失 id、选中上一位发言者均视为失败，由调用方按规则回退。
func parseNextSpeakerChoice(output string, validIDs []string, lastSpeakerID string) (string, string, bool) {
	text := strings.TrimSpace(output)
	if m := speakerChoiceJSONRe.FindString(text); m != "" {
		text = m
	}
	var choice speakerChoice
	if err := json.Unmarshal([]byte(text), &choice); err != nil {
		return "", "", false
	}
	id := strings.TrimSpace(choice.AgentID)
	if id == "" {
		return "", "", false
	}
	valid := false
	for _, v := range validIDs {
		if v == id {
			valid = true
			break
		}
	}
	if !valid || (lastSpeakerID != "" && id == lastSpeakerID) {
		return "", "", false
	}
	return id, strings.TrimSpace(choice.Question), true
}

// selectNextSpeakerByLLM 调用主持人模型选出下一位发言人；失败返回 ok=false。
func (e *meetingEngine) selectNextSpeakerByLLM(
	ctx context.Context, room *types.MeetingRoom, triggerContent, recentText, lastSpeakerID, lastSpeakerName string,
	candidates []speakerCandidate,
) (string, string, bool) {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "[meeting] host model unavailable for next speaker: %v", err)
		return "", "", false
	}
	prompt := buildNextSpeakerPrompt(room, triggerContent, recentText, lastSpeakerName, candidates)
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{Temperature: 0})
	if err != nil || resp == nil {
		logger.Warnf(ctx, "[meeting] host next speaker selection failed: %v", err)
		return "", "", false
	}
	return parseNextSpeakerChoice(resp.Content, candidateIDs(candidates), lastSpeakerID)
}

// chooseNextSpeaker 规则筛候选 + 小模型选一人：
//   - 规则先排除上一位发言者，并优先选择本轮尚未发言的人；
//   - 候选只剩一人时不再调用模型；
//   - 模型不可用或输出非法时按参会顺序回退第一位。
//
// spoken 可为 nil（首轮/用户插话后重置），此时只排除上一位发言者。
func (e *meetingEngine) chooseNextSpeaker(
	ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage,
	agents []*types.CustomAgent, spoken map[string]bool, lastSpeakerID string,
) (*types.CustomAgent, string) {
	if len(agents) == 0 {
		return nil, ""
	}
	// 规则层：先排除上一位发言者；再优先未发言者
	candidates := make([]*types.CustomAgent, 0, len(agents))
	for _, a := range agents {
		if a.ID != lastSpeakerID {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		candidates = agents
	}
	preferred := make([]*types.CustomAgent, 0, len(candidates))
	for _, a := range candidates {
		if spoken == nil || !spoken[a.ID] {
			preferred = append(preferred, a)
		}
	}
	if len(preferred) > 0 {
		candidates = preferred
	}
	if len(candidates) == 1 {
		return candidates[0], ""
	}

	// 模型层：在 2~3 个候选里选一人，并给出具体问题
	views := make([]speakerCandidate, len(candidates))
	for i, a := range candidates {
		views[i] = speakerCandidate{
			ID: a.ID, Name: a.Name, Description: a.Description,
			Skills: agentSkillNames(a), KnowledgeScope: describeKnowledgeScope(a.Config),
		}
	}
	triggerContent := ""
	if trigger != nil {
		triggerContent = trigger.Content
	}
	lastSpeakerName := ""
	if lastSpeakerID != "" {
		lastSpeakerName = e.agentDisplayName(ctx, room.ID, lastSpeakerID)
	}
	if id, question, ok := e.selectNextSpeakerByLLM(
		ctx, room, triggerContent, e.recentContextForSelection(ctx, room), lastSpeakerID, lastSpeakerName, views,
	); ok {
		picked := filterByIDs(agents, []string{id})
		if len(picked) == 1 {
			return picked[0], question
		}
	}
	return candidates[0], ""
}

// recentContextForSelection 取选人所需的最近讨论：启用议题时只看本议题，避免跨议题误选。
// 议题记录为空时回退到房间最近消息，兼容历史数据与精简测试仓储。
func (e *meetingEngine) recentContextForSelection(ctx context.Context, room *types.MeetingRoom) string {
	if e.messageRepo == nil {
		return ""
	}
	var recent []*types.MeetingMessage
	if room.AgendaEnabled && e.agendaRepo != nil {
		if _, active := e.ensureActiveAgenda(ctx, room); active != nil {
			recent, _ = e.messageRepo.ListByAgendaItem(ctx, room.ID, active.ID, meetingAgendaContextMax)
		}
	}
	if len(recent) == 0 {
		recent, _ = e.messageRepo.ListByRoom(ctx, room.ID, "", 8)
	}
	return e.renderRecentForHost(ctx, room.ID, recent, 6)
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
