package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEnsureRoomActive(t *testing.T) {
	active := &types.MeetingRoom{Status: types.MeetingStatusActive}
	assert.NoError(t, ensureRoomActive(active))

	archived := &types.MeetingRoom{Status: types.MeetingStatusArchived}
	err := ensureRoomActive(archived)
	assert.Equal(t, ErrRoomArchived, err) // 哨兵判等：只以原值返回、从不 wrap
}
