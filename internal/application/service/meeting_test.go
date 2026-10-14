package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
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
