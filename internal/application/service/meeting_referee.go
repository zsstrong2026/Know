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

// refereeStance 裁判从讨论中抽取的立场卡（不展示给用户，仅用于收敛判定）。
// 让裁判按"主张/置信度/反对点/改变条件"核对，而不是让智能体自报"我同意"。
type refereeStance struct {
	Agent         string   `json:"agent"`
	Position      string   `json:"position"`
	Confidence    float64  `json:"confidence"`
	Objections    []string `json:"objections"`
	WouldChangeIf string   `json:"would_change_if"`
}

// refereeVerdict 裁判判定结果（JSON 契约，见 buildRefereePrompt）
type refereeVerdict struct {
	// Status 收敛状态：consensus（完全一致）/ partial（目标已覆盖但仍有分歧）/ no_consensus
	Status    string `json:"status"`
	Converged bool   `json:"converged"` // 讨论是否已收敛（partial 时为 false）
	Reason    string `json:"reason"`    // 判定依据（一句话）
	Consensus string `json:"consensus"` // 已收敛/部分收敛时可写入的结论
	// OnTopic 最近讨论是否仍在当前子议题上（结构性治理新增；缺省视为在题内，保持向后兼容）
	OnTopic bool `json:"on_topic"`
	// GoalCovered 当前子议题要产出的结论是否已被覆盖
	GoalCovered bool `json:"goal_covered"`
	// Drift 是否已明显偏离当前子议题（与 OnTopic 互补：OnTopic 描述现状，Drift 描述已发生的漂移）
	Drift bool `json:"drift"`
	// Unresolved 尚未解决的分歧点（允许会议以"部分共识 + 未解决分歧"结束）
	Unresolved []string `json:"unresolved"`
	// EvidenceGaps 结论缺少的关键证据或待确认事实
	EvidenceGaps []string        `json:"evidence_gaps"`
	Stances      []refereeStance `json:"stances"`
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
	b.WriteString("3. goal_covered：当前子议题要产出的结论是否已被明确回答（未指定目标时与 converged 同义）。\n")
	b.WriteString("4. converged：只有各方核心分歧已消解，且不存在未被回应的明确反对点时才算 true。\n")
	b.WriteString("   不要因为双方互相附和、或只是重复表述就判定收敛；不要替任何一方假定同意。\n")
	b.WriteString("5. status：consensus（完全一致）/ partial（目标已覆盖但仍有分歧）/ no_consensus（未覆盖目标）。\n")
	b.WriteString("6. stances：抽取每位发言者的主张、置信度(0-1)、反对点、以及什么证据能让他改变立场。\n")
	b.WriteString("7. unresolved：列出仍未被回应的分歧点；没有则空数组。evidence_gaps：结论缺少的关键证据；没有则空数组。\n")
	b.WriteString("只输出如下 JSON 对象，不要输出其他任何内容：\n")
	b.WriteString(`{"status": "consensus|partial|no_consensus", "converged": true 或 false, ` +
		`"on_topic": true 或 false, "drift": true 或 false, "goal_covered": true 或 false, ` +
		`"reason": "判定依据（一句话）", "consensus": "若已收敛或部分收敛，写出各方当前能确认的结论；否则为空字符串", ` +
		`"unresolved": ["未解决的分歧点"], "evidence_gaps": ["缺失的关键证据"], ` +
		`"stances": [{"agent": "发言人", "position": "主张", "confidence": 0.0, "objections": ["反对点"], "would_change_if": "改变立场的条件"}]}`)
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
	// 机械前置：只有一人发过言，谈不上"经过交锋的共识"，直接判未收敛。
	// 这条规则专门防止 LLM 在单人表态后过早宣布收敛。
	if distinctAgentSpeakers(recent) < 2 {
		return refereeVerdict{}
	}
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
	normalizeRefereeVerdict(&v)
	return v
}

// distinctAgentSpeakers 统计最近发言中出现过的不同智能体数量（用户/主持人/系统不计）。
func distinctAgentSpeakers(msgs []*types.MeetingMessage) int {
	seen := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m.SenderKind == types.MeetingSenderAgent && m.SenderID != "" {
			seen[m.SenderID] = struct{}{}
		}
	}
	return len(seen)
}

// normalizeRefereeVerdict 用机械规则校正模型结论：
//   - 有未解决分歧时，不允许标记为"完全共识"；目标已覆盖则降级为"部分共识"；
//   - partial 一律不视为 converged，避免整场会议因"部分共识"提前终止。
func normalizeRefereeVerdict(v *refereeVerdict) {
	switch v.Status {
	case "consensus", "partial", "no_consensus":
	default:
		if v.Converged {
			v.Status = "consensus"
		} else {
			v.Status = "no_consensus"
		}
	}
	if len(v.Unresolved) > 0 && v.Status == "consensus" {
		if v.GoalCovered {
			v.Status = "partial"
		} else {
			v.Status = "no_consensus"
		}
	}
	if v.Status == "consensus" {
		v.Converged = true
	} else if v.Status == "partial" {
		v.Converged = false
	}
}

// consensusMessageContent 组装共识消息正文：优先裁判给出的一致结论，缺失时回退到含判定依据的兜底文案。
// 部分共识会把"未解决分歧/证据缺口"一并写出，避免把没谈拢的事包装成一致结论。
func consensusMessageContent(room *types.MeetingRoom, v refereeVerdict) string {
	title := "【共识】"
	if v.Status == "partial" {
		title = "【部分共识】"
	}
	var b strings.Builder
	switch {
	case strings.TrimSpace(v.Consensus) != "":
		b.WriteString(title + strings.TrimSpace(v.Consensus))
	case strings.TrimSpace(v.Reason) != "":
		fmt.Fprintf(&b, "%s讨论已收敛：%s", title, strings.TrimSpace(v.Reason))
	default:
		fmt.Fprintf(&b, "%s裁判判定讨论已围绕主题「%s」收敛。", title, effectiveTopic(room))
	}
	appendOpenIssues(&b, v)
	return b.String()
}

// appendOpenIssues 把未解决分歧与证据缺口追加到结论正文（没有则不加）。
func appendOpenIssues(b *strings.Builder, v refereeVerdict) {
	if len(v.Unresolved) > 0 {
		b.WriteString("\n未解决分歧：" + strings.Join(v.Unresolved, "；"))
	}
	if len(v.EvidenceGaps) > 0 {
		b.WriteString("\n待补证据：" + strings.Join(v.EvidenceGaps, "；"))
	}
}

// buildMeetingConclusion 汇总全部已完成议题，形成会议级结论。
// 这里做机械汇总而不是再调一次模型：议题小结已由裁判确认，避免二次生成引入新的事实。
func buildMeetingConclusion(
	items []*types.MeetingAgendaItem, current *types.MeetingAgendaItem, v refereeVerdict,
) string {
	var b strings.Builder
	b.WriteString("【会议结论】\n共识：")
	for _, it := range items {
		if it == nil {
			continue
		}
		if it.Status != types.MeetingAgendaDone && (current == nil || it.ID != current.ID) {
			continue
		}
		summary := strings.TrimSpace(it.Summary)
		if current != nil && it.ID == current.ID {
			summary = firstNonEmpty(v.Consensus, v.Reason)
		}
		if summary == "" {
			summary = "未形成明确结论"
		}
		fmt.Fprintf(&b, "\n- %s：%s", it.Title, summary)
	}
	if len(v.Unresolved) == 0 && len(v.EvidenceGaps) == 0 {
		b.WriteString("\n未解决分歧：无")
	} else {
		appendOpenIssues(&b, v)
	}
	return b.String()
}

// agendaRecap 生成议程小结正文（议题完成时落库，含本议题结论、未解决分歧与剩余的待讨论议题）
func agendaRecap(item *types.MeetingAgendaItem, remaining []*types.MeetingAgendaItem, v refereeVerdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【议题小结】%s\n", item.Title)
	if s := strings.TrimSpace(v.Consensus); s != "" {
		b.WriteString(s)
	} else if r := strings.TrimSpace(v.Reason); r != "" {
		b.WriteString(r)
	}
	appendOpenIssues(&b, v)
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
