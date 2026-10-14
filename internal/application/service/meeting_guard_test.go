package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDetectBickering(t *testing.T) {
	mk := func(id, sender string) *types.MeetingMessage {
		return &types.MeetingMessage{ID: id, SenderID: sender, SenderKind: types.MeetingSenderAgent}
	}
	ug := func(id string) *types.MeetingMessage {
		return &types.MeetingMessage{ID: id, SenderID: "user-1", SenderKind: types.MeetingSenderUser}
	}
	max := 3 // 与 guard_max_exchange 对齐

	// 场景 1：A/B 连续 3 来回，无用户消息 → 触发
	msgs := []*types.MeetingMessage{
		ug("u1"),
		mk("m1", "agent-A"), mk("m2", "agent-B"),
		mk("m3", "agent-A"), mk("m4", "agent-B"),
		mk("m5", "agent-A"), mk("m6", "agent-B"),
	}
	hit, pair := detectBickering(msgs, max)
	assert.True(t, hit)
	assert.Equal(t, [2]string{"agent-A", "agent-B"}, pair)

	// 场景 2：中间插了用户消息 → 计数重置，不触发
	msgs2 := []*types.MeetingMessage{
		mk("m1", "agent-A"), mk("m2", "agent-B"),
		mk("m3", "agent-A"), mk("m4", "agent-B"),
		ug("u1"),
		mk("m5", "agent-A"), mk("m6", "agent-B"),
	}
	hit, _ = detectBickering(msgs2, max)
	assert.False(t, hit)

	// 场景 3：三方轮换（A/B/C 各说一次）不构成"同一对" → 不触发
	msgs3 := []*types.MeetingMessage{
		mk("m1", "agent-A"), mk("m2", "agent-B"),
		mk("m3", "agent-C"), mk("m4", "agent-A"),
		mk("m5", "agent-B"), mk("m6", "agent-C"),
	}
	hit, _ = detectBickering(msgs3, max)
	assert.False(t, hit)

	// 场景 4：只有 2 个来回（未到阈值）→ 不触发
	hit, _ = detectBickering(msgs[:5], max) // u1 + m1..m4 → 尾段仅 4 条
	assert.False(t, hit)

	// 场景 5：阈值 1 时，任一对一个来回即触发（边界）
	hit, pair = detectBickering([]*types.MeetingMessage{mk("a", "s1"), mk("b", "s2")}, 1)
	assert.True(t, hit)
	assert.Equal(t, [2]string{"s1", "s2"}, pair)
}
