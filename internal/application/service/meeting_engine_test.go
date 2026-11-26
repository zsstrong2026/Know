package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestUnionKnowledgeBaseIDs(t *testing.T) {
	// selected 模式：智能体 KB ∪ 会议级 KB，去重
	got := unionKnowledgeBaseIDs("selected", []string{"a", "b"}, []string{"b", "c"})
	assert.ElementsMatch(t, []string{"a", "b", "c"}, got)
	// none 模式：仅会议级 KB
	got = unionKnowledgeBaseIDs("none", []string{"a"}, []string{"c"})
	assert.Equal(t, []string{"c"}, got)
	// all 模式：返回 nil（AgentQA 回退为智能体"全部知识库"，天然是会议级 KB 的超集）
	got = unionKnowledgeBaseIDs("all", nil, []string{"c"})
	assert.Nil(t, got)
}

func TestShouldRunParallel(t *testing.T) {
	// sequential：不走并发
	assert.False(t, shouldRunParallel(types.MeetingBroadcastSequential, 3))
	// parallel 但发言者仅 1 个：无需并发
	assert.False(t, shouldRunParallel(types.MeetingBroadcastParallel, 1))
	// parallel 且发言者多于 1 个：并发
	assert.True(t, shouldRunParallel(types.MeetingBroadcastParallel, 2))
	// 空模式：默认顺序
	assert.False(t, shouldRunParallel("", 3))
}

func TestBuildMeetingQuery(t *testing.T) {
	room := roomFixture()
	msgs := messagesFixture()
	participants := []*types.MeetingParticipant{
		{Kind: types.ParticipantKindHost, DisplayName: "主持人甲"},
		{Kind: types.ParticipantKindAgent, AgentID: "a1", DisplayName: "乙"},
	}
	q := buildMeetingQuery(room, "讨论下部署方案", msgs, participants, "张三")
	// 应包含会议主题与共享上下文
	assert.Contains(t, q, room.Title)
	assert.Contains(t, q, "讨论下部署方案")
	assert.Contains(t, q, msgs[0].Content)
	// 关键回归断言：名册必须注入 agent 展示名，否则模型无从产出可命中的 @展示名
	assert.Contains(t, q, "乙")
	// 讨论记录必须用展示名标注发言者，否则模型只能看到 UUID，容易 @ 错对象
	assert.Contains(t, q, "乙: 我建议用蓝绿发布")
	// 用户姓名必须注入，智能体才能用 @用户名 直接称呼用户
	assert.Contains(t, q, "【用户】张三")
	assert.Contains(t, q, "@张三")
	// 主持人不是 @ 目标，展示名不得出现在名册中
	assert.NotContains(t, q, "主持人甲")
}

// TestBuildMeetingQueryNoAgentRoster 仅有主持人（无 agent 展示名）时不输出名册行
func TestBuildMeetingQueryNoAgentRoster(t *testing.T) {
	room := roomFixture()
	msgs := messagesFixture()
	participants := []*types.MeetingParticipant{
		{Kind: types.ParticipantKindHost, DisplayName: "主持人甲"},
	}
	q := buildMeetingQuery(room, "讨论下部署方案", msgs, participants, "")
	assert.NotContains(t, q, "【参会智能体】")
}

// TestFreeMeetingModeBehaviors 自由模式：无主持人介入、可自动接话、显式 @ 优先，prompt 引导主动挖掘信息
func TestFreeMeetingModeBehaviors(t *testing.T) {
	room := &types.MeetingRoom{
		Title: "产品方向讨论", Mode: types.MeetingModeFree, RoutingMode: types.MeetingRoutingFree,
	}
	assert.False(t, hostInDiscussion(room), "自由模式没有主持人介入")
	assert.True(t, canAutoContinue(room), "自由模式允许无人 @ 时自动接话")

	agents := []*types.CustomAgent{{ID: "a1"}, {ID: "a2"}}
	e := &meetingEngine{}
	// 用户显式 @：优先点名对象，便于用户收束讨论
	got := e.selectSpeakers(context.Background(), room,
		&types.MeetingMessage{MentionedAgentIDs: []string{"a2"}}, agents)
	assert.Equal(t, []*types.CustomAgent{agents[1]}, got)
	// 无 @：保留全员自由发言
	got = e.selectSpeakers(context.Background(), room, &types.MeetingMessage{}, agents)
	assert.Equal(t, agents, got)

	// 自动接话不依赖主持人模型：按参会顺序选人，不附带点名问题
	next, hint := e.chooseNextSpeaker(context.Background(), room, &types.MeetingMessage{}, agents, nil, "")
	assert.Equal(t, agents[0], next)
	assert.Empty(t, hint)

	// prompt 必须注入自由讨论协议：主动提问、相互讨论、服从用户收束
	q := buildMeetingQuery(room, "会议开始", nil, []*types.MeetingParticipant{
		{Kind: types.ParticipantKindAgent, AgentID: "a1", DisplayName: "产品"},
		{Kind: types.ParticipantKindAgent, AgentID: "a2", DisplayName: "技术"},
	}, "张三")
	assert.Contains(t, q, "自由讨论模式")
	assert.Contains(t, q, "没有主持人")
	assert.Contains(t, q, "@用户")
	assert.Contains(t, q, "用户会随时输入来纠正或收束讨论")
}

func roomFixture() *types.MeetingRoom {
	// Status 显式 active：T6.1 归档守卫上线后，"进行中会议" fixture 必须带状态（DB 层有 default:active）
	return &types.MeetingRoom{ID: "r1", Title: "发布方案评审", Status: types.MeetingStatusActive, KnowledgeBaseIDs: []string{"c"}}
}

func messagesFixture() []*types.MeetingMessage {
	return []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "先同步下背景"},
		{ID: "m2", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "我建议用蓝绿发布"},
	}
}

// ---- runAgentTurns 测试替身（嵌入接口，仅覆写被调用方法）----

// fakeMeetingMessageRepo 记录落库消息；未覆写的方法继承嵌入接口（若被误调用会 panic，便于暴露越界）
type fakeMeetingMessageRepo struct {
	interfaces.MeetingMessageRepository
	mu      sync.Mutex
	created []*types.MeetingMessage
	preset  []*types.MeetingMessage // 预置消息（供 GetByID/ListByRoom 返回）
}

func (f *fakeMeetingMessageRepo) Create(_ context.Context, msg *types.MeetingMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, msg)
	return nil
}

// ListRecent 返回房间内最近 limit 条消息（旧->新正序），对齐真实仓储语义
func (f *fakeMeetingMessageRepo) ListRecent(_ context.Context, roomID string, limit int) ([]*types.MeetingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.allMsgs()
	filtered := make([]*types.MeetingMessage, 0, len(all))
	for _, m := range all {
		// 未标注房间的预置消息视为通用夹具，一并返回
		if m.MeetingRoomID != "" && m.MeetingRoomID != roomID {
			continue
		}
		filtered = append(filtered, m)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}

// ListByAgendaItem 夹具无按议题索引的消息，返回空集（避免嵌入接口 nil 调用 panic）
func (f *fakeMeetingMessageRepo) ListByAgendaItem(_ context.Context, _ string, _ string, _ int) ([]*types.MeetingMessage, error) {
	return nil, nil
}

// GetByID 从预置 + 本轮落库中按 ID 查找
func (f *fakeMeetingMessageRepo) GetByID(_ context.Context, id string) (*types.MeetingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range append(append([]*types.MeetingMessage{}, f.preset...), f.created...) {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, errors.New("message not found")
}

// ListByRoom 返回"预置 ∪ 本轮落库"的正序副本（旧->新），模拟真实持久化读回
func (f *fakeMeetingMessageRepo) ListByRoom(_ context.Context, _ string, _ string, _ int) ([]*types.MeetingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*types.MeetingMessage, 0, len(f.preset)+len(f.created))
	out = append(out, f.preset...)
	out = append(out, f.created...)
	return out, nil
}

// snapshot 线程安全地复制已落库消息，供并发分支断言使用（避免数据竞争）
func (f *fakeMeetingMessageRepo) snapshot() []*types.MeetingMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*types.MeetingMessage, len(f.created))
	copy(out, f.created)
	return out
}

// fakeMeetingSession 仅覆写 AgentQA，行为由测试注入的闭包决定
type fakeMeetingSession struct {
	interfaces.SessionService
	agentQA func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error
}

func (f *fakeMeetingSession) AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	return f.agentQA(ctx, req, bus)
}

func agentFixture(id, name string) *types.CustomAgent {
	return &types.CustomAgent{ID: id, Name: name}
}

func triggerFixture(content string) *types.MeetingMessage {
	return &types.MeetingMessage{ID: "trig", SenderKind: types.MeetingSenderUser, Content: content}
}

// countByKind 统计指定 SenderKind 的消息数量
func countByKind(msgs []*types.MeetingMessage, kind string) int {
	n := 0
	for _, m := range msgs {
		if m.SenderKind == kind {
			n++
		}
	}
	return n
}

// messagesByKind 过滤出指定 SenderKind 的消息
func messagesByKind(msgs []*types.MeetingMessage, kind string) []*types.MeetingMessage {
	out := make([]*types.MeetingMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.SenderKind == kind {
			out = append(out, m)
		}
	}
	return out
}

// TestRunAgentTurnsParallelConcurrent 验证 parallel 分支真正并发执行（而非顺序）
func TestRunAgentTurnsParallelConcurrent(t *testing.T) {
	repo := &fakeMeetingMessageRepo{}
	room := &types.MeetingRoom{ID: "r-par", BroadcastMode: types.MeetingBroadcastParallel}
	trigger := triggerFixture("议题")

	// 两个 goroutine 必须同时进入 AgentQA：先到者等待后到者，超时即视为退化为顺序执行
	var entered int32
	bothIn := make(chan struct{})
	var once sync.Once
	sess := &fakeMeetingSession{}
	sess.agentQA = func(_ context.Context, _ *types.QARequest, _ *event.EventBus) error {
		if atomic.AddInt32(&entered, 1) == 2 {
			once.Do(func() { close(bothIn) })
		}
		select {
		case <-bothIn:
		case <-time.After(2 * time.Second):
			return errors.New("timeout: calls were not concurrent")
		}
		return nil
	}
	e := &meetingEngine{messageRepo: repo, sessionService: sess, redis: nil, broadcaster: NewMeetingBroadcaster(), contextMax: 50}

	e.runAgentTurns(context.Background(), room, trigger,
		[]*types.CustomAgent{agentFixture("a1", "甲"), agentFixture("a2", "乙")})

	msgs := repo.snapshot()
	require.Equal(t, 2, countByKind(msgs, types.MeetingSenderAgent), "两个智能体均应完成发言")
	assert.Equal(t, 0, countByKind(msgs, types.MeetingSenderSystem), "并发成功时不应产生失败提示")
	assert.Equal(t, int32(2), atomic.LoadInt32(&entered))
}

// TestRunAgentTurnsParallelFailureDegrades 验证 parallel 单个智能体失败时降级但不影响其余发言
func TestRunAgentTurnsParallelFailureDegrades(t *testing.T) {
	repo := &fakeMeetingMessageRepo{}
	room := &types.MeetingRoom{ID: "r-par-fail", BroadcastMode: types.MeetingBroadcastParallel}
	trigger := triggerFixture("议题")

	sess := &fakeMeetingSession{}
	sess.agentQA = func(_ context.Context, req *types.QARequest, _ *event.EventBus) error {
		if req.CustomAgent.ID == "a2" { // 仅 a2 失败
			return errors.New("boom")
		}
		return nil
	}
	e := &meetingEngine{messageRepo: repo, sessionService: sess, redis: nil, broadcaster: NewMeetingBroadcaster(), contextMax: 50}

	e.runAgentTurns(context.Background(), room, trigger,
		[]*types.CustomAgent{agentFixture("a1", "甲"), agentFixture("a2", "乙")})

	msgs := repo.snapshot()
	assert.Equal(t, 1, countByKind(msgs, types.MeetingSenderAgent), "成功者仍应完成发言")
	sysMsgs := messagesByKind(msgs, types.MeetingSenderSystem)
	require.Len(t, sysMsgs, 1, "失败者应产生恰好一条系统提示")
	assert.Contains(t, sysMsgs[0].Content, "乙", "系统提示应包含失败者名称")
	assert.Contains(t, sysMsgs[0].Content, "发言失败")
}

// TestRunSpeakerTasksSequentialInterruptedStopsSilently 验证 sequential 在打断标记存在时静默终止：不调用 AgentQA、不落任何消息
func TestRunSpeakerTasksSequentialInterruptedStopsSilently(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := &fakeMeetingMessageRepo{}
	room := &types.MeetingRoom{ID: "r-seq", BroadcastMode: types.MeetingBroadcastSequential}
	trigger := triggerFixture("议题")

	ctx := context.Background()
	require.NoError(t, rdb.Set(ctx, interruptKey(room.ID), "1", time.Minute).Err())

	var calls int32
	sess := &fakeMeetingSession{}
	sess.agentQA = func(_ context.Context, _ *types.QARequest, _ *event.EventBus) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	e := &meetingEngine{messageRepo: repo, sessionService: sess, redis: rdb, broadcaster: NewMeetingBroadcaster(), contextMax: 50}

	e.runAgentTurns(ctx, room, trigger, []*types.CustomAgent{agentFixture("a1", "甲"), agentFixture("a2", "乙")})

	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "打断后不应调用 AgentQA")
	assert.Empty(t, repo.snapshot(), "runSpeakerTasks 不再落库，打断收尾统一由 RunTurn 处理")
}

// ---- 排队语义测试替身 ----

// fakeRoomRepoForTurn 仅覆写 GetByIDInternal（引擎加载会议入口）
type fakeRoomRepoForTurn struct {
	interfaces.MeetingRoomRepository
}

func (f *fakeRoomRepoForTurn) GetByIDInternal(_ context.Context, id string) (*types.MeetingRoom, error) {
	return &types.MeetingRoom{ID: id, TenantID: 1, BroadcastMode: types.MeetingBroadcastSequential}, nil
}

// fakeParticipantRepoForTurn 返回恰好 1 个 agent 参会者，使 selectSpeakers 走早退、无需主持人
type fakeParticipantRepoForTurn struct {
	interfaces.MeetingParticipantRepository
}

func (f *fakeParticipantRepoForTurn) ListByRoom(_ context.Context, roomID string) ([]*types.MeetingParticipant, error) {
	return []*types.MeetingParticipant{
		{MeetingRoomID: roomID, Kind: types.ParticipantKindAgent, AgentID: "a1"},
	}, nil
}

// fakeAgentRepoForTurn 覆写 GetAgentByID 返回固定智能体
type fakeAgentRepoForTurn struct {
	interfaces.CustomAgentRepository
}

func (f *fakeAgentRepoForTurn) GetAgentByID(_ context.Context, id string, _ uint64) (*types.CustomAgent, error) {
	return &types.CustomAgent{ID: id, Name: "甲"}, nil
}

// newTurnEngineFixture 组装带真实 miniredis 锁的引擎与消息仓储
func newTurnEngineFixture(t *testing.T, roomID string, preset []*types.MeetingMessage) (*meetingEngine, *fakeMeetingMessageRepo) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := &fakeMeetingMessageRepo{preset: preset}
	sess := &fakeMeetingSession{agentQA: func(_ context.Context, _ *types.QARequest, _ *event.EventBus) error {
		return nil
	}}
	e := &meetingEngine{
		roomRepo:        &fakeRoomRepoForTurn{},
		participantRepo: &fakeParticipantRepoForTurn{},
		messageRepo:     repo,
		agentRepo:       &fakeAgentRepoForTurn{},
		sessionService:  sess,
		redis:           rdb,
		broadcaster:     NewMeetingBroadcaster(),
		contextMax:      50,
		turnLockLease:   5 * time.Minute,
	}
	return e, repo
}

// TestExtractQueuedTrigger 纯逻辑：排队触发=当前轮触发消息之后最后一条用户消息
func TestExtractQueuedTrigger(t *testing.T) {
	msgs := []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "第一条"},
		{ID: "m2", SenderKind: types.MeetingSenderAgent, Content: "a 回复"},
		{ID: "m3", SenderKind: types.MeetingSenderUser, Content: "补充一下"},
		{ID: "m4", SenderKind: types.MeetingSenderUser, Content: "还有这个"},
	}
	// 排队触发 = 当前轮触发消息之后的最后一条用户消息
	got := extractQueuedTrigger(msgs, "m1")
	require.NotNil(t, got)
	assert.Equal(t, "m4", got.ID)
	// 触发消息之后没有新用户消息 → nil
	assert.Nil(t, extractQueuedTrigger(msgs, "m4"))
	// 触发消息不存在 → nil
	assert.Nil(t, extractQueuedTrigger(msgs, "nope"))
}

// TestParseMentionedAgentIDs 纯逻辑：从回复文本解析 @展示名 → agentID（按参会顺序去重，排除自己）
func TestParseMentionedAgentIDs(t *testing.T) {
	ps := []*types.MeetingParticipant{
		{Kind: types.ParticipantKindAgent, AgentID: "agent-A", DisplayName: "甲"},
		{Kind: types.ParticipantKindAgent, AgentID: "agent-B", DisplayName: "乙"},
		{Kind: types.ParticipantKindHost, AgentID: "", DisplayName: "主持人"},
	}
	// 命中对方：按参会顺序返回 agentID
	assert.Equal(t, []string{"agent-B"}, parseMentionedAgentIDs("我赞同 @乙 的看法", ps, "agent-A"))
	// 提及自己：排除
	assert.Nil(t, parseMentionedAgentIDs("@甲 我说完了", ps, "agent-A"))
	// 提及主持人（非 agent 参会者）与未知名字：不产生 ID
	assert.Nil(t, parseMentionedAgentIDs("@主持人 请裁决", ps, "agent-A"))
	assert.Nil(t, parseMentionedAgentIDs("没有提及任何人", ps, "agent-A"))
	// 参与者为空：不 panic、返回 nil
	assert.Nil(t, parseMentionedAgentIDs("@甲", nil, "agent-A"))
	// 展示名含空格（如 "AI 智能体专家"）：按全名匹配，不能被空格截断
	ps2 := []*types.MeetingParticipant{
		{Kind: types.ParticipantKindAgent, AgentID: "agent-A", DisplayName: "AI 智能体专家"},
		{Kind: types.ParticipantKindAgent, AgentID: "agent-B", DisplayName: "产品助理"},
	}
	assert.Equal(t, []string{"agent-B"}, parseMentionedAgentIDs("我建议按行业平铺。@产品助理 你认这个分类轴吗？", ps2, "agent-A"))
	assert.Nil(t, parseMentionedAgentIDs("@AI 智能体专家 我自己补充一句", ps2, "agent-A"))
}

// TestRunTurnConsumesQueuedTriggers 循环级行为：RunTurn 消费排队输入直至清空
func TestRunTurnConsumesQueuedTriggers(t *testing.T) {
	ctx := context.Background()

	// 用例 A：m1 触发 + m2 排队 → 应执行两轮
	e, repo := newTurnEngineFixture(t, "room1", []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "触发"},
		{ID: "m2", SenderKind: types.MeetingSenderUser, Content: "排队补刀"},
	})
	require.NoError(t, e.RunTurn(ctx, "room1", "m1"))
	agentMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderAgent)
	require.Len(t, agentMsgs, 2, "应消费排队输入执行第二轮")
	assert.Equal(t, "m1", agentMsgs[0].ReplyToMessageID)
	assert.Equal(t, "m2", agentMsgs[1].ReplyToMessageID, "第二轮应以排队消息为触发")

	// 用例 B：仅 m1，无排队输入 → 仅一轮
	e2, repo2 := newTurnEngineFixture(t, "room2", []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	require.NoError(t, e2.RunTurn(ctx, "room2", "m1"))
	require.Len(t, messagesByKind(repo2.snapshot(), types.MeetingSenderAgent), 1, "无排队输入只应执行一轮")
}

// ---- 护栏收敛测试（T3.2）----

// newGuardEngineFixture 复用 newTurnEngineFixture，额外注入 modelService：
// 避免 resolveHostChat 因 modelService==nil 而 panic；ListModels 返回 nil → 走 fallback 文案。
func newGuardEngineFixture(t *testing.T, roomID string, preset []*types.MeetingMessage) (*meetingEngine, *fakeMeetingMessageRepo) {
	t.Helper()
	e, repo := newTurnEngineFixture(t, roomID, preset)
	e.modelService = &stubModelService{}
	return e, repo
}

// TestGenerateGuardMessageFallback 主持人不可用时应返回兜底文案（含双方名与主题），而非 panic
func TestGenerateGuardMessageFallback(t *testing.T) {
	e := &meetingEngine{
		modelService:    &stubModelService{},
		participantRepo: &fakeParticipantRepoForTurn{},
		messageRepo:     &fakeMeetingMessageRepo{},
		broadcaster:     NewMeetingBroadcaster(),
	}
	room := &types.MeetingRoom{ID: "r", Title: "发布方案评审"}
	recent := []*types.MeetingMessage{
		{ID: "a", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "甲的观点"},
		{ID: "b", SenderKind: types.MeetingSenderAgent, SenderID: "a2", Content: "乙的观点"},
	}
	got := e.generateGuardMessage(context.Background(), room, "甲", "乙", recent)
	assert.NotEmpty(t, got)
	assert.Contains(t, got, "甲")
	assert.Contains(t, got, "乙")
	assert.Contains(t, got, room.Title)

	// truncate：按 rune 截断，避免中文截半
	assert.Equal(t, "中文测…", truncate("中文测试abc", 3))
	assert.Equal(t, "abc", truncate("abc", 5), "不超长时原样返回")
	assert.Equal(t, "论", truncate("论", 1), "长度恰等于 n 时不追加省略号")
	// tailN：len<=n 原样返回；len>n 取尾段
	assert.Equal(t, recent, tailN(recent, 2))
	assert.Equal(t, recent[1:], tailN(recent, 1))
}

// fakeGuardRoomRepoForTurn 返回带护栏上限配置的房间（覆盖 fakeRoomRepoForTurn 的默认配置）
type fakeGuardRoomRepoForTurn struct {
	interfaces.MeetingRoomRepository
}

func (f *fakeGuardRoomRepoForTurn) GetByIDInternal(_ context.Context, id string) (*types.MeetingRoom, error) {
	return &types.MeetingRoom{
		ID: id, TenantID: 1, Title: "发布方案评审",
		BroadcastMode:    types.MeetingBroadcastSequential,
		GuardMaxExchange: 3,
	}, nil
}

// fakeTwoAgentParticipantRepo 返回两个带展示名的 agent 参会者，用于校验护栏双方展示名反查
type fakeTwoAgentParticipantRepo struct {
	interfaces.MeetingParticipantRepository
}

func (f *fakeTwoAgentParticipantRepo) ListByRoom(_ context.Context, roomID string) ([]*types.MeetingParticipant, error) {
	return []*types.MeetingParticipant{
		{MeetingRoomID: roomID, Kind: types.ParticipantKindAgent, AgentID: "agent-A", DisplayName: "甲"},
		{MeetingRoomID: roomID, Kind: types.ParticipantKindAgent, AgentID: "agent-B", DisplayName: "乙"},
	}, nil
}

// fakeTwoAgentRepoForTurn 解析 agent-A/agent-B 两个智能体对象（子轮按 ID 取回发言人）
type fakeTwoAgentRepoForTurn struct {
	interfaces.CustomAgentRepository
}

func (f *fakeTwoAgentRepoForTurn) GetAgentByID(_ context.Context, id string, _ uint64) (*types.CustomAgent, error) {
	name := "甲"
	if id == "agent-B" {
		name = "乙"
	}
	return &types.CustomAgent{ID: id, Name: name}, nil
}

// TestRunTurnGuardTriggersHostConvergence 真实路径：两个智能体在本轮内互相 @，
// 子轮自然扩展出 3 个来回（6 条 agent 消息）→ 护栏触发 → 主持人收敛 + 广播 guard.triggered
func TestRunTurnGuardTriggersHostConvergence(t *testing.T) {
	ctx := context.Background()
	preset := []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	}
	e, repo := newGuardEngineFixture(t, "room-g", preset)
	e.roomRepo = &fakeGuardRoomRepoForTurn{}           // GuardMaxExchange=3
	e.participantRepo = &fakeTwoAgentParticipantRepo{} // 甲/乙
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	// 每个智能体都 @ 对方 → 每轮都产生新提及 → 子轮持续到硬上限（3 轮 × 2 人 = 6 条）
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		target := "@乙"
		if req.CustomAgent.ID == "agent-B" {
			target = "@甲"
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "我的观点 " + target},
		})
	}}

	ch, unsub := e.broadcaster.Subscribe("room-g")
	defer unsub()

	require.NoError(t, e.RunTurn(ctx, "room-g", "u1"))

	agentMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderAgent)
	require.Len(t, agentMsgs, 6, "3 个子轮 × 2 个智能体 = 6 条互相回应消息")
	assert.Equal(t, "u1", agentMsgs[0].ReplyToMessageID, "首轮回应触发消息")
	assert.NotEqual(t, "u1", agentMsgs[2].ReplyToMessageID, "子轮应回应其他智能体的消息")
	require.Len(t, agentMsgs[0].MentionedAgentIDs, 1, "应落库解析出的提及")

	hostMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderHost)
	require.Len(t, hostMsgs, 1, "护栏触发应产生恰好一条主持人收敛消息")
	assert.Contains(t, hostMsgs[0].Content, "发布方案评审", "兜底收敛文案应包含会议主题")

	var sawGuard bool
	for len(ch) > 0 {
		if evt := <-ch; evt.Type == MeetingEventGuardTriggered {
			sawGuard = true
		}
	}
	assert.True(t, sawGuard, "应广播 guard.triggered 事件")
}

// TestRunTurnStopsWhenNoMention 反向用例：智能体不提及他人 → 不展开子轮，仅 2 条消息、不触发护栏
func TestRunTurnStopsWhenNoMention(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-s", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeGuardRoomRepoForTurn{}
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}

	require.NoError(t, e.RunTurn(ctx, "room-s", "u1"))

	assert.Equal(t, 2, countByKind(repo.snapshot(), types.MeetingSenderAgent), "无提及应自然终止于首轮")
	assert.Equal(t, 0, countByKind(repo.snapshot(), types.MeetingSenderHost), "未达阈值不应触发护栏")
}

// TestRunTurnNoGuardWhenNotBickering 反向用例：尾部仅 2 条交替（不足 3 来回）→ 不应触发护栏
func TestRunTurnNoGuardWhenNotBickering(t *testing.T) {
	ctx := context.Background()
	preset := []*types.MeetingMessage{
		{ID: "m1", SenderKind: types.MeetingSenderUser, Content: "触发"},
		{ID: "g1", SenderKind: types.MeetingSenderAgent, SenderID: "a1", Content: "观点 1"},
		{ID: "g2", SenderKind: types.MeetingSenderAgent, SenderID: "a2", Content: "观点 2"},
	}
	e, repo := newGuardEngineFixture(t, "room-n", preset)

	require.NoError(t, e.RunTurn(ctx, "room-n", "m1"))
	assert.Equal(t, 0, countByKind(repo.snapshot(), types.MeetingSenderHost), "未构成互呛不应产生主持人收敛消息")
}

// TestRunTurnInterruptTriggersHostConvergence 真实路径：用户在讨论进行中打断 →
// 系统提示 + 主持人收敛消息 + 广播 guard.triggered
func TestRunTurnInterruptTriggersHostConvergence(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-i", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeGuardRoomRepoForTurn{}
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	// 首个智能体发言时模拟用户点击"回到正题"（置打断标记）
	e.sessionService = &fakeMeetingSession{agentQA: func(ctx context.Context, _ *types.QARequest, _ *event.EventBus) error {
		return e.redis.Set(ctx, interruptKey("room-i"), "1", time.Minute).Err()
	}}

	ch, unsub := e.broadcaster.Subscribe("room-i")
	defer unsub()

	require.NoError(t, e.RunTurn(ctx, "room-i", "u1"))

	msgs := repo.snapshot()
	sysMsgs := messagesByKind(msgs, types.MeetingSenderSystem)
	require.Len(t, sysMsgs, 1, "打断应落一条系统提示")
	assert.Contains(t, sysMsgs[0].Content, "讨论已被用户打断")

	hostMsgs := messagesByKind(msgs, types.MeetingSenderHost)
	require.Len(t, hostMsgs, 1, "打断应产生恰好一条主持人收敛消息")
	assert.Contains(t, hostMsgs[0].Content, "发布方案评审", "兜底收敛文案应包含会议主题")

	var sawGuard bool
	for len(ch) > 0 {
		if evt := <-ch; evt.Type == MeetingEventGuardTriggered {
			sawGuard = true
		}
	}
	assert.True(t, sawGuard, "打断应广播 guard.triggered")
}

// fakeHostInterventionRoomRepo 房间带主持人介入间隔配置（每 2 个子轮介入一次）
type fakeHostInterventionRoomRepo struct {
	interfaces.MeetingRoomRepository
}

func (f *fakeHostInterventionRoomRepo) GetByIDInternal(_ context.Context, id string) (*types.MeetingRoom, error) {
	return &types.MeetingRoom{
		ID: id, TenantID: 1, Title: "发布方案评审",
		BroadcastMode:    types.MeetingBroadcastSequential,
		GuardMaxExchange: 3,
		HostEveryNRounds: 2,
	}, nil
}

// TestRunDiscussionHostIntervenesEveryNRounds 真实路径：两个智能体前两个子轮互相 @，
// 每完成 2 个子轮主持人插入一条总结/重定向消息；第三个子轮无提及自然终止 → 仅介入一次。
func TestRunDiscussionHostIntervenesEveryNRounds(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-h", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeHostInterventionRoomRepo{} // HostEveryNRounds=2, GuardMaxExchange=3
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	// 前 4 次调用（子轮 1、2）互相 @ 以延续讨论；其后不再 @ → 子轮 3 自然终止
	var calls int32
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		content := "我的观点"
		if atomic.AddInt32(&calls, 1) <= 4 {
			if req.CustomAgent.ID == "agent-B" {
				content += " @甲"
			} else {
				content += " @乙"
			}
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: content},
		})
	}}

	require.NoError(t, e.RunTurn(ctx, "room-h", "u1"))

	msgs := repo.snapshot()
	require.Equal(t, 6, countByKind(msgs, types.MeetingSenderAgent), "3 个子轮 × 2 个智能体")
	hostMsgs := messagesByKind(msgs, types.MeetingSenderHost)
	require.Len(t, hostMsgs, 1, "每 2 轮介入一次：3 个子轮只在第 2 轮后介入 1 次")
	assert.Contains(t, hostMsgs[0].Content, "发布方案评审", "兜底介入文案应包含会议主题")
}

// TestHostInDiscussion 自由抢麦模式下主持人不介入讨论过程，其余模式仍参与
func TestHostInDiscussion(t *testing.T) {
	assert.False(t, hostInDiscussion(&types.MeetingRoom{RoutingMode: types.MeetingRoutingFree}),
		"自由抢麦：主持人不需要介入")
	assert.True(t, hostInDiscussion(&types.MeetingRoom{RoutingMode: types.MeetingRoutingHostAssign}))
	assert.True(t, hostInDiscussion(&types.MeetingRoom{RoutingMode: types.MeetingRoutingMention}))
	assert.True(t, hostInDiscussion(&types.MeetingRoom{}), "旧数据空值等同主持人指派")
}

// fakeFreeRoutingRoomRepo 自由抢麦房间：即便配置了每 2 轮介入 + 护栏阈值 3，
// 主持人在讨论过程中也不应产生任何消息。
type fakeFreeRoutingRoomRepo struct {
	interfaces.MeetingRoomRepository
}

func (f *fakeFreeRoutingRoomRepo) GetByIDInternal(_ context.Context, id string) (*types.MeetingRoom, error) {
	return &types.MeetingRoom{
		ID: id, TenantID: 1, Title: "发布方案评审",
		BroadcastMode:    types.MeetingBroadcastSequential,
		RoutingMode:      types.MeetingRoutingFree,
		GuardMaxExchange: 3,
		HostEveryNRounds: 2,
	}, nil
}

// TestRunTurnFreeRoutingSkipsHost 真实路径：自由抢麦下智能体互相 @ 跑满子轮上限，
// 但不触发阶段总结、也不触发互呛打断（主持人全程不介入）。
func TestRunTurnFreeRoutingSkipsHost(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-free", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeFreeRoutingRoomRepo{}
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		target := "@乙"
		if req.CustomAgent.ID == "agent-B" {
			target = "@甲"
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "我的观点 " + target},
		})
	}}

	require.NoError(t, e.RunTurn(ctx, "room-free", "u1"))

	msgs := repo.snapshot()
	assert.Equal(t, 6, countByKind(msgs, types.MeetingSenderAgent), "子轮仍按硬上限正常展开")
	assert.Equal(t, 0, countByKind(msgs, types.MeetingSenderHost), "自由抢麦下主持人不应产生任何消息")
}

// fakeNoInterventionRoomRepo 关闭主持人介入（0）；护栏阈值放宽到 4，
// 使「前 4 条互 @、其后不再 @」的 6 条讨论消息不足以触发护栏，避免干扰断言。
type fakeNoInterventionRoomRepo struct {
	interfaces.MeetingRoomRepository
}

func (f *fakeNoInterventionRoomRepo) GetByIDInternal(_ context.Context, id string) (*types.MeetingRoom, error) {
	return &types.MeetingRoom{
		ID: id, TenantID: 1, Title: "发布方案评审",
		BroadcastMode:    types.MeetingBroadcastSequential,
		GuardMaxExchange: 4,
		HostEveryNRounds: 0,
	}, nil
}

// TestRunDiscussionNoInterventionWhenDisabled 关闭介入（HostEveryNRounds=0）时讨论照常推进但不产生主持人消息
func TestRunDiscussionNoInterventionWhenDisabled(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-off", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeNoInterventionRoomRepo{}
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	var calls int32
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		content := "我的观点"
		if atomic.AddInt32(&calls, 1) <= 4 {
			if req.CustomAgent.ID == "agent-B" {
				content += " @甲"
			} else {
				content += " @乙"
			}
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: content},
		})
	}}

	require.NoError(t, e.RunTurn(ctx, "room-off", "u1"))

	assert.Equal(t, 6, countByKind(repo.snapshot(), types.MeetingSenderAgent), "历史房间保留批量发言：3 个子轮 × 2 人")
	assert.Equal(t, 0, countByKind(repo.snapshot(), types.MeetingSenderHost), "关闭介入不应产生主持人消息")
}

// TestIsPassReply 验证弃权判定：仅整条为 PASS（允许空白/标点/全角符号）才判为弃权
func TestIsPassReply(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"PASS", true},
		{" pass ", true},
		{"PASS。", true},
		{"**PASS**", true},
		{"Pass\n", true},
		{"", false},
		{"我认为可以 PASS 这个议题", false}, // 正文出现 PASS 不应误判
		{"PASS 之后还有内容", false},
		{"我先赞同 @甲 的观点", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isPassReply(c.in), "input=%q", c.in)
	}
}

// ---- 裁判 + 共识检测测试（③）----

// fakeConsensusRoomRepo 开启裁判 + 共识检测的房间；guardMaxExchange 控制子轮硬上限
type fakeConsensusRoomRepo struct {
	interfaces.MeetingRoomRepository
	guardMaxExchange int
}

func (f *fakeConsensusRoomRepo) GetByIDInternal(_ context.Context, id string) (*types.MeetingRoom, error) {
	return &types.MeetingRoom{
		ID: id, TenantID: 1, Title: "发布方案评审",
		BroadcastMode:             types.MeetingBroadcastSequential,
		GuardMaxExchange:          f.guardMaxExchange,
		ConsensusDetectionEnabled: true,
	}, nil
}

// newConsensusEngineFixture 组装「裁判判定收敛」场景：两个互相 @ 的智能体 + 返回 verdict 的裁判模型。
// mentionUntil<=0 表示始终互相 @；>0 表示仅前 mentionUntil 次发言互相 @。
func newConsensusEngineFixture(
	t *testing.T, roomID, verdict string, roomRepo interfaces.MeetingRoomRepository, mentionUntil int32,
) (*meetingEngine, *fakeMeetingMessageRepo) {
	t.Helper()
	e, repo := newGuardEngineFixture(t, roomID, []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = roomRepo
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	e.modelService = &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{content: verdict},
	}
	var calls int32
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		content := "我的观点"
		if mentionUntil <= 0 || atomic.AddInt32(&calls, 1) <= mentionUntil {
			if req.CustomAgent.ID == "agent-B" {
				content += " @甲"
			} else {
				content += " @乙"
			}
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: content},
		})
	}}
	return e, repo
}

// TestRunDiscussionConsensusConvergesStopsTurn 裁判判定收敛：首个子轮后即落 is_consensus 消息 +
// 广播 consensus.created 并终止讨论（不展开子轮、不触发护栏）
func TestRunDiscussionConsensusConvergesStopsTurn(t *testing.T) {
	ctx := context.Background()
	e, repo := newConsensusEngineFixture(t, "room-c",
		`{"converged":true,"reason":"已无实质分歧","consensus":"采用蓝绿发布"}`,
		&fakeConsensusRoomRepo{guardMaxExchange: 3}, 0)

	ch, unsub := e.broadcaster.Subscribe("room-c")
	defer unsub()

	require.NoError(t, e.RunTurn(ctx, "room-c", "u1"))

	msgs := repo.snapshot()
	assert.Equal(t, 2, countByKind(msgs, types.MeetingSenderAgent), "收敛应发生于首个子轮后，不再展开子轮")
	hostMsgs := messagesByKind(msgs, types.MeetingSenderHost)
	require.Len(t, hostMsgs, 1, "应收敛出一条主持人共识消息")
	assert.True(t, hostMsgs[0].IsConsensus, "共识消息须标记 is_consensus")
	assert.Contains(t, hostMsgs[0].Content, "采用蓝绿发布")

	var sawConsensus, sawGuard bool
	for len(ch) > 0 {
		switch evt := <-ch; evt.Type {
		case MeetingEventConsensusCreated:
			sawConsensus = true
		case MeetingEventGuardTriggered:
			sawGuard = true
		}
	}
	assert.True(t, sawConsensus, "应广播 consensus.created")
	assert.False(t, sawGuard, "收敛终止后不应再触发护栏")
}

// TestRunDiscussionConsensusNotConvergedKeepsGoing 裁判判未收敛：讨论照常推进，不产生共识消息
func TestRunDiscussionConsensusNotConvergedKeepsGoing(t *testing.T) {
	ctx := context.Background()
	// 前 4 次互相 @ → 4 个子轮（每轮 1 条）；GuardMaxExchange=4 使护栏不触发，避免干扰断言
	e, repo := newConsensusEngineFixture(t, "room-nc",
		`{"converged":false,"reason":"仍有分歧"}`,
		&fakeConsensusRoomRepo{guardMaxExchange: 4}, 4)

	require.NoError(t, e.RunTurn(ctx, "room-nc", "u1"))

	msgs := repo.snapshot()
	assert.Equal(t, 6, countByKind(msgs, types.MeetingSenderAgent), "未收敛应推进到硬上限 3 个子轮")
	assert.Equal(t, 0, countByKind(msgs, types.MeetingSenderHost), "未收敛不应产生主持人共识消息")
}

// TestRunDiscussionConsensusDisabledNoReferee 关闭共识检测时不调用裁判：讨论照常推进且无共识消息
func TestRunDiscussionConsensusDisabledNoReferee(t *testing.T) {
	ctx := context.Background()
	e, repo := newGuardEngineFixture(t, "room-cd", []*types.MeetingMessage{
		{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "触发"},
	})
	e.roomRepo = &fakeNoInterventionRoomRepo{} // GuardMaxExchange=4, HostEveryNRounds=0, 共识检测关闭
	e.participantRepo = &fakeTwoAgentParticipantRepo{}
	e.agentRepo = &fakeTwoAgentRepoForTurn{}
	// 注入可用的裁判模型：若误触发裁判，将产生共识消息从而断言失败
	e.modelService = &stubModelService{
		availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
		chatModel:       &hostChatStub{content: `{"converged":true,"consensus":"不该出现"}`},
	}
	var calls int32
	e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
		content := "我的观点"
		if atomic.AddInt32(&calls, 1) <= 4 {
			if req.CustomAgent.ID == "agent-B" {
				content += " @甲"
			} else {
				content += " @乙"
			}
		}
		return bus.Emit(context.Background(), event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: content},
		})
	}}

	require.NoError(t, e.RunTurn(ctx, "room-cd", "u1"))

	assert.Equal(t, 6, countByKind(repo.snapshot(), types.MeetingSenderAgent), "关闭共识检测不影响讨论推进")
	assert.Equal(t, 0, countByKind(repo.snapshot(), types.MeetingSenderHost), "关闭共识检测不应产生共识消息")
}

func TestRunAgentTurnsPassSilentlySkipped(t *testing.T) {
	modes := []struct {
		name string
		mode string
	}{
		{"parallel", types.MeetingBroadcastParallel},
		{"sequential", types.MeetingBroadcastSequential},
	}
	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			// sequential 分支需查打断标记（依赖 redis），故统一走 miniredis
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })

			repo := &fakeMeetingMessageRepo{}
			room := &types.MeetingRoom{ID: "r-pass-" + tc.name, BroadcastMode: tc.mode}
			trigger := triggerFixture("议题")

			sess := &fakeMeetingSession{}
			sess.agentQA = func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
				content := "我的观点"
				if req.CustomAgent.ID == "a2" { // a2 无信息增量，回 PASS
					content = "PASS"
				}
				return bus.Emit(context.Background(), event.Event{
					Type: event.EventAgentFinalAnswer,
					Data: event.AgentFinalAnswerData{Content: content},
				})
			}
			e := &meetingEngine{messageRepo: repo, sessionService: sess, redis: rdb, broadcaster: NewMeetingBroadcaster(), contextMax: 50}

			e.runAgentTurns(context.Background(), room, trigger,
				[]*types.CustomAgent{agentFixture("a1", "甲"), agentFixture("a2", "乙")})

			msgs := repo.snapshot()
			assert.Equal(t, 1, countByKind(msgs, types.MeetingSenderAgent), "弃权者不应落库，仅正常发言者落库")
			assert.Equal(t, 0, countByKind(msgs, types.MeetingSenderSystem), "弃权不应产生失败提示")
			agentMsgs := messagesByKind(msgs, types.MeetingSenderAgent)
			require.Len(t, agentMsgs, 1)
			assert.NotEqual(t, "PASS", agentMsgs[0].Content, "落库内容不应是 PASS")
		})
	}
}

// ---- 圆桌讨论：用户中途插话 / @用户 暂停 / 议题自动推进 ----

// fakeAgendaRoomRepo 启用议程的房间（固定返回同一 room 对象）
type fakeAgendaRoomRepo struct {
	interfaces.MeetingRoomRepository
	room *types.MeetingRoom
}

func (f *fakeAgendaRoomRepo) GetByIDInternal(_ context.Context, _ string) (*types.MeetingRoom, error) {
	return f.room, nil
}

// newAgendaEngineFixture 组装启用议程的圆桌讨论场景：两个参会智能体 + 可配置裁判结论 + 可配置发言内容
func newAgendaEngineFixture(
	t *testing.T, roomID, verdict string,
	items []*types.MeetingAgendaItem, preset []*types.MeetingMessage,
	reply func(req *types.QARequest) string,
) (*meetingEngine, *fakeMeetingMessageRepo, *fakeAgendaRepo) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := &fakeMeetingMessageRepo{preset: preset}
	agendaRepo := &fakeAgendaRepo{items: items}
	e := &meetingEngine{
		roomRepo: &fakeAgendaRoomRepo{room: &types.MeetingRoom{
			ID: roomID, TenantID: 1, Title: "发布方案评审", Status: types.MeetingStatusActive,
			BroadcastMode: types.MeetingBroadcastSequential, AgendaEnabled: true,
			CreatedBy: "owner-1",
		}},
		participantRepo: &fakeTwoAgentParticipantRepo{},
		messageRepo:     repo,
		agendaRepo:      agendaRepo,
		agentRepo:       &fakeTwoAgentRepoForTurn{},
		userService:     &fakeMeetingUserService{},
		modelService: &stubModelService{
			availableModels: []*types.Model{{ID: "m1", Type: types.ModelTypeKnowledgeQA, IsDefault: true}},
			chatModel:       &hostChatStub{content: verdict},
		},
		redis:         rdb,
		broadcaster:   NewMeetingBroadcaster(),
		contextMax:    50,
		turnLockLease: 5 * time.Minute,
	}
	if reply != nil {
		e.sessionService = &fakeMeetingSession{agentQA: func(_ context.Context, req *types.QARequest, bus *event.EventBus) error {
			return bus.Emit(context.Background(), event.Event{
				Type: event.EventAgentFinalAnswer,
				Data: event.AgentFinalAnswerData{Content: reply(req)},
			})
		}}
	}
	return e, repo, agendaRepo
}

// TestRunDiscussionUserInterjectionSwitchesTrigger 讨论进行中用户插话：不必等整轮结束，
// 下一位发言者立即改为回应用户的新消息。
func TestRunDiscussionUserInterjectionSwitchesTrigger(t *testing.T) {
	e, repo, _ := newAgendaEngineFixture(t, "room-interject",
		`{"converged":false,"reason":"仍有分歧"}`,
		[]*types.MeetingAgendaItem{{ID: "i0", Seq: 0, Title: "成本", Goal: "给出区间", Status: types.MeetingAgendaActive}},
		[]*types.MeetingMessage{
			{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "先看成本"},
			{ID: "u2", SenderKind: types.MeetingSenderUser, Content: "补充约束：预算上限 20 万"},
		},
		func(*types.QARequest) string { return "我的观点" },
	)

	require.NoError(t, e.RunTurn(context.Background(), "room-interject", "u1"))

	agentMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderAgent)
	require.GreaterOrEqual(t, len(agentMsgs), 2, "首轮至少一人回应 u1，插话后继续讨论")
	assert.Equal(t, "u1", agentMsgs[0].ReplyToMessageID, "首条应回应触发消息")
	// 插话后引擎应立即切换到新消息：后续发言可接在别人话后，但不得继续回应 u1
	repliedU2 := false
	for _, m := range agentMsgs[1:] {
		assert.NotEqual(t, "u1", m.ReplyToMessageID, "插话后不应继续回应旧消息")
		if m.ReplyToMessageID == "u2" {
			repliedU2 = true
		}
	}
	assert.True(t, repliedU2, "插话后应有人回应新的用户消息")
}

// TestRunDiscussionPausesWhenAgentAsksUser 智能体发言 @用户 提问时暂停讨论并广播 awaiting_user，
// 不再按 @ 提及继续展开子轮。
func TestRunDiscussionPausesWhenAgentAsksUser(t *testing.T) {
	e, repo, _ := newAgendaEngineFixture(t, "room-ask",
		`{"converged":false,"reason":"等待用户输入"}`,
		[]*types.MeetingAgendaItem{{ID: "i0", Seq: 0, Title: "选型", Goal: "确定方案", Status: types.MeetingAgendaActive}},
		[]*types.MeetingMessage{{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "开始"}},
		func(req *types.QARequest) string {
			if req.CustomAgent.ID == "agent-B" {
				return "我倾向 A。@用户 你更看重成本还是稳定性？"
			}
			return "我倾向 B。@乙 你怎么看？"
		},
	)

	ch, unsub := e.broadcaster.Subscribe("room-ask")
	defer unsub()

	require.NoError(t, e.RunTurn(context.Background(), "room-ask", "u1"))

	agentMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderAgent)
	require.Len(t, agentMsgs, 2, "向用户提问后应暂停，不再展开 @ 子轮")
	assert.True(t, asksUserMessage(agentMsgs) != nil, "落库消息应被识别为向用户提问")

	var sawAwait bool
	for len(ch) > 0 {
		if evt := <-ch; evt.Type == MeetingEventAwaitingUser {
			sawAwait = true
		}
	}
	assert.True(t, sawAwait, "应广播 discussion.awaiting_user")
}

// TestRunDiscussionAutoAdvancesAgenda 当前议题覆盖后，主持人自动开下一题并继续讨论，
// 全部议题完成才收束，不要求用户逐个议题点「下一步」。
func TestRunDiscussionAutoAdvancesAgenda(t *testing.T) {
	e, repo, agendaRepo := newAgendaEngineFixture(t, "room-adv",
		`{"converged":true,"goal_covered":true,"reason":"已得出结论","consensus":"结论已确认"}`,
		[]*types.MeetingAgendaItem{
			{ID: "i0", Seq: 0, Title: "成本", Goal: "给出区间", Status: types.MeetingAgendaActive},
			{ID: "i1", Seq: 1, Title: "兼容性", Goal: "列出不兼容项", Status: types.MeetingAgendaPending},
		},
		[]*types.MeetingMessage{{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "开始"}},
		func(*types.QARequest) string { return "我的观点" },
	)

	require.NoError(t, e.RunTurn(context.Background(), "room-adv", "u1"))

	msgs := repo.snapshot()
	assert.Equal(t, 4, countByKind(msgs, types.MeetingSenderAgent), "两个议题各展开一轮，共 4 条智能体发言")
	require.Len(t, agendaRepo.items, 2)
	assert.Equal(t, types.MeetingAgendaDone, agendaRepo.items[0].Status, "首个议题应自动收束")
	assert.Equal(t, types.MeetingAgendaDone, agendaRepo.items[1].Status, "下一议题应自动开题并收束")

	var sawNextOpen, sawWrapUp bool
	for _, m := range messagesByKind(msgs, types.MeetingSenderHost) {
		if strings.Contains(m.Content, "【议题 2/2】兼容性") {
			sawNextOpen = true
		}
		if strings.Contains(m.Content, "【会议结论】") {
			sawWrapUp = true
		}
	}
	assert.True(t, sawNextOpen, "应自动落下一议题的开题消息")
	assert.True(t, sawWrapUp, "全部议题完成后应自动生成会议结论")
}

// fakeMeetingUserService 仅实现 GetUserByID：会议创建者统一解析为「张三」
type fakeMeetingUserService struct {
	interfaces.UserService
}

func (f *fakeMeetingUserService) GetUserByID(_ context.Context, id string) (*types.User, error) {
	return &types.User{ID: id, Username: "张三"}, nil
}

// TestAsksUserByAnswer 发言是否在向用户提问：兼容 @用户名 与 @用户 两种写法
func TestAsksUserByAnswer(t *testing.T) {
	assert.True(t, asksUserByAnswer("@张三 你更看重成本还是稳定性？", "张三"))
	assert.True(t, asksUserByAnswer("@用户 你更看重哪个？", "张三"), "通用占位词仍兼容")
	assert.False(t, asksUserByAnswer("@李四 你怎么看？", "张三"), "提及他人不算向用户提问")
	assert.False(t, asksUserByAnswer("我的观点如下", "张三"))
	assert.True(t, asksUserByAnswer("@用户 请确认", ""), "用户名缺失时仍可用 @用户")
}

// TestAsksUserMessagePrefersPayload payload 已标记 asks_user 的消息应被识别（即使正文没有 @用户）
func TestAsksUserMessagePrefersPayload(t *testing.T) {
	m := &types.MeetingMessage{
		SenderKind: types.MeetingSenderAgent,
		Content:    "请确认预算上限",
		Payload:    []byte(`{"asks_user":true}`),
	}
	assert.NotNil(t, asksUserMessage([]*types.MeetingMessage{m}))
}

// TestRunDiscussionPausesWhenAgentMentionsUserName 智能体用 @用户名 提问时同样暂停讨论，
// 不再只识别 @用户 这一种写法。
func TestRunDiscussionPausesWhenAgentMentionsUserName(t *testing.T) {
	e, repo, _ := newAgendaEngineFixture(t, "room-ask-name",
		`{"converged":false,"reason":"等待用户输入"}`,
		[]*types.MeetingAgendaItem{{ID: "i0", Seq: 0, Title: "选型", Goal: "确定方案", Status: types.MeetingAgendaActive}},
		[]*types.MeetingMessage{{ID: "u1", SenderKind: types.MeetingSenderUser, Content: "开始"}},
		func(req *types.QARequest) string {
			if req.CustomAgent.ID == "agent-B" {
				return "我倾向 A。@张三 你更看重成本还是稳定性？"
			}
			return "我倾向 B。@乙 你怎么看？"
		},
	)

	require.NoError(t, e.RunTurn(context.Background(), "room-ask-name", "u1"))

	agentMsgs := messagesByKind(repo.snapshot(), types.MeetingSenderAgent)
	require.Len(t, agentMsgs, 2, "@用户名 提问后应暂停，不再展开 @ 子轮")
	askMsg := asksUserMessage(agentMsgs)
	require.NotNil(t, askMsg, "应识别出向用户提问的消息")
	assert.Contains(t, askMsg.Content, "@张三")
	assert.NotEmpty(t, askMsg.Payload, "落库时应带 asks_user 标记")
}

// fakeMeetingAttachmentDocs 仅实现会议附件上下文所需的两个方法
type fakeMeetingAttachmentDocs struct {
	interfaces.TemporaryDocumentService
	docs []*types.TemporaryDocument
}

func (f *fakeMeetingAttachmentDocs) List(_ context.Context, _ uint64, _ string) ([]*types.TemporaryDocument, error) {
	return f.docs, nil
}

func (f *fakeMeetingAttachmentDocs) ResolveForPrompt(
	_ context.Context, _ uint64, _ string, ids []string, _ string,
) (*types.TemporaryDocumentPromptResult, error) {
	return &types.TemporaryDocumentPromptResult{Attachments: types.MessageAttachments{{
		ID: ids[0], FileName: "预算.pdf", Content: "预算上限 20 万", ContentMode: types.AttachmentContentModeFull,
	}}}, nil
}

// TestMeetingAttachmentContext 已就绪文件注入正文，解析中的文件只提示状态
func TestMeetingAttachmentContext(t *testing.T) {
	e := &meetingEngine{temporaryDocs: &fakeMeetingAttachmentDocs{docs: []*types.TemporaryDocument{
		{ID: "d1", FileName: "预算.pdf", Status: types.TemporaryDocumentStatusReady},
		{ID: "d2", FileName: "架构.docx", Status: types.TemporaryDocumentStatusProcessing},
	}}}
	room := &types.MeetingRoom{ID: "r1", TenantID: 1}

	got := e.meetingAttachmentContext(context.Background(), room, "预算怎么定？")
	assert.Contains(t, got, "【会议文件资料】")
	assert.Contains(t, got, "预算.pdf")
	assert.Contains(t, got, "预算上限 20 万")
	assert.Contains(t, got, "【文件解析中】架构.docx")
}

// TestMeetingAttachmentContextDisabled 未注入附件服务时不产生任何附件上下文
func TestMeetingAttachmentContextDisabled(t *testing.T) {
	e := &meetingEngine{}
	assert.Empty(t, e.meetingAttachmentContext(context.Background(), &types.MeetingRoom{ID: "r1"}, "问题"))
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeRoomRepoForTurn) UpdatePhase(_ context.Context, _ string, phase string) error {
	return nil
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeGuardRoomRepoForTurn) UpdatePhase(_ context.Context, _ string, phase string) error {
	return nil
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeHostInterventionRoomRepo) UpdatePhase(_ context.Context, _ string, phase string) error {
	return nil
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeFreeRoutingRoomRepo) UpdatePhase(_ context.Context, _ string, phase string) error {
	return nil
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeNoInterventionRoomRepo) UpdatePhase(_ context.Context, _ string, phase string) error {
	return nil
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeConsensusRoomRepo) UpdatePhase(_ context.Context, _ string, phase string) error {
	return nil
}

// UpdatePhase 必须显式实现：嵌入接口只有方法签名没有实现，引擎推进阶段时会 panic 到 nil。
func (f *fakeAgendaRoomRepo) UpdatePhase(_ context.Context, _ string, phase string) error { return nil }
