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

// refereeVerdict 裁判判定结果（JSON 契约，见 buildRefereePrompt）
type refereeVerdict struct {
	Converged bool   `json:"converged"` // 讨论是否已收敛
	Reason    string `json:"reason"`    // 判定依据（一句话）
	Consensus string `json:"consensus"` // 已收敛时的一致结论；未收敛为空
}

// refereeJSONRe 提取被 ```json 围栏包裹的 JSON 对象
var refereeJSONRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// buildRefereePrompt 组装裁判判定提示词（纯函数，单测覆盖）。
// recentText 为最近发言的渲染文本（由 renderRecentForHost 生成）。
func buildRefereePrompt(room *types.MeetingRoom, recentText string, completed int) string {
	var b strings.Builder
	b.WriteString("你是本次会议的独立裁判，不参与讨论、不发表观点，只负责判定讨论是否已收敛。\n")
	fmt.Fprintf(&b, "会议主题：%s\n会议描述：%s\n", room.Title, room.Description)
	fmt.Fprintf(&b, "讨论已完成 %d 个子轮，以下是最近的发言（截取）：\n", completed)
	b.WriteString(recentText)
	b.WriteString("\n判定标准：各方核心分歧已消解（或仅剩重复表述、无新增信息）即视为已收敛；仍有实质性分歧在推进则未收敛。\n")
	b.WriteString("只输出如下 JSON 对象，不要输出其他任何内容：\n")
	b.WriteString(`{"converged": true 或 false, "reason": "判定依据（一句话）", "consensus": "若已收敛，写出各方达成的一致结论；若未收敛则为空字符串"}`)
	return b.String()
}

// parseRefereeVerdict 容错解析裁判输出的 JSON 对象（支持裸 JSON 与 ```json 围栏）。
// 解析失败返回 ok=false，由调用方按「未收敛」处理（fail-safe，保证讨论不被误终止）。
func parseRefereeVerdict(output string) (refereeVerdict, bool) {
	text := strings.TrimSpace(output)
	if m := refereeJSONRe.FindStringSubmatch(text); m != nil {
		text = m[1]
	} else if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		// 无围栏时取首个 '{' 到末个 '}' 之间的片段，容忍模型前后加解释性文字
		text = text[i : j+1]
	}
	var v refereeVerdict
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return refereeVerdict{}, false
	}
	return v, true
}

// judgeConsensus 调用裁判 LLM 判定是否收敛。
// 模型不可用 / 生成失败 / 输出不可解析一律返回「未收敛」（零值），保证讨论继续推进。
func (e *meetingEngine) judgeConsensus(ctx context.Context, room *types.MeetingRoom, recent []*types.MeetingMessage, completed int) refereeVerdict {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "[meeting] referee model unavailable, room=%s err=%v", room.ID, err)
		return refereeVerdict{}
	}
	prompt := buildRefereePrompt(room, e.renderRecentForHost(ctx, room.ID, recent, 8), completed)
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{Temperature: 0})
	if err != nil || resp == nil {
		logger.Warnf(ctx, "[meeting] referee judge failed, room=%s err=%v", room.ID, err)
		return refereeVerdict{}
	}
	v, ok := parseRefereeVerdict(resp.Content)
	if !ok {
		logger.Warnf(ctx, "[meeting] referee verdict unparsable, room=%s", room.ID)
		return refereeVerdict{}
	}
	return v
}

// consensusMessageContent 组装共识消息正文：优先裁判给出的一致结论，缺失时回退到含判定依据的兜底文案。
func consensusMessageContent(room *types.MeetingRoom, v refereeVerdict) string {
	if c := strings.TrimSpace(v.Consensus); c != "" {
		return "【共识】" + c
	}
	if r := strings.TrimSpace(v.Reason); r != "" {
		return fmt.Sprintf("【共识】讨论已收敛：%s", r)
	}
	return fmt.Sprintf("【共识】裁判判定讨论已围绕主题「%s」收敛。", room.Title)
}
