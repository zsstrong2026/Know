package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrNoDiscussionMessages 房间内尚无讨论内容时拒绝生成方案（handler 判等映射 400）
var ErrNoDiscussionMessages = errors.New("meeting: no discussion messages")

// meetingSolutionContextMax 无议程时的兜底回溯条数（远小于历史全量，避免链式漂移）
const meetingSolutionContextMax = 20

// buildSolutionPrompt 组装主持人生成方案的提示词（纯函数，单测覆盖）。
// messages 为讨论记录（优先当前议题），progress 为议程进度摘要；分歧由模型从讨论记录中识别，
// 不再依赖「采纳/否决」标记——评审动作已从交互中移除。
func buildSolutionPrompt(
	room *types.MeetingRoom,
	messages []*types.MeetingMessage,
	progress string,
	nameOf func(agentID string) string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n会议描述：%s\n", effectiveTopic(room), room.Description)
	if progress != "" {
		b.WriteString("\n议程进度：\n" + progress + "\n")
	}
	fmt.Fprintf(&b, "\n以下是本次讨论的记录（共 %d 条，按时间顺序）：\n", len(messages))
	for i, m := range messages {
		fmt.Fprintf(&b, "%d. %s：%s\n", i+1, nameOf(m.SenderID), m.Content)
	}
	b.WriteString("\n请以主持人身份，将上述讨论汇总为一份结构化方案，固定包含以下小节：\n")
	b.WriteString("## 背景与目标\n## 分歧与未达成一致的点\n## 方案要点\n## 风险与待确认事项\n## 下一步行动\n")
	b.WriteString("\n要求：\n")
	b.WriteString("1. 「## 分歧与未达成一致的点」必须逐条列出讨论中尚未形成一致意见的点及各方立场，不得只写共识、不得省略该节；若确无分歧，须写明「本次讨论未出现分歧」并给出依据。\n")
	b.WriteString("2. 其余小节不得引入讨论记录之外的新观点；要点可溯源到对应发言；语言简明。")
	return b.String()
}

// collectSolutionContext 汇总方案生成所需的上下文：
// 优先取当前议题内的讨论记录，无议程或议题无记录时回退到房间最近消息；
// 议程进度用于替代全量历史，保持"按议题聚焦"的结构。
func (e *meetingEngine) collectSolutionContext(
	ctx context.Context,
	room *types.MeetingRoom,
) ([]*types.MeetingMessage, string) {
	var messages []*types.MeetingMessage
	progress := ""
	if room.AgendaEnabled && e.agendaRepo != nil {
		if items, err := e.agendaRepo.ListByRoom(ctx, room.ID); err == nil {
			progress = agendaProgressText(items)
		}
		if active, err := e.agendaRepo.Active(ctx, room.ID); err == nil && active != nil {
			if msgs, err := e.messageRepo.ListByAgendaItem(ctx, room.ID, active.ID, meetingAgendaContextMax); err == nil {
				messages = msgs
			}
		}
	}
	if len(messages) == 0 {
		if msgs, err := e.messageRepo.ListRecent(ctx, room.ID, meetingSolutionContextMax); err == nil {
			messages = msgs
		}
	}
	// 方案消息本身不是讨论内容；空内容无信息量
	out := make([]*types.MeetingMessage, 0, len(messages))
	for _, m := range messages {
		if m.IsSolution || strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, m)
	}
	return out, progress
}

// GenerateSolution 主持人基于讨论记录生成结构化方案：
// 落 is_solution 消息（版本递增）→ 写 meeting_artifacts → 更新房间当前方案指针 → 广播
func (e *meetingEngine) GenerateSolution(ctx context.Context, room *types.MeetingRoom, userID string) (*types.MeetingMessage, error) {
	messages, progress := e.collectSolutionContext(ctx, room)
	if len(messages) == 0 {
		return nil, ErrNoDiscussionMessages
	}

	// 进入收敛阶段（无阶段专属事件，事件只在方案落库后记录）
	e.advancePhase(ctx, room, types.MeetingPhaseConverging, "", types.MeetingSenderHost, userID, "", nil)
	// 生成中事件无配对终态：LLM 失败时前端 loading 由 HTTP 错误响应复位（见 T4.3）
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSolutionGenerating, Data: map[string]any{"room_id": room.ID}})

	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return nil, err
	}
	nameOf := func(agentID string) string { return e.agentDisplayName(ctx, room.ID, agentID) }
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: buildSolutionPrompt(room, messages, progress, nameOf)}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil {
		return nil, fmt.Errorf("host generate solution: %w", err)
	}

	// 产物行与方案消息共用 version 序列：以 meeting_artifacts 最大版本为准（UNIQUE(room_id, version)）
	version, err := e.artifactRepo.MaxVersionByRoom(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	version++
	msg := &types.MeetingMessage{
		MeetingRoomID:   room.ID,
		SenderKind:      types.MeetingSenderHost,
		Content:         resp.Content,
		IsSolution:      true,
		SolutionVersion: version,
		ReviewState:     types.ReviewStateNone,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return nil, err
	}

	// 产物元数据：正文不重复存储，经 solution_message_id 关联（统计失败不阻断）
	adoptedCount, _ := e.messageRepo.CountByReviewStates(ctx, room.ID, []string{types.ReviewStateAdopted})
	rejectedCount, _ := e.messageRepo.CountByReviewStates(ctx, room.ID, []string{types.ReviewStateRejected})
	artifact := &types.MeetingArtifact{
		MeetingRoomID:     room.ID,
		SolutionMessageID: msg.ID,
		Version:           version,
		AdoptedCount:      int(adoptedCount),
		RejectedCount:     int(rejectedCount),
		Kind:              types.MeetingArtifactKindSolution,
		Status:            types.MeetingArtifactStatusReady,
		CreatedBy:         userID,
	}
	if err := e.artifactRepo.Create(ctx, artifact); err != nil {
		return nil, err
	}

	room.CurrentSolutionMessageID = msg.ID
	if err := e.roomRepo.Update(ctx, room); err != nil {
		return nil, err
	}

	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSolutionCreated, Data: artifact})
	e.advancePhase(ctx, room, "", types.MeetingEventSolutionReady, types.MeetingSenderHost, userID,
		"方案已生成", map[string]any{"artifact_id": artifact.ID, "message_id": msg.ID, "version": version})
	return msg, nil
}
