package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// waitingRun 造一条等待审批的运行：n1 处于 waiting_approval（已尝试 2 次）。
func waitingRun(t *testing.T, e *Engine, def types.SopDefinition) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def, WorkspaceDir: t.TempDir(),
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval, Attempt: 2,
	}))
	return run
}

func TestApproveWritesDecisionAndContinues(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.Approve(ctx, 1, run.ID, "n1", "同意上线", "张三"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeSucceeded, nr.Status)
	require.Equal(t, types.SopGatePass, nr.GateResult)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	// decision.md 落盘且含审批人与产物哈希段
	matches, _ := filepath.Glob(filepath.Join(run.WorkspaceDir, ".sop", "decisions", "n1-*.md"))
	require.Len(t, matches, 1)
	body, _ := os.ReadFile(matches[0])
	require.Contains(t, string(body), "张三")
	require.Contains(t, string(body), "同意上线")
	require.Contains(t, string(body), "sha256")
}

func TestApproveRequiresWaitingApproval(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := waitingRun(t, e, types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}})
	require.NoError(t, e.Approve(ctx, 1, run.ID, "n1", "同意", "张三"))
	err := e.Approve(ctx, 1, run.ID, "n1", "再同意一次", "张三")
	require.Error(t, err)
	require.Contains(t, err.Error(), "不在等待审批")
	// 租户不匹配：既读不到运行也不得改状态。
	require.Error(t, e.Approve(ctx, 2, run.ID, "n1", "同意", "张三"))
}

func TestRejectApprovalResetsUpstream(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n0", Type: "agent", MaxReworks: 3},
		{ID: "n1", Type: "approval", RejectTo: "n0"},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n0", Target: "n1"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n0", Status: types.SopNodeSucceeded, Output: "old",
	}))
	require.NoError(t, e.Reject(ctx, 1, run.ID, "n1", "方案重做", "李四", ""))
	n0, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n0")
	require.Equal(t, types.SopNodePending, n0.Status)
	require.Empty(t, n0.Output)
	require.Equal(t, "方案重做", n0.Feedback)
	require.Equal(t, 1, n0.ReworkCount)
	n1, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, n1.Status)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
}

func TestRejectExplicitTargetWinsOverTemplate(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n0", Type: "agent"},
		{ID: "n2", Type: "agent"},
		{ID: "n1", Type: "approval", RejectTo: "n0"},
	}, Edges: []types.SopEdge{
		{ID: "e1", Source: "n0", Target: "n2"},
		{ID: "e2", Source: "n2", Target: "n1"},
	}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n0", Status: types.SopNodeSucceeded,
	}))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n2", Status: types.SopNodeSucceeded,
	}))
	// 参数指定打回 n2（模板写的是 n0）：n2 与其下游 n1 重置，n0 不动。
	require.NoError(t, e.Reject(ctx, 1, run.ID, "n1", "就近打回", "李四", "n2"))
	n2, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.Equal(t, types.SopNodePending, n2.Status)
	require.Equal(t, "就近打回", n2.Feedback)
	n0, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n0")
	require.Equal(t, types.SopNodeSucceeded, n0.Status, "未指定的上游不得被重置")
}

func TestRejectExhaustedAgentNodeRestartsBudget(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", OnFailure: "loop", MaxAttempts: 2}}}
	run := waitingRun(t, e, def) // n1 因重试耗尽 waiting_approval
	require.NoError(t, e.Reject(ctx, 1, run.ID, "n1", "按新口径重来", "王五", ""))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, 0, nr.Attempt) // 人工打回重置自动重试预算
	require.Equal(t, "按新口径重来", nr.Feedback)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
}

func TestRejectApprovalWithoutTargetIsRejected(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := waitingRun(t, e, def)
	err := e.Reject(ctx, 1, run.ID, "n1", "不行", "李四", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "请选择打回哪一步")
	// 拒绝失败不改状态：节点仍在等待审批。
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeWaitingApproval, nr.Status)
}

func TestRejectExhaustedTargetGoesWaiting(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n0", Type: "agent", MaxReworks: 1},
		{ID: "n1", Type: "approval", RejectTo: "n0"},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n0", Target: "n1"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n0", Status: types.SopNodeSucceeded, ReworkCount: 1,
	}))
	require.NoError(t, e.Reject(ctx, 1, run.ID, "n1", "再改一次也不行", "李四", ""))
	n0, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n0")
	require.Equal(t, types.SopNodeWaitingApproval, n0.Status, "返工次数用尽转人工")
	require.Equal(t, 2, n0.ReworkCount)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
}

func TestReworkCurrentKeepsNodeAndResetsBudget(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.ReworkCurrent(ctx, 1, run.ID, "n1", "补充：优先移动端", "赵六"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, 0, nr.Attempt)
	require.Equal(t, "补充：优先移动端", nr.Feedback)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	require.Len(t, got.HumanContext, 1) // 补充信息同时进人工上下文
}

func TestTerminateFailsRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "approval"},
		{ID: "n2", Type: "agent"},
	}}
	run := waitingRun(t, e, def)
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n2", Status: types.SopNodePending,
	}))
	require.NoError(t, e.Terminate(ctx, 1, run.ID, "n1", "需求取消", "王五"))
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeFailed, nr.Status)
	require.Contains(t, nr.Error, "人工终止")
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "需求取消")
	require.NotNil(t, got.FinishedAt)
	// 未开始的节点被收口为 skipped。
	n2, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.Equal(t, types.SopNodeSkipped, n2.Status)
}

func TestRetryFailedRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunFailed, WorkflowSnapshot: def, Error: "boom"}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeFailed, Attempt: 3, Error: "boom",
	}))
	require.NoError(t, e.Retry(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunQueued, got.Status)
	require.Empty(t, got.Error)
	require.Nil(t, got.FinishedAt)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, nr.Status)
	require.Equal(t, 0, nr.Attempt)
}

func TestRetryRejectsNonTerminalRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	err := e.Retry(ctx, 1, run.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "失败或已取消")
}

func TestCancelInactiveRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunPaused,
		WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}},
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodePending,
	}))
	require.NoError(t, e.Cancel(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunCancelled, got.Status)
	require.NotNil(t, got.FinishedAt)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeCancelled, nr.Status)
}

func TestPauseResumeFlags(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.Pause(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.True(t, got.PauseRequested)
	got.Status = types.SopRunPaused
	require.NoError(t, e.deps.Runs.UpdateRun(ctx, got))
	require.NoError(t, e.Resume(ctx, 1, run.ID))
	got, _ = e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunQueued, got.Status)
	require.False(t, got.PauseRequested)
	// 非运行中不可暂停；非暂停态不可恢复。
	require.Error(t, e.Pause(ctx, 1, run.ID))
	require.Error(t, e.Resume(ctx, 1, run.ID))
}

func TestAddHumanContextAppends(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.AddHumanContext(ctx, 1, run.ID, "优先移动端", "manual", "n1"))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Len(t, got.HumanContext, 1)
	require.Equal(t, "优先移动端", got.HumanContext[0].Text)
	require.Equal(t, "n1", got.HumanContext[0].NodeID)
	require.Equal(t, "manual", got.HumanContext[0].Source)
}

// fakeNodeUpdatedAt 直接改库里的 updated_at（UpdateNodeRun 会刷新它为 now，
// 伪造「久等」只能绕开列白名单直接写库）。
func fakeNodeUpdatedAt(t *testing.T, e *Engine, db *gorm.DB, runID, nodeID string, at time.Time) {
	t.Helper()
	nr, err := e.deps.Runs.GetNodeRun(context.Background(), 1, runID, nodeID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.SopNodeRun{}).
		Where("id = ?", nr.ID).Update("updated_at", at).Error)
}

func TestApprovalTimeoutSweepFailsRun(t *testing.T) {
	e, _, db := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "approval", ApprovalTimeoutHours: 1, ApprovalTimeoutAction: "fail",
	}}}
	run := waitingRun(t, e, def)
	fakeNodeUpdatedAt(t, e, db, run.ID, "n1", time.Now().Add(-2*time.Hour))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "审批超时")
	matches, _ := filepath.Glob(filepath.Join(run.WorkspaceDir, ".sop", "decisions", "n1-*.md"))
	require.Len(t, matches, 1, "超时终止同样要留痕")
}

func TestApprovalDefaultWaitsForever(t *testing.T) {
	e, _, db := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}} // 无超时配置=无限等待
	run := waitingRun(t, e, def)
	fakeNodeUpdatedAt(t, e, db, run.ID, "n1", time.Now().Add(-30*24*time.Hour))
	require.NoError(t, e.TickOnce(ctx))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status) // 人不在就停着（spec §6）
}

func TestApprovalTimeoutNotifyReminderOnce(t *testing.T) {
	e, _, db := newTestEngine(t)
	var kinds []string
	e.deps.Notify = func(_ context.Context, _ uint64, kind, _, _, _ string) { kinds = append(kinds, kind) }
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "approval", ApprovalTimeoutHours: 1, ApprovalTimeoutAction: "notify",
	}}}
	run := waitingRun(t, e, def)
	fakeNodeUpdatedAt(t, e, db, run.ID, "n1", time.Now().Add(-2*time.Hour))
	require.NoError(t, e.TickOnce(ctx))
	require.NoError(t, e.TickOnce(ctx)) // 第二轮不重复提醒
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
	require.Equal(t, []string{"sop_approval"}, kinds)
}

// 重启后 waiting 运行必须被接回推进（没人审批的 waiting 不会经 ClaimDue 认领）。
func TestResumeWaitingAfterRestart(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.DisableRunLoop = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.mu.Lock()
	e.ctx, e.cancel = ctx, cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		require.Eventually(t, func() bool { return e.activeCount() == 0 },
			5*time.Second, 20*time.Millisecond, "推进协程未退出")
	}()

	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "approval"}}}
	run := waitingRun(t, e, def)
	require.Equal(t, 0, e.activeCount())
	require.NoError(t, e.TickOnce(ctx))
	require.Eventually(t, func() bool { return e.activeCount() == 1 },
		5*time.Second, 20*time.Millisecond, "waiting 运行未被接回推进")
	require.True(t, e.isActive(run.ID))
}
