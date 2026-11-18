package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 说明：fakeArtifactRepo 由 meeting_solution_test.go（T5.2）声明并已实现
// Create/GetByID/ListByRoom/MaxVersionByRoom，此处仅补齐产物重生成链路所需的 Update。

// Update 按 ID 覆盖已落库产物（对齐真实仓储 Save 语义；未命中即报错）
func (f *fakeArtifactRepo) Update(_ context.Context, a *types.MeetingArtifact) error {
	for i, item := range f.created {
		if item.ID == a.ID {
			f.created[i] = a
			return nil
		}
	}
	return errors.New("artifact not found")
}

// stubArtifactEngine 记录产物生成调用与锁状态（genCall/lastArtifact 跨协程读写，用原子量避免数据竞争）
type stubArtifactEngine struct {
	interfaces.MeetingEngine
	running      bool
	panicOnRun   bool
	broadcaster  *MeetingBroadcaster
	genCall      atomic.Int32
	lastArtifact atomic.Pointer[types.MeetingArtifact]
}

// Broadcaster 供 publishEvent 走 broadcasterProvider 分支（与实际引擎一致）
func (e *stubArtifactEngine) Broadcaster() *MeetingBroadcaster { return e.broadcaster }

func (e *stubArtifactEngine) IsArtifactRunning(context.Context, string) (bool, error) {
	return e.running, nil
}

// RunArtifactGeneration 记录入参指针（供快照隔离断言）；panicOnRun 用于验证 recover 兜底
func (e *stubArtifactEngine) RunArtifactGeneration(_ context.Context, _ *types.MeetingRoom, a *types.MeetingArtifact) error {
	e.lastArtifact.Store(a) // 先存指针再计数：等待方看到 genCall==1 时入参指针必已就绪
	e.genCall.Add(1)
	if e.panicOnRun {
		panic("stub artifact engine: 模拟后台生成 panic")
	}
	return nil
}

// waitForEvent 等待广播通道出现指定类型事件（超时即失败）：
// 经 channel 同步建立 happens-before，避免轮询后台写入的仓储切片触发数据竞争
func waitForEvent(t *testing.T, ch chan MeetingEvent, eventType string) MeetingEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == eventType {
				return evt
			}
		case <-deadline:
			t.Fatalf("等待事件 %s 超时", eventType)
			return MeetingEvent{}
		}
	}
}

// fakeSessionMessageService 只实现产物链路用到的 GetMessage，并记录本次调用看到的会话属主身份
type fakeSessionMessageService struct {
	interfaces.MessageService
	messages    map[string]*types.Message
	lastOwnerID string
}

func (f *fakeSessionMessageService) GetMessage(ctx context.Context, _ string, id string) (*types.Message, error) {
	f.lastOwnerID = types.SessionOwnerIDFromContext(ctx)
	if m, ok := f.messages[id]; ok {
		return m, nil
	}
	return nil, assert.AnError
}

func artifactFixture(status string, running bool) (*meetingService, *isolationRoomRepo, *fakeArtifactRepo, *stubArtifactEngine) {
	rooms := &isolationRoomRepo{rooms: map[string]*types.MeetingRoom{}}
	_ = rooms.Create(context.Background(), &types.MeetingRoom{
		ID: "r1", TenantID: 1, Status: status,
		ArtifactSessionID: "sess-1", CreatedBy: "owner-1",
	})
	artifacts := &fakeArtifactRepo{}
	engine := &stubArtifactEngine{running: running, broadcaster: NewMeetingBroadcaster()}
	svc := &meetingService{
		roomRepo:       rooms,
		messageRepo:    &fakeMeetingMessageRepo{},
		artifactRepo:   artifacts,
		messageService: &fakeSessionMessageService{messages: map[string]*types.Message{}},
		engine:         engine,
	}
	return svc, rooms, artifacts, engine
}

func TestRequestArtifactMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("归档拒绝", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusArchived, false)
		_, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeDocx, "报告", "")
		assert.Equal(t, ErrRoomArchived, err)
		assert.Empty(t, arts.created)
	})

	t.Run("非法形态拒绝", func(t *testing.T) {
		svc, _, _, engine := artifactFixture(types.MeetingStatusActive, false)
		_, err := svc.RequestArtifact(ctx, 1, "r1", "u1", "pdf", "报告", "")
		assert.Equal(t, ErrInvalidArtifactKind, err)
		assert.Equal(t, int32(0), engine.genCall.Load())
	})

	t.Run("生成中 409", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, true)
		_, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeDocx, "报告", "")
		assert.Equal(t, ErrArtifactBusy, err)
		assert.Empty(t, arts.created)
	})

	t.Run("正常入队 + 版本递增 + 缺省标题", func(t *testing.T) {
		svc, _, _, engine := artifactFixture(types.MeetingStatusActive, false)
		a, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeMinutes, "", "要含行动项")
		require.NoError(t, err)
		assert.Equal(t, types.MeetingArtifactKindGenerated, a.Kind)
		assert.Equal(t, types.MeetingArtifactTypeMinutes, a.KindType)
		assert.Equal(t, "会议纪要", a.Title, "空标题按形态回缺省")
		assert.Equal(t, "要含行动项", a.Requirement)
		assert.Equal(t, types.MeetingArtifactStatusGenerating, a.Status)
		assert.Equal(t, 1, a.Version)
		waitFor(t, func() bool { return engine.genCall.Load() == 1 }, "应异步触发一次生成")
		// 第二次生成版本 +1（共用序列）
		a2, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeTodoList, "清单", "")
		require.NoError(t, err)
		assert.Equal(t, 2, a2.Version)
	})
}

func TestRegenerateArtifactMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("solution 产物不可重生成", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "s1", MeetingRoomID: "r1", Version: 1, SolutionMessageID: "m1",
			Kind: types.MeetingArtifactKindSolution,
		}))
		_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "s1")
		assert.Equal(t, ErrNotRegenerable, err)
	})

	t.Run("历史空 kind 行按 solution 处理", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "legacy", MeetingRoomID: "r1", Version: 1, SolutionMessageID: "m1",
		}))
		_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "legacy")
		assert.Equal(t, ErrNotRegenerable, err)
	})

	t.Run("generating 中拒绝", func(t *testing.T) {
		svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "g1", MeetingRoomID: "r1", Version: 1,
			Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
			Status: types.MeetingArtifactStatusGenerating,
		}))
		_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "g1")
		assert.Equal(t, ErrArtifactBusy, err)
	})

	t.Run("正常重生成重置字段", func(t *testing.T) {
		svc, _, arts, engine := artifactFixture(types.MeetingStatusActive, false)
		require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
			ID: "g2", MeetingRoomID: "r1", Version: 3,
			Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
			Title: "报告", Requirement: "含图表", Status: types.MeetingArtifactStatusFailed,
			Error: "boom", FileName: "old.docx", URL: "provider://old", FileSize: 2048,
		}))
		a, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "g2")
		require.NoError(t, err)
		assert.Equal(t, types.MeetingArtifactStatusGenerating, a.Status)
		assert.Empty(t, a.Error)
		assert.Empty(t, a.FileName)
		assert.Empty(t, a.URL)
		assert.Zero(t, a.FileSize, "文件体积应一并清零，避免残留旧值污染详情")
		assert.Equal(t, 3, a.Version, "重生成沿用原版本号")
		assert.Equal(t, "含图表", a.Requirement, "原参数保留供回放")
		waitFor(t, func() bool { return engine.genCall.Load() == 1 }, "应异步触发重新生成")
	})
}

func TestGetArtifactDetailContentJoin(t *testing.T) {
	ctx := context.Background()
	svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
	// generated 产物：正文来自后台会话消息
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeMinutes,
		Status: types.MeetingArtifactStatusReady, SourceMessageID: "sm-1",
	}))
	d, err := svc.GetArtifactDetail(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Empty(t, d.Content, "源消息不存在时降级空正文")

	// 注入源消息后能带出正文
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Content: "纪要正文"},
	}}
	d2, err := svc.GetArtifactDetail(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Equal(t, "纪要正文", d2.Content)
}

func TestResolveArtifactDownload(t *testing.T) {
	ctx := context.Background()
	svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
		Status: types.MeetingArtifactStatusReady, SourceMessageID: "sm-1", FileName: "report.docx",
	}))
	// 源消息带两个产物，目标文件按文件名定位索引 1
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Artifacts: types.MessageArtifacts{
			{FileName: "notes.md"}, {FileName: "report.docx"},
		}},
	}}
	loc, err := svc.ResolveArtifactDownload(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/sessions/sess-1/messages/sm-1/artifacts/1/download", loc)

	// 文件名不匹配 → 报错
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Artifacts: types.MessageArtifacts{
			{FileName: "other.xlsx"},
		}},
	}}
	_, err = svc.ResolveArtifactDownload(ctx, 1, "r1", "g1")
	assert.Error(t, err)
}

// TestExportGeneratedArtifact 文本形态产物（无文件）也要能本地导出：
// 正文取后台会话 assistant 消息，文件名取产物标题；缺源消息时明确报错而不是导出空文件
func TestExportGeneratedArtifact(t *testing.T) {
	ctx := context.Background()
	svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeReportMarkdown,
		Title: "安防智能体应用范围", Status: types.MeetingArtifactStatusReady, SourceMessageID: "sm-1",
	}))
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Content: "# 报告正文"},
	}}

	filename, content, err := svc.ExportArtifact(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Equal(t, "安防智能体应用范围.md", filename)
	assert.Equal(t, "# 报告正文", content)

	// 源消息缺失：明确报错，避免下载到空文件
	svc.messageService = &fakeSessionMessageService{messages: map[string]*types.Message{}}
	_, _, err = svc.ExportArtifact(ctx, 1, "r1", "g1")
	assert.Error(t, err)
}

// TestArtifactReturnIsolatesBackgroundMutation 返回值与后台协程解耦：
// RequestArtifact 必须返回快照，否则调用方持有的结构会被后台生成回写（数据竞争）
func TestArtifactReturnIsolatesBackgroundMutation(t *testing.T) {
	ctx := context.Background()
	svc, _, _, engine := artifactFixture(types.MeetingStatusActive, false)

	a, err := svc.RequestArtifact(ctx, 1, "r1", "u1", types.MeetingArtifactTypeMinutes, "纪要", "")
	require.NoError(t, err)
	waitFor(t, func() bool { return engine.genCall.Load() == 1 }, "应异步触发一次生成")
	assert.NotSame(t, a, engine.lastArtifact.Load(), "返回值应为快照，不与后台协程共享同一结构")
}

// TestArtifactInternalReadAlignsToRoomOwner 内部会话读取身份对齐房间创建者：
// 调用者是同租户管理员（非创建者）时，后台会话属主校验不能因此拒绝，否则详情降级空正文、下载恒 404
func TestArtifactInternalReadAlignsToRoomOwner(t *testing.T) {
	// 调用者身份为 admin-9，房间创建者仍为 fixture 中的 owner-1
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "admin-9")
	svc, _, arts, _ := artifactFixture(types.MeetingStatusActive, false)
	rec := &fakeSessionMessageService{messages: map[string]*types.Message{
		"sm-1": {ID: "sm-1", SessionID: "sess-1", Content: "纪要正文"},
	}}
	svc.messageService = rec
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeMinutes,
		Status: types.MeetingArtifactStatusReady, SourceMessageID: "sm-1",
	}))

	d, err := svc.GetArtifactDetail(ctx, 1, "r1", "g1")
	require.NoError(t, err)
	assert.Equal(t, "纪要正文", d.Content)
	assert.Equal(t, "owner-1", rec.lastOwnerID, "内部会话读取应以房间创建者身份进行")
}

// TestArtifactPanicMarksFailed panic 兜底：后台生成 panic 必须落 failed，
// 否则产物永久停在 generating，RegenerateArtifact 恒返回 ErrArtifactBusy 而无法自愈
func TestArtifactPanicMarksFailed(t *testing.T) {
	ctx := context.Background()
	svc, _, arts, engine := artifactFixture(types.MeetingStatusActive, false)
	engine.panicOnRun = true
	ch, unsub := engine.Broadcaster().Subscribe("r1")
	defer unsub()
	require.NoError(t, arts.Create(ctx, &types.MeetingArtifact{
		ID: "g1", MeetingRoomID: "r1", Version: 1,
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
		Status: types.MeetingArtifactStatusReady,
	}))

	_, err := svc.RegenerateArtifact(ctx, 1, "r1", "u1", "g1")
	require.NoError(t, err)
	waitForEvent(t, ch, MeetingEventArtifactFailed) // 事件同步建立 happens-before，后续读取才安全

	stored, err := arts.GetByID(ctx, "g1", "r1")
	require.NoError(t, err)
	assert.Equal(t, types.MeetingArtifactStatusFailed, stored.Status)
	assert.Contains(t, stored.Error, "panic")
}
