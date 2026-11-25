package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// advanceMeetingPhase 推进房间显式阶段，并追加一条会议事件。
//
// 约定：
//   - phase 为空表示不改变阶段，只记事件；
//   - 阶段未变化时不重复写房间行，只追加事件（事件表是审计事实来源）；
//   - 事件写入失败仅告警，绝不阻断会议主流程（可观测性不能反向影响可用性）。
func advanceMeetingPhase(
	ctx context.Context,
	roomRepo interfaces.MeetingRoomRepository,
	eventRepo interfaces.MeetingEventRepository,
	room *types.MeetingRoom,
	phase, eventType, actorKind, actorID, summary string,
	payload map[string]any,
) {
	if roomRepo != nil && room != nil && phase != "" && room.Phase != phase {
		room.Phase = phase
		// 只更新 phase 列：阶段推进不应整行覆盖并发写入的 status/方案指针等字段
		if err := roomRepo.UpdatePhase(ctx, room.ID, phase); err != nil {
			logger.Warnf(ctx, "meeting phase update failed, room=%s phase=%s err=%v", room.ID, phase, err)
		}
	}
	recordMeetingEvent(ctx, eventRepo, room, phase, eventType, actorKind, actorID, summary, payload)
}

// recordMeetingEvent 追加一条会议事件；仓储为空时静默跳过（单测/精简引擎场景）。
func recordMeetingEvent(
	ctx context.Context,
	eventRepo interfaces.MeetingEventRepository,
	room *types.MeetingRoom,
	phase, eventType, actorKind, actorID, summary string,
	payload map[string]any,
) {
	if eventRepo == nil || room == nil || room.ID == "" || eventType == "" {
		return
	}
	if phase == "" {
		phase = room.Phase
	}
	if actorKind == "" {
		actorKind = types.MeetingSenderSystem
	}
	ev := &types.MeetingEvent{
		TenantID:      room.TenantID,
		MeetingRoomID: room.ID,
		Phase:         phase,
		Type:          eventType,
		ActorKind:     actorKind,
		ActorID:       actorID,
		Summary:       summary,
		// payload 列为 NOT NULL：无结构化上下文时也必须写空对象，不能留 nil（GORM 会显式写 NULL）
		Payload: []byte("{}"),
	}
	if len(payload) > 0 {
		if b, err := json.Marshal(payload); err == nil {
			ev.Payload = b
		}
	}
	if err := eventRepo.Append(ctx, ev); err != nil {
		logger.Warnf(ctx, "meeting event append failed, room=%s type=%s err=%v", room.ID, eventType, err)
	}
}

// advancePhase 引擎侧快捷方式：复用同一套阶段推进与事件写入规则。
func (e *meetingEngine) advancePhase(
	ctx context.Context,
	room *types.MeetingRoom,
	phase, eventType, actorKind, actorID, summary string,
	payload map[string]any,
) {
	advanceMeetingPhase(ctx, e.roomRepo, e.eventRepo, room, phase, eventType, actorKind, actorID, summary, payload)
}
