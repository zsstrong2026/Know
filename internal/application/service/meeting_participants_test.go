package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeReplaceParticipantRepo 记录清理与插入动作（host 行由 ListByRoom 模拟保留）
type fakeReplaceParticipantRepo struct {
	interfaces.MeetingParticipantRepository
	batched []*types.MeetingParticipant
	deleted []string
}

func (f *fakeReplaceParticipantRepo) CreateBatch(_ context.Context, ps []*types.MeetingParticipant) error {
	f.batched = append(f.batched, ps...)
	return nil
}

func (f *fakeReplaceParticipantRepo) DeleteAgentsByRoom(_ context.Context, roomID string) error {
	f.deleted = append(f.deleted, roomID)
	return nil
}

func (f *fakeReplaceParticipantRepo) ListByRoom(_ context.Context, roomID string) ([]*types.MeetingParticipant, error) {
	out := []*types.MeetingParticipant{{
		ID: "host-row", MeetingRoomID: roomID, Kind: types.ParticipantKindHost,
		DisplayName: "主持人", OrderIndex: 0,
	}}
	return append(out, f.batched...), nil
}

// stubTurnEngine 可控返回讨论锁状态
type stubTurnEngine struct {
	interfaces.MeetingEngine
	running bool
}

func (e *stubTurnEngine) IsTurnRunning(context.Context, string) (bool, error) {
	return e.running, nil
}

func replaceFixture(agents map[string]*types.CustomAgent, running bool) (*meetingService, *isolationRoomRepo, *fakeReplaceParticipantRepo, *stubTurnEngine) {
	rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
	parts := &fakeReplaceParticipantRepo{}
	engine := &stubTurnEngine{running: running}
	svc := &meetingService{
		roomRepo:        rooms,
		participantRepo: parts,
		messageRepo:     &fakeMeetingMessageRepo{},
		agentRepo:       &fakeCRUDAgentRepo{agents: agents},
		engine:          engine,
	}
	return svc, rooms, parts, engine
}

func TestReplaceParticipantsMatrix(t *testing.T) {
	ctx := context.Background()
	agents := map[string]*types.CustomAgent{
		"a1": {ID: "a1", Name: "架构师"},
		"a2": {ID: "a2", Name: "测试专家"},
	}

	t.Run("归档只读", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusArchived}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1"})
		assert.Equal(t, ErrRoomArchived, err)
		assert.Empty(t, parts.deleted, "归档拒绝时不触发清理")
	})

	t.Run("空智能体拒绝", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", nil)
		assert.Equal(t, ErrNoAgents, err)
		assert.Empty(t, parts.deleted)
	})

	t.Run("讨论进行中 409", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, true)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1"})
		assert.Equal(t, ErrTurnRunning, err)
		assert.Empty(t, parts.deleted)
	})

	t.Run("跨租户拒绝", func(t *testing.T) {
		svc, rooms, _, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 2, "r1", []string{"a1"})
		assert.Error(t, err)
	})

	t.Run("待确认状态允许替换", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusAwaitingConfirmation}))
		out, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1"})
		require.NoError(t, err)
		assert.Equal(t, []string{"r1"}, parts.deleted)
		require.Len(t, parts.batched, 1)
		assert.Equal(t, 1, parts.batched[0].OrderIndex, "agent 序号从 1 开始（0 留给主持人）")
		// 主持人记录保留在返回列表首位
		require.NotEmpty(t, out)
		assert.Equal(t, types.ParticipantKindHost, out[0].Kind)
	})

	t.Run("agent 不存在时无副作用", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a1", "ghost"})
		assert.ErrorContains(t, err, "agent not found: ghost")
		assert.Empty(t, parts.deleted, "预校验失败不得先删后插")
	})

	t.Run("重复 ID 去重保序", func(t *testing.T) {
		svc, rooms, parts, _ := replaceFixture(agents, false)
		require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Status: types.MeetingStatusActive}))
		_, err := svc.ReplaceParticipants(ctx, 1, "r1", []string{"a2", "a1", "a2"})
		require.NoError(t, err)
		require.Len(t, parts.batched, 2)
		assert.Equal(t, "a2", parts.batched[0].AgentID)
		assert.Equal(t, "a1", parts.batched[1].AgentID)
	})
}
