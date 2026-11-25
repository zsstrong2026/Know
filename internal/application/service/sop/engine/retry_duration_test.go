package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// 重试必须重新打点：旧的 started_at 会让最终耗时把停机时间也算进去，
// 运行详情里出现「跑了半小时」但实际只执行了几秒的假数据。
func TestRetryResetsExecutionClock(t *testing.T) {
	e, _, _ := newTestEngine(t)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	started := time.Now().Add(-90 * time.Second)
	finished := time.Now().Add(-30 * time.Second)
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunFailed, WorkflowSnapshot: def, Error: "boom",
		StartedAt: &started, FinishedAt: &finished, ExecutionDurationMs: 90_000,
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeFailed, Attempt: 3, Error: "boom",
	}))

	require.NoError(t, e.Retry(ctx, 1, run.ID))
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunQueued, got.Status)
	require.Nil(t, got.StartedAt, "重试后必须重新打点，不能沿用首跑时间")
	require.Equal(t, int64(0), got.ExecutionDurationMs, "旧耗时必须清零")
	require.Nil(t, got.FinishedAt)
}

// 重试后跑完：耗时只覆盖这一段，不含中间停机。
func TestRetryDurationExcludesDowntime(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.deps.Registry.Register(&scriptedProvider{kind: "platform_agent"})
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Type: "agent", Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	started := time.Now().Add(-90 * time.Second)
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunFailed, WorkflowSnapshot: def, Error: "boom",
		StartedAt: &started, ExecutionDurationMs: 90_000,
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	require.NoError(t, e.deps.Runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeFailed, Attempt: 1, Error: "boom",
	}))

	require.NoError(t, e.Retry(ctx, 1, run.ID))
	require.NoError(t, e.TickOnce(ctx))
	for i := 0; i < 4; i++ {
		e.Advance(ctx, run.ID)
	}
	got, _ := e.deps.Runs.GetRun(ctx, 1, run.ID)
	require.Equal(t, types.SopRunSucceeded, got.Status)
	require.Less(t, got.ExecutionDurationMs, int64(30_000),
		"耗时必须从重试认领时重新计，不能把停机 90s 算进去")
}
