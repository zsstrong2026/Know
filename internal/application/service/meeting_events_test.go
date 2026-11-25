package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type stubEventRepo struct {
	events []*types.MeetingEvent
	fail   bool
}

func (s *stubEventRepo) Append(_ context.Context, ev *types.MeetingEvent) error {
	if s.fail {
		return assert.AnError
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *stubEventRepo) ListByRoom(_ context.Context, _ string, _ int) ([]*types.MeetingEvent, error) {
	return s.events, nil
}

func TestRecordMeetingEvent(t *testing.T) {
	repo := &stubEventRepo{}
	room := &types.MeetingRoom{ID: "room-1", TenantID: 7, Phase: types.MeetingPhaseDiscussing}

	recordMeetingEvent(context.Background(), repo, room, "", types.MeetingEventTurnStarted, "", "", "讨论轮开始",
		map[string]any{"trigger_message_id": "m1"})

	require.Len(t, repo.events, 1)
	ev := repo.events[0]
	assert.Equal(t, "room-1", ev.MeetingRoomID)
	assert.Equal(t, uint64(7), ev.TenantID)
	assert.Equal(t, types.MeetingPhaseDiscussing, ev.Phase, "phase 为空时沿用房间当前阶段")
	assert.Equal(t, types.MeetingSenderSystem, ev.ActorKind, "actor 为空时兜底 system")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(ev.Payload, &payload))
	assert.Equal(t, "m1", payload["trigger_message_id"])
}

func TestRecordMeetingEvent_SkipAndTolerate(t *testing.T) {
	room := &types.MeetingRoom{ID: "room-1", TenantID: 7}

	// 仓储为空（精简引擎/单测）时静默跳过，不 panic
	recordMeetingEvent(context.Background(), nil, room, "", types.MeetingEventTurnStarted, "", "", "", nil)

	// 房间缺失或事件类型为空时不落库
	repo := &stubEventRepo{}
	recordMeetingEvent(context.Background(), repo, nil, "", types.MeetingEventTurnStarted, "", "", "", nil)
	recordMeetingEvent(context.Background(), repo, room, "", "", "", "", "", nil)
	assert.Empty(t, repo.events)

	// 无 payload 时写空对象而不是 nil：payload 列 NOT NULL，nil 会被 GORM 写成 NULL 导致落库失败
	recordMeetingEvent(context.Background(), repo, room, types.MeetingPhaseArchived, types.MeetingEventRoomArchived, "", "", "", nil)
	require.Len(t, repo.events, 1)
	assert.JSONEq(t, "{}", string(repo.events[0].Payload))

	// 事件写入失败只告警，不影响调用方（此处断言不 panic 且无返回错误）
	recordMeetingEvent(context.Background(), &stubEventRepo{fail: true}, room, "", types.MeetingEventTurnEnded, "", "", "", nil)
}
