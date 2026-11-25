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

// MeetingOpeningStage 开场消息 payload.stage 固定值（前端据此渲染「开始会议 / 调整流程」按钮组）
const MeetingOpeningStage = "opening"

// openingPayload 开场消息的 jsonb 载荷
type openingPayload struct {
	Stage string `json:"stage"`
	// NeedsClarification 主持人判定主题过于宽泛、需要用户补充说明；前端据此渲染澄清提示。
	// 非首次（调整流程 / 回答澄清）恒为 false（硬约束「最多 1 轮」）。
	NeedsClarification bool `json:"needs_clarification,omitempty"`
}

var (
	// openingMarkerTagRe 匹配 need_clarification / topic_brief 标记的前后标签 token（含属性容错），
	// 用于「仅去标签、保留内部文本」的落库前剥离。
	openingMarkerTagRe = regexp.MustCompile(`(?is)</?(?:need_clarification|topic_brief)[^>]*>`)
	// openingTopicBriefBodyRe 提取 <topic_brief> 包裹体（收敛后的一句话主题）。
	openingTopicBriefBodyRe = regexp.MustCompile(`(?is)<topic_brief[^>]*>(.*?)</topic_brief>`)
	// openingNeedClarificationTagRe 命中开标签即视为需要澄清（容忍模型漏写闭合标签）。
	openingNeedClarificationTagRe = regexp.MustCompile(`(?is)<need_clarification[^>]*>`)
	// openingClarifyHeadingRe 兜底：模型以「## 需要澄清的问题」小节表达澄清意图但漏写标记。
	openingClarifyHeadingRe = regexp.MustCompile(`(?m)^#{1,6}\s*需要澄清`)
)

// parseOpeningMarkers 落库前剥离标记 token（仅去标签、保留内部文本，前端无需感知标记），
// 返回干净正文、收敛主题（topic_brief 内容）与是否需要用户澄清。
func parseOpeningMarkers(content string) (clean string, topicBrief string, needsClarification bool) {
	if m := openingTopicBriefBodyRe.FindStringSubmatch(content); len(m) == 2 {
		topicBrief = strings.TrimSpace(m[1])
	}
	needsClarification = openingNeedClarificationTagRe.MatchString(content) ||
		openingClarifyHeadingRe.MatchString(content)
	clean = strings.TrimSpace(openingMarkerTagRe.ReplaceAllString(content, ""))
	return clean, topicBrief, needsClarification
}

// buildOpeningPrompt 组装主持人的开场提示词（纯函数，单测覆盖）。
// feedback 为空 = 创建后的首次开场（允许输出澄清问题）；
// 非空 = 用户「调整流程」或回答澄清后的重出（要求收敛 TopicBrief、禁止再澄清）。
func buildOpeningPrompt(room *types.MeetingRoom, agents []*types.CustomAgent, feedback string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是会议室主持人。会议主题：%s\n", room.Title)
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	if len(agents) > 0 {
		b.WriteString("参会智能体：\n")
		for _, a := range agents {
			line := "- " + a.Name
			if a.Description != "" {
				line += "：" + truncate(a.Description, 80)
			}
			b.WriteString(line + "\n")
		}
	}
	if strings.TrimSpace(feedback) == "" {
		// 首次开场：主题宽泛时引导用户补充关键约束（grill-me 风格：编号问题 + 推荐答案）
		b.WriteString("\n请以主持人身份输出开场发言，包含以下部分：\n")
		b.WriteString("## 主题分析\n对本次会议议题的理解与拆解（背景、要解决的核心问题、关键约束）。\n")
		b.WriteString("## 需要澄清的问题\n仅当主题过于宽泛、缺少关键约束（如目标、范围、时间、对象、评价标准）时输出本节：\n")
		b.WriteString("用 <need_clarification> 与 </need_clarification> 包裹 1-3 个编号问题，每个问题附上你的推荐答案（形如「1. 问题？推荐：xxx」）。\n")
		b.WriteString("若主题已足够明确、可直接讨论，请完全省略本节，不要输出 need_clarification 标记。\n")
		if room.AgendaEnabled {
			b.WriteString(buildAgendaPromptSection())
		}
		b.WriteString("## 建议流程\n给出分段/分轮的会议流程建议：每个阶段的讨论重点、希望哪些智能体先发言，以及需要用户确认或拍板的点。\n")
		b.WriteString("\n要求：语言简洁、面向普通用户；不要编造会议中不存在的材料；不要展开具体讨论内容（讨论在用户确认后开始）。")
		return b.String()
	}
	b.WriteString("\n用户对上一版会议流程与主题提出了修改或补充意见：\n" + feedback + "\n")
	b.WriteString("请结合该意见重新输出开场内容，包含以下部分：\n")
	b.WriteString("## 主题分析\n首行必须用 <topic_brief> 与 </topic_brief> 包裹收敛后的一句话会议主题（含明确目标与范围），随后是背景、核心问题与关键约束。\n")
	if room.AgendaEnabled {
		b.WriteString(buildAgendaPromptSection())
	}
	b.WriteString("## 建议流程\n给出分段/分轮的会议流程建议：每个阶段的讨论重点、希望哪些智能体先发言，以及需要用户确认或拍板的点。\n")
	b.WriteString("\n要求：本次不再追问，直接给出收敛结论（不要输出 need_clarification 标记）；语言简洁、面向普通用户；不要编造会议中不存在的材料。")
	return b.String()
}

// GenerateOpening 主持人生成开场消息并落库广播（message.created + discussion.flow_confirmation）。
// feedbackMessageID 为空 = 首次开场（允许输出澄清问题）；非空 = 「调整流程」或回答澄清后的重出。
// LLM 不可用/失败时落 system 兜底消息，房间保持在 awaiting_confirmation，用户可直接发言隐式开跑。
func (e *meetingEngine) GenerateOpening(ctx context.Context, room *types.MeetingRoom, feedbackMessageID string) error {
	feedback := ""
	if feedbackMessageID != "" {
		if msg, err := e.messageRepo.GetByID(ctx, feedbackMessageID); err == nil {
			feedback = msg.Content
		}
	}
	isInitial := strings.TrimSpace(feedback) == ""
	agents := e.loadRoomAgents(ctx, room)
	raw := e.generateOpeningContent(ctx, room, agents, feedback)
	if raw == "" {
		return nil // 兜底 system 消息已在 generateOpeningContent 内落库
	}
	// 剥离标记 token：落库内容已是干净的（含澄清问题正文，但不含标记），前端无需感知标记
	content, topicBrief, needsClarification := parseOpeningMarkers(raw)
	// 议程拆解：仅在启用议程且非澄清轮时落库（澄清轮还没有稳定主题，拆了也会被覆盖）
	var agendaItems []*types.MeetingAgendaItem
	if room.AgendaEnabled {
		if raw2, ok := parseAgenda(raw); ok && !needsClarification {
			agendaItems = toAgendaItems(room.ID, raw2, agents)
		}
		content = stripAgendaTag(content)
	}
	if content == "" {
		content = strings.TrimSpace(raw)
	}
	// 硬约束「最多 1 轮」：非首次（调整流程 / 回答澄清）一律不再追问
	if !isInitial {
		needsClarification = false
	}
	// 收敛主题回写房间，供后续议题聚焦；为空（用户跳过澄清）时保持原值，讨论回退 Title+Description
	if topicBrief != "" && room.TopicBrief != topicBrief {
		room.TopicBrief = topicBrief
		if err := e.roomRepo.Update(ctx, room); err != nil {
			logger.Warnf(ctx, "meeting topic brief persist failed, room=%s err=%v", room.ID, err)
		}
	}
	payload, err := json.Marshal(openingPayload{Stage: MeetingOpeningStage, NeedsClarification: needsClarification})
	if err != nil {
		return err
	}
	msg := &types.MeetingMessage{
		MeetingRoomID: room.ID,
		SenderKind:    types.MeetingSenderHost,
		Content:       content,
		Payload:       payload,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventFlowConfirmation, Data: map[string]interface{}{
		"room_id": room.ID, "message_id": msg.ID, "stage": MeetingOpeningStage,
	}})
	// 开场完成：阶段进入待确认；若随后自动开跑，RunTurn 会立即推进到 discussing
	e.advancePhase(ctx, room, types.MeetingPhaseConfirming, types.MeetingEventOpeningReady,
		types.MeetingSenderHost, "", "开场分析完成", map[string]any{
			"message_id": msg.ID, "needs_clarification": needsClarification, "agenda_items": len(agendaItems),
		})
	// 议程落库：首次开会用新增，调整流程/回答澄清用重建（覆盖旧议程）
	if len(agendaItems) > 0 {
		e.persistAgenda(ctx, room.ID, agendaItems, true)
		e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventAgendaUpdated, Data: map[string]interface{}{
			"room_id": room.ID, "items": agendaItems,
		}})
	}
	// 开场完成即自动开跑，用户不必再点一次「开始会议」。
	// 主持人若提出了澄清问题，用户仍可随时补充，该补充会作为会议中的插话进入讨论。
	e.maybeAutoStart(ctx, room, msg.ID)
	return nil
}

// maybeAutoStart 把待确认房间置为 active 并异步启动首轮讨论。
// 仅在房间仍处于 awaiting_confirmation 时生效，避免用户已手动开始/发言后重复触发；
// 无 Redis 的单测精简引擎直接跳过（讨论轮依赖分布式锁）。
func (e *meetingEngine) maybeAutoStart(ctx context.Context, room *types.MeetingRoom, openingMsgID string) {
	if room == nil || openingMsgID == "" || e.redis == nil {
		return
	}
	fresh, err := e.roomRepo.GetByIDInternal(ctx, room.ID)
	if err != nil || fresh == nil || fresh.Status != types.MeetingStatusAwaitingConfirmation {
		return
	}
	fresh.Status = types.MeetingStatusActive
	if err := e.roomRepo.Update(ctx, fresh); err != nil {
		logger.Warnf(ctx, "meeting auto start update failed, room=%s err=%v", room.ID, err)
		return
	}
	go func() {
		bgCtx := logger.CloneContext(ctx)
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(bgCtx, "meeting auto start panic, room=%s recover=%v", room.ID, r)
			}
		}()
		if err := e.RunTurn(bgCtx, room.ID, openingMsgID); err != nil {
			logger.Errorf(bgCtx, "meeting auto start RunTurn failed, room=%s err=%v", room.ID, err)
		}
	}()
}

// loadRoomAgents 加载参会智能体实体（开场/产物生成取名称与专长用）
func (e *meetingEngine) loadRoomAgents(ctx context.Context, room *types.MeetingRoom) []*types.CustomAgent {
	participants, err := e.participantRepo.ListByRoom(ctx, room.ID)
	if err != nil {
		return nil
	}
	out := make([]*types.CustomAgent, 0)
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" {
			continue
		}
		if agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID); err == nil {
			out = append(out, agent)
		}
	}
	return out
}

// generateOpeningContent 主持人生成开场正文；模型不可用/生成失败时落 system 兜底消息并返回空串
func (e *meetingEngine) generateOpeningContent(ctx context.Context, room *types.MeetingRoom, agents []*types.CustomAgent, feedback string) string {
	hostChat, err := e.resolveHostChat(ctx)
	if err == nil {
		resp, cerr := hostChat.Chat(ctx,
			[]chat.Message{{Role: "user", Content: buildOpeningPrompt(room, agents, feedback)}},
			&chat.ChatOptions{Temperature: 0.3})
		if cerr != nil {
			logger.Warnf(ctx, "meeting opening generate failed, room=%s err=%v", room.ID, cerr)
		} else if resp != nil && strings.TrimSpace(resp.Content) != "" {
			return resp.Content
		}
	} else {
		logger.Warnf(ctx, "meeting opening host model unavailable, room=%s err=%v", room.ID, err)
	}
	e.publishSystemMessage(ctx, room.ID, "主持人开场生成失败，可直接发言开始会议")
	return ""
}
