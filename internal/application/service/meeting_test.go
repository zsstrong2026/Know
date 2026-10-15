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
	// 合法
	err = validateMeetingCreate(&types.MeetingRoom{Title: "x"}, []string{"a1"})
	assert.NoError(t, err)
	// 广播模式非法时回退默认
	room := &types.MeetingRoom{Title: "x", BroadcastMode: "bogus"}
	_ = validateMeetingCreate(room, []string{"a1"})
	assert.Equal(t, types.MeetingBroadcastSequential, room.BroadcastMode)
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
