package service

import (
	"context"
	"errors"
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
	q := buildMeetingQuery(room, "讨论下部署方案", msgs)
	// 应包含会议主题与共享上下文
	assert.Contains(t, q, room.Title)
	assert.Contains(t, q, "讨论下部署方案")
	assert.Contains(t, q, msgs[0].Content)
}

func roomFixture() *types.MeetingRoom {
	return &types.MeetingRoom{ID: "r1", Title: "发布方案评审", KnowledgeBaseIDs: []string{"c"}}
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
}

func (f *fakeMeetingMessageRepo) Create(_ context.Context, msg *types.MeetingMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, msg)
	return nil
}

func (f *fakeMeetingMessageRepo) ListRecent(_ context.Context, _ string, _ int) ([]*types.MeetingMessage, error) {
	return nil, nil
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

// TestRunAgentTurnsSequentialInterrupted 验证 sequential 在打断标记存在时终止且不调用 AgentQA
func TestRunAgentTurnsSequentialInterrupted(t *testing.T) {
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
	msgs := repo.snapshot()
	require.Len(t, msgs, 1)
	assert.Equal(t, types.MeetingSenderSystem, msgs[0].SenderKind)
	assert.Contains(t, msgs[0].Content, "讨论已被用户打断")
}
