package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// 定义里选了「打回上游」却没有目标时，不能悄悄退化成「本节点重跑」：
// 那会和 loop 重试语义混为一谈，用户永远不知道模板缺了打回目标。
// 必须带着清楚的原因让运行失败。
func TestRejectWithoutTargetFailsLoudly(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{GateResult: types.SopGateRework, GateReason: "方案不行"},
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Name: "实现", Type: "agent",
		Executor:  types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		OnFailure: types.SopOnFailureReject, RejectTo: "",
	}}}
	run := startRun(t, e, def)
	for i := 0; i < 3; i++ {
		e.Advance(ctx, run.ID)
	}

	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunFailed, got.Status, "配置缺失不能悄悄重跑")
	require.Contains(t, got.Error, "打回目标", "错误要说清是打回目标缺失：%q", got.Error)
}

// 打回只重置「目标 → 当前节点」这条链上的节点：并行兄弟分支的产出必须保住，
// 否则下游节点重跑时读不到本该存在的上游产物。
func TestRejectResetsOnlyPathToFailingNode(t *testing.T) {
	e, _, _ := newTestEngine(t)
	// n1 分叉出 n2 / n3（同组串行，保证取步骤顺序确定），两者汇聚到 n4；n3 打回 n1。
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent", steps: []executor.ExecResult{
		{Output: "a", GateResult: types.SopGatePass},                         // n1
		{Output: "b", GateResult: types.SopGatePass},                         // n2
		{Output: "c1", GateResult: types.SopGateRework, GateReason: "横向不一致"}, // n3 第一轮
		{Output: "a2", GateResult: types.SopGatePass},                        // n1 重跑
		{Output: "c2", GateResult: types.SopGatePass},                        // n3 重跑
		{Output: "d", GateResult: types.SopGatePass},                         // n4
	}})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n2", Type: "agent", SerialGroup: "g", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
		{ID: "n3", Type: "agent", SerialGroup: "g", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
			OnFailure: types.SopOnFailureReject, RejectTo: "n1", MaxReworks: 2},
		{ID: "n4", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}},
	}, Edges: []types.SopEdge{
		{ID: "e1", Source: "n1", Target: "n2"},
		{ID: "e2", Source: "n1", Target: "n3"},
		{ID: "e3", Source: "n2", Target: "n4"},
		{ID: "e4", Source: "n3", Target: "n4"},
	}}
	run := startRun(t, e, def)
	for i := 0; i < 12; i++ {
		e.Advance(ctx, run.ID)
	}

	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status, "运行错误：%s", got.Error)

	n2, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n2")
	require.Equal(t, "b", n2.Output, "并行兄弟分支的产出不得被重置")
	require.Equal(t, 1, n2.Attempt, "兄弟分支不该重跑")

	n1, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n1")
	require.Equal(t, "a2", n1.Output, "打回链上的目标必须重跑")
	n3, _ := e.deps.Runs.GetNodeRun(ctx, 1, run.ID, "n3")
	require.Equal(t, "c2", n3.Output)
	require.Equal(t, 1, n3.ReworkCount)
}
