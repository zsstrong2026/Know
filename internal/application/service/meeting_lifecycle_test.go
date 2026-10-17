package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEnsureRoomActive(t *testing.T) {
	active := &types.MeetingRoom{Status: types.MeetingStatusActive}
	assert.NoError(t, ensureRoomActive(active))

	// M1：待确认状态视为可写（隐式确认/调整流程均发生在该状态），仅 archived 只读
	awaiting := &types.MeetingRoom{Status: types.MeetingStatusAwaitingConfirmation}
	assert.NoError(t, ensureRoomActive(awaiting))

	archived := &types.MeetingRoom{Status: types.MeetingStatusArchived}
	err := ensureRoomActive(archived)
	assert.Equal(t, ErrRoomArchived, err) // 哨兵判等：只以原值返回、从不 wrap
}
