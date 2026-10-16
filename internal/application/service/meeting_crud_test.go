package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- M1 CRUD 与 M4/M5 服务层透传补测（T6.6 覆盖率补齐；沿用嵌入接口 fake 风格）----

// fakeCRUDAgentRepo 按 ID 返回预置智能体（租户归属校验由真实仓储承担，fake 不模拟）
type fakeCRUDAgentRepo struct {
	interfaces.CustomAgentRepository
	agents map[string]*types.CustomAgent
}

func (f *fakeCRUDAgentRepo) GetAgentByID(_ context.Context, id string, _ uint64) (*types.CustomAgent, error) {
	if a, ok := f.agents[id]; ok {
		return a, nil
	}
	return nil, errors.New("agent not found")
}

// fakeCRUDParticipantRepo 记录批量落库的参与者
type fakeCRUDParticipantRepo struct {
	interfaces.MeetingParticipantRepository
	batched []*types.MeetingParticipant
}

func (f *fakeCRUDParticipantRepo) CreateBatch(_ context.Context, ps []*types.MeetingParticipant) error {
	f.batched = append(f.batched, ps...)
	return nil
}

// stubSolutionEngine 记录 GenerateSolution 委托调用
type stubSolutionEngine struct {
	interfaces.MeetingEngine
	called bool
	room   *types.MeetingRoom
	userID string
}

func (e *stubSolutionEngine) GenerateSolution(_ context.Context, room *types.MeetingRoom, userID string) (*types.MeetingMessage, error) {
	e.called, e.room, e.userID = true, room, userID
	return &types.MeetingMessage{MeetingRoomID: room.ID, Content: "solution"}, nil
}

// crudFixture 组装 service 层 CRUD 依赖（isolationRoomRepo 复用隔离仓储，含 Create/Update/Delete）
func crudFixture(agents map[string]*types.CustomAgent) (*meetingService, *isolationRoomRepo, *fakeCRUDParticipantRepo) {
	rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
	parts := &fakeCRUDParticipantRepo{}
	svc := &meetingService{
		roomRepo:        rooms,
		participantRepo: parts,
		messageRepo:     &fakeMeetingMessageRepo{},
		agentRepo:       &fakeCRUDAgentRepo{agents: agents},
	}
	return svc, rooms, parts
}

// TestCreateMeetingBuildsParticipants 创建会议：参数校验回填 + 主持人首位 + 智能体依次编序；非法参数/缺智能体拒绝
func TestCreateMeetingBuildsParticipants(t *testing.T) {
	ctx := context.Background()
	agents := map[string]*types.CustomAgent{
		"a1": {ID: "a1", Name: "架构师", Avatar: "bot1"},
		"a2": {ID: "a2", Name: "测试专家", Avatar: "bot2"},
	}
	svc, rooms, parts := crudFixture(agents)

	// 空标题 / 无智能体：校验拒绝且零副作用
	_, err := svc.CreateMeeting(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1}, []string{"a1"})
	assert.ErrorContains(t, err, "title is required")
	_, err = svc.CreateMeeting(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Title: "发布评审"}, nil)
	assert.ErrorContains(t, err, "at least one agent")
	assert.Empty(t, parts.batched)

	// 正常创建：主持人排 0，智能体按传入顺序编序；缺省字段回填默认值
	room := &types.MeetingRoom{ID: "r1", TenantID: 1, Title: "发布评审"} // BroadcastMode/GuardMaxExchange/Status 均缺省
	created, err := svc.CreateMeeting(ctx, room, []string{"a1", "a2"})
	require.NoError(t, err)
	assert.Equal(t, types.MeetingBroadcastSequential, created.BroadcastMode, "缺省广播模式回退 sequential")
	assert.Equal(t, 3, created.GuardMaxExchange, "缺省护栏回退 3")
	assert.Equal(t, types.MeetingStatusActive, created.Status, "缺省状态回退 active")
	require.Len(t, parts.batched, 3)
	assert.Equal(t, types.ParticipantKindHost, parts.batched[0].Kind)
	assert.Equal(t, 0, parts.batched[0].OrderIndex)
	assert.Equal(t, "架构师", parts.batched[1].DisplayName)
	assert.Equal(t, 1, parts.batched[1].OrderIndex)
	assert.Equal(t, 2, parts.batched[2].OrderIndex)
	assert.Equal(t, "r1", parts.batched[2].MeetingRoomID, "参与者房间 ID 回填")
	assert.NotNil(t, rooms.rooms["r1"], "房间应已落库")

	// 智能体不存在：拒绝创建
	_, err = svc.CreateMeeting(ctx, &types.MeetingRoom{ID: "r2", TenantID: 1, Title: "x"}, []string{"ghost"})
	assert.ErrorContains(t, err, "agent not found: ghost")
}

// TestUpdateMeetingPassthrough 更新会议透传仓储（T6.2 前端 PATCH 链路的服务层等价）
func TestUpdateMeetingPassthrough(t *testing.T) {
	ctx := context.Background()
	svc, rooms, _ := crudFixture(nil)
	require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Title: "旧标题"}))

	room := rooms.rooms["r1"]
	room.Title = "新标题"
	require.NoError(t, svc.UpdateMeeting(ctx, room))
	assert.Equal(t, "新标题", rooms.rooms["r1"].Title)
}

// TestListMessagesPassthrough 消息列表透传（返回预置∪落库正序）
func TestListMessagesPassthrough(t *testing.T) {
	ctx := context.Background()
	msgRepo := &fakeMeetingMessageRepo{preset: []*types.MeetingMessage{
		{ID: "m1", MeetingRoomID: "r1", Content: "第一条"},
	}}
	svc := &meetingService{messageRepo: msgRepo}

	msgs, err := svc.ListMessages(ctx, "r1", "", 50)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "m1", msgs[0].ID)
}

// TestGenerateSolutionTenantGuard 服务层方案生成：租户校验失败不触达引擎；同租户委托成功
func TestGenerateSolutionTenantGuard(t *testing.T) {
	ctx := context.Background()
	svc, rooms, _ := crudFixture(nil)
	require.NoError(t, rooms.Create(ctx, &types.MeetingRoom{ID: "r1", TenantID: 1, Title: "发布评审", Status: types.MeetingStatusActive}))
	engine := &stubSolutionEngine{}
	svc.engine = engine

	// 跨租户：GetByID 早退，引擎未被调用
	_, err := svc.GenerateSolution(ctx, 2, "r1", "user-a")
	assert.Error(t, err)
	assert.False(t, engine.called)

	// 同租户：委托引擎并透传结果
	msg, err := svc.GenerateSolution(ctx, 1, "r1", "user-a")
	require.NoError(t, err)
	assert.True(t, engine.called)
	assert.Equal(t, "r1", engine.room.ID)
	assert.Equal(t, "user-a", engine.userID)
	assert.Equal(t, "solution", msg.Content)
}
