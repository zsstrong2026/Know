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
	Description string // 取自 agent 描述，供主持人判断相关性
}

// resolveHostChat 获取租户默认对话模型作为主持人大脑。
// 模式仿 agent_eval.go resolveJudgeModel：ListModels → KnowledgeQA 默认模型 → GetChatModel。
func (e *meetingEngine) resolveHostChat(ctx context.Context) (chat.Chat, error) {
	models, err := e.modelService.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	for _, m := range models {
		if m.Type == types.ModelTypeKnowledgeQA && m.IsDefault {
			return e.modelService.GetChatModel(ctx, m.ID)
		}
	}
	return nil, fmt.Errorf("no default KnowledgeQA model for meeting host")
}

// selectSpeakersByLLM 让主持人从候选中挑选本轮发言者；失败/解析失败返回 nil 由调用方回退全员。
func (e *meetingEngine) selectSpeakersByLLM(ctx context.Context, room *types.MeetingRoom, trigger *types.MeetingMessage, candidates []speakerCandidate) []string {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "[meeting] host model unavailable, fallback to all agents: %v", err)
		return nil
	}
	prompt := buildSpeakerSelectionPrompt(room.Description, trigger.Content, candidates)
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

func buildSpeakerSelectionPrompt(topic, userMessage string, candidates []speakerCandidate) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人。会议主题：" + topic + "\n")
	b.WriteString("用户最新发言：" + userMessage + "\n\n")
	b.WriteString("参会智能体列表：\n")
	for _, c := range candidates {
		fmt.Fprintf(&b, "- id=%q 名称=%q 专长=%q\n", c.ID, c.Name, c.Description)
	}
	b.WriteString("\n请挑选最需要对这条发言表态的 1-3 个智能体，只输出 JSON 数组（元素为 id），不要输出其他内容。")
	return b.String()
}

func candidateIDs(cs []speakerCandidate) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

// generateGuardMessage 护栏触发后由主持人生成"回到正题"收敛消息；主持人不可用或生成失败时返回兜底文案。
func (e *meetingEngine) generateGuardMessage(ctx context.Context, room *types.MeetingRoom, nameA, nameB string, recent []*types.MeetingMessage) string {
	fallback := fmt.Sprintf("主持人提醒：「%s」与「%s」的讨论已较深入，我们先回到主题「%s」。请用户指示下一步方向。", nameA, nameB, room.Title)
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return fallback
	}
	// 组装收敛提示：主题 + 互呛双方 + 最近若干条发言（截断），要求主持人中立引导回主题
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n智能体「%s」和「%s」已连续多轮互相回应，内容如下（截取）：\n", room.Title, nameA, nameB)
	for _, m := range tailN(recent, 6) {
		name := senderLabel(m)
		if m.SenderKind == types.MeetingSenderAgent {
			name = e.agentDisplayName(ctx, room.ID, m.SenderID)
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, truncate(m.Content, 200))
	}
	b.WriteString("\n请以主持人身份用 2-3 句话：肯定双方观点中有价值的部分，指出分歧焦点，然后把讨论引导回主题并向用户提问下一步。语气中立、简洁。")
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
