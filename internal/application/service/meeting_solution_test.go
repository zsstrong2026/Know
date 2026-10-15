package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSolutionPrompt(t *testing.T) {
	room := &types.MeetingRoom{Title: "发布方案评审", Description: "讨论 v2 发布策略"}
	adopted := []*types.MeetingMessage{
		{SenderID: "a1", Content: "建议蓝绿发布"},
		{SenderID: "a2", Content: "需要回滚预案"},
	}
	nameOf := func(id string) string {
		return map[string]string{"a1": "架构师", "a2": "测试专家"}[id]
	}
	out := buildSolutionPrompt(room, adopted, nameOf)
	assert.Contains(t, out, "发布方案评审") // 主题入提示词
	assert.Contains(t, out, "架构师：建议蓝绿发布")
	assert.Contains(t, out, "测试专家：需要回滚预案")
	assert.Contains(t, out, "## 背景与目标") // 固定结构四小节
	assert.Contains(t, out, "## 方案要点")
	assert.Contains(t, out, "## 风险与待确认事项")
	assert.Contains(t, out, "## 下一步行动")
}

// ---- M4 验收测试替身（T4.4 单测等价覆盖；沿用 meeting_engine_test.go 风格，嵌入接口仅覆写被调方法）----

// fakeSolutionRoomRepo GetByID 返回预置房间；Update 记录最后一次落库的房间指针
type fakeSolutionRoomRepo struct {
	interfaces.MeetingRoomRepository
	room    *types.MeetingRoom
	updated *types.MeetingRoom
}

func (f *fakeSolutionRoomRepo) GetByID(_ context.Context, id string, _ uint64) (*types.MeetingRoom, error) {
	if f.room != nil && f.room.ID == id {
		return f.room, nil
	}
	return nil, errors.New("room not found")
}

func (f *fakeSolutionRoomRepo) Update(_ context.Context, room *types.MeetingRoom) error {
	f.updated = room
	return nil
}

// fakeArtifactRepo 记录落库产物
type fakeArtifactRepo struct {
	interfaces.MeetingArtifactRepository
	created []*types.MeetingArtifact
}

func (f *fakeArtifactRepo) Create(_ context.Context, a *types.MeetingArtifact) error {
	f.created = append(f.created, a)
	return nil
}

// fakeSolutionMessageRepo 在 fakeMeetingMessageRepo 上补 Create 自动赋 ID（模拟 GORM BeforeCreate），
// 使方案消息与产物/房间指针的关联断言有意义（msg.ID 非空）
type fakeSolutionMessageRepo struct {
	fakeMeetingMessageRepo
	seq int
}

func (f *fakeSolutionMessageRepo) Create(ctx context.Context, msg *types.MeetingMessage) error {
	if msg.ID == "" {
		f.mu.Lock()
		f.seq++
		msg.ID = fmt.Sprintf("sol-%d", f.seq)
		f.mu.Unlock()
	}
	return f.fakeMeetingMessageRepo.Create(ctx, msg)
}

// allMsgs 预置 ∪ 本轮落库 的合并视图（调用方需已持有 f.mu）
func (f *fakeMeetingMessageRepo) allMsgs() []*types.MeetingMessage {
	return append(append([]*types.MeetingMessage{}, f.preset...), f.created...)
}

// UpdateReviewState 按 ID 修改消息评审状态（模拟持久化写回，未找到即报错）
func (f *fakeMeetingMessageRepo) UpdateReviewState(_ context.Context, id string, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.allMsgs() {
		if m.ID == id {
			m.ReviewState = state
			return nil
		}
	}
	return errors.New("message not found")
}

// ListByReviewState 返回房间内指定评审状态的消息
func (f *fakeMeetingMessageRepo) ListByReviewState(_ context.Context, roomID string, state string) ([]*types.MeetingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*types.MeetingMessage
	for _, m := range f.allMsgs() {
		if m.MeetingRoomID == roomID && m.ReviewState == state {
			out = append(out, m)
		}
	}
	return out, nil
}

// CountByReviewStates 按状态集合求和统计
func (f *fakeMeetingMessageRepo) CountByReviewStates(_ context.Context, roomID string, states []string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set := make(map[string]bool, len(states))
	for _, s := range states {
		set[s] = true
	}
	var n int64
	for _, m := range f.allMsgs() {
		if m.MeetingRoomID == roomID && set[m.ReviewState] {
			n++
		}
	}
	return n, nil
}

// MaxSolutionVersion 房间内现有消息的最大方案版本（无方案时 0）
func (f *fakeMeetingMessageRepo) MaxSolutionVersion(_ context.Context, roomID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	maxV := 0
	for _, m := range f.allMsgs() {
		if m.MeetingRoomID == roomID && m.SolutionVersion > maxV {
			maxV = m.SolutionVersion
		}
	}
	return maxV, nil
}

// solutionPreset 预置讨论消息：2 条 adopted + 1 条 rejected
func solutionPreset() []*types.MeetingMessage {
	return []*types.MeetingMessage{
		{ID: "d1", MeetingRoomID: "r1", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "建议蓝绿发布", ReviewState: types.ReviewStateAdopted},
		{ID: "d2", MeetingRoomID: "r1", SenderKind: types.MeetingSenderAgent, SenderID: "a2", Content: "需要回滚预案", ReviewState: types.ReviewStateAdopted},
		{ID: "d3", MeetingRoomID: "r1", SenderKind: types.MeetingSenderAgent, SenderID: "a3", Content: "直接上生产", ReviewState: types.ReviewStateRejected},
	}
}

// eventTypes 取出当前已广播的事件类型序列（Publish 非阻塞写入，单消费者直接排空）
func eventTypes(ch chan MeetingEvent) []string {
	out := make([]string, 0, len(ch))
	for len(ch) > 0 {
		out = append(out, (<-ch).Type)
	}
	return out
}

// solutionEngineFixture 组装方案生成引擎（modelService 由用例注入）
func solutionEngineFixture(msgRepo interfaces.MeetingMessageRepository, artRepo *fakeArtifactRepo, roomRepo *fakeSolutionRoomRepo, models *stubModelService) *meetingEngine {
	return &meetingEngine{
		modelService:    models,
		messageRepo:     msgRepo,
		artifactRepo:    artRepo,
		roomRepo:        roomRepo,
		participantRepo: &fakeParticipantRepoForTurn{},
		broadcaster:     NewMeetingBroadcaster(),
	}
}

// TestGenerateSolutionNoAdopted 无采纳意见时拒绝生成：哨兵错误 + 零副作用（T4.4 Step 4 空采纳拦截等价覆盖）
func TestGenerateSolutionNoAdopted(t *testing.T) {
	ctx := context.Background()
	msgRepo := &fakeMeetingMessageRepo{preset: []*types.MeetingMessage{
		{ID: "p1", MeetingRoomID: "r1", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "待定意见", ReviewState: types.ReviewStatePending},
	}}
	artRepo := &fakeArtifactRepo{}
	roomRepo := &fakeSolutionRoomRepo{}
	// 早退发生在模型解析之前，modelService 留空即可
	e := solutionEngineFixture(msgRepo, artRepo, roomRepo, nil)
	room := roomFixture()
	ch, unsub := e.broadcaster.Subscribe(room.ID)
	defer unsub()

	msg, err := e.GenerateSolution(ctx, room, "user-1")

	assert.Nil(t, msg)
	assert.ErrorIs(t, err, ErrNoAdoptedMessages)
	assert.Empty(t, msgRepo.snapshot(), "不应有消息落库")
	assert.Empty(t, artRepo.created, "不应有产物落库")
	assert.Nil(t, roomRepo.updated, "不应更新房间")
	assert.Empty(t, room.CurrentSolutionMessageID, "方案指针不应变化")
	assert.Empty(t, eventTypes(ch), "不应广播任何事件（含 solution.generating/solution.created）")
}

// TestUpdateReviewStateFlow 评审流转全路径：pending→adopted→pending 成功并广播；
// 非法状态/user 消息/is_solution 消息/跨房间消息拒绝（T4.4 Step 1/2 等价覆盖）
func TestUpdateReviewStateFlow(t *testing.T) {
	ctx := context.Background()
	msgRepo := &fakeMeetingMessageRepo{preset: []*types.MeetingMessage{
		{ID: "am", MeetingRoomID: "r1", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "讨论意见", ReviewState: types.ReviewStatePending},
		{ID: "um", MeetingRoomID: "r1", SenderKind: types.MeetingSenderUser, Content: "用户消息"},
		{ID: "sm", MeetingRoomID: "r1", SenderKind: types.MeetingSenderHost, Content: "方案正文", IsSolution: true},
		{ID: "om", MeetingRoomID: "r2", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "别的房间的消息", ReviewState: types.ReviewStatePending},
	}}
	roomRepo := &fakeSolutionRoomRepo{room: roomFixture()}
	engine := &meetingEngine{broadcaster: NewMeetingBroadcaster()}
	svc := &meetingService{roomRepo: roomRepo, messageRepo: msgRepo, engine: engine}

	ch, unsub := engine.broadcaster.Subscribe("r1")
	defer unsub()

	// pending → adopted：返回值、落库、广播三者一致
	msg, err := svc.UpdateReviewState(ctx, 1, "r1", "am", types.ReviewStateAdopted)
	require.NoError(t, err)
	assert.Equal(t, types.ReviewStateAdopted, msg.ReviewState)
	stored, err := msgRepo.GetByID(ctx, "am")
	require.NoError(t, err)
	assert.Equal(t, types.ReviewStateAdopted, stored.ReviewState, "状态应落库")
	assert.Equal(t, []string{MeetingEventMessageReviewUpdated}, eventTypes(ch))

	// adopted → pending：允许回设
	msg, err = svc.UpdateReviewState(ctx, 1, "r1", "am", types.ReviewStatePending)
	require.NoError(t, err)
	assert.Equal(t, types.ReviewStatePending, msg.ReviewState)
	assert.Equal(t, []string{MeetingEventMessageReviewUpdated}, eventTypes(ch))

	// 非法状态：none 仅初始值不可回设；未定义状态同样拒绝
	for _, state := range []string{types.ReviewStateNone, "bogus"} {
		_, err := svc.UpdateReviewState(ctx, 1, "r1", "am", state)
		assert.ErrorContains(t, err, "invalid review state", "state=%s", state)
	}
	// user 消息与 is_solution 消息不可评审
	_, err = svc.UpdateReviewState(ctx, 1, "r1", "um", types.ReviewStateAdopted)
	assert.ErrorContains(t, err, "only agent discussion messages can be reviewed")
	_, err = svc.UpdateReviewState(ctx, 1, "r1", "sm", types.ReviewStateAdopted)
	assert.ErrorContains(t, err, "only agent discussion messages can be reviewed")
	// 消息不属于该房间
	_, err = svc.UpdateReviewState(ctx, 1, "r1", "om", types.ReviewStateAdopted)
	assert.ErrorContains(t, err, "not in room")

	// 非法请求均不应广播
	assert.Empty(t, eventTypes(ch))
}

// TestGenerateSolutionVersioning 版本化产物与事件序列（T4.4 Step 3 等价覆盖）：
// v1 消息/产物/房间指针/SSE 三连事件 → 再生成得到 v2
func TestGenerateSolutionVersioning(t *testing.T) {
	ctx := context.Background()
	msgRepo := &fakeSolutionMessageRepo{}
	msgRepo.preset = solutionPreset()
	artRepo := &fakeArtifactRepo{}
	roomRepo := &fakeSolutionRoomRepo{}
	e := solutionEngineFixture(msgRepo, artRepo, roomRepo, &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{content: "## 背景与目标\n采纳：蓝绿发布 + 回滚预案"},
	})
	room := roomFixture()
	ch, unsub := e.broadcaster.Subscribe(room.ID)
	defer unsub()

	wantEvents := []string{MeetingEventSolutionGenerating, MeetingEventMessageCreated, MeetingEventSolutionCreated}

	// 第一次生成 → v1
	msg, err := e.GenerateSolution(ctx, room, "user-1")
	require.NoError(t, err)
	require.Len(t, msgRepo.snapshot(), 1)
	assert.True(t, msg.IsSolution)
	assert.Equal(t, 1, msg.SolutionVersion)
	assert.Equal(t, types.MeetingSenderHost, msg.SenderKind)
	assert.Equal(t, types.ReviewStateNone, msg.ReviewState)
	// repo 收到的消息同上（落库即所得）
	stored := msgRepo.snapshot()[0]
	assert.True(t, stored.IsSolution)
	assert.Equal(t, 1, stored.SolutionVersion)
	assert.Equal(t, types.MeetingSenderHost, stored.SenderKind)
	assert.Equal(t, types.ReviewStateNone, stored.ReviewState)
	// 产物元数据
	require.Len(t, artRepo.created, 1)
	art := artRepo.created[0]
	assert.Equal(t, 1, art.Version)
	assert.Equal(t, 2, art.AdoptedCount)
	assert.Equal(t, 1, art.RejectedCount, "预置 rejected 应计入产物统计")
	assert.Equal(t, "user-1", art.CreatedBy)
	assert.Equal(t, msg.ID, art.SolutionMessageID, "产物应关联新方案消息")
	assert.Equal(t, room.ID, art.MeetingRoomID)
	// 房间当前方案指针已更新（内存对象 + Update 落库两处一致）
	assert.Equal(t, msg.ID, room.CurrentSolutionMessageID)
	require.NotNil(t, roomRepo.updated)
	assert.Equal(t, msg.ID, roomRepo.updated.CurrentSolutionMessageID)
	// SSE 事件序列：solution.generating → message.created → solution.created
	assert.Equal(t, wantEvents, eventTypes(ch))

	// 第二次生成：MaxSolutionVersion 此时返回已落库的 1 → v2
	msg2, err := e.GenerateSolution(ctx, room, "user-1")
	require.NoError(t, err)
	assert.Equal(t, 2, msg2.SolutionVersion)
	require.Len(t, artRepo.created, 2)
	assert.Equal(t, 2, artRepo.created[1].Version)
	assert.Equal(t, msg2.ID, artRepo.created[1].SolutionMessageID)
	assert.Equal(t, msg2.ID, room.CurrentSolutionMessageID, "指针应指向最新版本")
	assert.Equal(t, wantEvents, eventTypes(ch), "第二轮事件序列应完整重放")
}

// TestGenerateSolutionFallbackContent 方案内容可用性（T4.4 Step 3 内容断言等价覆盖）。
// 实现现状：GenerateSolution 在主持人模型不可用时直接报错（无内部兜底文案，与 generateGuardMessage
// 的降级路径不同），因此分两层验证：
//  1) 无可用模型 → 干净失败，不产生消息/产物半落库状态，仅有 solution.generating 事件；
//  2) 以固定兜底文案作为模型输出 → 方案正文非空且包含固定小节与采纳意见关键词（正文通路不丢内容）。
func TestGenerateSolutionFallbackContent(t *testing.T) {
	ctx := context.Background()

	// 1) 模型完全不可用：错误返回、零落库
	msgRepo := &fakeMeetingMessageRepo{preset: solutionPreset()}
	artRepo := &fakeArtifactRepo{}
	roomRepo := &fakeSolutionRoomRepo{}
	e := solutionEngineFixture(msgRepo, artRepo, roomRepo, &stubModelService{}) // ListModels 为空 → resolveHostChat 失败
	room := roomFixture()
	ch, unsub := e.broadcaster.Subscribe(room.ID)
	defer unsub()

	msg, err := e.GenerateSolution(ctx, room, "user-1")
	require.Error(t, err)
	assert.Nil(t, msg)
	assert.NotErrorIs(t, err, ErrNoAdoptedMessages, "已有采纳意见，失败应源于模型不可用")
	assert.Empty(t, msgRepo.snapshot(), "失败路径不应有消息落库")
	assert.Empty(t, artRepo.created, "失败路径不应有产物落库")
	assert.Empty(t, room.CurrentSolutionMessageID)
	assert.Equal(t, []string{MeetingEventSolutionGenerating}, eventTypes(ch),
		"生成中事件先发，失败由 HTTP 错误响应复位 loading")

	// 2) 固定兜底文案作为模型输出：正文完整透传且可用
	fallback := "## 背景与目标\n汇总本次采纳意见：建议蓝绿发布；需要回滚预案。\n## 方案要点\n## 风险与待确认事项\n## 下一步行动\n"
	msgRepo2 := &fakeSolutionMessageRepo{}
	msgRepo2.preset = solutionPreset()
	e2 := solutionEngineFixture(msgRepo2, &fakeArtifactRepo{}, &fakeSolutionRoomRepo{}, &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{content: fallback},
	})
	got, err := e2.GenerateSolution(ctx, roomFixture(), "user-1")
	require.NoError(t, err)
	assert.NotEmpty(t, got.Content)
	assert.Contains(t, got.Content, "背景与目标", "固定小节标题应保留")
	assert.Contains(t, got.Content, "蓝绿发布", "采纳意见关键词应进入正文")
}
