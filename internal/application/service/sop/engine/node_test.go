package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// scriptedProvider 按调用次序返回预设步骤，并写出 out/a.md 供产物检查命中。
// 步骤用尽后一律回报 pass（避免测试因次数估计偏差而挂）。
type scriptedProvider struct {
	kind  string
	steps []executor.ExecResult
	mu    sync.Mutex
	i     int
}

func (s *scriptedProvider) Kind() string { return s.kind }

func (s *scriptedProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error {
	return nil
}

func (s *scriptedProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	s.mu.Lock()
	var r executor.ExecResult
	if s.i >= len(s.steps) {
		r = executor.ExecResult{GateResult: types.SopGatePass}
	} else {
		r = s.steps[s.i]
		s.i++
	}
	s.mu.Unlock()
	if tb.OutDir != "" {
		_ = os.WriteFile(filepath.Join(tb.OutDir, "a.md"), []byte(r.Output), 0o644)
	}
	return r, nil
}

func (s *scriptedProvider) Cancel(ctx context.Context) error { return nil }

// silentProvider 什么都不写（用于「必需产物缺失」路径）。
type silentProvider struct{ kind string }

func (s *silentProvider) Kind() string { return s.kind }

func (s *silentProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error { return nil }

func (s *silentProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	return executor.ExecResult{}, nil
}

func (s *silentProvider) Cancel(ctx context.Context) error { return nil }

// concurrencyProvider 记录同时在跑的调用数峰值（并行额度断言用）。
type concurrencyProvider struct {
	kind    string
	running int32
	peak    int32
}

func (c *concurrencyProvider) Kind() string { return c.kind }

func (c *concurrencyProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error {
	return nil
}

func (c *concurrencyProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	cur := atomic.AddInt32(&c.running, 1)
	for {
		peak := atomic.LoadInt32(&c.peak)
		if cur <= peak || atomic.CompareAndSwapInt32(&c.peak, peak, cur) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	atomic.AddInt32(&c.running, -1)
	return executor.ExecResult{Output: "ok", GateResult: types.SopGatePass}, nil
}

func (c *concurrencyProvider) Cancel(ctx context.Context) error { return nil }

// countProvider 记录 Execute 被调次数（并发推进下不得同一节点重复执行）。
type countProvider struct {
	kind  string
	calls int32
}

func (c *countProvider) Kind() string { return c.kind }

func (c *countProvider) Prepare(ctx context.Context, n *types.SopNode, ws string) error {
	return nil
}

func (c *countProvider) Execute(
	ctx context.Context, tb executor.TaskBook, o executor.ExecOptions,
) (executor.ExecResult, error) {
	atomic.AddInt32(&c.calls, 1)
	time.Sleep(50 * time.Millisecond) // 放大「两个推进者同时看到 pending」的时间窗
	return executor.ExecResult{Output: "ok", GateResult: types.SopGatePass}, nil
}

func (c *countProvider) Cancel(ctx context.Context) error { return nil }

// startRun 建一条 queued 运行并认领（节点记录随之建立）。
func startRun(t *testing.T, e *Engine, def types.SopDefinition) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunQueued, WorkflowSnapshot: def}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.TickOnce(ctx))
	return run
}

func TestReadyNodesParallelAndSerialGroup(t *testing.T) {
	def := &types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1"}, {ID: "n2", SerialGroup: "g"}, {ID: "n3", SerialGroup: "g"}, {ID: "n4"},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}, {ID: "e2", Source: "n1", Target: "n3"}, {ID: "e3", Source: "n1", Target: "n4"}}}
	nrs := map[string]*types.SopNodeRun{"n1": {NodeID: "n1", Status: types.SopNodeSucceeded},
		"n2": {NodeID: "n2", Status: types.SopNodePending}, "n3": {NodeID: "n3", Status: types.SopNodePending}, "n4": {NodeID: "n4", Status: types.SopNodePending}}
	ready := readyNodes(def, nrs)
	ids := map[string]bool{}
	for _, n := range ready {
		ids[n.ID] = true
	}
	if !ids["n4"] || ids["n1"] {
		t.Fatalf("ready wrong: %v", ids)
	}
	if ids["n2"] && ids["n3"] {
		t.Fatal("同一 serial_group 不得同时就绪两个")
	}
}

func TestAdvanceLinearSuccess(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "A", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/a.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}},
		{ID: "n2", Name: "B", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}}}
	run := startRun(t, e, def)
	for i := 0; i < 4; i++ {
		if _, err := e.Advance(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	arts, _ := e.deps.Runs.ListArtifacts(ctx, 1, run.ID)
	require.Len(t, arts, 1)
	require.NotEmpty(t, arts[0].SHA256)
}

func TestGateReworkThenPass(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "v1", GateResult: types.SopGateRework, GateReason: "缺少异常态"},
		{Output: "v2", GateResult: types.SopGatePass},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "loop", MaxAttempts: 3, CarryOver: true}}}
	run := startRun(t, e, def)
	for i := 0; i < 4; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, 2, nr.Attempt)
	require.Equal(t, "v2", nr.Output)
}

func TestReworkBudgetExhaustedGoesHuman(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{GateResult: types.SopGateRework, GateReason: "r1"}, {GateResult: types.SopGateRework, GateReason: "r2"}, {GateResult: types.SopGateRework, GateReason: "r3"},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "loop", MaxAttempts: 2}}}
	run := startRun(t, e, def)
	for i := 0; i < 5; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeWaitingApproval, nr.Status)
}

func TestRejectResetsTargetAndDescendants(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "a1", GateResult: types.SopGatePass},
		{Output: "b1", GateResult: types.SopGateRework, GateReason: "方案不行"},
		{Output: "a2", GateResult: types.SopGatePass},
		{Output: "b2", GateResult: types.SopGatePass},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, OnFailure: "reject", RejectTo: "n1", MaxReworks: 2},
		{ID: "n3", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}, {ID: "e2", Source: "n2", Target: "n3"}}}
	run := startRun(t, e, def)
	for i := 0; i < 8; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	n1, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, "a2", n1.Output)
	require.Equal(t, 1, n1.ReworkCount)
	require.Contains(t, n1.Feedback, "方案不行")
}

func TestGateCommandsScriptFailureMeansRework(t *testing.T) {
	e, _, _ := newTestEngine(t)
	// 上报结论缺失（交门禁裁定）：产物契约满足，但 gate_extra 命令失败 → 打回；
	// 第二次仍失败 → 重试预算耗尽转人工。
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "v1"}, {Output: "v2"},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent",
		Executor:        types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/a.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}},
		GateExtra:       "exit 1",
		OnFailure:       "loop", MaxAttempts: 2}}}
	run := startRun(t, e, def)
	for i := 0; i < 4; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopGateRework, nr.GateResult)
	require.Contains(t, nr.GateReason, "检查没通过")
}

func TestMissingRequiredArtifactFailsRun(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&silentProvider{kind: "platform_agent"}) // 不写产物
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/missing.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}},
		OnFailure:       "fail"}}}
	run := startRun(t, e, def)
	for i := 0; i < 3; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Contains(t, nr.GateReason, "out/n1/missing.md", "产物路径按节点目录 out/<node>/ 报错")
}

func TestHumanGateWaitsForApproval(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{GateResult: types.SopGateHuman, GateReason: "需要人确认"},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "评审", Type: "agent",
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}}}}
	run := startRun(t, e, def)
	e.Advance(ctx, run.ID)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunWaiting, got.Status)
	nr, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodeWaitingApproval, nr.Status)

	// 再推进不得把等待中的人工节点重新执行。
	progressed, err := e.Advance(ctx, run.ID)
	require.NoError(t, err)
	require.False(t, progressed)
}

func TestFanOutRespectsMaxParallel(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.MaxParallel = 1
	p := &concurrencyProvider{kind: "platform_agent"}
	e.deps.Registry.Register(p)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n0", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n3", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{
		{ID: "e1", Source: "n0", Target: "n1"},
		{ID: "e2", Source: "n0", Target: "n2"},
		{ID: "e3", Source: "n0", Target: "n3"},
	}}
	run := startRun(t, e, def)
	for i := 0; i < 4; i++ {
		_, err := e.Advance(ctx, run.ID)
		require.NoError(t, err)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.LessOrEqual(t, atomic.LoadInt32(&p.peak), int32(1), "并行额度为 1 时不得并发执行")
}

func TestSerialGroupRunsOneAtATime(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent"})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", SerialGroup: "g", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n3", Type: "agent", SerialGroup: "g", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{
		{ID: "e1", Source: "n1", Target: "n2"},
		{ID: "e2", Source: "n1", Target: "n3"},
	}}
	run := startRun(t, e, def)
	// 第一轮只放行 n1（n2/n3 的上游还没完成）。
	_, err := e.Advance(ctx, run.ID)
	require.NoError(t, err)
	// 第二轮：n1 已成功，n2/n3 同组只放行定义顺序靠前的 n2。
	_, err = e.Advance(ctx, run.ID)
	require.NoError(t, err)
	n2, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	n3, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n3")
	require.Equal(t, types.SopNodeSucceeded, n2.Status, "定义顺序靠前的同组节点先执行")
	require.Equal(t, types.SopNodePending, n3.Status, "同组另一个必须等待")

	// 第三轮：n3 才轮到。
	_, err = e.Advance(ctx, run.ID)
	require.NoError(t, err)
	n3, _ = e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n3")
	require.Equal(t, types.SopNodeSucceeded, n3.Status)

	_, err = e.Advance(ctx, run.ID)
	require.NoError(t, err)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
}

// 上游返工后同一路径会留下多条产物（attempt 1/2）：下游物化只取最新一份，
// 不能因「inputs/<up>/同名文件」把整条下游链判死。
func TestReworkedUpstreamStillMaterializesForDownstream(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "v1", GateResult: types.SopGateRework, GateReason: "再来一遍"},
		{Output: "v2", GateResult: types.SopGatePass},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", OnFailure: "loop", MaxAttempts: 3,
			Executor:        types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
			ArtifactOutputs: []types.SopArtifactOutput{{Name: "r", Path: "out/a.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}}}
	run := startRun(t, e, def)
	for i := 0; i < 6; i++ {
		if _, err := e.Advance(ctx, run.ID); err != nil {
			t.Fatalf("第 %d 轮推进失败：%v", i, err)
		}
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)

	// 产物历史保留两次尝试，但路径是节点目录口径。
	arts, err := e.deps.Runs.ListArtifacts(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Len(t, arts, 2, "两次尝试各留一条产物历史")
	for _, a := range arts {
		require.Equal(t, "out/n1/a.md", a.Path)
	}
	// 下游物化拿到的是最新一次（v2），且只拿到一份（物化目录按上游节点名）。
	n2, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.Equal(t, types.SopNodeSucceeded, n2.Status)
	data, err := os.ReadFile(filepath.Join(got.WorkspaceDir, "inputs", "n1", "a.md"))
	require.NoError(t, err)
	require.Equal(t, "v2", string(data))
}

// 运行在 running 时，全部节点终态且其中有 failed ⇒ 运行必须落 failed（不得判成功）。
func TestFailedNodeDuringRunningSettlesRunFailed(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := startRun(t, e, types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}})
	n1, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.NoError(t, err)
	n1.Status, n1.Error = types.SopNodeFailed, "环境不成立"
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, n1))
	n2, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.NoError(t, err)
	n2.Status = types.SopNodeSucceeded
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, n2))

	_, err = e.Advance(ctx, run.ID)
	require.NoError(t, err)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "环境不成立")
}

// 全部节点终态且其中有 cancelled ⇒ 运行落 cancelled（不是成功，也不是失败）。
func TestCancelledNodeSettlesRunCancelled(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	run := startRun(t, e, types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}})
	n1, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.NoError(t, err)
	n1.Status = types.SopNodeSucceeded
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, n1))
	n2, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.NoError(t, err)
	n2.Status = types.SopNodeCancelled
	require.NoError(t, e.deps.Runs.UpdateNodeRun(ctx, n2))

	_, err = e.Advance(ctx, run.ID)
	require.NoError(t, err)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunCancelled, got.Status)
}

// 打回重置的范围：目标与下游回到 pending 且执行痕迹清空、Attempt 归零，
// 但被打回目标的 ReworkCount 仍要记账（返工预算不能被重置吞掉）。
func TestRejectResetClearsTracesAndKeepsReworkBudget(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "a1", GateResult: types.SopGatePass},
		{Output: "b1", GateResult: types.SopGateRework, GateReason: "方案不行"},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", OnFailure: "reject", RejectTo: "n1", MaxReworks: 3,
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n3", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}, {ID: "e2", Source: "n2", Target: "n3"}}}
	run := startRun(t, e, def)
	for i := 0; i < 2; i++ {
		_, err := e.Advance(ctx, run.ID)
		require.NoError(t, err)
	}

	n1, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, types.SopNodePending, n1.Status)
	require.Empty(t, n1.Output, "重置必须清掉旧输出")
	require.Empty(t, n1.GateResult)
	require.Nil(t, n1.FinishedAt)
	require.Equal(t, 0, n1.Attempt, "返工不消耗被打回节点的重试预算")
	require.Equal(t, 1, n1.ReworkCount, "返工次数要记账")
	require.Contains(t, n1.Feedback, "方案不行")

	n2, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.Equal(t, types.SopNodePending, n2.Status)
	require.Equal(t, 1, n2.ReworkCount)

	n3, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n3")
	require.Equal(t, types.SopNodePending, n3.Status)
	require.Empty(t, n3.Output)
}

// 并行兄弟各自写 out/<node>/：同名产物互不覆盖，产物行按节点目录记录。
func TestSiblingNodesWriteOwnOutDir(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "a", GateResult: types.SopGatePass},
		{Output: "b", GateResult: types.SopGatePass},
	}})
	ctx := context.Background()
	outArt := func() []types.SopArtifactOutput {
		return []types.SopArtifactOutput{{Name: "a", Path: "out/a.md", Required: true, Check: types.SopArtifactCheck{Type: "exists"}}}
	}
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, ArtifactOutputs: outArt()},
		{ID: "n2", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}, ArtifactOutputs: outArt()},
	}}
	run := startRun(t, e, def)
	for i := 0; i < 4; i++ {
		_, err := e.Advance(ctx, run.ID)
		require.NoError(t, err)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)

	arts, err := e.deps.Runs.ListArtifacts(ctx, 1, run.ID)
	require.NoError(t, err)
	paths := map[string]bool{}
	for _, a := range arts {
		paths[a.Path] = true
	}
	require.True(t, paths["out/n1/a.md"], "n1 产物落在自己的目录：%v", paths)
	require.True(t, paths["out/n2/a.md"], "n2 产物落在自己的目录：%v", paths)
	for _, id := range []string{"n1", "n2"} {
		_, statErr := os.Stat(filepath.Join(got.WorkspaceDir, "out", id, "a.md"))
		require.NoError(t, statErr, "out/%s/a.md 应存在", id)
	}
}

// 两个推进者同时 Advance 同一条运行：节点只能被真正执行一次（单运行串行推进）。
func TestConcurrentAdvanceExecutesNodeOnce(t *testing.T) {
	e, _, _ := newTestEngine(t)
	p := &countProvider{kind: "platform_agent"}
	e.deps.Registry.Register(p)
	ctx := context.Background()
	run := startRun(t, e, types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Advance(ctx, run.ID)
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), atomic.LoadInt32(&p.calls), "同一节点不得被并发重复执行")
	nr, err := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.NoError(t, err)
	require.Equal(t, 1, nr.Attempt)
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
}
