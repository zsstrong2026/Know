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
	// OnTopic 最近讨论是否仍在当前子议题上（结构性治理新增；缺省视为在题内，保持向后兼容）
	OnTopic bool `json:"on_topic"`
	// GoalCovered 当前子议题要产出的结论是否已被覆盖
	GoalCovered bool `json:"goal_covered"`
	// Drift 是否已明显偏离当前子议题（与 OnTopic 互补：OnTopic 描述现状，Drift 描述已发生的漂移）
	Drift bool `json:"drift"`
}

// refereeJSONRe 提取被 ```json 围栏包裹的 JSON 对象
var refereeJSONRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// buildRefereePrompt 组装裁判判定提示词（纯函数，单测覆盖）。
// recentText 为最近发言的渲染文本（由 renderRecentForHost 生成）。
// item 非空时按"当前子议题 + 目标覆盖度"判定，否则退回仅判主题收敛。
func buildRefereePrompt(room *types.MeetingRoom, item *types.MeetingAgendaItem, recentText string, completed int) string {
	var b strings.Builder
	b.WriteString("你是本次会议的独立裁判，不参与讨论、不发表观点，只负责判定讨论状态。\n")
	fmt.Fprintf(&b, "会议主题：%s\n会议描述：%s\n", effectiveTopic(room), room.Description)
	if item != nil {
		fmt.Fprintf(&b, "当前子议题：%s\n", item.Title)
		if item.Goal != "" {
			fmt.Fprintf(&b, "本议题要产出的结论：%s\n", item.Goal)
		}
	}
	fmt.Fprintf(&b, "讨论已完成 %d 个子轮，以下是最近的发言（截取）：\n", completed)
	b.WriteString(recentText)
	b.WriteString("\n判定标准：\n")
	b.WriteString("1. on_topic：最近发言是否仍在讨论当前子议题（未指定子议题时指会议主题）；转到其他议题或闲聊判为 false。\n")
	b.WriteString("2. drift：是否已出现明显偏离（连续多轮讨论与当前子议题无关的内容）；仅一次轻微偏离不算漂移。\n")
	b.WriteString("3. goal_covered：当前子议题要产出的结论是否已被明确回答或达成一致（未指定目标时与 converged 同义）。\n")
	b.WriteString("4. converged：各方核心分歧已消解（或仅剩重复表述、无新增信息）即视为已收敛。\n")
	b.WriteString("只输出如下 JSON 对象，不要输出其他任何内容：\n")
	b.WriteString(`{"converged": true 或 false, "on_topic": true 或 false, "drift": true 或 false, ` +
		`"goal_covered": true 或 false, "reason": "判定依据（一句话）", "consensus": "若已收敛，写出各方达成的一致结论；若未收敛则为空字符串"}`)
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

// judgeConsensus 调用裁判 LLM 判定是否收敛（item 非空时同时判定相关度与目标覆盖度）。
// 模型不可用 / 生成失败 / 输出不可解析一律返回「未收敛」（零值），保证讨论继续推进。
func (e *meetingEngine) judgeConsensus(
	ctx context.Context, room *types.MeetingRoom, item *types.MeetingAgendaItem,
	recent []*types.MeetingMessage, completed int,
) refereeVerdict {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		logger.Warnf(ctx, "[meeting] referee model unavailable, room=%s err=%v", room.ID, err)
		return refereeVerdict{}
	}
	prompt := buildRefereePrompt(room, item, e.renderRecentForHost(ctx, room.ID, recent, 8), completed)
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
	return fmt.Sprintf("【共识】裁判判定讨论已围绕主题「%s」收敛。", effectiveTopic(room))
}

// agendaRecap 生成议程小结正文（议题完成时落库，含本议题结论与剩余的待讨论议题）
func agendaRecap(item *types.MeetingAgendaItem, remaining []*types.MeetingAgendaItem, v refereeVerdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【议题小结】%s\n", item.Title)
	if s := strings.TrimSpace(v.Consensus); s != "" {
		b.WriteString(s)
	} else if r := strings.TrimSpace(v.Reason); r != "" {
		b.WriteString(r)
	}
	if len(remaining) > 0 {
		b.WriteString("\n待讨论：")
		titles := make([]string, 0, len(remaining))
		for _, it := range remaining {
			titles = append(titles, it.Title)
		}
		b.WriteString(strings.Join(titles, "、"))
	}
	return b.String()
}
