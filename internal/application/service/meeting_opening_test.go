package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// jsonMarshalForTest 直接透传标准库 Marshal，便于契约测试断言 payload 形状
func jsonMarshalForTest(v any) ([]byte, error) { return json.Marshal(v) }

func TestBuildOpeningPrompt(t *testing.T) {
	room := &types.MeetingRoom{ID: "r1", Title: "发布评审", Description: "评估 v2 发布风险"}
	agents := []*types.CustomAgent{
		{ID: "a1", Name: "架构师", Description: "负责系统架构"},
		{ID: "a2", Name: "测试专家"},
	}

	p := buildOpeningPrompt(room, agents, "")
	assert.Contains(t, p, "发布评审")
	assert.Contains(t, p, "评估 v2 发布风险")
	assert.Contains(t, p, "架构师")
	assert.Contains(t, p, "负责系统架构", "开场应带智能体专长供主持人规划流程")
	assert.Contains(t, p, "## 主题分析")
	assert.Contains(t, p, "## 建议流程")
	assert.NotContains(t, p, "修改意见", "首次开场不含调整反馈段")

	p2 := buildOpeningPrompt(room, nil, "先讨论性能再谈兼容性")
	assert.Contains(t, p2, "先讨论性能再谈兼容性")
	assert.Contains(t, p2, "重新输出开场内容", "调整流程模式要求主持人结合意见重出流程")
}

func TestOpeningPayloadMarshal(t *testing.T) {
	// payload 固定为 {"stage":"opening"}，前端据此渲染按钮组（契约测试）
	p := openingPayload{Stage: MeetingOpeningStage}
	b, err := jsonMarshalForTest(p)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(b), `"stage":"opening"`), "got %s", string(b))
}

func TestLatestOpeningMessageID(t *testing.T) {
	// 覆盖真实方法 latestOpeningMessageID：仓储返回正序（旧 -> 新），方法倒序取最近一条开场消息
	svc := &meetingService{messageRepo: &fakeMeetingMessageRepo{preset: []*types.MeetingMessage{
		{ID: "m1", Payload: []byte(`{"stage":"opening"}`)},
		{ID: "m2", Payload: []byte(`{}`)},
		{ID: "m3", Payload: []byte(`{"stage":"opening"}`)},
	}}}
	assert.Equal(t, "m3", svc.latestOpeningMessageID(context.Background(), "r1"))

	// 无开场消息时返回空串（供 ConfirmFlow 判断不触发 RunTurn）
	empty := &meetingService{messageRepo: &fakeMeetingMessageRepo{}}
	assert.Equal(t, "", empty.latestOpeningMessageID(context.Background(), "r1"))
}

func TestConfirmFlowMatrix(t *testing.T) {
	ctx := context.Background()

	newFixture := func(status string) (*meetingService, *isolationRoomRepo, *stubConfirmEngine) {
		rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: status}))
		engine := &stubConfirmEngine{}
		svc := &meetingService{
			roomRepo:    rooms,
			messageRepo: &fakeMeetingMessageRepo{},
			engine:      engine,
		}
		return svc, rooms, engine
	}

	t.Run("active 幂等", func(t *testing.T) {
		svc, rooms, engine := newFixture(types.MeetingStatusActive)
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, types.MeetingStatusActive, rooms.rooms["r1"].Status)
		assert.Equal(t, 0, engine.turns(), "已激活房间不重复触发讨论轮")
	})

	t.Run("archived 拒绝", func(t *testing.T) {
		svc, _, engine := newFixture(types.MeetingStatusArchived)
		assert.Equal(t, ErrRoomArchived, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, 0, engine.turns())
	})

	t.Run("待确认 → active（无开场消息时不空转触发）", func(t *testing.T) {
		svc, rooms, engine := newFixture(types.MeetingStatusAwaitingConfirmation)
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		assert.Equal(t, types.MeetingStatusActive, rooms.rooms["r1"].Status)
		waitFor(t, func() bool { return engine.turns() == 0 }, "无开场消息不应触发 RunTurn")
	})

	t.Run("跨租户拒绝", func(t *testing.T) {
		svc, _, engine := newFixture(types.MeetingStatusAwaitingConfirmation)
		assert.Error(t, svc.ConfirmFlow(ctx, 2, "r1"))
		assert.Equal(t, 0, engine.turns())
	})

	t.Run("待确认且有开场消息 → 触发首轮", func(t *testing.T) {
		rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusAwaitingConfirmation}))
		engine := &stubConfirmEngine{}
		svc := &meetingService{
			roomRepo: rooms,
			messageRepo: &fakeMeetingMessageRepo{preset: []*types.MeetingMessage{
				{ID: "open-1", MeetingRoomID: "r1", SenderKind: types.MeetingSenderHost, Payload: []byte(`{"stage":"opening"}`)},
			}},
			engine: engine,
		}
		assert.NoError(t, svc.ConfirmFlow(ctx, 1, "r1"))
		waitFor(t, func() bool { return engine.turns() == 1 }, "应以最近开场消息为触发异步开跑")
	})
}

// stubConfirmEngine 记录 RunTurn 触发次数（触发消息为最近开场；无开场时不触发）。
// RunTurn 在后台 goroutine 执行、测试主线程读取计数，故用 atomic 消除 -race 下的数据竞争。
type stubConfirmEngine struct {
	interfaces.MeetingEngine
	turnCount atomic.Int64
}

func (e *stubConfirmEngine) RunTurn(context.Context, string, string) error {
	e.turnCount.Add(1)
	return nil
}

// turns 线程安全读取已触发的讨论轮次数
func (e *stubConfirmEngine) turns() int { return int(e.turnCount.Load()) }

// waitFor 轮询等待异步动作完成（上限 2s）
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}
