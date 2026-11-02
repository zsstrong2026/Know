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

// 发言准入 Gate：把"是否跑题"从事后收敛（裁判）提前到发布前判定。
//
// 三档模式（room.AdmissionMode）：
//   - off     ：不判定（默认，行为与改造前一致）；
//   - observe ：只判定并记录结论（admission_state/note），发言照常发布，用于灰度调参；
//   - enforce ：判定未通过时带着"未通过原因"让智能体重写一次；重写结果照常发布并打标，
//               不静默丢弃（避免误杀有价值的分歧）。
//
// 判定由规则层 + 模型层组成：规则层先过滤极短/空回复（无需模型调用），
// 模型层用低温度单次调用输出结构化结论。

// admissionVerdict 模型层判定结论（JSON 契约）
type admissionVerdict struct {
	// OnTopic 是否围绕当前子议题
	OnTopic bool `json:"on_topic"`
	// AddsInfo 相比已有本议题讨论是否有新增信息
	AddsInfo bool `json:"adds_info"`
	// Reason 一句话判定依据（未通过时用于重写提示与运维观测）
	Reason string `json:"reason"`
}

var admissionJSONRe = regexp.MustCompile(`(?s)\{.*\}`)

// buildAdmissionPrompt 组装准入判定提示词（纯函数，单测覆盖）
func buildAdmissionPrompt(topic string, item *types.MeetingAgendaItem, recent []*types.MeetingMessage, answer string) string {
	var b strings.Builder
	b.WriteString("你是会议发言审核员，只判断一条发言是否切题，不评价观点对错。\n")
	b.WriteString("会议主题：" + topic + "\n")
	if item != nil {
		b.WriteString("当前子议题：" + item.Title + "\n")
		if item.Goal != "" {
			b.WriteString("本议题要产出的结论：" + item.Goal + "\n")
		}
	}
	if len(recent) > 0 {
		b.WriteString("\n本议题已有发言（截取）：\n")
		for _, m := range recent {
			fmt.Fprintf(&b, "- %s\n", truncate(m.Content, 200))
		}
	}
	b.WriteString("\n待判定发言：\n" + truncate(answer, 1500) + "\n")
	b.WriteString("\n判定标准：\n")
	b.WriteString("1. on_topic：发言是否在讨论当前子议题（或推进本议题要产出的结论）；仅是泛泛表态、闲聊、或转去其他子议题，判为 false。\n")
	b.WriteString("2. adds_info：发言是否带来已有发言中没有的信息（新事实、新约束、新方案、明确的分歧理由）；仅复述他人或附和而无新增内容，判为 false。\n")
	b.WriteString("3. 有实质分歧的反驳应判为切题且有新增信息。\n")
	b.WriteString("只输出如下 JSON，不要输出其他任何内容：\n")
	b.WriteString(`{"on_topic": true 或 false, "adds_info": true 或 false, "reason": "一句话依据"}`)
	return b.String()
}

// parseAdmissionVerdict 容错解析判定 JSON（裸 JSON 或 ```json 围栏）；失败返回 ok=false
func parseAdmissionVerdict(output string) (admissionVerdict, bool) {
	text := strings.TrimSpace(output)
	if m := admissionJSONRe.FindString(text); m != "" {
		text = m
	}
	var v admissionVerdict
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return admissionVerdict{}, false
	}
	return v, true
}

// admits 判定结论是否放行：切题且有新增信息才通过。
// 判定失败（模型不可用/输出不可解析）一律按通过处理（fail-open），避免治理机制反过来阻断会议。
func (v admissionVerdict) admits() bool { return v.OnTopic && v.AddsInfo }

// admitAnswer 对一条发言执行准入判定，返回（最终内容，判定状态，判定说明）。
// 关闭或未启用议程时原样返回；enforce 未通过时携带原因重写一次。
func (e *meetingEngine) admitAnswer(
	ctx context.Context, room *types.MeetingRoom, agendaItemID string,
	agent *types.CustomAgent, answer string,
) (string, string, string) {
	mode := room.AdmissionMode
	if mode == "" || mode == types.MeetingAdmissionOff || !room.AgendaEnabled || e.agendaRepo == nil {
		return answer, types.MeetingAdmissionNone, ""
	}
	// 规则层：空回复与过短回复直接放行（无需浪费一次模型调用）
	if len(strings.TrimSpace(answer)) < 20 {
		return answer, types.MeetingAdmissionPassed, ""
	}
	items, _ := e.agendaRepo.ListByRoom(ctx, room.ID)
	var active *types.MeetingAgendaItem
	for _, it := range items {
		if it.ID == agendaItemID {
			active = it
			break
		}
	}
	recent, _ := e.messageRepo.ListByAgendaItem(ctx, room.ID, agendaItemID, 5)
	verdict, ok := e.judgeAdmission(ctx, room, active, recent, answer)
	if !ok {
		return answer, types.MeetingAdmissionPassed, "" // fail-open
	}
	if verdict.admits() {
		return answer, types.MeetingAdmissionPassed, ""
	}
	note := strings.TrimSpace(verdict.Reason)
	if mode == types.MeetingAdmissionObserve {
		logger.Infof(ctx, "[meeting] admission observed off-topic answer (room=%s item=%s agent=%s): %s",
			room.ID, agendaItemID, agent.ID, note)
		return answer, types.MeetingAdmissionObserved, note
	}
	// enforce：带着未通过原因重写一次；重写结果直接发布并标记 rewritten
	// （不再复判，避免同一发言叠加两次额外模型调用；原始未通过原因保留在 admission_note）
	rewritten, rerr := e.rewriteAnswer(ctx, room, active, agent, answer, note)
	if rerr != nil || strings.TrimSpace(rewritten) == "" {
		logger.Warnf(ctx, "[meeting] admission rewrite failed (room=%s agent=%s): %v", room.ID, agent.ID, rerr)
		return answer, types.MeetingAdmissionObserved, note
	}
	if isPassReply(rewritten) {
		// 重写后自愿弃权：视作无增量，交给上层按 PASS 处理
		return rewritten, types.MeetingAdmissionRewritten, note
	}
	return rewritten, types.MeetingAdmissionRewritten, note
}

// judgeAdmission 调用模型层判定；模型不可用或解析失败返回 ok=false（调用方按放行处理）
func (e *meetingEngine) judgeAdmission(
	ctx context.Context, room *types.MeetingRoom, item *types.MeetingAgendaItem,
	recent []*types.MeetingMessage, answer string,
) (admissionVerdict, bool) {
	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return admissionVerdict{}, false
	}
	prompt := buildAdmissionPrompt(effectiveTopic(room), item, recent, answer)
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{Temperature: 0})
	if err != nil || resp == nil {
		return admissionVerdict{}, false
	}
	return parseAdmissionVerdict(resp.Content)
}

// rewriteAnswer 要求智能体按未通过原因重写一次发言
func (e *meetingEngine) rewriteAnswer(
	ctx context.Context, room *types.MeetingRoom, item *types.MeetingAgendaItem,
	agent *types.CustomAgent, answer string, reason string,
) (string, error) {
	meetingAgent := e.cloneAgentForMeeting(agent, room)
	kbIDs := unionKnowledgeBaseIDs(meetingAgent.Config.KBSelectionMode, meetingAgent.Config.KnowledgeBases, room.KnowledgeBaseIDs)
	var b strings.Builder
	b.WriteString("【发言需重写】你上一条发言未通过会议准入检查。\n")
	b.WriteString("会议主题：" + effectiveTopic(room) + "\n")
	if item != nil {
		b.WriteString("当前子议题：" + item.Title + "\n")
		if item.Goal != "" {
			b.WriteString("本议题要产出的结论：" + item.Goal + "\n")
		}
	}
	b.WriteString("未通过原因：" + reason + "\n")
	b.WriteString("\n你上一条发言：\n" + truncate(answer, 1200) + "\n")
	b.WriteString("\n请重写：只围绕当前子议题，给出不超过 3 条核心观点，必须包含已有讨论中没有的新信息（新事实/新约束/新方案/明确分歧理由）；")
	b.WriteString("若确实没有新增内容，只回复 PASS。不要解释为什么重写。")
	return e.runAgentOnce(ctx, room, meetingAgent, b.String(), kbIDs)
}
