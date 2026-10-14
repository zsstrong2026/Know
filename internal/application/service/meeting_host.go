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
