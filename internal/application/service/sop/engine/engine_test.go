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
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}, OnSuccessItemStatus: "in_review"}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning, WorkflowSnapshot: def, ItemID: "item1"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded,
	}))
	require.NoError(t, e.finalizeRun(ctx, run))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.NotNil(t, got.FinishedAt)
	require.Equal(t, [][2]string{{"item1", "in_review"}}, st.calls)
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
