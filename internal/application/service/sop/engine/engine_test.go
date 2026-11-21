package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// newSopEngineTestDB 建 sqlite :memory: 测试库并 AutoMigrate 全部 sop_* 模型。
//
// 只留单连接（SetMaxOpenConns(1)）：内存库每个连接各有一份独立库，多连接会看到
// 不同的表/行；引擎测试是同步驱动（DisableRunLoop=true），单连接足够。
// GORM tag 表达不了 ux_sop_node_runs_run_node，按迁移原文补上，让「不重复创建
// 节点运行记录」这条不变式由库本身兜底。
func newSopEngineTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(
		&types.SopProject{},
		&types.SopItem{},
		&types.SopItemAttachment{},
		&types.SopItemClarification{},
		&types.SopItemActivity{},
		&types.SopTemplate{},
		&types.SopTemplateRevision{},
		&types.SopRun{},
		&types.SopNodeRun{},
		&types.SopArtifact{},
		&types.SopCLICredential{},
		&types.SopTriggerConfig{},
		&types.SopTriggerLog{},
		&types.SopDispatchReservation{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	return db
}

// fakeProvider 是最小执行器：写出一个产物文件并回报固定结论，用于注册表装配。
type fakeProvider struct {
	kind string
	out  string
	err  error
}

func (f *fakeProvider) Kind() string { return f.kind }

func (f *fakeProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error { return nil }

func (f *fakeProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	if f.err != nil {
		return executor.ExecResult{}, f.err
	}
	if tb.OutDir != "" {
		_ = os.WriteFile(filepath.Join(tb.OutDir, "a.md"), []byte("# out"), 0o644)
	}
	return executor.ExecResult{Output: f.out, GateResult: types.SopGatePass}, nil
}

func (f *fakeProvider) Cancel(ctx context.Context) error { return nil }

// stubItemStatus 记录事项状态联动调用（引擎不得直接写事项状态）。
type stubItemStatus struct{ calls [][2]string }

func (s *stubItemStatus) UpdateStatus(ctx context.Context, t uint64, id, to string) error {
	s.calls = append(s.calls, [2]string{id, to})
	return nil
}

// newTestEngine 组装一个不启动推进循环的引擎（TickOnce/Advance 由测试同步驱动）。
func newTestEngine(t *testing.T) (*Engine, *stubItemStatus, *gorm.DB) {
	t.Helper()
	db := newSopEngineTestDB(t)
	reg := executor.NewRegistry()
	reg.Register(&fakeProvider{kind: "platform_agent", out: "done"})
	st := &stubItemStatus{}
	e := NewEngine(Deps{
		Runs: repository.NewSopRunRepo(db), Templates: repository.NewSopTemplateRepo(db),
		Projects: repository.NewSopProjectRepo(db), Items: repository.NewSopItemRepo(db),
		Extras: repository.NewSopItemExtraRepo(db), Registry: reg, ItemStatus: st,
		WorkspaceRoot: t.TempDir(), MaxParallel: 2, DisableRunLoop: true,
	})
	return e, st, db
}

func TestStartQueuedCreatesWorkspaceAndNodeRuns(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "A", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Name: "B", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def, ItemID: "item1"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	require.DirExists(t, got.WorkspaceDir)
	ns, _ := e.deps.Runs.ListNodeRuns(ctx, 1, run.ID)
	require.Len(t, ns, 2)
	for _, n := range ns {
		require.Equal(t, types.SopNodePending, n.Status)
	}
}

// 同一 queued run 不能被认领两次：第二轮 tick 既不重开工作区，也不重复建节点记录。
func TestNoDoubleStart(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))

	require.NoError(t, e.TickOnce(ctx))
	first, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunRunning, first.Status)

	require.NoError(t, e.TickOnce(ctx))
	second, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunRunning, second.Status)
	require.Equal(t, first.WorkspaceDir, second.WorkspaceDir)

	ns, err := e.deps.Runs.ListNodeRuns(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Len(t, ns, 2, "第二轮不得重复创建节点运行记录")
}

func TestFinalizeSuccessLinksItemAndNotify(t *testing.T) {
	e, st, _ := newTestEngine(t)
	var notified []string
	e.deps.Notify = func(ctx context.Context, t uint64, kind, title, body, ref string) {
		notified = append(notified, kind)
	}
	ctx := context.Background()
	item := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusInProgress}
	require.NoError(t, e.deps.Items.Create(ctx, item))
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}, OnSuccessItemStatus: "in_review"}
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def, ItemID: item.ID,
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded,
	}))
	require.NoError(t, e.finalizeRun(ctx, run))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.NotNil(t, got.FinishedAt)
	require.Equal(t, [][2]string{{item.ID, "in_review"}}, st.calls)
	require.Equal(t, []string{"sop_run_finished"}, notified)
}

func TestFinalizeFailedSkipsPendingAndNotifies(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}, {ID: "n2", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeFailed, Error: "boom",
	}))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n2", Status: types.SopNodePending,
	}))
	require.NoError(t, e.finalizeRun(ctx, run))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Equal(t, "boom", got.Error)
	ns, _ := e.deps.Runs.ListNodeRuns(ctx, 1, run.ID)
	for _, n := range ns {
		if n.NodeID == "n2" {
			require.Equal(t, types.SopNodeSkipped, n.Status)
		}
	}
}

func TestExpireLeasesOnTick(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.Heartbeat(ctx, run.ID, time.Now().Add(-time.Minute)))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "租约")
}

// 开始执行时事项从 todo 推进到 in_progress；已是其它状态则不动（引擎不替用户决策）。
func TestStartQueuedLinksItemToInProgress(t *testing.T) {
	e, st, _ := newTestEngine(t)
	ctx := context.Background()
	todo := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusTodo}
	require.NoError(t, e.deps.Items.Create(ctx, todo))
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def, ItemID: todo.ID}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	require.Equal(t, [][2]string{{todo.ID, types.SopItemStatusInProgress}}, st.calls)

	// 事项已不在 todo（例如刚才那次联动已落库）：不再重复联动。
	other := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "另一个", Status: types.SopItemStatusInProgress}
	require.NoError(t, e.deps.Items.Create(ctx, other))
	run2 := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def, ItemID: other.ID}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run2))
	require.NoError(t, e.TickOnce(ctx))
	require.Len(t, st.calls, 1)
}

// Start/Stop 生命周期：ticker 在测试模式下也能认领并初始化 queued 运行，
// Stop 后不再有新运行被接手。
func TestStartStopLifecycle(t *testing.T) {
	e, _, _ := newTestEngine(t)
	// 只验证 ticker 的认领/停止：推进循环由 T13 的 node.go 提供，测试不驱动它。
	e.deps.DisableRunLoop = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, e.Start(ctx))

	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))

	require.Eventually(t, func() bool {
		got, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
		return err == nil && got.Status == types.SopRunRunning
	}, 10*time.Second, 50*time.Millisecond, "ticker 未在预期时间内认领运行")

	require.NoError(t, e.Stop(context.Background()))

	queued := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, queued))
	time.Sleep(tickInterval + 500*time.Millisecond) // 留出一轮余量，确认没有残留 ticker
	got, err := e.deps.Runs.GetRun(ctx, 1, queued.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunQueued, got.Status, "Stop 后不得再有运行被认领")
}

// blockingAdvance 让推进卡在「执行中」直到 ctx 被取消，再原样返回取消错误：
// 这正是 CancelRun/Stop 打断在途推进时的形态。
func blockingAdvance(entered chan struct{}) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, _ string) (bool, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-ctx.Done()
		return false, ctx.Err()
	}
}

// 取消发生在推进途中时，运行必须收口为 cancelled（不是 failed），且不发 sop_run_failed。
func TestCancelMidDriveIsNotFailure(t *testing.T) {
	e, _, _ := newTestEngine(t)
	notified := make(chan string, 8)
	e.deps.Notify = func(_ context.Context, _ uint64, kind, _, _, _ string) { notified <- kind }
	entered := make(chan struct{})
	e.advanceHook = blockingAdvance(entered)

	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeRunning,
	}))

	go e.runLoop(run.ID)
	require.Eventually(t, func() bool {
		select {
		case <-entered:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "推进未被驱动")

	e.CancelRun(run.ID)
	require.Eventually(t, func() bool { return e.activeCount() == 0 },
		10*time.Second, 20*time.Millisecond, "推进协程未退出")

	got, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunCancelled, got.Status, "取消不得落成 failed")
	require.NotNil(t, got.FinishedAt)
	require.Empty(t, notified, "取消不发任何终态通知")
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, 1, run.ID)
	require.NoError(t, err)
	for _, nr := range nrs {
		require.Equal(t, types.SopNodeCancelled, nr.Status)
	}
}

// 重复 Start 必须是 no-op：若第二次 Start 取消 e.ctx，在途 runLoop 会被误判为取消。
func TestRepeatedStartKeepsActiveRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	entered := make(chan struct{})
	e.advanceHook = blockingAdvance(entered)
	require.NoError(t, e.Start(context.Background()))
	defer func() { require.NoError(t, e.Stop(context.Background())) }()

	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	go e.runLoop(run.ID)
	require.Eventually(t, func() bool {
		select {
		case <-entered:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "推进未被驱动")

	require.NoError(t, e.Start(context.Background()), "重复 Start 应返回成功")
	time.Sleep(100 * time.Millisecond) // 给旧实现「第二次 Start 取消在途运行」留出发生窗口

	// 在途运行必须还活着：真正的取消要等显式 CancelRun 才发生。
	require.Equal(t, 1, e.activeCount(), "重复 Start 不得终止在途推进")
	e.CancelRun(run.ID)
	require.Eventually(t, func() bool { return e.activeCount() == 0 },
		10*time.Second, 20*time.Millisecond, "推进协程未退出")
	got, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunCancelled, got.Status, "显式取消仍应收口为 cancelled")
}

// successRunWithNode 造一条 running 运行 + 一个 succeeded 节点，供收口类测试复用。
func successRunWithNode(t *testing.T, e *Engine, def types.SopDefinition, projectID, itemID string) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def,
		ProjectID: projectID, ItemID: itemID,
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded,
	}))
	return run
}

// 模板级 OnSuccessItemStatus 优先于项目级。
func TestSuccessItemStatusTemplateWins(t *testing.T) {
	e, st, _ := newTestEngine(t)
	ctx := context.Background()
	item := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusInProgress}
	require.NoError(t, e.deps.Items.Create(ctx, item))
	proj := &types.SopProject{TenantID: 1, Name: "P", OnRunSuccessItemStatus: "done"}
	require.NoError(t, e.deps.Projects.Create(ctx, proj))

	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}, OnSuccessItemStatus: "in_review"}
	run := successRunWithNode(t, e, def, proj.ID, item.ID)
	require.NoError(t, e.finalizeRun(ctx, run))
	require.Equal(t, [][2]string{{item.ID, types.SopItemStatusInReview}}, st.calls)
}

// 模板未声明时回退到项目级 on_run_success_item_status。
func TestSuccessItemStatusFallsBackToProject(t *testing.T) {
	e, st, _ := newTestEngine(t)
	ctx := context.Background()
	item := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusInProgress}
	require.NoError(t, e.deps.Items.Create(ctx, item))
	proj := &types.SopProject{TenantID: 1, Name: "P", OnRunSuccessItemStatus: "in_review"}
	require.NoError(t, e.deps.Projects.Create(ctx, proj))

	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := successRunWithNode(t, e, def, proj.ID, item.ID)
	require.NoError(t, e.finalizeRun(ctx, run))
	require.Equal(t, [][2]string{{item.ID, types.SopItemStatusInReview}}, st.calls)
}

// 模板与项目都未声明 → 不做任何事项状态改动。
func TestSuccessItemStatusBothEmptyNoUpdate(t *testing.T) {
	e, st, _ := newTestEngine(t)
	ctx := context.Background()
	item := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusInProgress}
	require.NoError(t, e.deps.Items.Create(ctx, item))
	proj := &types.SopProject{TenantID: 1, Name: "P"}
	require.NoError(t, e.deps.Projects.Create(ctx, proj))

	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := successRunWithNode(t, e, def, proj.ID, item.ID)
	require.NoError(t, e.finalizeRun(ctx, run))
	require.Empty(t, st.calls)
	got, err := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, got.Status)
}

// 事项读不到时不得继续写状态（读错误不能退化成「照写」）。
func TestSuccessItemStatusSkipsWhenItemUnreadable(t *testing.T) {
	e, st, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}, OnSuccessItemStatus: "in_review"}
	run := successRunWithNode(t, e, def, "", "missing-item")
	require.NoError(t, e.finalizeRun(ctx, run))
	require.Empty(t, st.calls)
}

// finalizeRun 幂等：二次收口不再产生事项联动与通知。
func TestFinalizeRunIdempotent(t *testing.T) {
	e, st, _ := newTestEngine(t)
	notified := make(chan string, 8)
	e.deps.Notify = func(_ context.Context, _ uint64, kind, _, _, _ string) { notified <- kind }
	ctx := context.Background()
	item := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusInProgress}
	require.NoError(t, e.deps.Items.Create(ctx, item))

	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}, OnSuccessItemStatus: "in_review"}
	run := successRunWithNode(t, e, def, "", item.ID)
	require.NoError(t, e.finalizeRun(ctx, run))
	require.Len(t, st.calls, 1)

	require.NoError(t, e.finalizeRun(ctx, run), "二次收口应静默返回")
	require.Len(t, st.calls, 1, "二次收口不得再联动事项")
	require.Len(t, notified, 1, "二次收口不得重复通知")
}
