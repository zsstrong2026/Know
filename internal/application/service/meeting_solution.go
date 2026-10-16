package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrNoAdoptedMessages 无已采纳消息时拒绝生成方案（handler 判等映射 400）
var ErrNoAdoptedMessages = errors.New("meeting: no adopted messages")

// buildSolutionPrompt 组装主持人生成方案的提示词（纯函数，单测覆盖）
// adopted 为被用户采纳的意见，rejected 为被用户否决的意见（代表尚未达成一致的一方立场）。
func buildSolutionPrompt(room *types.MeetingRoom, adopted, rejected []*types.MeetingMessage, nameOf func(agentID string) string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "会议主题：%s\n会议描述：%s\n\n以下是本次讨论中被用户采纳的意见（共 %d 条）：\n", room.Title, room.Description, len(adopted))
	for i, m := range adopted {
		fmt.Fprintf(&b, "%d. %s：%s\n", i+1, nameOf(m.SenderID), m.Content)
	}
	// 否决意见是"分歧清单"的事实来源：只输出共识的纪要是废纸，分歧才是决策价值所在
	fmt.Fprintf(&b, "\n以下是本次讨论中被用户否决的意见（共 %d 条），它们代表尚未达成一致的一方立场：\n", len(rejected))
	for i, m := range rejected {
		fmt.Fprintf(&b, "%d. %s：%s\n", i+1, nameOf(m.SenderID), m.Content)
	}
	b.WriteString("\n请以主持人身份，将上述意见汇总为一份结构化方案，固定包含以下小节：\n")
	b.WriteString("## 背景与目标\n## 分歧与未达成一致的点\n## 方案要点\n## 风险与待确认事项\n## 下一步行动\n")
	b.WriteString("\n要求：\n")
	b.WriteString("1. 「## 分歧与未达成一致的点」必须逐条列出尚未形成一致意见的点及各方立场，不得只写共识、不得省略该节；若确无分歧，须写明「本次讨论未出现分歧」并给出依据。\n")
	b.WriteString("2. 其余小节不得引入采纳意见之外的新观点；要点可溯源到对应意见；语言简明。")
	return b.String()
}

// GenerateSolution 主持人基于已采纳意见生成结构化方案：
// 落 is_solution 消息（版本递增）→ 写 meeting_artifacts → 更新房间当前方案指针 → 广播
func (e *meetingEngine) GenerateSolution(ctx context.Context, room *types.MeetingRoom, userID string) (*types.MeetingMessage, error) {
	adopted, err := e.messageRepo.ListByReviewState(ctx, room.ID, types.ReviewStateAdopted)
	if err != nil {
		return nil, err
	}
	if len(adopted) == 0 {
		return nil, ErrNoAdoptedMessages
	}
	// 已否决意见用于生成"分歧与未达成一致的点"，查询失败不阻断方案生成（分歧节将缺省为空）
	rejected, err := e.messageRepo.ListByReviewState(ctx, room.ID, types.ReviewStateRejected)
	if err != nil {
		rejected = nil
	}
	// 生成中事件无配对终态：LLM 失败时前端 loading 由 HTTP 错误响应复位（见 T4.3）
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSolutionGenerating, Data: map[string]any{"room_id": room.ID}})

	hostChat, err := e.resolveHostChat(ctx)
	if err != nil {
		return nil, err
	}
	nameOf := func(agentID string) string { return e.agentDisplayName(ctx, room.ID, agentID) }
	resp, err := hostChat.Chat(ctx, []chat.Message{{Role: "user", Content: buildSolutionPrompt(room, adopted, rejected, nameOf)}}, &chat.ChatOptions{Temperature: 0.3})
	if err != nil {
		return nil, fmt.Errorf("host generate solution: %w", err)
	}

	version, err := e.messageRepo.MaxSolutionVersion(ctx, room.ID)
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
	return msg, nil
}
