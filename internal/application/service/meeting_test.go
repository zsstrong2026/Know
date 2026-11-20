package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestValidateMeetingCreate(t *testing.T) {
	// 标题必填
	err := validateMeetingCreate(&types.MeetingRoom{}, []string{"a1"})
	assert.Error(t, err)
	// 至少 1 个智能体
	err = validateMeetingCreate(&types.MeetingRoom{Title: "x"}, nil)
	assert.Error(t, err)
	// 合法：无论请求里填了什么治理参数，一律套用内置默认
	room := &types.MeetingRoom{
		Title: "x", BroadcastMode: "bogus", RoutingMode: types.MeetingRoutingFree,
		HostEveryNRounds: 5, ConsensusDetectionEnabled: false, AgendaEnabled: false,
		AdmissionMode: types.MeetingAdmissionOff, WebSearchEnabled: false,
		KnowledgeBaseIDs: []string{"kb1"},
	}
	err = validateMeetingCreate(room, []string{"a1"})
	assert.NoError(t, err)
	assert.Equal(t, types.MeetingBroadcastSequential, room.BroadcastMode)
	assert.Equal(t, types.MeetingRoutingHostAssign, room.RoutingMode)
	assert.Equal(t, 3, room.GuardMaxExchange)
	assert.Equal(t, 0, room.HostEveryNRounds)
	assert.True(t, room.ConsensusDetectionEnabled)
	assert.True(t, room.AgendaEnabled)
	assert.Equal(t, types.MeetingAdmissionObserve, room.AdmissionMode)
	assert.True(t, room.WebSearchEnabled)
	assert.Nil(t, room.KnowledgeBaseIDs) // 会议级知识库下线
	assert.Equal(t, types.MeetingModeHosted, room.Mode, "未指定模式默认主持模式")
	assert.Equal(t, types.MeetingStatusAwaitingConfirmation, room.Status)
}

// TestValidateMeetingCreateFreeMode 自由模式：无主持人治理，创建即进入讨论
func TestValidateMeetingCreateFreeMode(t *testing.T) {
	room := &types.MeetingRoom{Title: "x", Mode: types.MeetingModeFree}
	err := validateMeetingCreate(room, []string{"a1"})
	assert.NoError(t, err)
	assert.Equal(t, types.MeetingModeFree, room.Mode)
	assert.Equal(t, types.MeetingRoutingFree, room.RoutingMode)
	assert.False(t, room.AgendaEnabled, "自由模式不拆议题")
	assert.False(t, room.ConsensusDetectionEnabled, "自由模式不做裁判收敛")
	assert.Equal(t, types.MeetingAdmissionOff, room.AdmissionMode)
	assert.Equal(t, 0, room.HostEveryNRounds)
	assert.Equal(t, types.MeetingStatusActive, room.Status, "自由模式没有开场待确认环节")

	// 非法模式归一为主持模式
	invalid := &types.MeetingRoom{Title: "x", Mode: "bogus"}
	assert.NoError(t, validateMeetingCreate(invalid, []string{"a1"}))
	assert.Equal(t, types.MeetingModeHosted, invalid.Mode)
}

// TestApplyMeetingRuleDefaultsPreservesStatus 旧会议升级只重置治理规则，不改变状态/标题
func TestApplyMeetingRuleDefaultsPreservesStatus(t *testing.T) {
	room := &types.MeetingRoom{
		Title: "旧会议", Status: types.MeetingStatusActive,
		ConsensusDetectionEnabled: false, AgendaEnabled: false,
		AdmissionMode: types.MeetingAdmissionOff, WebSearchEnabled: false,
		KnowledgeBaseIDs: []string{"kb1"},
	}
	ApplyMeetingRuleDefaults(room)
	assert.Equal(t, "旧会议", room.Title)
	assert.Equal(t, types.MeetingStatusActive, room.Status)
	assert.True(t, room.ConsensusDetectionEnabled)
	assert.True(t, room.AgendaEnabled)
	assert.True(t, room.WebSearchEnabled)
	assert.Equal(t, types.MeetingAdmissionObserve, room.AdmissionMode)
	assert.Nil(t, room.KnowledgeBaseIDs)

	// 自由模式旧会议升级：保持无主持人治理，且不改状态
	free := &types.MeetingRoom{
		Title: "自由会议", Mode: types.MeetingModeFree, Status: types.MeetingStatusActive,
		AgendaEnabled: true, ConsensusDetectionEnabled: true,
	}
	ApplyMeetingRuleDefaults(free)
	assert.Equal(t, types.MeetingStatusActive, free.Status)
	assert.False(t, free.AgendaEnabled)
	assert.False(t, free.ConsensusDetectionEnabled)
	assert.Equal(t, types.MeetingRoutingFree, free.RoutingMode)
}

// stubRoomRepo 仅覆写 GetByID；其余方法由嵌入接口提供（本测试不会调用）
type stubRoomRepo struct {
	interfaces.MeetingRoomRepository
	room *types.MeetingRoom
	err  error
}

func (s *stubRoomRepo) GetByID(_ context.Context, _ string, _ uint64) (*types.MeetingRoom, error) {
	return s.room, s.err
}

// stubEngine 记录 Interrupt 是否被调用并可返回预设错误
type stubEngine struct {
	interfaces.MeetingEngine
	called   bool
	calledID string
	err      error
}

func (s *stubEngine) Interrupt(_ context.Context, roomID string) error {
	s.called = true
	s.calledID = roomID
	return s.err
}

func TestInterruptDiscussion(t *testing.T) {
	// 房间不存在：回传仓储错误，且不得调用引擎
	repoErr := errors.New("room not found")
	eng := &stubEngine{}
	svc := &meetingService{roomRepo: &stubRoomRepo{err: repoErr}, engine: eng}
	err := svc.InterruptDiscussion(context.Background(), 1, "room-x")
	assert.ErrorIs(t, err, repoErr)
	assert.False(t, eng.called)

	// 房间存在：调用引擎并透传其返回值（Status 显式 active：归档守卫只放行进行中会议）
	engErr := errors.New("interrupt failed")
	eng2 := &stubEngine{err: engErr}
	svc2 := &meetingService{roomRepo: &stubRoomRepo{room: &types.MeetingRoom{ID: "room-2", Status: types.MeetingStatusActive}}, engine: eng2}
	err = svc2.InterruptDiscussion(context.Background(), 1, "room-2")
	assert.ErrorIs(t, err, engErr)
	assert.True(t, eng2.called)
	assert.Equal(t, "room-2", eng2.calledID)
}
